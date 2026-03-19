package core

import "time"

// Category represents a customer-visible grouping of pharmacy products.
type Category struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	SortOrder   int       `json:"sort_order"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Product represents a pharmacy item in the catalog.
type Product struct {
	ID                   string    `json:"id"`
	CategoryID           string    `json:"category_id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	Price                float64   `json:"price"`
	Category             string    `json:"category"`
	StockQuantity        int       `json:"stock_quantity"`
	ImageURL             string    `json:"image_url"`
	IsActive             bool      `json:"is_active"`
	SKU                  string    `json:"sku"`
	BrandName            string    `json:"brand_name"`
	GenericName          string    `json:"generic_name"`
	Strength             string    `json:"strength"`
	DosageForm           string    `json:"dosage_form"`
	PackSize             string    `json:"pack_size"`
	Unit                 string    `json:"unit"`
	ActiveIngredient     string    `json:"active_ingredient"`
	RequiresPrescription bool      `json:"requires_prescription"`
	IsControlled         bool      `json:"is_controlled"`
	PriceSource          string    `json:"price_source"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

// Order represents a customer order.
type Order struct {
	ID                  string         `json:"id"`
	UserID              string         `json:"user_id"`
	CustomerPhone       string         `json:"customer_phone"`
	TableNumber         string         `json:"table_number"`
	TotalAmount         float64        `json:"total_amount"`
	Status              OrderStatus    `json:"status"`
	PaymentMethod       string         `json:"payment_method"`
	PaymentRef          string         `json:"payment_reference"`
	PickupCode          string         `json:"pickup_code"`
	FulfillmentType     string         `json:"fulfillment_type"`
	DeliveryZoneID      string         `json:"delivery_zone_id,omitempty"`
	DeliveryZoneName    string         `json:"delivery_zone_name,omitempty"`
	DeliveryFee         float64        `json:"delivery_fee"`
	DeliveryAddress     string         `json:"delivery_address,omitempty"`
	DeliveryContactName string         `json:"delivery_contact_name,omitempty"`
	DeliveryNotes       string         `json:"delivery_notes,omitempty"`
	ReviewRequired      bool           `json:"review_required"`
	ReviewNotes         string         `json:"review_notes,omitempty"`
	ReviewedAt          *time.Time     `json:"reviewed_at,omitempty"`
	ReviewedByUserID    string         `json:"reviewed_by_user_id,omitempty"`
	PrescriptionCount   int            `json:"prescription_count"`
	PaidAt              *time.Time     `json:"paid_at,omitempty"`
	PreparingAt         *time.Time     `json:"preparing_at,omitempty"`
	PreparingByUserID   string         `json:"preparing_by_user_id,omitempty"`
	PreparingByName     string         `json:"preparing_by_name,omitempty"`
	PreparingByCode     string         `json:"preparing_by_code,omitempty"`
	ReadyAt             *time.Time     `json:"ready_at,omitempty"`
	ReadyByUserID       string         `json:"ready_by_user_id,omitempty"`
	ReadyByName         string         `json:"ready_by_name,omitempty"`
	ReadyByCode         string         `json:"ready_by_code,omitempty"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
	CompletedByUserID   string         `json:"completed_by_user_id,omitempty"`
	CompletedByName     string         `json:"completed_by_name,omitempty"`
	CompletedByCode     string         `json:"completed_by_code,omitempty"`
	ExpiresAt           *time.Time     `json:"expires_at,omitempty"`
	Items               []OrderItem    `json:"items"`
	Prescriptions       []Prescription `json:"prescriptions,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

// OrderItem represents a single item in an order.
type OrderItem struct {
	ID                   string  `json:"id"`
	OrderID              string  `json:"order_id"`
	ProductID            string  `json:"product_id"`
	Quantity             int     `json:"quantity"`
	PriceAtTime          float64 `json:"price_at_time"`
	ProductName          string  `json:"product_name" gorm:"-"`
	RequiresPrescription bool    `json:"requires_prescription"`
}

// OrderStatus represents the state of an order.
type OrderStatus string

const (
	OrderStatusPendingReview           OrderStatus = "PENDING_REVIEW"
	OrderStatusApprovedAwaitingPayment OrderStatus = "APPROVED_AWAITING_PAYMENT"
	OrderStatusPaid                    OrderStatus = "PAID"
	OrderStatusPacking                 OrderStatus = "PACKING"
	OrderStatusReady                   OrderStatus = "READY"
	OrderStatusOutForDelivery          OrderStatus = "OUT_FOR_DELIVERY"
	OrderStatusCompleted               OrderStatus = "COMPLETED"
	OrderStatusRejected                OrderStatus = "REJECTED"
	OrderStatusFailed                  OrderStatus = "FAILED"
	OrderStatusCancelled               OrderStatus = "CANCELLED"
	OrderStatusExpired                 OrderStatus = "EXPIRED"
)

// Compatibility aliases retained while the old queue/order code is being reused.
const (
	OrderStatusPending   = OrderStatusApprovedAwaitingPayment
	OrderStatusPreparing = OrderStatusPacking
)

// PaymentMethod represents the payment method used.
type PaymentMethod string

const (
	PaymentMethodMpesa       PaymentMethod = "MPESA"
	PaymentMethodCard        PaymentMethod = "CARD"
	PaymentMethodCash        PaymentMethod = "CASH"
	PaymentMethodAirtelMoney PaymentMethod = "AIRTEL_MONEY"
)

// FulfillmentType describes how the order is completed.
type FulfillmentType string

const (
	FulfillmentTypePickup   FulfillmentType = "PICKUP"
	FulfillmentTypeDelivery FulfillmentType = "DELIVERY"
)

// User represents a customer in the system.
type User struct {
	ID          string    `json:"id"`
	PhoneNumber string    `json:"phone_number"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
}

