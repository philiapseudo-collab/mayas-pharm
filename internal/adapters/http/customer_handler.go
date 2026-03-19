package http

import (
	"context"
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/service"
	"github.com/gofiber/fiber/v2"
)

type customerService interface {
	GetActiveProducts(ctx context.Context) (map[string][]*core.Product, error)
	GetActiveCategories(ctx context.Context) ([]string, error)
	GetActiveDeliveryZones(ctx context.Context) ([]*core.DeliveryZone, error)
	CreateOrder(ctx context.Context, input service.CreateOrderInput) (*core.Order, bool, error)
	UploadPrescription(ctx context.Context, orderID string, input service.UploadPrescriptionInput) (*core.Prescription, error)
	InitiateSTKPush(ctx context.Context, orderID string, phone string, idempotencyKey string) (*core.PaymentAttempt, bool, error)
	InitiatePesapalPayment(ctx context.Context, orderID string, callbackURL string) (*service.CardPaymentInitResult, error)
	ReconcilePesapalPayment(ctx context.Context, orderTrackingID string, merchantReference string) (*core.Order, error)
	GetOrderStatus(ctx context.Context, orderID string) (*core.Order, error)
	GetLatestPaymentAttempt(ctx context.Context, orderID string) (*core.PaymentAttempt, error)
}

// CustomerHandler handles public HTTP requests from the web ordering site.
// No authentication is required for any of these endpoints.
type CustomerHandler struct {
	customerService customerService
}

// NewCustomerHandler creates a new CustomerHandler.
func NewCustomerHandler(customerService customerService) *CustomerHandler {
	return &CustomerHandler{customerService: customerService}
}

// GetMenu returns all active products grouped by category.
// GET /api/menu
func (h *CustomerHandler) GetMenu(c *fiber.Ctx) error {
	menu, err := h.customerService.GetActiveProducts(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load menu",
		})
	}
	return c.JSON(menu)
}

// GetCategories returns a list of active category names.
// GET /api/menu/categories
func (h *CustomerHandler) GetCategories(c *fiber.Ctx) error {
	categories, err := h.customerService.GetActiveCategories(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load categories",
		})
	}
	return c.JSON(categories)
}

// GetDeliveryZones returns active delivery zones for checkout.
// GET /api/customer/delivery-zones
func (h *CustomerHandler) GetDeliveryZones(c *fiber.Ctx) error {
	zones, err := h.customerService.GetActiveDeliveryZones(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load delivery zones",
		})
	}
	return c.JSON(zones)
}

