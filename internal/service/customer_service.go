package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	paymentadapter "github.com/philia-technologies/mayas-pharm/internal/adapters/payment"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/events"
	"github.com/philia-technologies/mayas-pharm/internal/observability"
	"github.com/google/uuid"
)

// CustomerService handles customer-facing ordering logic for the web ordering site.
// All operations are unauthenticated — customers are identified only by phone number.
type CustomerService struct {
	productRepo      core.ProductRepository
	orderRepo        core.OrderRepository
	userRepo         core.UserRepository
	paymentService   *PaymentService
	attemptRepo      core.PaymentAttemptRepository
	prescriptionRepo core.PrescriptionRepository
	deliveryZoneRepo core.DeliveryZoneRepository
	pesapalGateway   *paymentadapter.PesapalClient
	eventBus         *events.EventBus
	metrics          *observability.RuntimeMetrics
}

const pesapalAttemptProvider = "PESAPAL"

// NewCustomerService creates a new CustomerService.
func NewCustomerService(
	productRepo core.ProductRepository,
	orderRepo core.OrderRepository,
	userRepo core.UserRepository,
	paymentService *PaymentService,
	attemptRepo core.PaymentAttemptRepository,
	prescriptionRepo core.PrescriptionRepository,
	deliveryZoneRepo core.DeliveryZoneRepository,
	pesapalGateway *paymentadapter.PesapalClient,
	eventBus *events.EventBus,
) *CustomerService {
	return &CustomerService{
		productRepo:      productRepo,
		orderRepo:        orderRepo,
		userRepo:         userRepo,
		paymentService:   paymentService,
		attemptRepo:      attemptRepo,
		prescriptionRepo: prescriptionRepo,
		deliveryZoneRepo: deliveryZoneRepo,
		pesapalGateway:   pesapalGateway,
		eventBus:         eventBus,
	}
}

func (s *CustomerService) SetMetrics(metrics *observability.RuntimeMetrics) {
	s.metrics = metrics
}

// GetActiveProducts returns all active products grouped by category.
// This is a public endpoint — no authentication required.
func (s *CustomerService) GetActiveProducts(ctx context.Context) (map[string][]*core.Product, error) {
	return s.productRepo.GetMenu(ctx)
}

// GetActiveCategories returns a sorted list of distinct active category names.
func (s *CustomerService) GetActiveCategories(ctx context.Context) ([]string, error) {
	menu, err := s.productRepo.GetMenu(ctx)
	if err != nil {
		return nil, err
	}

	categories := make([]string, 0, len(menu))
	for cat := range menu {
		categories = append(categories, cat)
	}
	return categories, nil
}

func (s *CustomerService) GetActiveDeliveryZones(ctx context.Context) ([]*core.DeliveryZone, error) {
	if s.deliveryZoneRepo == nil {
		return nil, fmt.Errorf("delivery zones unavailable")
	}
	return s.deliveryZoneRepo.ListActive(ctx)
}

// CreateOrderInput is the payload for creating a new customer order from the web.
type CreateOrderInput struct {
	Phone               string
	TableNumber         string
	FulfillmentType     string
	DeliveryZoneID      string
	DeliveryAddress     string
	DeliveryContactName string
	DeliveryNotes       string
	IdempotencyKey      string
	Items               []OrderItemInput
}

// OrderItemInput represents a single cart line.
type OrderItemInput struct {
	ProductID string
	Quantity  int
}

// CardPaymentInitResult represents the redirect metadata returned for Pesapal checkout.
type CardPaymentInitResult struct {
	RedirectURL       string
	OrderTrackingID   string
	MerchantReference string
}

type UploadPrescriptionInput struct {
	MediaID   string
	MediaType string
	FileName  string
	Caption   string
}