// Session represents a user's current state in Redis.
type Session struct {
	State                      string                   `json:"state"`
	CurrentCategory            string                   `json:"current_category"`
	CurrentCategoryPage        int                      `json:"current_category_page,omitempty"`
	CurrentSubcategory         string                   `json:"current_subcategory,omitempty"`
	CurrentProductID           string                   `json:"current_product_id"`
	Cart                       []CartItem               `json:"cart"`
	PendingOrderID             string                   `json:"pending_order_id"`
	PendingRetryOrderID        string                   `json:"pending_retry_order_id,omitempty"`
	PendingResolvedItems       []CartItem               `json:"pending_resolved_items,omitempty"`
	PendingRawSelections       []string                 `json:"pending_raw_selections,omitempty"`
	PendingSelectionErrors     []string                 `json:"pending_selection_errors,omitempty"`
	PendingAmbiguousInput      string                   `json:"pending_ambiguous_input,omitempty"`
	PendingAmbiguousQty        int                      `json:"pending_ambiguous_qty,omitempty"`
	PendingAmbiguousOptions    []PendingAmbiguousOption `json:"pending_ambiguous_options,omitempty"`
	FulfillmentType            string                   `json:"fulfillment_type,omitempty"`
	DeliveryZoneID             string                   `json:"delivery_zone_id,omitempty"`
	DeliveryZoneName           string                   `json:"delivery_zone_name,omitempty"`
	DeliveryFee                float64                  `json:"delivery_fee,omitempty"`
	DeliveryAddress            string                   `json:"delivery_address,omitempty"`
	DeliveryContactName        string                   `json:"delivery_contact_name,omitempty"`
	DeliveryNotes              string                   `json:"delivery_notes,omitempty"`
	RequiresPrescription       bool                     `json:"requires_prescription,omitempty"`
	PendingPrescriptionOrderID string                   `json:"pending_prescription_order_id,omitempty"`
}

// CartItem represents an item in the user's shopping cart.
type CartItem struct {
	ProductID            string  `json:"product_id"`
	Quantity             int     `json:"quantity"`
	Name                 string  `json:"name"`
	Price                float64 `json:"price"`
	RequiresPrescription bool    `json:"requires_prescription,omitempty"`
}

// PendingAmbiguousOption stores candidate products when user input is ambiguous.
type PendingAmbiguousOption struct {
	ProductID            string  `json:"product_id"`
	Name                 string  `json:"name"`
	Price                float64 `json:"price"`
	RequiresPrescription bool    `json:"requires_prescription,omitempty"`
}

// AdminUser represents a staff user who can access the dashboard.
type AdminUser struct {
	ID            string    `json:"id"`
	PhoneNumber   string    `json:"phone_number"`
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	BartenderCode string    `json:"bartender_code,omitempty"`
	PinHash       string    `json:"-"`
	IsActive      bool      `json:"is_active"`
	CreatedAt     time.Time `json:"created_at"`
}

// StaffUser is a forward-looking alias for admin/staff access records.
type StaffUser = AdminUser