// CreateOrder creates a new PENDING order from the customer's cart.
// POST /api/customer/orders
func (h *CustomerHandler) CreateOrder(c *fiber.Ctx) error {
	idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Idempotency-Key header is required",
		})
	}

	var req struct {
		Phone               string `json:"phone"`
		TableNumber         string `json:"table_number"`
		FulfillmentType     string `json:"fulfillment_type"`
		DeliveryZoneID      string `json:"delivery_zone_id"`
		DeliveryAddress     string `json:"delivery_address"`
		DeliveryContactName string `json:"delivery_contact_name"`
		DeliveryNotes       string `json:"delivery_notes"`
		Items               []struct {
			ProductID string `json:"product_id"`
			Quantity  int    `json:"quantity"`
		} `json:"items"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if strings.TrimSpace(req.Phone) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "phone number is required",
		})
	}
	if len(req.Items) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "cart is empty",
		})
	}

	items := make([]service.OrderItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		if strings.TrimSpace(it.ProductID) == "" || it.Quantity <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "each item must have a valid product_id and quantity > 0",
			})
		}
		items = append(items, service.OrderItemInput{
			ProductID: it.ProductID,
			Quantity:  it.Quantity,
		})
	}

	order, created, err := h.customerService.CreateOrder(c.Context(), service.CreateOrderInput{
		Phone:               req.Phone,
		TableNumber:         req.TableNumber,
		FulfillmentType:     req.FulfillmentType,
		DeliveryZoneID:      req.DeliveryZoneID,
		DeliveryAddress:     req.DeliveryAddress,
		DeliveryContactName: req.DeliveryContactName,
		DeliveryNotes:       req.DeliveryNotes,
		IdempotencyKey:      idempotencyKey,
		Items:               items,
	})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid phone") ||
			strings.Contains(msg, "cart is empty") ||
			strings.Contains(msg, "product not found") ||
			strings.Contains(msg, "insufficient stock") ||
			strings.Contains(msg, "no longer available") ||
			strings.Contains(msg, "invalid quantity") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to place order"})
	}

	attempt, _ := h.customerService.GetLatestPaymentAttempt(c.Context(), order.ID)

	statusCode := fiber.StatusCreated
	if !created {
		statusCode = fiber.StatusOK
	}

	return c.Status(statusCode).JSON(buildCustomerOrderResponse(order, attempt))
}

// UploadPrescription stores a prescription media reference for an order awaiting review.
// POST /api/customer/orders/:id/prescriptions
func (h *CustomerHandler) UploadPrescription(c *fiber.Ctx) error {
	orderID := strings.TrimSpace(c.Params("id"))
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "order ID is required"})
	}

	var req struct {
		MediaID   string `json:"media_id"`
		MediaType string `json:"media_type"`
		FileName  string `json:"file_name"`
		Caption   string `json:"caption"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	prescription, err := h.customerService.UploadPrescription(c.Context(), orderID, service.UploadPrescriptionInput{
		MediaID:   req.MediaID,
		MediaType: req.MediaType,
		FileName:  req.FileName,
		Caption:   req.Caption,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(prescription)
}

// PayWithMpesa triggers an M-Pesa STK push for an existing PENDING order.
// POST /api/customer/orders/:id/pay/mpesa
func (h *CustomerHandler) PayWithMpesa(c *fiber.Ctx) error {
	orderID := strings.TrimSpace(c.Params("id"))
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Idempotency-Key header is required",
		})
	}

	var req struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if strings.TrimSpace(req.Phone) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "phone is required for M-Pesa STK push",
		})
	}

	attempt, created, err := h.customerService.InitiateSTKPush(c.Context(), orderID, req.Phone, idempotencyKey)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "order not found") {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		}
		if strings.Contains(msg, "not in a payable state") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		}
		if strings.Contains(msg, "invalid phone") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to initiate payment"})
	}

	order, orderErr := h.customerService.GetOrderStatus(c.Context(), orderID)
	if orderErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "payment queued but failed to load order",
		})
	}

	statusCode := fiber.StatusCreated
	if !created {
		statusCode = fiber.StatusOK
	}

	return c.Status(statusCode).JSON(buildCustomerOrderResponse(order, attempt))
}

