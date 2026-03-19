package http

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/events"
	"github.com/philia-technologies/mayas-pharm/internal/service"
)

// DashboardHandler handles dashboard HTTP requests
type DashboardHandler struct {
	dashboardService *service.DashboardService
}

const (
	defaultOrdersLimit = 100
	maxOrdersLimit     = 200
)

// NewDashboardHandler creates a new dashboard handler
func NewDashboardHandler(dashboardService *service.DashboardService) *DashboardHandler {
	return &DashboardHandler{
		dashboardService: dashboardService,
	}
}

// GetBartenderAccounts returns active bartender identities for login selection.
// GET /api/admin/auth/bartenders
func (h *DashboardHandler) GetBartenderAccounts(c *fiber.Ctx) error {
	accounts, err := h.dashboardService.GetBartenderAccounts(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get bartender accounts",
		})
	}

	return c.JSON(accounts)
}

// RequestOTP handles OTP request
// POST /api/admin/auth/request-otp
func (h *DashboardHandler) RequestOTP(c *fiber.Ctx) error {
	var req struct {
		Phone string `json:"phone"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Phone == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "phone number is required",
		})
	}

	if err := h.dashboardService.RequestOTP(c.Context(), req.Phone); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "OTP sent successfully",
	})
}

// VerifyOTP handles OTP verification
// POST /api/admin/auth/verify-otp
func (h *DashboardHandler) VerifyOTP(c *fiber.Ctx) error {
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Phone == "" || req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "phone and code are required",
		})
	}

	token, err := h.dashboardService.VerifyOTP(c.Context(), req.Phone, req.Code)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	secureCookie, sameSite := authCookieSettings(c)
	c.Cookie(&fiber.Cookie{
		Name:     "auth_token",
		Value:    token,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
		HTTPOnly: true,
		Secure:   secureCookie,
		SameSite: sameSite,
	})

	return c.JSON(fiber.Map{
		"message": "login successful",
		"token":   token,
		"role":    core.AdminRoleManager,
	})
}

// BartenderLogin handles bartender PIN login.
// POST /api/admin/auth/bartender-login
func (h *DashboardHandler) BartenderLogin(c *fiber.Ctx) error {
	var req struct {
		PIN    string `json:"pin"`
		UserID string `json:"user_id"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.PIN == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "PIN is required",
		})
	}

	var (
		token string
		err   error
	)

	if strings.TrimSpace(req.UserID) != "" {
		token, err = h.dashboardService.VerifyBartenderPINForUser(c.Context(), req.UserID, req.PIN)
	} else {
		token, err = h.dashboardService.VerifyBartenderPIN(c.Context(), req.PIN)
	}

	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "PIN must be exactly 4 digits") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": msg,
			})
		}
		if strings.Contains(msg, "invalid bartender account") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": msg,
			})
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid PIN",
		})
	}

	secureCookie, sameSite := authCookieSettings(c)
	c.Cookie(&fiber.Cookie{
		Name:     "auth_token",
		Value:    token,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
		HTTPOnly: true,
		Secure:   secureCookie,
		SameSite: sameSite,
	})

	return c.JSON(fiber.Map{
		"message": "login successful",
		"token":   token,
		"role":    core.AdminRoleBartender,
	})
}

// Logout handles user logout
// POST /api/admin/auth/logout
func (h *DashboardHandler) Logout(c *fiber.Ctx) error {
	secureCookie, sameSite := authCookieSettings(c)
	c.Cookie(&fiber.Cookie{
		Name:     "auth_token",
		Value:    "",
		Expires:  time.Now().Add(-1 * time.Hour),
		HTTPOnly: true,
		Secure:   secureCookie,
		SameSite: sameSite,
	})

	return c.JSON(fiber.Map{
		"message": "logged out successfully",
	})
}

// GetMe returns current user info
// GET /api/admin/auth/me
func (h *DashboardHandler) GetMe(c *fiber.Ctx) error {
	// Get admin user from database
	phone := c.Locals("phone").(string)
	adminUser, err := h.dashboardService.GetAdminUserByPhone(c.Context(), phone)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get user",
		})
	}

	return c.JSON(adminUser) // Returns full AdminUser struct
}

// GetProducts retrieves all products
// GET /api/admin/products
func (h *DashboardHandler) GetProducts(c *fiber.Ctx) error {
	products, err := h.dashboardService.GetProducts(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get products",
		})
	}

	return c.JSON(products)
}