const (
	AdminRoleOwner      = "OWNER"
	AdminRolePharmacist = "PHARMACIST"
	AdminRoleDispatcher = "DISPATCHER"
	AdminRoleManager    = AdminRoleOwner
	AdminRoleBartender  = AdminRoleDispatcher
)

// OTPCode represents a one-time password for authentication.
type OTPCode struct {
	ID          string    `json:"id"`
	PhoneNumber string    `json:"phone_number"`
	Code        string    `json:"code"`
	ExpiresAt   time.Time `json:"expires_at"`
	Verified    bool      `json:"verified"`
	CreatedAt   time.Time `json:"created_at"`
}

// Analytics represents dashboard overview metrics.
type Analytics struct {
	TodayRevenue      float64    `json:"today_revenue"`
	TodayOrders       int        `json:"today_orders"`
	BestSeller        BestSeller `json:"best_seller"`
	AverageOrderValue float64    `json:"average_order_value"`
}

// BestSeller represents the top-selling product.
type BestSeller struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

// RevenueTrend represents daily revenue data.
type RevenueTrend struct {
	Date       string  `json:"date"`
	Revenue    float64 `json:"revenue"`
	OrderCount int     `json:"order_count"`
}

// TopProduct represents a top-selling product with stats.
type TopProduct struct {
	ProductName  string  `json:"product_name"`
	QuantitySold int     `json:"quantity_sold"`
	Revenue      float64 `json:"revenue"`
}

// SalesReport represents an exportable sales report for a time range.
type SalesReport struct {
	Title               string    `json:"title"`
	DateLabel           string    `json:"date_label"`
	Timezone            string    `json:"timezone"`
	BusinessDayStart    string    `json:"business_day_start"`
	StartAt             time.Time `json:"start_at"`
	EndAt               time.Time `json:"end_at"`
	GeneratedAt         time.Time `json:"generated_at"`
	TotalRevenue        float64   `json:"total_revenue"`
	OrderCount          int       `json:"order_count"`
	AverageOrderValue   float64   `json:"average_order_value"`
	SettledStatusFilter []string  `json:"settled_status_filter"`
	Orders              []Order   `json:"orders"`
}

// PendingOrderItemInput is a product/quantity pair used during order creation.
type PendingOrderItemInput struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

// CreatePendingOrderInput describes a reserved-stock pending order request.
type CreatePendingOrderInput struct {
	UserID              string                  `json:"user_id"`
	CustomerPhone       string                  `json:"customer_phone"`
	TableNumber         string                  `json:"table_number"`
	PaymentMethod       string                  `json:"payment_method"`
	IdempotencyKey      string                  `json:"idempotency_key"`
	ExpiresAt           time.Time               `json:"expires_at"`
	Items               []PendingOrderItemInput `json:"items"`
	FulfillmentType     string                  `json:"fulfillment_type,omitempty"`
	DeliveryZoneID      string                  `json:"delivery_zone_id,omitempty"`
	DeliveryFee         float64                 `json:"delivery_fee,omitempty"`
	DeliveryAddress     string                  `json:"delivery_address,omitempty"`
	DeliveryContactName string                  `json:"delivery_contact_name,omitempty"`
	DeliveryNotes       string                  `json:"delivery_notes,omitempty"`
	ReviewRequired      bool                    `json:"review_required,omitempty"`
	Status              OrderStatus             `json:"status,omitempty"`
}

// PaymentAttemptStatus describes checkout progress for a payment attempt.
type PaymentAttemptStatus string

const (
	PaymentAttemptStatusQueued           PaymentAttemptStatus = "QUEUED"
	PaymentAttemptStatusProcessing       PaymentAttemptStatus = "PROCESSING"
	PaymentAttemptStatusAwaitingCustomer PaymentAttemptStatus = "AWAITING_CUSTOMER"
	PaymentAttemptStatusSucceeded        PaymentAttemptStatus = "SUCCEEDED"
	PaymentAttemptStatusFailed           PaymentAttemptStatus = "FAILED"
	PaymentAttemptStatusExpired          PaymentAttemptStatus = "EXPIRED"
)