// CreateOrder creates a PENDING order for a web customer.
func (s *CustomerService) CreateOrder(ctx context.Context, input CreateOrderInput) (order *core.Order, created bool, err error) {
	start := time.Now()
	defer func() {
		if s.metrics != nil {
			s.metrics.ObserveOrderCreate(created, err, time.Since(start))
		}
	}()

	// Normalise phone
	phone, err := normaliseCustomerPhone(input.Phone)
	if err != nil {
		return nil, false, fmt.Errorf("invalid phone number: %w", err)
	}

	if len(input.Items) == 0 {
		return nil, false, fmt.Errorf("cart is empty")
	}

	tableNumber := strings.TrimSpace(input.TableNumber)
	fulfillmentType := strings.ToUpper(strings.TrimSpace(input.FulfillmentType))
	if fulfillmentType == "" {
		fulfillmentType = string(core.FulfillmentTypePickup)
	}
	if tableNumber == "" {
		tableNumber = fulfillmentType
	}

	deliveryZoneID := strings.TrimSpace(input.DeliveryZoneID)
	deliveryFee := 0.0
	if fulfillmentType == string(core.FulfillmentTypeDelivery) {
		if s.deliveryZoneRepo == nil {
			return nil, false, fmt.Errorf("delivery is unavailable")
		}
		if deliveryZoneID == "" {
			return nil, false, fmt.Errorf("delivery_zone_id is required for delivery orders")
		}
		zone, zoneErr := s.deliveryZoneRepo.GetByID(ctx, deliveryZoneID)
		if zoneErr != nil {
			return nil, false, zoneErr
		}
		deliveryFee = zone.Fee
	}

	// Upsert guest user by phone
	user, err := s.userRepo.GetOrCreateByPhone(ctx, phone)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve customer: %w", err)
	}

	items := make([]core.PendingOrderItemInput, 0, len(input.Items))
	for _, item := range input.Items {
		items = append(items, core.PendingOrderItemInput{
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		})
	}

	order, created, err = s.orderRepo.CreatePendingOrder(ctx, core.CreatePendingOrderInput{
		UserID:              user.ID,
		CustomerPhone:       phone,
		TableNumber:         tableNumber,
		PaymentMethod:       string(core.PaymentMethodMpesa),
		IdempotencyKey:      strings.TrimSpace(input.IdempotencyKey),
		ExpiresAt:           time.Now().Add(20 * time.Minute),
		Items:               items,
		FulfillmentType:     fulfillmentType,
		DeliveryZoneID:      deliveryZoneID,
		DeliveryFee:         deliveryFee,
		DeliveryAddress:     strings.TrimSpace(input.DeliveryAddress),
		DeliveryContactName: strings.TrimSpace(input.DeliveryContactName),
		DeliveryNotes:       strings.TrimSpace(input.DeliveryNotes),
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to save order: %w", err)
	}

	return order, created, nil
}

func (s *CustomerService) UploadPrescription(ctx context.Context, orderID string, input UploadPrescriptionInput) (*core.Prescription, error) {
	if s.prescriptionRepo == nil {
		return nil, fmt.Errorf("prescription uploads are unavailable")
	}
	order, err := s.orderRepo.GetByID(ctx, strings.TrimSpace(orderID))
	if err != nil {
		return nil, fmt.Errorf("order not found")
	}
	if !order.ReviewRequired && order.Status != core.OrderStatusPendingReview {
		return nil, fmt.Errorf("order does not require prescription review")
	}
	prescription := &core.Prescription{
		ID:            uuid.New().String(),
		OrderID:       order.ID,
		CustomerPhone: order.CustomerPhone,
		MediaID:       strings.TrimSpace(input.MediaID),
		MediaType:     strings.TrimSpace(input.MediaType),
		FileName:      strings.TrimSpace(input.FileName),
		Caption:       strings.TrimSpace(input.Caption),
		Status:        core.PrescriptionStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if prescription.MediaID == "" || prescription.MediaType == "" {
		return nil, fmt.Errorf("media_id and media_type are required")
	}
	if err := s.prescriptionRepo.Create(ctx, prescription); err != nil {
		return nil, err
	}
	if s.eventBus != nil {
		if updated, getErr := s.orderRepo.GetByID(ctx, order.ID); getErr == nil {
			s.eventBus.PublishOrderUpdated(updated)
		}
	}
	return prescription, nil
}

// InitiateSTKPush triggers a Kopo Kopo M-Pesa STK push for an existing PENDING order.
func (s *CustomerService) InitiateSTKPush(ctx context.Context, orderID string, phone string, idempotencyKey string) (*core.PaymentAttempt, bool, error) {
	if s.paymentService == nil {
		return nil, false, fmt.Errorf("payment service unavailable")
	}

	if s.attemptRepo != nil {
		existing, err := s.attemptRepo.GetByIdempotencyKey(ctx, strings.TrimSpace(idempotencyKey))
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			attempt, attachErr := s.paymentService.attachQueueMetrics(ctx, existing)
			return attempt, false, attachErr
		}

		existing, err = s.attemptRepo.GetActiveByOrderID(ctx, orderID, string(core.PaymentMethodMpesa))
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			attempt, attachErr := s.paymentService.attachQueueMetrics(ctx, existing)
			return attempt, false, attachErr
		}
	}

	attempt, err := s.paymentService.QueueMPESA(ctx, orderID, phone, idempotencyKey)
	if err != nil {
		return nil, false, err
	}
	return attempt, true, nil
}