// UpdateStock updates product stock
// PATCH /api/admin/products/:id/stock
func (h *DashboardHandler) UpdateStock(c *fiber.Ctx) error {
	productID := c.Params("id")
	if productID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "product ID is required",
		})
	}

	var req struct {
		StockQuantity int `json:"stock_quantity"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if err := h.dashboardService.UpdateStock(c.Context(), productID, req.StockQuantity); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "stock updated successfully",
	})
}

// UpdatePrice updates product price
// PATCH /api/admin/products/:id/price
func (h *DashboardHandler) UpdatePrice(c *fiber.Ctx) error {
	productID := c.Params("id")
	if productID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "product ID is required",
		})
	}

	var req struct {
		Price float64 `json:"price"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Price <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "price must be greater than 0",
		})
	}

	if err := h.dashboardService.UpdatePrice(c.Context(), productID, req.Price); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "price updated successfully",
	})
}

// GetOrders retrieves orders with optional filters
// GET /api/admin/orders?status=PAID&limit=50
func (h *DashboardHandler) GetOrders(c *fiber.Ctx) error {
	status := c.Query("status", "")
	limitStr := c.Query("limit", "100")
	updatedAfterStr := strings.TrimSpace(c.Query("updated_after", ""))

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = defaultOrdersLimit
	}
	if limit <= 0 {
		limit = defaultOrdersLimit
	}
	if limit > maxOrdersLimit {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("limit must be between 1 and %d", maxOrdersLimit),
		})
	}

	var updatedAfter *time.Time
	if updatedAfterStr != "" {
		parsed, parseErr := time.Parse(time.RFC3339, updatedAfterStr)
		if parseErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "updated_after must be RFC3339",
			})
		}
		updatedAfter = &parsed
	}

	orders, err := h.dashboardService.GetOrders(c.Context(), status, limit, updatedAfter)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get orders",
		})
	}

	return c.JSON(orders)
}

// ListUnmatchedPayments retrieves pending unmatched provider payments.
// GET /api/admin/payments/unmatched?limit=100
func (h *DashboardHandler) ListUnmatchedPayments(c *fiber.Ctx) error {
	limitStr := c.Query("limit", "100")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = defaultOrdersLimit
	}
	if limit <= 0 {
		limit = defaultOrdersLimit
	}
	if limit > maxOrdersLimit {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("limit must be between 1 and %d", maxOrdersLimit),
		})
	}

	payments, err := h.dashboardService.ListPendingUnmatchedPayments(c.Context(), limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(payments)
}

// ResolveUnmatchedPayment applies a pending unmatched payment to a known order.
// POST /api/admin/payments/unmatched/:id/resolve
func (h *DashboardHandler) ResolveUnmatchedPayment(c *fiber.Ctx) error {
	unmatchedPaymentID := strings.TrimSpace(c.Params("id"))
	if unmatchedPaymentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "unmatched payment ID is required",
		})
	}

	var req struct {
		OrderID string `json:"order_id"`
		Note    string `json:"note"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if strings.TrimSpace(req.OrderID) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order_id is required",
		})
	}

	payment, err := h.dashboardService.ResolveUnmatchedPayment(c.Context(), unmatchedPaymentID, req.OrderID, req.Note)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(strings.ToLower(msg), "not payable"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(payment)
}

// RejectUnmatchedPayment rejects an unmatched payment after manual review.
// POST /api/admin/payments/unmatched/:id/reject
func (h *DashboardHandler) RejectUnmatchedPayment(c *fiber.Ctx) error {
	unmatchedPaymentID := strings.TrimSpace(c.Params("id"))
	if unmatchedPaymentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "unmatched payment ID is required",
		})
	}

	var req struct {
		Note string `json:"note"`
	}
	if err := c.BodyParser(&req); err != nil && len(c.Body()) > 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	payment, err := h.dashboardService.RejectUnmatchedPayment(c.Context(), unmatchedPaymentID, req.Note)
	if err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "not found") {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
	}

	return c.JSON(payment)
}

// GetOrderHistory retrieves completed orders for bartender/manager dispute checks.
// GET /api/admin/orders/history?pickup_code=0031&phone=2547&limit=50
func (h *DashboardHandler) GetOrderHistory(c *fiber.Ctx) error {
	pickupCode := strings.TrimSpace(c.Query("pickup_code", ""))
	phone := strings.TrimSpace(c.Query("phone", ""))
	limitStr := c.Query("limit", "100")

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = defaultOrdersLimit
	}
	if limit <= 0 {
		limit = defaultOrdersLimit
	}
	if limit > maxOrdersLimit {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("limit must be between 1 and %d", maxOrdersLimit),
		})
	}

	orders, err := h.dashboardService.GetOrderHistory(c.Context(), pickupCode, phone, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get order history",
		})
	}

	return c.JSON(orders)
}

// ListBartenders returns bartender accounts for manager administration.
// GET /api/admin/bartenders
func (h *DashboardHandler) ListBartenders(c *fiber.Ctx) error {
	users, err := h.dashboardService.ListBartenders(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list bartender accounts",
		})
	}

	return c.JSON(users)
}

// CreateBartender creates a new bartender account.
// POST /api/admin/bartenders
func (h *DashboardHandler) CreateBartender(c *fiber.Ctx) error {
	var req struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		PIN   string `json:"pin"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	user, err := h.dashboardService.CreateBartender(c.Context(), service.CreateBartenderInput{
		Name:  req.Name,
		Phone: req.Phone,
		PIN:   req.PIN,
	})
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "name is required"),
			strings.Contains(msg, "phone number is required"),
			strings.Contains(msg, "PIN must be exactly 4 digits"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "phone number is already used"):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.Status(fiber.StatusCreated).JSON(user)
}