// PayWithPesapal initiates an Airtel Money/Card payment and returns a redirect URL.
// POST /api/customer/orders/:id/pay/pesapal
func (h *CustomerHandler) PayWithPesapal(c *fiber.Ctx) error {
	orderID := strings.TrimSpace(c.Params("id"))
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	var req struct {
		CallbackURL string `json:"callback_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		// Empty body is allowed.
		req.CallbackURL = ""
	}

	result, err := h.customerService.InitiatePesapalPayment(c.Context(), orderID, strings.TrimSpace(req.CallbackURL))
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "order not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "not in a payable state"):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "unavailable"):
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to initiate Airtel Money/Card payment"})
		}
	}

	return c.JSON(fiber.Map{
		"status":             "pending",
		"redirect_url":       result.RedirectURL,
		"order_tracking_id":  result.OrderTrackingID,
		"merchant_reference": result.MerchantReference,
	})
}

// HandlePesapalWebhook reconciles payment state from Pesapal IPN callbacks.
// GET /api/webhooks/pesapal
func (h *CustomerHandler) HandlePesapalWebhook(c *fiber.Ctx) error {
	orderTrackingID := firstNonEmpty(
		c.Query("OrderTrackingId"),
		c.Query("OrderTrackingID"),
		c.Query("orderTrackingId"),
		c.Query("order_tracking_id"),
	)
	merchantReference := firstNonEmpty(
		c.Query("OrderMerchantReference"),
		c.Query("orderMerchantReference"),
		c.Query("order_merchant_reference"),
	)
	notificationType := firstNonEmpty(
		c.Query("OrderNotificationType"),
		c.Query("orderNotificationType"),
		c.Query("order_notification_type"),
	)

	if strings.TrimSpace(orderTrackingID) == "" || strings.TrimSpace(merchantReference) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "OrderTrackingId and OrderMerchantReference are required",
		})
	}

	order, err := h.customerService.ReconcilePesapalPayment(c.Context(), orderTrackingID, merchantReference)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "order not found"):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
		case strings.Contains(msg, "unavailable"):
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": msg})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
		}
	}

	return c.JSON(fiber.Map{
		"orderNotificationType":  firstNonEmpty(notificationType, "IPNCHANGE"),
		"orderTrackingId":        orderTrackingID,
		"orderMerchantReference": merchantReference,
		"status":                 200,
		"order_status":           order.Status,
	})
}

// GetOrderStatus returns the current status and pickup code for an order.
// GET /api/customer/orders/:id/status
func (h *CustomerHandler) GetOrderStatus(c *fiber.Ctx) error {
	orderID := strings.TrimSpace(c.Params("id"))
	if orderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "order ID is required",
		})
	}

	order, err := h.customerService.GetOrderStatus(c.Context(), orderID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "order not found",
		})
	}

	attempt, err := h.customerService.GetLatestPaymentAttempt(c.Context(), orderID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load payment status",
		})
	}

	return c.JSON(buildCustomerOrderResponse(order, attempt))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func buildCustomerOrderResponse(order *core.Order, attempt *core.PaymentAttempt) fiber.Map {
	response := fiber.Map{
		"id":                 order.ID,
		"customer_phone":     order.CustomerPhone,
		"status":             order.Status,
		"pickup_code":        order.PickupCode,
		"total_amount":       order.TotalAmount,
		"table_number":       order.TableNumber,
		"fulfillment_type":   order.FulfillmentType,
		"delivery_zone_id":   order.DeliveryZoneID,
		"delivery_zone_name": order.DeliveryZoneName,
		"delivery_fee":       order.DeliveryFee,
		"delivery_address":   order.DeliveryAddress,
		"review_required":    order.ReviewRequired,
		"prescription_count": order.PrescriptionCount,
		"payment_method":     order.PaymentMethod,
		"items":              order.Items,
		"created_at":         order.CreatedAt,
		"updated_at":         order.UpdatedAt,
		"expires_at":         order.ExpiresAt,
		"payment_status":     "",
		"payment_attempt_id": nil,
		"queue_position":     0,
		"eta_seconds":        0,
	}

	if attempt != nil {
		response["payment_status"] = attempt.Status
		response["payment_attempt_id"] = attempt.ID
		response["queue_position"] = attempt.QueuePosition
		response["eta_seconds"] = attempt.ETASeconds
		return response
	}

	switch order.Status {
	case core.OrderStatusPaid, core.OrderStatusPreparing, core.OrderStatusReady, core.OrderStatusOutForDelivery, core.OrderStatusCompleted:
		response["payment_status"] = core.PaymentAttemptStatusSucceeded
	case core.OrderStatusFailed, core.OrderStatusCancelled:
		response["payment_status"] = core.PaymentAttemptStatusFailed
	case core.OrderStatusExpired:
		response["payment_status"] = core.PaymentAttemptStatusExpired
	case core.OrderStatusPendingReview:
		response["payment_status"] = "AWAITING_REVIEW"
	}

	return response
}