// InitiatePesapalPayment creates a Pesapal checkout link for Airtel Money/Card payments.
func (s *CustomerService) InitiatePesapalPayment(ctx context.Context, orderID string, callbackURL string) (*CardPaymentInitResult, error) {
	if s.pesapalGateway == nil {
		return nil, fmt.Errorf("Airtel Money/Card payments are currently unavailable")
	}

	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("order not found")
	}

	if order.Status == core.OrderStatusPendingReview {
		return nil, fmt.Errorf("order is awaiting pharmacist review")
	}
	if order.Status != core.OrderStatusPending {
		return nil, fmt.Errorf("order is not in a payable state (status: %s)", order.Status)
	}

	result, err := s.pesapalGateway.InitiatePayment(ctx, paymentadapter.PesapalInitiateInput{
		OrderID:     order.ID,
		Amount:      order.TotalAmount,
		Description: fmt.Sprintf("Order %s at Maya's Pharm", order.PickupCode),
		CallbackURL: callbackURL,
		Phone:       order.CustomerPhone,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initiate Airtel Money/Card payment: %w", err)
	}

	// Persist payment metadata before redirecting customer.
	paymentRef := strings.TrimSpace(result.OrderTrackingID)
	if paymentRef == "" {
		paymentRef = strings.TrimSpace(result.MerchantReference)
	}
	if err := s.orderRepo.UpdatePaymentDetails(ctx, order.ID, pesapalAttemptProvider, paymentRef); err != nil {
		return nil, fmt.Errorf("failed to save Pesapal payment details: %w", err)
	}

	if s.attemptRepo != nil {
		_, _, _ = s.attemptRepo.Create(ctx, &core.PaymentAttempt{
			ID:                uuid.New().String(),
			OrderID:           order.ID,
			Provider:          pesapalAttemptProvider,
			Status:            core.PaymentAttemptStatusAwaitingCustomer,
			RequestedPhone:    order.CustomerPhone,
			Amount:            order.TotalAmount,
			ProviderReference: paymentRef,
			CheckoutURL:       result.RedirectURL,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		})
	}

	return &CardPaymentInitResult{
		RedirectURL:       result.RedirectURL,
		OrderTrackingID:   result.OrderTrackingID,
		MerchantReference: result.MerchantReference,
	}, nil
}

// ReconcilePesapalPayment verifies callback/IPN status with Pesapal and updates order state.
func (s *CustomerService) ReconcilePesapalPayment(ctx context.Context, orderTrackingID string, merchantReference string) (*core.Order, error) {
	if s.pesapalGateway == nil {
		return nil, fmt.Errorf("Airtel Money/Card payments are currently unavailable")
	}

	merchantReference = strings.TrimSpace(merchantReference)
	if merchantReference == "" {
		return nil, fmt.Errorf("merchant reference is required")
	}

	order, err := s.orderRepo.GetByID(ctx, merchantReference)
	if err != nil {
		return nil, fmt.Errorf("order not found")
	}

	status, err := s.pesapalGateway.GetTransactionStatus(ctx, orderTrackingID)
	if err != nil {
		return nil, fmt.Errorf("failed to verify Pesapal payment: %w", err)
	}

	next := classifyPesapalStatus(status.StatusCode, status.PaymentStatusDescription)
	paymentMethod := normalizePesapalPaymentMethod(status.PaymentMethod)
	if paymentMethod == "" {
		paymentMethod = pesapalAttemptProvider
	}

	// Always refresh payment metadata when reconciliation succeeds.
	ref := strings.TrimSpace(status.OrderTrackingID)
	if ref == "" {
		ref = strings.TrimSpace(orderTrackingID)
	}
	_ = s.orderRepo.UpdatePaymentDetails(ctx, order.ID, paymentMethod, ref)

	switch next {
	case core.OrderStatusPaid:
		if order.Status == core.OrderStatusFailed || order.Status == core.OrderStatusCancelled {
			if err := s.orderRepo.UpdateStatus(ctx, order.ID, core.OrderStatusPending); err != nil {
				return nil, fmt.Errorf("failed to reopen order for Pesapal confirmation: %w", err)
			}
			order, err = s.orderRepo.GetByID(ctx, order.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to reload reopened order: %w", err)
			}
		}
		if s.attemptRepo != nil {
			attempt, attemptErr := s.attemptRepo.GetByProviderReference(ctx, pesapalAttemptProvider, ref)
			if attemptErr == nil && attempt != nil {
				_ = s.attemptRepo.MarkSucceeded(ctx, attempt.ID, ref)
			}
		}
		if order.Status == core.OrderStatusPending || order.Status == core.OrderStatusFailed {
			if _, _, err := s.orderRepo.MarkPaid(ctx, order.ID, paymentMethod, ref); err != nil {
				return nil, fmt.Errorf("failed to mark order as paid: %w", err)
			}
			if s.eventBus != nil {
				updated, getErr := s.orderRepo.GetByID(ctx, order.ID)
				if getErr == nil {
					if s.paymentService != nil {
						s.paymentService.notifyPaymentReceived(updated)
					}
					s.eventBus.PublishNewOrder(updated)
					s.eventBus.PublishOrderUpdated(updated)
					return updated, nil
				}
			}
		}
	case core.OrderStatusFailed:
		if s.attemptRepo != nil {
			attempt, attemptErr := s.attemptRepo.GetByProviderReference(ctx, pesapalAttemptProvider, ref)
			if attemptErr == nil && attempt != nil {
				_ = s.attemptRepo.MarkFailed(ctx, attempt.ID, ref, status.PaymentStatusDescription)
			}
		}
	}

	updated, err := s.orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load reconciled order: %w", err)
	}
	return updated, nil
}

