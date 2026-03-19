package core

import (
	"context"
	"time"
)

// ProductRepository defines the interface for product data access.
type ProductRepository interface {
	GetByID(ctx context.Context, id string) (*Product, error)
	GetByCategory(ctx context.Context, category string) ([]*Product, error)
	GetAll(ctx context.Context) ([]*Product, error)
	GetMenu(ctx context.Context) (map[string][]*Product, error)
	UpdateStock(ctx context.Context, id string, quantity int) error
	UpdatePrice(ctx context.Context, id string, price float64) error
	SearchProducts(ctx context.Context, query string) ([]*Product, error)
}

// CategoryRepository manages pharmacy category metadata.
type CategoryRepository interface {
	ListActive(ctx context.Context) ([]*Category, error)
}

// OrderRepository defines the interface for order data access.
type OrderRepository interface {
	CreateOrder(ctx context.Context, order *Order) error
	CreatePendingOrder(ctx context.Context, input CreatePendingOrderInput) (*Order, bool, error)
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*Order, error)
	GenerateNextPickupCode(ctx context.Context) (string, error)
	GetByID(ctx context.Context, id string) (*Order, error)
	GetByUserID(ctx context.Context, userID string) ([]*Order, error)
	GetByPhone(ctx context.Context, phone string) ([]*Order, error)
	GetByDateRangeAndStatuses(ctx context.Context, start time.Time, end time.Time, statuses []OrderStatus) ([]*Order, error)
	UpdateStatus(ctx context.Context, id string, status OrderStatus) error
	UpdateStatusWithActor(ctx context.Context, id string, status OrderStatus, actorUserID string) error
	MarkPaid(ctx context.Context, id string, paymentMethod string, paymentRef string) (*Order, bool, error)
	ExpirePendingOrders(ctx context.Context, now time.Time, limit int) ([]*Order, error)
	UpdateCustomerPhone(ctx context.Context, id string, phone string) error
	UpdatePaymentDetails(ctx context.Context, id string, paymentMethod string, paymentRef string) error
	GetAllWithFilters(ctx context.Context, status string, limit int, updatedAfter *time.Time) ([]*Order, error)
	GetCompletedHistory(ctx context.Context, pickupCode string, phone string, limit int) ([]*Order, error)
	FindPendingByPhoneAndAmount(ctx context.Context, phone string, amount float64) (*Order, error)
	FindPendingByHashedPhoneAndAmount(ctx context.Context, hashedPhone string, amount float64) (*Order, error)
	FindPendingByAmount(ctx context.Context, amount float64) (*Order, error)
	ClaimPreparing(ctx context.Context, id string, actorUserID string) error
	ForceTakeoverPreparing(ctx context.Context, id string, actorUserID string) error
	UnlockPreparing(ctx context.Context, id string) error
	MarkReadyFromPreparing(ctx context.Context, id string, actorUserID string) error
}

// UserRepository defines the interface for user data access.
type UserRepository interface {
	GetByID(ctx context.Context, id string) (*User, error)
	GetByPhone(ctx context.Context, phone string) (*User, error)
	Create(ctx context.Context, user *User) error
	GetOrCreateByPhone(ctx context.Context, phone string) (*User, error)
}

// PaymentAttemptRepository defines durable checkout queue and reconciliation storage.
type PaymentAttemptRepository interface {
	Create(ctx context.Context, attempt *PaymentAttempt) (*PaymentAttempt, bool, error)
	GetByID(ctx context.Context, id string) (*PaymentAttempt, error)
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*PaymentAttempt, error)
	GetByOrderID(ctx context.Context, orderID string) ([]*PaymentAttempt, error)
	GetLatestByOrderID(ctx context.Context, orderID string) (*PaymentAttempt, error)
	GetActiveByOrderID(ctx context.Context, orderID string, provider string) (*PaymentAttempt, error)
	GetByProviderReference(ctx context.Context, provider string, reference string) (*PaymentAttempt, error)
	ClaimNextQueued(ctx context.Context, provider string, throttleWindow time.Duration) (*PaymentAttempt, error)
	MarkAwaitingCustomer(ctx context.Context, id string, providerReference string) error
	MarkRetryQueued(ctx context.Context, id string, nextRetryAt time.Time, lastError string) error
	MarkFailed(ctx context.Context, id string, providerReference string, lastError string) error
	MarkSucceeded(ctx context.Context, id string, providerReference string) error
	ExpireActiveForOrder(ctx context.Context, orderID string, now time.Time) error
	GetQueueMetrics(ctx context.Context, attemptID string, provider string, dispatchInterval time.Duration) (int, time.Duration, error)
	GetQueueStats(ctx context.Context, provider string) (*PaymentQueueStats, error)
}