// UpdateBartender updates bartender account details.
// PATCH /api/admin/bartenders/:id
func (h *DashboardHandler) UpdateBartender(c *fiber.Ctx) error {
	bartenderID := c.Params("id")
	if strings.TrimSpace(bartenderID) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "bartender ID is required",
		})
	}

	var req struct {
		Name     string `json:"name"`
		Phone    string `json:"phone"`
		IsActive *bool  `json:"is_active"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.IsActive == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "is_active is required",
		})
	}

	user, err := h.dashboardService.UpdateBartender(c.Context(), service.UpdateBartenderInput{
		ID:       bartenderID,
		Name:     req.Name,
		Phone:    req.Phone,
		IsActive: *req.IsActive,
	})
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "bartender not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only bartender accounts can be updated"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "name is required"),
			strings.Contains(msg, "phone number is required"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "phone number is already used"):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(user)
}

// ResetBartenderPIN resets a bartender PIN.
// PATCH /api/admin/bartenders/:id/pin
func (h *DashboardHandler) ResetBartenderPIN(c *fiber.Ctx) error {
	bartenderID := c.Params("id")
	if strings.TrimSpace(bartenderID) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "bartender ID is required",
		})
	}

	var req struct {
		PIN string `json:"pin"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if err := h.dashboardService.ResetBartenderPIN(c.Context(), bartenderID, req.PIN); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "bartender not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "PIN must be exactly 4 digits"),
			strings.Contains(msg, "only bartender accounts can have PIN reset"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"message": "bartender PIN reset successfully",
	})
}

// MarkOrderPreparing claims an order by transitioning PAID -> PREPARING.
// POST /api/admin/orders/:id/prepare
func (h *DashboardHandler) MarkOrderPreparing(c *fiber.Ctx) error {
	orderID := c.Params("id")
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	actorUserID, _ := c.Locals("user_id").(string)
	if err := h.dashboardService.MarkOrderPreparing(c.Context(), orderID, actorUserID); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "already being prepared by another bartender"):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only PAID orders can be marked PREPARING"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"message": "order marked as PREPARING",
	})
}

// ForceTakeoverPreparing reassigns a PREPARING order owner.
// POST /api/admin/orders/:id/prepare/takeover
func (h *DashboardHandler) ForceTakeoverPreparing(c *fiber.Ctx) error {
	orderID := c.Params("id")
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	actorUserID, _ := c.Locals("user_id").(string)
	if err := h.dashboardService.ForceTakeoverPreparing(c.Context(), orderID, actorUserID); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only PREPARING orders can be force taken over"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"message": "order preparation taken over",
	})
}

// UnlockPreparing releases PREPARING -> PAID for reassignment.
// POST /api/admin/orders/:id/prepare/unlock
func (h *DashboardHandler) UnlockPreparing(c *fiber.Ctx) error {
	orderID := c.Params("id")
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	if err := h.dashboardService.UnlockPreparing(c.Context(), orderID); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only PREPARING orders can be unlocked"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"message": "order unlocked for reassignment",
	})
}