// GetOrderStatus returns the current status, pickup code, and items for an order.
// Used by the frontend polling loop.
func (s *CustomerService) GetOrderStatus(ctx context.Context, orderID string) (*core.Order, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("order not found")
	}
	return order, nil
}

func (s *CustomerService) GetLatestPaymentAttempt(ctx context.Context, orderID string) (*core.PaymentAttempt, error) {
	if s.paymentService != nil {
		return s.paymentService.GetLatestPaymentAttempt(ctx, orderID)
	}
	if s.attemptRepo == nil {
		return nil, nil
	}
	return s.attemptRepo.GetLatestByOrderID(ctx, orderID)
}

// normaliseCustomerPhone converts Kenyan phone formats to 254xxxxxxxxx.
// Accepts: 07xxxxxxxx, 7xxxxxxxx, 2547xxxxxxxx, +2547xxxxxxxx
func normaliseCustomerPhone(phone string) (string, error) {
	phone = strings.TrimSpace(phone)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.TrimPrefix(phone, "+")

	if strings.HasPrefix(phone, "0") {
		phone = "254" + phone[1:]
	} else if !strings.HasPrefix(phone, "254") {
		phone = "254" + phone
	}

	if len(phone) != 12 {
		return "", fmt.Errorf("phone must be 12 digits in format 254xxxxxxxxx, got %d digits", len(phone))
	}

	for _, c := range phone {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("phone number must contain only digits")
		}
	}

	prefix := phone[3:4]
	if prefix != "7" && prefix != "1" {
		return "", fmt.Errorf("not a valid Kenyan mobile number")
	}

	return phone, nil
}

func classifyPesapalStatus(statusCode int, statusDescription string) core.OrderStatus {
	desc := strings.ToUpper(strings.TrimSpace(statusDescription))

	switch statusCode {
	case 1:
		return core.OrderStatusPaid
	case 2, 3:
		return core.OrderStatusFailed
	}

	switch {
	case strings.Contains(desc, "COMPLETED"), strings.Contains(desc, "PAID"):
		return core.OrderStatusPaid
	case strings.Contains(desc, "FAILED"), strings.Contains(desc, "CANCEL"), strings.Contains(desc, "INVALID"), strings.Contains(desc, "REVERSE"):
		return core.OrderStatusFailed
	default:
		return core.OrderStatusPending
	}
}

func normalizePesapalPaymentMethod(method string) string {
	normalized := strings.ToUpper(strings.TrimSpace(method))
	if normalized == "" {
		return ""
	}

	replacer := strings.NewReplacer("-", "_", " ", "_")
	normalized = replacer.Replace(normalized)

	switch {
	case strings.Contains(normalized, "AIRTEL"):
		return "AIRTEL_MONEY"
	case strings.Contains(normalized, "MPESA"), strings.Contains(normalized, "M_PESA"):
		return string(core.PaymentMethodMpesa)
	case strings.Contains(normalized, "CARD"),
		strings.Contains(normalized, "VISA"),
		strings.Contains(normalized, "MASTERCARD"),
		strings.Contains(normalized, "AMEX"),
		strings.Contains(normalized, "AMERICAN_EXPRESS"),
		strings.Contains(normalized, "DISCOVER"),
		strings.Contains(normalized, "DINERS"),
		strings.Contains(normalized, "UNIONPAY"),
		strings.Contains(normalized, "JCB"):
		return string(core.PaymentMethodCard)
	default:
		if len(normalized) > 20 {
			return normalized[:20]
		}
		return normalized
	}
}
