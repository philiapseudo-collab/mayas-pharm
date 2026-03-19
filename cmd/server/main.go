package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	adapterhttp "github.com/philia-technologies/mayas-pharm/internal/adapters/http"
	"github.com/philia-technologies/mayas-pharm/internal/adapters/payment"
	"github.com/philia-technologies/mayas-pharm/internal/adapters/postgres"
	"github.com/philia-technologies/mayas-pharm/internal/adapters/redis"
	"github.com/philia-technologies/mayas-pharm/internal/adapters/whatsapp"
	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/events"
	"github.com/philia-technologies/mayas-pharm/internal/middleware"
	"github.com/philia-technologies/mayas-pharm/internal/observability"
	"github.com/philia-technologies/mayas-pharm/internal/service"
	goredis "github.com/redis/go-redis/v9"
)

func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	repo, err := postgres.NewRepository(cfg.DBURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	metrics := observability.NewRuntimeMetrics()
	repo.SetMetrics(metrics)

	sqlDB, err := repo.SQLDB()
	if err != nil {
		log.Fatalf("failed to get database pool handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.DBConnMaxLifetimeMinutes) * time.Minute)
	sqlDB.SetConnMaxIdleTime(time.Duration(cfg.DBConnMaxIdleTimeMinutes) * time.Minute)
	log.Println("database connected")

	redisOpts, err := goredis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to parse redis url: %v", err)
	}
	if cfg.RedisPassword != "" {
		redisOpts.Password = cfg.RedisPassword
	}

	redisClient := goredis.NewClient(redisOpts)
	if err := redisClient.Ping(rootCtx).Err(); err != nil {
		log.Fatalf("failed to connect to redis: %v", err)
	}
	log.Println("redis connected")

	sessionRepo := redis.NewRepository(redisClient)
	whatsappClient := whatsapp.NewClient(cfg.WhatsAppPhoneNumberID, cfg.WhatsAppToken)
	if strings.TrimSpace(cfg.WhatsAppPhoneNumberID) == "" || strings.TrimSpace(cfg.WhatsAppToken) == "" {
		log.Println("whatsapp client disabled: set WHATSAPP_PHONE_NUMBER_ID and WHATSAPP_TOKEN to enable outbound messaging")
	} else {
		log.Println("whatsapp client initialized")
	}

	paymentGateway, err := payment.NewClient()
	if err != nil {
		log.Fatalf("failed to initialize payment gateway: %v", err)
	}
	log.Println("payment gateway initialized")

	pesapalClient, pesapalErr := payment.NewPesapalClient()
	if pesapalErr != nil {
		log.Printf("pesapal disabled: %v", pesapalErr)
	} else {
		log.Println("pesapal client initialized")
	}

	productRepo := repo.ProductRepository()
	orderRepo := repo.OrderRepository()
	userRepo := repo.UserRepository()
	paymentAttemptRepo := repo.PaymentAttemptRepository()
	unmatchedPaymentRepo := repo.UnmatchedPaymentRepository()

	eventBus := events.NewEventBus(redisClient, metrics)
	paymentService := service.NewPaymentService(
		orderRepo,
		paymentAttemptRepo,
		unmatchedPaymentRepo,
		userRepo,
		paymentGateway,
		whatsappClient,
		eventBus,
	)
	paymentService.SetMetrics(metrics)
	paymentService.ConfigureWorkerIntervals(
		time.Duration(cfg.PaymentDispatchIntervalMS)*time.Millisecond,
		time.Duration(cfg.PaymentDispatchPollIntervalMS)*time.Millisecond,
		time.Duration(cfg.OrderExpirySweepIntervalMS)*time.Millisecond,
	)

	botService := service.NewBotService(
		productRepo,
		sessionRepo,
		whatsappClient,
		paymentGateway,
		orderRepo,
		userRepo,
	)
	botService.SetPaymentService(paymentService)
	botService.SetPesapalGateway(pesapalClient)
	botService.SetDeliveryZoneRepository(repo.DeliveryZoneRepository())
	botService.SetPrescriptionRepository(repo.PrescriptionRepository())
	log.Println("bot service initialized")

	httpHandler := adapterhttp.NewHandler(
		botService,
		paymentGateway,
		paymentService,
		cfg.WhatsAppWebhookMaxConcurrent,
		metrics,
	)

	dashboardService := service.NewDashboardService(
		repo.AdminUserRepository(),
		repo.OTPRepository(),
		productRepo,
		orderRepo,
		paymentService,
		userRepo,
		repo.AnalyticsRepository(),
		repo.PrescriptionRepository(),
		repo.DeliveryZoneRepository(),
		repo.BusinessHoursRepository(),
		repo.OutboundMessageRepository(),
		repo.AuditLogRepository(),
		whatsappClient,
		eventBus,
		cfg.JWTSecret,
	)
	dashboardHandler := adapterhttp.NewDashboardHandler(dashboardService)

	customerService := service.NewCustomerService(
		productRepo,
		orderRepo,
		userRepo,
		paymentService,
		paymentAttemptRepo,
		repo.PrescriptionRepository(),
		repo.DeliveryZoneRepository(),
		pesapalClient,
		eventBus,
	)
	customerService.SetMetrics(metrics)
	customerHandler := adapterhttp.NewCustomerHandler(customerService)

	runtimeMode := strings.ToLower(strings.TrimSpace(cfg.AppRuntimeMode))
	if runtimeMode == "" {
		runtimeMode = "all"
	}
	if runtimeMode != "all" && runtimeMode != "api" && runtimeMode != "worker" {
		log.Fatalf("invalid APP_RUNTIME_MODE: %s", cfg.AppRuntimeMode)
	}

	if runtimeMode == "all" || runtimeMode == "worker" {
		go paymentService.RunDispatcher(rootCtx)
		go paymentService.RunOrderExpiryLoop(rootCtx)
		slog.Info("payment workers started", "runtime_mode", runtimeMode)
	}

	if runtimeMode == "worker" {
		slog.Info("worker runtime started")
		<-rootCtx.Done()
		slog.Info("worker runtime stopped")
		return
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	app.Use(recover.New())
	app.Use(logger.New())
	allowedOrigin, allowCredentials := resolveAllowedOrigins(cfg)
	app.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigin,
		AllowMethods:     "GET,POST,PUT,DELETE,PATCH,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,Idempotency-Key",
		ExposeHeaders:    "Content-Disposition",
		AllowCredentials: allowCredentials,
	}))

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":       "ok",
			"service":      "mayas-pharm",
			"runtime_mode": runtimeMode,
		})
	})

	app.Get("/metrics", func(c *fiber.Ctx) error {
		queueStats, err := paymentService.QueueStats(c.Context(), string(core.PaymentMethodMpesa))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		if queueStats != nil {
			oldestAge := time.Duration(0)
			if queueStats.OldestQueuedAt != nil {
				oldestAge = time.Since(*queueStats.OldestQueuedAt)
			}
			metrics.SetQueueStats(queueStats.QueuedCount, oldestAge)
		} else {
			metrics.SetQueueStats(0, 0)
		}

		unmatchedPending, err := paymentService.PendingUnmatchedCount(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(metrics.Snapshot(sqlDB.Stats(), unmatchedPending))
	})

	app.Get("/api/webhooks/whatsapp", httpHandler.VerifyWebhook)
	app.Post("/api/webhooks/whatsapp", httpHandler.ReceiveMessage)
	app.Post("/api/webhooks/payment", httpHandler.HandlePaymentWebhook)
	app.Get("/api/webhooks/pesapal", customerHandler.HandlePesapalWebhook)

	app.Get("/api/menu", customerHandler.GetMenu)
	app.Get("/api/menu/categories", customerHandler.GetCategories)
	app.Get("/api/customer/delivery-zones", customerHandler.GetDeliveryZones)
	app.Post("/api/customer/orders", customerHandler.CreateOrder)
	app.Post("/api/customer/orders/:id/prescriptions", customerHandler.UploadPrescription)
	app.Post("/api/customer/orders/:id/pay/mpesa", customerHandler.PayWithMpesa)
	app.Post("/api/customer/orders/:id/pay/pesapal", customerHandler.PayWithPesapal)
	app.Get("/api/customer/orders/:id/status", customerHandler.GetOrderStatus)

	app.Post("/api/admin/auth/request-otp", dashboardHandler.RequestOTP)
	app.Post("/api/admin/auth/verify-otp", dashboardHandler.VerifyOTP)
	app.Get("/api/admin/auth/staff", dashboardHandler.GetStaffAccounts)
	app.Post("/api/admin/auth/staff-login", dashboardHandler.StaffLogin)
	app.Get("/api/admin/auth/bartenders", dashboardHandler.GetBartenderAccounts)
	app.Post("/api/admin/auth/bartender-login", dashboardHandler.BartenderLogin)
	app.Post("/api/admin/auth/logout", dashboardHandler.Logout)

	admin := app.Group("/api/admin", middleware.AuthMiddleware(dashboardService))
	admin.Get("/auth/me", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.GetMe)
	admin.Get("/products", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist), dashboardHandler.GetProducts)
	admin.Patch("/products/:id/stock", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist), dashboardHandler.UpdateStock)
	admin.Patch("/products/:id/price", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpdatePrice)
	admin.Get("/staff", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ListStaff)
	admin.Post("/staff", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.CreateStaff)
	admin.Patch("/staff/:id", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpdateStaff)
	admin.Patch("/staff/:id/pin", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ResetStaffPIN)
	admin.Get("/bartenders", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ListBartenders)
	admin.Post("/bartenders", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.CreateBartender)
	admin.Patch("/bartenders/:id", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpdateBartender)
	admin.Patch("/bartenders/:id/pin", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ResetBartenderPIN)
	admin.Get("/analytics/overview", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.GetAnalyticsOverview)
	admin.Get("/analytics/revenue", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.GetRevenueTrend)
	admin.Get("/analytics/top-products", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.GetTopProducts)
	admin.Get("/analytics/reports/daily", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ExportDailySalesReportPDF)
	admin.Get("/analytics/reports/last-30-days", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ExportLast30DaysSalesReportPDF)
	admin.Get("/payments/unmatched", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ListUnmatchedPayments)
	admin.Post("/payments/unmatched/:id/resolve", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ResolveUnmatchedPayment)
	admin.Post("/payments/unmatched/:id/reject", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.RejectUnmatchedPayment)
	admin.Get("/prescriptions", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist), dashboardHandler.ListPendingPrescriptions)
	admin.Post("/prescriptions/:id/review", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist), dashboardHandler.ReviewPrescription)
	admin.Get("/delivery-zones", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRoleDispatcher), dashboardHandler.GetDeliveryZones)
	admin.Put("/delivery-zones/:id", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpsertDeliveryZone)
	admin.Post("/delivery-zones", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpsertDeliveryZone)
	admin.Get("/business-hours", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.GetBusinessHours)
	admin.Put("/business-hours/:id", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpsertBusinessHours)
	admin.Post("/business-hours", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UpsertBusinessHours)
	admin.Get("/orders", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.GetOrders)
	admin.Get("/orders/history", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.GetOrderHistory)
	admin.Post("/orders/:id/prepare", middleware.RequireRoles(core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.MarkOrderPreparing)
	admin.Post("/orders/:id/dispatch", middleware.RequireRoles(core.AdminRoleDispatcher), dashboardHandler.MarkOrderOutForDelivery)
	admin.Post("/orders/:id/ready", middleware.RequireRoles(core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.MarkOrderReady)
	admin.Post("/orders/:id/complete", middleware.RequireRoles(core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.MarkOrderComplete)
	admin.Post("/orders/:id/prepare/takeover", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.ForceTakeoverPreparing)
	admin.Post("/orders/:id/prepare/unlock", middleware.RequireRoles(core.AdminRoleOwner), dashboardHandler.UnlockPreparing)
	admin.Get("/events", middleware.RequireRoles(core.AdminRoleOwner, core.AdminRolePharmacist, core.AdminRoleDispatcher), dashboardHandler.SSEEvents)

	port := cfg.AppPort
	if port == "" {
		port = "8080"
	}

	go func() {
		<-rootCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.AppShutdownTimeoutSeconds)*time.Second)
		defer cancel()
		if shutdownErr := app.ShutdownWithContext(shutdownCtx); shutdownErr != nil {
			slog.Error("http shutdown failed", "error", shutdownErr)
		}
	}()

	log.Printf("server starting on port %s", port)
	log.Printf("runtime mode: %s", runtimeMode)
	log.Printf("cors allow origins: %s", allowedOrigin)

	if err := app.Listen(fmt.Sprintf(":%s", port)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "closed") {
		log.Fatalf("failed to start server: %v", err)
	}
}