// MarkOrderReady updates an order status from PREPARING to READY and notifies the customer.
// POST /api/admin/orders/:id/ready
func (h *DashboardHandler) MarkOrderReady(c *fiber.Ctx) error {
	orderID := c.Params("id")
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	actorUserID, _ := c.Locals("user_id").(string)
	if err := h.dashboardService.MarkOrderReady(c.Context(), orderID, actorUserID); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only PREPARING orders can be marked READY"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only the assigned bartender can notify customer"):
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"message": "order marked as READY",
	})
}

// MarkOrderComplete updates an order status from READY to COMPLETED.
// POST /api/admin/orders/:id/complete
func (h *DashboardHandler) MarkOrderComplete(c *fiber.Ctx) error {
	orderID := c.Params("id")
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	actorUserID, _ := c.Locals("user_id").(string)
	if err := h.dashboardService.MarkOrderCompleted(c.Context(), orderID, actorUserID); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "only READY orders can be marked COMPLETED"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"message": "order marked as COMPLETED",
	})
}

// GetAnalyticsOverview retrieves dashboard overview metrics
// GET /api/admin/analytics/overview
func (h *DashboardHandler) GetAnalyticsOverview(c *fiber.Ctx) error {
	analytics, err := h.dashboardService.GetAnalyticsOverview(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get analytics",
		})
	}

	return c.JSON(analytics)
}

// GetRevenueTrend retrieves revenue trend data
// GET /api/admin/analytics/revenue?days=30
func (h *DashboardHandler) GetRevenueTrend(c *fiber.Ctx) error {
	daysStr := c.Query("days", "30")
	days, err := strconv.Atoi(daysStr)
	if err != nil {
		days = 30
	}

	trends, err := h.dashboardService.GetRevenueTrend(c.Context(), days)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get revenue trend",
		})
	}

	return c.JSON(trends)
}

// GetTopProducts retrieves top-selling products
// GET /api/admin/analytics/top-products?limit=10
func (h *DashboardHandler) GetTopProducts(c *fiber.Ctx) error {
	limitStr := c.Query("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 10
	}

	products, err := h.dashboardService.GetTopProducts(c.Context(), limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get top products",
		})
	}

	return c.JSON(products)
}

// ExportDailySalesReportPDF exports a single operational business-day sales report as PDF.
// GET /api/admin/analytics/reports/daily?date=YYYY-MM-DD
func (h *DashboardHandler) ExportDailySalesReportPDF(c *fiber.Ctx) error {
	dateParam := strings.TrimSpace(c.Query("date", ""))

	pdfBytes, filename, err := h.dashboardService.GenerateDailySalesReportPDF(c.Context(), dateParam)
	if err != nil {
		status := fiber.StatusInternalServerError
		if strings.Contains(strings.ToLower(err.Error()), "invalid date format") {
			status = fiber.StatusBadRequest
		}

		return c.Status(status).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	return c.Send(pdfBytes)
}

// ExportLast30DaysSalesReportPDF exports previous 30 completed operational business days as PDF.
// GET /api/admin/analytics/reports/last-30-days
func (h *DashboardHandler) ExportLast30DaysSalesReportPDF(c *fiber.Ctx) error {
	pdfBytes, filename, err := h.dashboardService.GenerateLast30DaysSalesReportPDF(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate 30-day report",
		})
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	return c.Send(pdfBytes)
}

// SSEEvents handles Server-Sent Events for real-time updates
// GET /api/admin/events
func (h *DashboardHandler) SSEEvents(c *fiber.Ctx) error {
	// Set headers for SSE
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")

	// Create context with timeout
	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()

	// Subscribe to event bus
	subscriberID := uuid.New().String()
	eventChan := h.dashboardService.GetEventBus().Subscribe(ctx, subscriberID)

	// Send initial connection message
	c.Write([]byte("event: connected\ndata: {\"message\":\"connected\"}\n\n"))

	// Stream events
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Send heartbeat every 30 seconds
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					return
				}

				// Format and send event
				sseData, err := events.FormatSSE(event)
				if err != nil {
					fmt.Printf("Error formatting SSE: %v\n", err)
					continue
				}

				if _, err := w.Write([]byte(sseData)); err != nil {
					return
				}

				if err := w.Flush(); err != nil {
					return
				}

			case <-ticker.C:
				// Send heartbeat
				if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}

			case <-ctx.Done():
				return
			}
		}
	})

	return nil
}