// UnmatchedPaymentRepository stores payments that require manual reconciliation.
type UnmatchedPaymentRepository interface {
	Create(ctx context.Context, payment *UnmatchedPayment) error
	GetByID(ctx context.Context, id string) (*UnmatchedPayment, error)
	ListPending(ctx context.Context, limit int) ([]*UnmatchedPayment, error)
	CountPending(ctx context.Context) (int, error)
	Resolve(ctx context.Context, id string, orderID string, note string) (*UnmatchedPayment, error)
	Reject(ctx context.Context, id string, note string) (*UnmatchedPayment, error)
}

// PrescriptionRepository manages order-linked prescription submissions.
type PrescriptionRepository interface {
	Create(ctx context.Context, prescription *Prescription) error
	GetByID(ctx context.Context, id string) (*Prescription, error)
	ListByOrderID(ctx context.Context, orderID string) ([]*Prescription, error)
	ListPending(ctx context.Context, limit int) ([]*Prescription, error)
	UpdateStatus(ctx context.Context, id string, status string, reviewedBy string, notes string) error
	CreateReview(ctx context.Context, review *PrescriptionReview) error
}

// DeliveryZoneRepository manages flat-fee delivery pricing.
type DeliveryZoneRepository interface {
	ListActive(ctx context.Context) ([]*DeliveryZone, error)
	GetByID(ctx context.Context, id string) (*DeliveryZone, error)
	Upsert(ctx context.Context, zone *DeliveryZone) error
}

// BusinessHoursRepository manages operating-hour rules used by the bot.
type BusinessHoursRepository interface {
	List(ctx context.Context) ([]*BusinessHours, error)
	Upsert(ctx context.Context, hours *BusinessHours) error
}

// OutboundMessageRepository manages durable outgoing notification records.
type OutboundMessageRepository interface {
	Create(ctx context.Context, message *OutboundMessage) error
}

// AuditLogRepository manages immutable action logs.
type AuditLogRepository interface {
	Create(ctx context.Context, log *AuditLog) error
}

// SessionRepository defines the interface for session state management in Redis.
type SessionRepository interface {
	Get(ctx context.Context, phone string) (*Session, error)
	Set(ctx context.Context, phone string, session *Session, ttl int) error
	Delete(ctx context.Context, phone string) error
	UpdateStep(ctx context.Context, phone string, step string) error
	UpdateCart(ctx context.Context, phone string, cartItems string) error
}

// Button represents a quick reply button.
type Button struct {
	ID    string
	Title string
}

// WhatsAppGateway defines the interface for WhatsApp messaging.
type WhatsAppGateway interface {
	SendText(ctx context.Context, phone string, message string) error
	SendMenu(ctx context.Context, phone string, products []*Product) error
	SendCategoryList(ctx context.Context, phone string, categories []string) error
	SendCategoryListWithText(ctx context.Context, phone string, text string, categories []string) error
	SendProductList(ctx context.Context, phone string, category string, products []*Product) error
	SendMenuButtons(ctx context.Context, phone string, text string, buttons []Button) error
}

// PaymentGateway defines the interface for payment processing.
type PaymentGateway interface {
	InitiateSTKPush(ctx context.Context, orderID string, phone string, amount float64) error
	VerifyWebhook(ctx context.Context, signature string, payload []byte) bool
	ProcessWebhook(ctx context.Context, payload []byte) (*PaymentWebhook, error)
}

// PaymentWebhook represents the structure of a payment webhook result.
type PaymentWebhook struct {
	OrderID     string
	Status      string
	Reference   string
	Amount      float64
	Phone       string
	HashedPhone string
	Success     bool
}

// AdminUserRepository defines the interface for staff user data access.
type AdminUserRepository interface {
	GetByID(ctx context.Context, id string) (*AdminUser, error)
	GetByPhone(ctx context.Context, phone string) (*AdminUser, error)
	GetByRole(ctx context.Context, role string) ([]*AdminUser, error)
	GetActiveByRole(ctx context.Context, role string) ([]*AdminUser, error)
	GetActiveBartenders(ctx context.Context) ([]*AdminUser, error)
	Update(ctx context.Context, user *AdminUser) error
	UpdatePinHash(ctx context.Context, userID string, pinHash string) error
	Create(ctx context.Context, user *AdminUser) error
	IsActive(ctx context.Context, phone string) (bool, error)
}

// StaffUserRepository is an alias retained for the new pharmacy naming.
type StaffUserRepository = AdminUserRepository

// OTPRepository defines the interface for OTP code management.
type OTPRepository interface {
	Create(ctx context.Context, otp *OTPCode) error
	GetLatestByPhone(ctx context.Context, phone string) (*OTPCode, error)
	MarkAsVerified(ctx context.Context, id string) error
	CleanupExpired(ctx context.Context) error
}

// AnalyticsRepository defines the interface for analytics data access.
type AnalyticsRepository interface {
	GetOverview(ctx context.Context) (*Analytics, error)
	GetRevenueTrend(ctx context.Context, days int) ([]*RevenueTrend, error)
	GetTopProducts(ctx context.Context, limit int) ([]*TopProduct, error)
}