// PaymentAttempt tracks a single payment initiation and reconciliation lifecycle.
type PaymentAttempt struct {
	ID                string               `json:"id"`
	OrderID           string               `json:"order_id"`
	Provider          string               `json:"provider"`
	Status            PaymentAttemptStatus `json:"status"`
	IdempotencyKey    string               `json:"idempotency_key,omitempty"`
	RequestedPhone    string               `json:"requested_phone,omitempty"`
	Amount            float64              `json:"amount"`
	ProviderReference string               `json:"provider_reference,omitempty"`
	CheckoutURL       string               `json:"checkout_url,omitempty"`
	LastError         string               `json:"last_error,omitempty"`
	Attempts          int                  `json:"attempts"`
	QueuePosition     int                  `json:"queue_position,omitempty"`
	ETASeconds        int                  `json:"eta_seconds,omitempty"`
	NextRetryAt       *time.Time           `json:"next_retry_at,omitempty"`
	DispatchedAt      *time.Time           `json:"dispatched_at,omitempty"`
	CompletedAt       *time.Time           `json:"completed_at,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

// PaymentQueueStats summarises queued attempts for a provider.
type PaymentQueueStats struct {
	Provider       string     `json:"provider"`
	QueuedCount    int        `json:"queued_count"`
	OldestQueuedAt *time.Time `json:"oldest_queued_at,omitempty"`
}

// UnmatchedPaymentStatus tracks manual reconciliation state for orphaned payments.
type UnmatchedPaymentStatus string

const (
	UnmatchedPaymentStatusPending  UnmatchedPaymentStatus = "PENDING"
	UnmatchedPaymentStatusResolved UnmatchedPaymentStatus = "RESOLVED"
	UnmatchedPaymentStatusRejected UnmatchedPaymentStatus = "REJECTED"
)

// UnmatchedPayment stores provider callbacks that could not be linked safely to an order.
type UnmatchedPayment struct {
	ID                string                 `json:"id"`
	Provider          string                 `json:"provider"`
	Status            UnmatchedPaymentStatus `json:"status"`
	ProviderReference string                 `json:"provider_reference,omitempty"`
	OrderIDHint       string                 `json:"order_id_hint,omitempty"`
	Phone             string                 `json:"phone,omitempty"`
	HashedPhone       string                 `json:"hashed_phone,omitempty"`
	Amount            float64                `json:"amount"`
	Payload           string                 `json:"payload"`
	ResolutionNote    string                 `json:"resolution_note,omitempty"`
	ResolvedOrderID   string                 `json:"resolved_order_id,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	ResolvedAt        *time.Time             `json:"resolved_at,omitempty"`
}

// Prescription captures a prescription media submission linked to an order.
type Prescription struct {
	ID               string     `json:"id"`
	OrderID          string     `json:"order_id"`
	CustomerPhone    string     `json:"customer_phone"`
	MediaID          string     `json:"media_id"`
	MediaType        string     `json:"media_type"`
	FileName         string     `json:"file_name,omitempty"`
	Caption          string     `json:"caption,omitempty"`
	Status           string     `json:"status"`
	ReviewNotes      string     `json:"review_notes,omitempty"`
	ReviewedByUserID string     `json:"reviewed_by_user_id,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const (
	PrescriptionStatusPending  = "PENDING"
	PrescriptionStatusApproved = "APPROVED"
	PrescriptionStatusRejected = "REJECTED"
)

// PrescriptionReview records pharmacist review actions.
type PrescriptionReview struct {
	ID             string    `json:"id"`
	PrescriptionID string    `json:"prescription_id"`
	OrderID        string    `json:"order_id"`
	ReviewerUserID string    `json:"reviewer_user_id"`
	Decision       string    `json:"decision"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
}

// DeliveryZone stores flat-fee delivery rules for Nairobi zones.
type DeliveryZone struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Fee           float64   `json:"fee"`
	EstimatedMins int       `json:"estimated_mins"`
	IsActive      bool      `json:"is_active"`
	SortOrder     int       `json:"sort_order"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// BusinessHours stores the operating window used for queue and expectation messaging.
type BusinessHours struct {
	ID        string    `json:"id"`
	DayOfWeek int       `json:"day_of_week"`
	OpenTime  string    `json:"open_time"`
	CloseTime string    `json:"close_time"`
	IsOpen    bool      `json:"is_open"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OutboundMessage records durable WhatsApp notifications and retries.
type OutboundMessage struct {
	ID          string     `json:"id"`
	PhoneNumber string     `json:"phone_number"`
	MessageType string     `json:"message_type"`
	Payload     string     `json:"payload"`
	Status      string     `json:"status"`
	LastError   string     `json:"last_error,omitempty"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// AuditLog records meaningful staff/system actions for traceability.
type AuditLog struct {
	ID         string    `json:"id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Action     string    `json:"action"`
	ActorID    string    `json:"actor_id,omitempty"`
	Metadata   string    `json:"metadata,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
