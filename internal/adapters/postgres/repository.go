package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/observability"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository implements ProductRepository, OrderRepository, and UserRepository using GORM with pgx driver
type Repository struct {
	db                   *gorm.DB
	productRepository    *productRepository
	orderRepository      *orderRepository
	userRepository       *userRepository
	adminUserRepository  *adminUserRepository
	otpRepository        *otpRepository
	analyticsRepository  *analyticsRepository
	paymentAttemptRepo   *paymentAttemptRepository
	unmatchedPaymentRepo *unmatchedPaymentRepository
	metrics              *observability.RuntimeMetrics
}

// productRepository implements ProductRepository methods
type productRepository struct {
	*Repository
}

// orderRepository implements OrderRepository methods
type orderRepository struct {
	*Repository
}

// userRepository implements UserRepository methods
type userRepository struct {
	*Repository
}

// adminUserRepository implements AdminUserRepository methods
type adminUserRepository struct {
	*Repository
}

// otpRepository implements OTPRepository methods
type otpRepository struct {
	*Repository
}

// analyticsRepository implements AnalyticsRepository methods
type analyticsRepository struct {
	*Repository
}

// paymentAttemptRepository implements PaymentAttemptRepository methods.
type paymentAttemptRepository struct {
	*Repository
}

// unmatchedPaymentRepository implements UnmatchedPaymentRepository methods.
type unmatchedPaymentRepository struct {
	*Repository
}

// NewRepository creates a new Postgres repository instance
func NewRepository(dbURL string) (*Repository, error) {
	// GORM with pgx driver (postgres driver uses pgx under the hood)
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	repo := &Repository{db: db}
	// Set up embedded types
	repo.productRepository = &productRepository{Repository: repo}
	repo.orderRepository = &orderRepository{Repository: repo}
	repo.userRepository = &userRepository{Repository: repo}
	repo.adminUserRepository = &adminUserRepository{Repository: repo}
	repo.otpRepository = &otpRepository{Repository: repo}
	repo.analyticsRepository = &analyticsRepository{Repository: repo}
	repo.paymentAttemptRepo = &paymentAttemptRepository{Repository: repo}
	repo.unmatchedPaymentRepo = &unmatchedPaymentRepository{Repository: repo}
	return repo, nil
}

// ProductRepository returns the ProductRepository interface implementation
func (r *Repository) ProductRepository() core.ProductRepository {
	return r.productRepository
}

// OrderRepository returns the OrderRepository interface implementation
func (r *Repository) OrderRepository() core.OrderRepository {
	return r.orderRepository
}

// UserRepository returns the UserRepository interface implementation
func (r *Repository) UserRepository() core.UserRepository {
	return r.userRepository
}

// AdminUserRepository returns the AdminUserRepository interface implementation
func (r *Repository) AdminUserRepository() core.AdminUserRepository {
	return r.adminUserRepository
}

// OTPRepository returns the OTPRepository interface implementation
func (r *Repository) OTPRepository() core.OTPRepository {
	return r.otpRepository
}

// AnalyticsRepository returns the AnalyticsRepository interface implementation
func (r *Repository) AnalyticsRepository() core.AnalyticsRepository {
	return r.analyticsRepository
}

// PaymentAttemptRepository returns the PaymentAttemptRepository interface implementation.
func (r *Repository) PaymentAttemptRepository() core.PaymentAttemptRepository {
	return r.paymentAttemptRepo
}

// UnmatchedPaymentRepository returns the UnmatchedPaymentRepository interface implementation.
func (r *Repository) UnmatchedPaymentRepository() core.UnmatchedPaymentRepository {
	return r.unmatchedPaymentRepo
}

// SQLDB exposes the underlying sql.DB for pool configuration.
func (r *Repository) SQLDB() (*sql.DB, error) {
	return r.db.DB()
}

// SetMetrics attaches runtime metrics collectors used by retryable DB paths.
func (r *Repository) SetMetrics(metrics *observability.RuntimeMetrics) {
	r.metrics = metrics
}

// ProductRepository implementation

// GetByID retrieves a product by its ID
func (r *productRepository) GetByID(ctx context.Context, id string) (*core.Product, error) {
	var productModel ProductModel
	if err := r.db.WithContext(ctx).Table("products").Where("id = ?", id).First(&productModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("product not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get product: %w", err)
	}
	return productModel.ToDomain(), nil
}

// GetByCategory retrieves all products in a specific category
func (r *productRepository) GetByCategory(ctx context.Context, category string) ([]*core.Product, error) {
	var productModels []ProductModel
	if err := r.db.WithContext(ctx).Table("products").
		Where("category = ? AND is_active = ?", category, true).
		Find(&productModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get products by category: %w", err)
	}

	products := make([]*core.Product, len(productModels))
	for i, pm := range productModels {
		products[i] = pm.ToDomain()
	}
	return products, nil
}

// GetAll retrieves all active products
func (r *productRepository) GetAll(ctx context.Context) ([]*core.Product, error) {
	var productModels []ProductModel
	if err := r.db.WithContext(ctx).Table("products").
		Where("is_active = ?", true).
		Find(&productModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get all products: %w", err)
	}

	products := make([]*core.Product, len(productModels))
	for i, pm := range productModels {
		products[i] = pm.ToDomain()
	}
	return products, nil
}

// GetMenu retrieves all active products grouped by category
func (r *productRepository) GetMenu(ctx context.Context) (map[string][]*core.Product, error) {
	var productModels []ProductModel
	if err := r.db.WithContext(ctx).Table("products").
		Where("is_active = ?", true).
		Order("category, name").
		Find(&productModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get menu: %w", err)
	}

	menu := make(map[string][]*core.Product)
	for _, pm := range productModels {
		product := pm.ToDomain()
		category := product.Category
		if menu[category] == nil {
			menu[category] = make([]*core.Product, 0)
		}
		menu[category] = append(menu[category], product)
	}

	return menu, nil
}

// UpdateStock updates the stock quantity for a product
func (r *productRepository) UpdateStock(ctx context.Context, id string, quantity int) error {
	result := r.db.WithContext(ctx).Table("products").
		Where("id = ?", id).
		Update("stock_quantity", quantity)

	if result.Error != nil {
		return fmt.Errorf("failed to update stock: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("product not found")
	}
	return nil
}

// SearchProducts searches for products by name (case-insensitive partial match)
func (r *productRepository) SearchProducts(ctx context.Context, query string) ([]*core.Product, error) {
	var productModels []ProductModel
	searchPattern := "%" + query + "%"
	if err := r.db.WithContext(ctx).Table("products").
		Where("LOWER(name) LIKE LOWER(?) AND is_active = ?", searchPattern, true).
		Order("name").
		Find(&productModels).Error; err != nil {
		return nil, fmt.Errorf("failed to search products: %w", err)
	}

	products := make([]*core.Product, len(productModels))
	for i, pm := range productModels {
		products[i] = pm.ToDomain()
	}
	return products, nil
}

// UpdatePrice updates the price for a product
func (r *productRepository) UpdatePrice(ctx context.Context, id string, price float64) error {
	result := r.db.WithContext(ctx).Table("products").
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"price":      price,
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to update price: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("product not found")
	}
	return nil
}

// OrderRepository implementation

// CreateOrder creates a new order with its items in a transaction
func (r *orderRepository) CreateOrder(ctx context.Context, order *core.Order) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Create order
		orderModel := OrderModelFromDomain(order)
		if err := tx.Table("orders").Create(&orderModel).Error; err != nil {
			return fmt.Errorf("failed to create order: %w", err)
		}

		// Create order items
		for _, item := range order.Items {
			itemModel := OrderItemModelFromDomain(&item)
			itemModel.OrderID = orderModel.ID
			if err := tx.Table("order_items").Create(&itemModel).Error; err != nil {
				return fmt.Errorf("failed to create order item: %w", err)
			}
		}

		return nil
	})
}

// GenerateNextPickupCode allocates the next sequential 6-digit pickup code from a database sequence.
func (r *orderRepository) GenerateNextPickupCode(ctx context.Context) (string, error) {
	var pickupCode string
	if err := r.db.WithContext(ctx).
		Raw("SELECT LPAD(nextval('pickup_code_seq')::text, 6, '0')").
		Scan(&pickupCode).Error; err != nil {
		return "", fmt.Errorf("failed to generate pickup code: %w", err)
	}

	if pickupCode == "" {
		return "", fmt.Errorf("failed to generate pickup code")
	}

	return pickupCode, nil
}

func (r *orderRepository) orderQueryWithAdminNames(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("orders").
		Select(
			"orders.*, preparing_admin.name AS preparing_by_name, preparing_admin.bartender_code AS preparing_by_code, ready_admin.name AS ready_by_name, ready_admin.bartender_code AS ready_by_code, completed_admin.name AS completed_by_name, completed_admin.bartender_code AS completed_by_code",
		).
		Joins("LEFT JOIN admin_users AS preparing_admin ON preparing_admin.id = orders.preparing_by_admin_user_id").
		Joins("LEFT JOIN admin_users AS ready_admin ON ready_admin.id = orders.ready_by_admin_user_id").
		Joins("LEFT JOIN admin_users AS completed_admin ON completed_admin.id = orders.completed_by_admin_user_id")
}

// fetchOrderItemsWithProductNames is a helper method that retrieves order items with product names via JOIN
// This ensures consistent OrderItem shape across all retrieval methods
func (r *orderRepository) fetchOrderItemsWithProductNames(ctx context.Context, orderID string) ([]core.OrderItem, error) {
	type OrderItemWithProduct struct {
		OrderItemModel
		ProductName string `gorm:"column:product_name"`
	}

	var itemsWithProducts []OrderItemWithProduct
	if err := r.db.WithContext(ctx).Table("order_items").
		Select("order_items.*, products.name as product_name").
		Joins("LEFT JOIN products ON order_items.product_id = products.id").
		Where("order_items.order_id = ?", orderID).
		Find(&itemsWithProducts).Error; err != nil {
		return nil, fmt.Errorf("failed to get order items: %w", err)
	}

	items := make([]core.OrderItem, len(itemsWithProducts))
	for i, iwp := range itemsWithProducts {
		item := iwp.OrderItemModel.ToDomain()
		items[i] = core.OrderItem{
			ID:          item.ID,
			OrderID:     item.OrderID,
			ProductID:   item.ProductID,
			Quantity:    item.Quantity,
			PriceAtTime: item.PriceAtTime,
			ProductName: iwp.ProductName, // Populated from JOIN
		}
	}

	return items, nil
}

// GetByID retrieves an order by its ID with all items (implements OrderRepository)
func (r *orderRepository) GetByID(ctx context.Context, id string) (*core.Order, error) {
	var orderModel OrderModel
	if err := r.orderQueryWithAdminNames(ctx).Where("orders.id = ?", id).First(&orderModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("order not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	order := orderModel.ToDomain()
	order.Items = itemsByOrder[id]

	return order, nil
}

// GetByUserID retrieves all orders for a specific user
func (r *orderRepository) GetByUserID(ctx context.Context, userID string) ([]*core.Order, error) {
	var orderModels []OrderModel
	if err := r.orderQueryWithAdminNames(ctx).
		Where("orders.user_id = ?", userID).
		Order("orders.created_at DESC").
		Find(&orderModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get orders by user ID: %w", err)
	}

	orders := make([]*core.Order, len(orderModels))
	orderIDs := make([]string, 0, len(orderModels))
	for _, om := range orderModels {
		orderIDs = append(orderIDs, om.ID)
	}
	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for i, om := range orderModels {
		order := om.ToDomain()
		order.Items = itemsByOrder[om.ID]
		orders[i] = order
	}

	return orders, nil
}

// GetByPhone retrieves all orders for a specific phone number
func (r *orderRepository) GetByPhone(ctx context.Context, phone string) ([]*core.Order, error) {
	var orderModels []OrderModel
	patterns := buildPhoneSearchPatterns(canonicalizePhoneForStorage(phone))
	query := r.orderQueryWithAdminNames(ctx).Order("orders.created_at DESC")
	if len(patterns) > 0 {
		query = query.Where("orders.customer_phone IN ?", patterns)
	} else {
		query = query.Where("orders.customer_phone = ?", strings.TrimSpace(phone))
	}
	if err := query.Find(&orderModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get orders by phone: %w", err)
	}

	orders := make([]*core.Order, len(orderModels))
	orderIDs := make([]string, 0, len(orderModels))
	for _, om := range orderModels {
		orderIDs = append(orderIDs, om.ID)
	}
	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for i, om := range orderModels {
		order := om.ToDomain()
		order.Items = itemsByOrder[om.ID]
		orders[i] = order
	}

	return orders, nil
}

// GetByDateRangeAndStatuses retrieves orders for a specific time window and optional statuses.
func (r *orderRepository) GetByDateRangeAndStatuses(ctx context.Context, start time.Time, end time.Time, statuses []core.OrderStatus) ([]*core.Order, error) {
	query := r.orderQueryWithAdminNames(ctx).
		Where("COALESCE(orders.paid_at, orders.created_at) >= ? AND COALESCE(orders.paid_at, orders.created_at) < ?", start, end).
		Order("COALESCE(orders.paid_at, orders.created_at) ASC")

	if len(statuses) > 0 {
		statusValues := make([]string, 0, len(statuses))
		for _, status := range statuses {
			statusValues = append(statusValues, string(status))
		}
		query = query.Where("orders.status IN ?", statusValues)
	}

	var orderModels []OrderModel
	if err := query.Find(&orderModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get orders by date range: %w", err)
	}

	orders := make([]*core.Order, len(orderModels))
	orderIDs := make([]string, 0, len(orderModels))
	for _, om := range orderModels {
		orderIDs = append(orderIDs, om.ID)
	}
	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for i, om := range orderModels {
		order := om.ToDomain()
		order.Items = itemsByOrder[om.ID]
		orders[i] = order
	}

	return orders, nil
}

// UpdateStatus updates the status of an order
func (r *orderRepository) UpdateStatus(ctx context.Context, id string, status core.OrderStatus) error {
	switch status {
	case core.OrderStatusPending, core.OrderStatusFailed, core.OrderStatusCancelled, core.OrderStatusExpired:
	default:
		return r.UpdateStatusWithActor(ctx, id, status, "")
	}

	err := r.updateStatus(ctx, id, status, true)
	if err != nil && isOrderHardeningCompatibilityError(err) {
		return r.updateStatus(ctx, id, status, false)
	}
	return err
}

func (r *orderRepository) updateStatus(ctx context.Context, id string, status core.OrderStatus, useHardeningColumns bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var orderModel OrderModel
		if err := tx.Table("orders").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", id).
			First(&orderModel).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("order not found")
			}
			return fmt.Errorf("failed to lock order: %w", err)
		}

		currentStatus := core.OrderStatus(orderModel.Status)
		if currentStatus == status {
			return nil
		}
		if isPaidWorkflowStatus(currentStatus) && !isPaidWorkflowStatus(status) {
			return fmt.Errorf("cannot downgrade order in status %s", currentStatus)
		}

		updates := map[string]interface{}{
			"status":     string(status),
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}

		switch status {
		case core.OrderStatusPending:
			if currentStatus != core.OrderStatusFailed && currentStatus != core.OrderStatusCancelled {
				return fmt.Errorf("only FAILED or CANCELLED orders can be reopened")
			}
			if useHardeningColumns {
				if orderModel.StockReleased {
					if err := reserveStockForExistingOrderTx(ctx, tx, id); err != nil {
						return err
					}
					updates["stock_released"] = false
				}
				updates["expires_at"] = time.Now().Add(defaultPendingOrderTTL)
			} else {
				if err := reserveStockForExistingOrderTx(ctx, tx, id); err != nil {
					return err
				}
			}
		case core.OrderStatusFailed, core.OrderStatusCancelled, core.OrderStatusExpired:
			if currentStatus != core.OrderStatusPending {
				return fmt.Errorf("only PENDING orders can be marked %s", status)
			}
			if useHardeningColumns {
				if !orderModel.StockReleased {
					if err := releaseReservedStockTx(ctx, tx, id); err != nil {
						return err
					}
					updates["stock_released"] = true
				}
				if status != core.OrderStatusExpired {
					updates["expires_at"] = nil
				}
			} else {
				if err := releaseReservedStockTx(ctx, tx, id); err != nil {
					return err
				}
			}
			if err := tx.Table("payment_attempts").
				Where("order_id = ? AND status IN ?", id, []string{
					string(core.PaymentAttemptStatusQueued),
					string(core.PaymentAttemptStatusProcessing),
					string(core.PaymentAttemptStatusAwaitingCustomer),
				}).
				Updates(map[string]interface{}{
					"status":       string(core.PaymentAttemptStatusExpired),
					"completed_at": gorm.Expr("CURRENT_TIMESTAMP"),
					"updated_at":   gorm.Expr("CURRENT_TIMESTAMP"),
				}).Error; err != nil {
				return fmt.Errorf("failed to expire active payment attempts: %w", err)
			}
		}

		if err := tx.Table("orders").Where("id = ?", id).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}

		return nil
	})
}

// UpdateCustomerPhone updates the payment phone number stored on an order.
func (r *orderRepository) UpdateCustomerPhone(ctx context.Context, id string, phone string) error {
	phone = canonicalizePhoneForStorage(phone)
	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"customer_phone": phone,
			"updated_at":     gorm.Expr("CURRENT_TIMESTAMP"),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to update customer phone: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order not found")
	}
	return nil
}

// UpdatePaymentDetails updates the payment method and payment reference metadata for an order.
func (r *orderRepository) UpdatePaymentDetails(ctx context.Context, id string, paymentMethod string, paymentRef string) error {
	updates := map[string]interface{}{
		"payment_method":    paymentMethod,
		"payment_reference": paymentRef,
		"updated_at":        gorm.Expr("CURRENT_TIMESTAMP"),
	}

	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ?", id).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update payment details: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order not found")
	}

	return nil
}

// UpdateStatusWithActor updates order status and records audit metadata for bartender workflow actions.
func (r *orderRepository) UpdateStatusWithActor(ctx context.Context, id string, status core.OrderStatus, actorUserID string) error {
	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ?", id)

	updates := map[string]interface{}{
		"status":     string(status),
		"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
	}

	switch status {
	case core.OrderStatusApprovedAwaitingPayment:
		updates["reviewed_at"] = gorm.Expr("COALESCE(reviewed_at, CURRENT_TIMESTAMP)")
		updates["review_required"] = false
		if actorUserID != "" {
			updates["reviewed_by_admin_user_id"] = actorUserID
		}
	case core.OrderStatusRejected:
		updates["reviewed_at"] = gorm.Expr("COALESCE(reviewed_at, CURRENT_TIMESTAMP)")
		if actorUserID != "" {
			updates["reviewed_by_admin_user_id"] = actorUserID
		}
	case core.OrderStatusPaid:
		updates["paid_at"] = gorm.Expr("COALESCE(paid_at, CURRENT_TIMESTAMP)")
		updates["preparing_at"] = nil
		updates["preparing_by_admin_user_id"] = nil
	case core.OrderStatusPreparing:
		updates["preparing_at"] = gorm.Expr("CURRENT_TIMESTAMP")
		if actorUserID != "" {
			updates["preparing_by_admin_user_id"] = actorUserID
		}
	case core.OrderStatusReady:
		updates["ready_at"] = gorm.Expr("CURRENT_TIMESTAMP")
		if actorUserID != "" {
			updates["ready_by_admin_user_id"] = actorUserID
		}
	case core.OrderStatusCompleted:
		updates["completed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
		if actorUserID != "" {
			updates["completed_by_admin_user_id"] = actorUserID
		}
	case core.OrderStatusOutForDelivery:
		updates["ready_at"] = gorm.Expr("COALESCE(ready_at, CURRENT_TIMESTAMP)")
	}

	result = result.Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update order status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order not found")
	}
	return nil
}

type orderTransitionState struct {
	Status                 string         `gorm:"column:status"`
	PreparingByAdminUserID sql.NullString `gorm:"column:preparing_by_admin_user_id"`
	ReadyByAdminUserID     sql.NullString `gorm:"column:ready_by_admin_user_id"`
}

func (r *orderRepository) getOrderTransitionState(ctx context.Context, id string) (*orderTransitionState, error) {
	var state orderTransitionState
	if err := r.db.WithContext(ctx).Table("orders").
		Select("status, preparing_by_admin_user_id, ready_by_admin_user_id").
		Where("id = ?", id).
		First(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("order not found")
		}
		return nil, fmt.Errorf("failed to get order state: %w", err)
	}

	return &state, nil
}

func (r *orderRepository) ClaimPreparing(ctx context.Context, id string, actorUserID string) error {
	if strings.TrimSpace(actorUserID) == "" {
		return fmt.Errorf("actor user ID is required")
	}

	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ? AND status = ?", id, string(core.OrderStatusPaid)).
		Updates(map[string]interface{}{
			"status":                     string(core.OrderStatusPreparing),
			"preparing_at":               gorm.Expr("CURRENT_TIMESTAMP"),
			"preparing_by_admin_user_id": actorUserID,
			"updated_at":                 gorm.Expr("CURRENT_TIMESTAMP"),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to claim order for preparation: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}

	state, err := r.getOrderTransitionState(ctx, id)
	if err != nil {
		return err
	}

	if state.Status == string(core.OrderStatusPreparing) {
		if state.PreparingByAdminUserID.Valid && state.PreparingByAdminUserID.String == actorUserID {
			return nil
		}
		return fmt.Errorf("order is already being prepared by another bartender")
	}

	return fmt.Errorf("only PAID orders can be marked PREPARING")
}

func (r *orderRepository) ForceTakeoverPreparing(ctx context.Context, id string, actorUserID string) error {
	if strings.TrimSpace(actorUserID) == "" {
		return fmt.Errorf("actor user ID is required")
	}

	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ? AND status = ?", id, string(core.OrderStatusPreparing)).
		Updates(map[string]interface{}{
			"preparing_at":               gorm.Expr("CURRENT_TIMESTAMP"),
			"preparing_by_admin_user_id": actorUserID,
			"updated_at":                 gorm.Expr("CURRENT_TIMESTAMP"),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to force takeover: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}

	state, err := r.getOrderTransitionState(ctx, id)
	if err != nil {
		return err
	}

	if state.Status != string(core.OrderStatusPreparing) {
		return fmt.Errorf("only PREPARING orders can be force taken over")
	}
	if state.PreparingByAdminUserID.Valid && state.PreparingByAdminUserID.String == actorUserID {
		return nil
	}

	return fmt.Errorf("failed to force takeover")
}

func (r *orderRepository) UnlockPreparing(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ? AND status = ?", id, string(core.OrderStatusPreparing)).
		Updates(map[string]interface{}{
			"status":                     string(core.OrderStatusPaid),
			"preparing_at":               nil,
			"preparing_by_admin_user_id": nil,
			"updated_at":                 gorm.Expr("CURRENT_TIMESTAMP"),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to unlock order: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}

	state, err := r.getOrderTransitionState(ctx, id)
	if err != nil {
		return err
	}

	if state.Status == string(core.OrderStatusPaid) {
		return nil
	}
	if state.Status != string(core.OrderStatusPreparing) {
		return fmt.Errorf("only PREPARING orders can be unlocked")
	}

	return fmt.Errorf("failed to unlock order")
}

func (r *orderRepository) MarkReadyFromPreparing(ctx context.Context, id string, actorUserID string) error {
	if strings.TrimSpace(actorUserID) == "" {
		return fmt.Errorf("actor user ID is required")
	}

	result := r.db.WithContext(ctx).Table("orders").
		Where("id = ? AND status = ? AND preparing_by_admin_user_id = ?", id, string(core.OrderStatusPreparing), actorUserID).
		Updates(map[string]interface{}{
			"status":                 string(core.OrderStatusReady),
			"ready_at":               gorm.Expr("CURRENT_TIMESTAMP"),
			"ready_by_admin_user_id": actorUserID,
			"updated_at":             gorm.Expr("CURRENT_TIMESTAMP"),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to mark order ready: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}

	state, err := r.getOrderTransitionState(ctx, id)
	if err != nil {
		return err
	}

	if state.Status == string(core.OrderStatusReady) {
		if state.ReadyByAdminUserID.Valid && state.ReadyByAdminUserID.String == actorUserID {
			return nil
		}
	}
	if state.Status != string(core.OrderStatusPreparing) {
		return fmt.Errorf("only PREPARING orders can be marked READY")
	}
	if !state.PreparingByAdminUserID.Valid || state.PreparingByAdminUserID.String != actorUserID {
		return fmt.Errorf("only the assigned bartender can notify customer")
	}

	return fmt.Errorf("failed to mark order ready")
}

// GetAllWithFilters retrieves orders with optional status filter and limit
func (r *orderRepository) GetAllWithFilters(ctx context.Context, status string, limit int, updatedAfter *time.Time) ([]*core.Order, error) {
	query := r.orderQueryWithAdminNames(ctx).Order("orders.created_at DESC")

	// Apply status filter if provided
	if status != "" {
		query = query.Where("orders.status = ?", status)
	}

	if updatedAfter != nil {
		query = query.Where("orders.updated_at > ?", *updatedAfter)
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		return nil, fmt.Errorf("limit must be between 1 and 200")
	}
	query = query.Limit(limit)

	var orderModels []OrderModel
	if err := query.Find(&orderModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get orders: %w", err)
	}

	orders := make([]*core.Order, len(orderModels))
	orderIDs := make([]string, 0, len(orderModels))
	for _, om := range orderModels {
		orderIDs = append(orderIDs, om.ID)
	}
	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for i, om := range orderModels {
		order := om.ToDomain()
		order.Items = itemsByOrder[om.ID]
		orders[i] = order
	}

	return orders, nil
}

// GetCompletedHistory retrieves completed orders for dispute/history review with optional filters.
func (r *orderRepository) GetCompletedHistory(ctx context.Context, pickupCode string, phone string, limit int) ([]*core.Order, error) {
	query := r.orderQueryWithAdminNames(ctx).
		Where("orders.status = ?", string(core.OrderStatusCompleted)).
		Order("orders.completed_at DESC NULLS LAST, orders.created_at DESC")

	if pickupCode != "" {
		query = query.Where("orders.pickup_code ILIKE ?", "%"+pickupCode+"%")
	}

	if phone != "" {
		phoneDigits := extractDigits(phone)
		if phoneDigits != "" {
			// Kenya phone equivalence: 2547xxxxxxxx, +2547xxxxxxxx, 07xxxxxxxx, 01xxxxxxxx
			// all map to the same last 9 digits for matching.
			if len(phoneDigits) >= 9 {
				localDigits := extractLast9Digits(phoneDigits)
				query = query.Where(
					"RIGHT(regexp_replace(orders.customer_phone, '[^0-9]', '', 'g'), 9) = ?",
					localDigits,
				)
			} else {
				query = query.Where(
					"regexp_replace(orders.customer_phone, '[^0-9]', '', 'g') LIKE ?",
					"%"+phoneDigits+"%",
				)
			}
		} else {
			query = query.Where("orders.customer_phone ILIKE ?", "%"+strings.TrimSpace(phone)+"%")
		}
	}

	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query = query.Limit(limit)

	var orderModels []OrderModel
	if err := query.Find(&orderModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get order history: %w", err)
	}

	orders := make([]*core.Order, len(orderModels))
	orderIDs := make([]string, 0, len(orderModels))
	for _, om := range orderModels {
		orderIDs = append(orderIDs, om.ID)
	}
	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for i, om := range orderModels {
		order := om.ToDomain()
		order.Items = itemsByOrder[om.ID]
		orders[i] = order
	}

	return orders, nil
}

// FindPendingByPhoneAndAmount finds the most recent pending order matching phone and amount
// Uses hybrid phone matching: exact match first, then last 9 digits
func (r *orderRepository) FindPendingByPhoneAndAmount(ctx context.Context, phone string, amount float64) (*core.Order, error) {
	// Normalize phone: extract last 9 digits for fallback matching
	phoneDigits := extractLast9Digits(phone)

	var orderModel OrderModel
	// Try exact match first, then fallback to last 9 digits match
	err := r.db.WithContext(ctx).Table("orders").
		Where("status = ? AND total_amount = ? AND (customer_phone = ? OR customer_phone LIKE ?)",
			"PENDING", amount, phone, "%"+phoneDigits).
		Order("created_at DESC").
		First(&orderModel).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // No matching order found (not an error)
		}
		return nil, fmt.Errorf("failed to find pending order: %w", err)
	}

	// Get order items with product names
	items, err := r.fetchOrderItemsWithProductNames(ctx, orderModel.ID)
	if err != nil {
		return nil, err
	}

	order := orderModel.ToDomain()
	order.Items = items

	return order, nil
}

// buildPhoneSearchPatterns expands input phone search across equivalent KE formats.
// Example: 0708116809 -> [0708116809, 708116809, 254708116809, +254708116809]
func buildPhoneSearchPatterns(phone string) []string {
	input := strings.TrimSpace(phone)
	if input == "" {
		return nil
	}

	patterns := make([]string, 0, 6)
	seen := make(map[string]struct{}, 6)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		patterns = append(patterns, value)
	}

	add(input)

	digits := extractDigits(input)
	add(digits)

	local := extractLast9Digits(digits)
	if local != "" {
		add(local)
		add("0" + local)
		add("254" + local)
		add("+254" + local)
	}

	if strings.HasPrefix(digits, "254") && len(digits) > 3 {
		add("0" + digits[3:])
	}

	return patterns
}

// extractDigits keeps only numeric characters in a string.
func extractDigits(input string) string {
	var builder strings.Builder
	builder.Grow(len(input))
	for _, char := range input {
		if char >= '0' && char <= '9' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

// extractLast9Digits extracts the last 9 digits from a phone number.
func extractLast9Digits(phone string) string {
	digits := extractDigits(phone)
	if len(digits) >= 9 {
		return digits[len(digits)-9:]
	}
	return digits
}

// FindPendingByAmount finds the most recent pending order matching amount only
// Used as fallback when phone number is not available (e.g., buygoods webhooks)
// Only matches orders created within the last 30 minutes for safety
func (r *orderRepository) FindPendingByAmount(ctx context.Context, amount float64) (*core.Order, error) {
	cutoffTime := time.Now().Add(-30 * time.Minute)
	var orderModels []OrderModel

	err := r.db.WithContext(ctx).Table("orders").
		Where("status = ? AND total_amount = ? AND created_at > ?",
			"PENDING", amount, cutoffTime).
		Order("created_at DESC").
		Limit(2).
		Find(&orderModels).Error

	if err != nil {
		return nil, fmt.Errorf("failed to find pending order by amount: %w", err)
	}
	if len(orderModels) != 1 {
		return nil, nil
	}

	items, err := r.fetchOrderItemsWithProductNames(ctx, orderModels[0].ID)
	if err != nil {
		return nil, err
	}

	order := orderModels[0].ToDomain()
	order.Items = items

	return order, nil
}

// FindPendingByHashedPhoneAndAmount finds a pending order by matching the hashed phone number
// Kopo Kopo sends hashed_sender_phone in buygoods webhooks - we compute hashes of stored phones to match
// This is more precise than amount-only matching for concurrent orders
func (r *orderRepository) FindPendingByHashedPhoneAndAmount(ctx context.Context, hashedPhone string, amount float64) (*core.Order, error) {
	if hashedPhone == "" {
		return nil, nil // Can't match without hash
	}

	cutoffTime := time.Now().Add(-30 * time.Minute)
	var orderModels []OrderModel

	err := r.db.WithContext(ctx).Table("orders").
		Where("status = ? AND total_amount = ? AND created_at > ?",
			"PENDING", amount, cutoffTime).
		Order("created_at DESC").
		Find(&orderModels).Error

	if err != nil {
		return nil, fmt.Errorf("failed to find pending orders: %w", err)
	}

	var matches []*core.Order
	for _, orderModel := range orderModels {
		if matchesHashedPhone(orderModel.CustomerPhone, hashedPhone) {
			items, err := r.fetchOrderItemsWithProductNames(ctx, orderModel.ID)
			if err != nil {
				return nil, err
			}

			order := orderModel.ToDomain()
			order.Items = items
			matches = append(matches, order)
		}
	}

	if len(matches) != 1 {
		return nil, nil
	}
	return matches[0], nil
}

// matchesHashedPhone checks if a phone number matches the hashed phone from Kopo Kopo
// Tries multiple phone formats as Kopo Kopo's exact hashing format isn't documented
func matchesHashedPhone(phone, hashedPhone string) bool {
	// Normalize phone - remove spaces and special chars
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")

	// Try various phone formats that Kopo Kopo might use
	formats := []string{
		phone,                          // As stored (e.g., 254708116809)
		"+" + phone,                    // With + prefix (+254708116809)
		strings.TrimPrefix(phone, "+"), // Without + prefix
	}

	// Also try with/without country code variations
	digits := extractLast9Digits(phone)
	if digits != "" {
		formats = append(formats, digits)        // Just 9 digits (708116809)
		formats = append(formats, "0"+digits)    // Local format (0708116809)
		formats = append(formats, "254"+digits)  // With country code
		formats = append(formats, "+254"+digits) // E.164 format
	}

	for _, format := range formats {
		hash := computeSHA256(format)
		if hash == hashedPhone {
			return true
		}
	}

	return false
}

// computeSHA256 computes SHA256 hash of a string and returns hex-encoded result
func computeSHA256(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// Database Models (with GORM tags)

// ProductModel represents the product table structure
type ProductModel struct {
	ID                   string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	CategoryID           sql.NullString `gorm:"column:category_id;type:uuid"`
	Name                 string         `gorm:"column:name;type:varchar(255);not null"`
	Description          sql.NullString `gorm:"column:description;type:text"`
	Price                float64        `gorm:"column:price;type:decimal(10,2);not null"`
	Category             string         `gorm:"column:category;type:varchar(100);not null"`
	StockQuantity        int            `gorm:"column:stock_quantity;type:integer;not null;default:0"`
	ImageURL             sql.NullString `gorm:"column:image_url;type:varchar(500)"`
	IsActive             bool           `gorm:"column:is_active;type:boolean;not null;default:true"`
	SKU                  sql.NullString `gorm:"column:sku;type:varchar(64)"`
	BrandName            sql.NullString `gorm:"column:brand_name;type:varchar(255)"`
	GenericName          sql.NullString `gorm:"column:generic_name;type:varchar(255)"`
	Strength             sql.NullString `gorm:"column:strength;type:varchar(64)"`
	DosageForm           sql.NullString `gorm:"column:dosage_form;type:varchar(64)"`
	PackSize             sql.NullString `gorm:"column:pack_size;type:varchar(64)"`
	Unit                 sql.NullString `gorm:"column:unit;type:varchar(32)"`
	ActiveIngredient     sql.NullString `gorm:"column:active_ingredient;type:varchar(255)"`
	RequiresPrescription bool           `gorm:"column:requires_prescription;type:boolean;not null;default:false"`
	IsControlled         bool           `gorm:"column:is_controlled;type:boolean;not null;default:false"`
	PriceSource          sql.NullString `gorm:"column:price_source;type:varchar(64)"`
	CreatedAt            time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt            time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (ProductModel) TableName() string {
	return "products"
}

// ToDomain converts ProductModel to core.Product
func (p *ProductModel) ToDomain() *core.Product {
	product := &core.Product{
		ID:                   p.ID,
		Name:                 p.Name,
		Price:                p.Price,
		Category:             p.Category,
		StockQuantity:        p.StockQuantity,
		IsActive:             p.IsActive,
		RequiresPrescription: p.RequiresPrescription,
		IsControlled:         p.IsControlled,
		CreatedAt:            p.CreatedAt,
		UpdatedAt:            p.UpdatedAt,
	}

	if p.Description.Valid {
		product.Description = p.Description.String
	}
	if p.ImageURL.Valid {
		product.ImageURL = p.ImageURL.String
	}
	if p.CategoryID.Valid {
		product.CategoryID = p.CategoryID.String
	}
	if p.SKU.Valid {
		product.SKU = p.SKU.String
	}
	if p.BrandName.Valid {
		product.BrandName = p.BrandName.String
	}
	if p.GenericName.Valid {
		product.GenericName = p.GenericName.String
	}
	if p.Strength.Valid {
		product.Strength = p.Strength.String
	}
	if p.DosageForm.Valid {
		product.DosageForm = p.DosageForm.String
	}
	if p.PackSize.Valid {
		product.PackSize = p.PackSize.String
	}
	if p.Unit.Valid {
		product.Unit = p.Unit.String
	}
	if p.ActiveIngredient.Valid {
		product.ActiveIngredient = p.ActiveIngredient.String
	}
	if p.PriceSource.Valid {
		product.PriceSource = p.PriceSource.String
	}

	return product
}

// OrderModel represents the order table structure
type OrderModel struct {
	ID                     string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	UserID                 string         `gorm:"column:user_id;type:uuid;not null"`
	CustomerPhone          string         `gorm:"column:customer_phone;type:varchar(20);not null;index"`
	TableNumber            string         `gorm:"column:table_number;type:varchar(20)"`
	TotalAmount            float64        `gorm:"column:total_amount;type:decimal(10,2);not null"`
	Status                 string         `gorm:"column:status;type:varchar(20);not null;default:'PENDING';index"`
	PaymentMethod          string         `gorm:"column:payment_method;type:varchar(20)"`
	PaymentRef             string         `gorm:"column:payment_reference;type:varchar(255)"`
	IdempotencyKey         string         `gorm:"column:idempotency_key;type:varchar(255)"`
	PickupCode             string         `gorm:"column:pickup_code;type:varchar(6);index"` // 6-digit pickup code for bar staff
	FulfillmentType        string         `gorm:"column:fulfillment_type;type:varchar(20);not null;default:'PICKUP'"`
	DeliveryZoneID         sql.NullString `gorm:"column:delivery_zone_id;type:uuid"`
	DeliveryZoneName       sql.NullString `gorm:"column:delivery_zone_name;->"`
	DeliveryFee            float64        `gorm:"column:delivery_fee;type:decimal(10,2);not null;default:0"`
	DeliveryAddress        sql.NullString `gorm:"column:delivery_address;type:text"`
	DeliveryContactName    sql.NullString `gorm:"column:delivery_contact_name;type:varchar(255)"`
	DeliveryNotes          sql.NullString `gorm:"column:delivery_notes;type:text"`
	ReviewRequired         bool           `gorm:"column:review_required;type:boolean;not null;default:false"`
	ReviewNotes            sql.NullString `gorm:"column:review_notes;type:text"`
	ReviewedAt             sql.NullTime   `gorm:"column:reviewed_at;type:timestamp"`
	ReviewedByAdminUserID  sql.NullString `gorm:"column:reviewed_by_admin_user_id;type:uuid"`
	PrescriptionCount      int            `gorm:"column:prescription_count;type:integer;not null;default:0"`
	PaidAt                 sql.NullTime   `gorm:"column:paid_at;type:timestamp"`
	PreparingAt            sql.NullTime   `gorm:"column:preparing_at;type:timestamp"`
	PreparingByAdminUserID sql.NullString `gorm:"column:preparing_by_admin_user_id;type:uuid"`
	PreparingByName        sql.NullString `gorm:"column:preparing_by_name;->"`
	PreparingByCode        sql.NullString `gorm:"column:preparing_by_code;->"`
	ReadyAt                sql.NullTime   `gorm:"column:ready_at;type:timestamp"`
	ReadyByAdminUserID     sql.NullString `gorm:"column:ready_by_admin_user_id;type:uuid"`
	ReadyByName            sql.NullString `gorm:"column:ready_by_name;->"`
	ReadyByCode            sql.NullString `gorm:"column:ready_by_code;->"`
	CompletedAt            sql.NullTime   `gorm:"column:completed_at;type:timestamp"`
	CompletedByAdminUserID sql.NullString `gorm:"column:completed_by_admin_user_id;type:uuid"`
	CompletedByName        sql.NullString `gorm:"column:completed_by_name;->"`
	CompletedByCode        sql.NullString `gorm:"column:completed_by_code;->"`
	ExpiresAt              sql.NullTime   `gorm:"column:expires_at;type:timestamp"`
	StockReleased          bool           `gorm:"column:stock_released;type:boolean;not null;default:false"`
	CreatedAt              time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt              time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OrderModel) TableName() string {
	return "orders"
}

// OrderModelFromDomain creates OrderModel from core.Order
func OrderModelFromDomain(order *core.Order) *OrderModel {
	paidAt := sql.NullTime{}
	if order.PaidAt != nil {
		paidAt = sql.NullTime{
			Time:  *order.PaidAt,
			Valid: true,
		}
	}

	preparingAt := sql.NullTime{}
	if order.PreparingAt != nil {
		preparingAt = sql.NullTime{
			Time:  *order.PreparingAt,
			Valid: true,
		}
	}

	readyAt := sql.NullTime{}
	if order.ReadyAt != nil {
		readyAt = sql.NullTime{
			Time:  *order.ReadyAt,
			Valid: true,
		}
	}

	completedAt := sql.NullTime{}
	if order.CompletedAt != nil {
		completedAt = sql.NullTime{
			Time:  *order.CompletedAt,
			Valid: true,
		}
	}

	expiresAt := sql.NullTime{}
	if order.ExpiresAt != nil {
		expiresAt = sql.NullTime{
			Time:  *order.ExpiresAt,
			Valid: true,
		}
	}

	preparingBy := sql.NullString{}
	if order.PreparingByUserID != "" {
		preparingBy = sql.NullString{
			String: order.PreparingByUserID,
			Valid:  true,
		}
	}

	readyBy := sql.NullString{}
	if order.ReadyByUserID != "" {
		readyBy = sql.NullString{
			String: order.ReadyByUserID,
			Valid:  true,
		}
	}

	completedBy := sql.NullString{}
	if order.CompletedByUserID != "" {
		completedBy = sql.NullString{
			String: order.CompletedByUserID,
			Valid:  true,
		}
	}

	return &OrderModel{
		ID:                     order.ID,
		UserID:                 order.UserID,
		CustomerPhone:          order.CustomerPhone,
		TableNumber:            order.TableNumber,
		TotalAmount:            order.TotalAmount,
		Status:                 string(order.Status),
		PaymentMethod:          order.PaymentMethod,
		PaymentRef:             order.PaymentRef,
		IdempotencyKey:         "",
		PickupCode:             order.PickupCode,
		FulfillmentType:        order.FulfillmentType,
		DeliveryZoneID:         nullableString(order.DeliveryZoneID),
		DeliveryFee:            order.DeliveryFee,
		DeliveryAddress:        nullableString(order.DeliveryAddress),
		DeliveryContactName:    nullableString(order.DeliveryContactName),
		DeliveryNotes:          nullableString(order.DeliveryNotes),
		ReviewRequired:         order.ReviewRequired,
		ReviewNotes:            nullableString(order.ReviewNotes),
		PrescriptionCount:      order.PrescriptionCount,
		PaidAt:                 paidAt,
		PreparingAt:            preparingAt,
		PreparingByAdminUserID: preparingBy,
		ReadyAt:                readyAt,
		ReadyByAdminUserID:     readyBy,
		CompletedAt:            completedAt,
		CompletedByAdminUserID: completedBy,
		ExpiresAt:              expiresAt,
		CreatedAt:              order.CreatedAt,
		UpdatedAt:              order.UpdatedAt,
	}
}

// ToDomain converts OrderModel to core.Order
func (o *OrderModel) ToDomain() *core.Order {
	var preparingAt *time.Time
	if o.PreparingAt.Valid {
		t := o.PreparingAt.Time
		preparingAt = &t
	}

	var paidAt *time.Time
	if o.PaidAt.Valid {
		t := o.PaidAt.Time
		paidAt = &t
	}

	var readyAt *time.Time
	if o.ReadyAt.Valid {
		t := o.ReadyAt.Time
		readyAt = &t
	}

	var completedAt *time.Time
	if o.CompletedAt.Valid {
		t := o.CompletedAt.Time
		completedAt = &t
	}

	var expiresAt *time.Time
	if o.ExpiresAt.Valid {
		t := o.ExpiresAt.Time
		expiresAt = &t
	}
	var reviewedAt *time.Time
	if o.ReviewedAt.Valid {
		t := o.ReviewedAt.Time
		reviewedAt = &t
	}

	preparingBy := ""
	if o.PreparingByAdminUserID.Valid {
		preparingBy = o.PreparingByAdminUserID.String
	}

	preparingByName := ""
	if o.PreparingByName.Valid {
		preparingByName = o.PreparingByName.String
	}
	preparingByCode := ""
	if o.PreparingByCode.Valid {
		preparingByCode = o.PreparingByCode.String
	}

	readyBy := ""
	if o.ReadyByAdminUserID.Valid {
		readyBy = o.ReadyByAdminUserID.String
	}

	readyByName := ""
	if o.ReadyByName.Valid {
		readyByName = o.ReadyByName.String
	}
	readyByCode := ""
	if o.ReadyByCode.Valid {
		readyByCode = o.ReadyByCode.String
	}

	completedBy := ""
	if o.CompletedByAdminUserID.Valid {
		completedBy = o.CompletedByAdminUserID.String
	}

	completedByName := ""
	if o.CompletedByName.Valid {
		completedByName = o.CompletedByName.String
	}
	completedByCode := ""
	if o.CompletedByCode.Valid {
		completedByCode = o.CompletedByCode.String
	}
	deliveryZoneID := ""
	if o.DeliveryZoneID.Valid {
		deliveryZoneID = o.DeliveryZoneID.String
	}
	deliveryZoneName := ""
	if o.DeliveryZoneName.Valid {
		deliveryZoneName = o.DeliveryZoneName.String
	}
	deliveryAddress := ""
	if o.DeliveryAddress.Valid {
		deliveryAddress = o.DeliveryAddress.String
	}
	deliveryContactName := ""
	if o.DeliveryContactName.Valid {
		deliveryContactName = o.DeliveryContactName.String
	}
	deliveryNotes := ""
	if o.DeliveryNotes.Valid {
		deliveryNotes = o.DeliveryNotes.String
	}
	reviewNotes := ""
	if o.ReviewNotes.Valid {
		reviewNotes = o.ReviewNotes.String
	}
	reviewedBy := ""
	if o.ReviewedByAdminUserID.Valid {
		reviewedBy = o.ReviewedByAdminUserID.String
	}

	return &core.Order{
		ID:                  o.ID,
		UserID:              o.UserID,
		CustomerPhone:       o.CustomerPhone,
		TableNumber:         o.TableNumber,
		TotalAmount:         o.TotalAmount,
		Status:              core.OrderStatus(o.Status),
		PaymentMethod:       o.PaymentMethod,
		PaymentRef:          o.PaymentRef,
		PickupCode:          o.PickupCode,
		FulfillmentType:     o.FulfillmentType,
		DeliveryZoneID:      deliveryZoneID,
		DeliveryZoneName:    deliveryZoneName,
		DeliveryFee:         o.DeliveryFee,
		DeliveryAddress:     deliveryAddress,
		DeliveryContactName: deliveryContactName,
		DeliveryNotes:       deliveryNotes,
		ReviewRequired:      o.ReviewRequired,
		ReviewNotes:         reviewNotes,
		ReviewedAt:          reviewedAt,
		ReviewedByUserID:    reviewedBy,
		PrescriptionCount:   o.PrescriptionCount,
		PaidAt:              paidAt,
		PreparingAt:         preparingAt,
		PreparingByUserID:   preparingBy,
		PreparingByName:     preparingByName,
		PreparingByCode:     preparingByCode,
		ReadyAt:             readyAt,
		ReadyByUserID:       readyBy,
		ReadyByName:         readyByName,
		ReadyByCode:         readyByCode,
		CompletedAt:         completedAt,
		CompletedByUserID:   completedBy,
		CompletedByName:     completedByName,
		CompletedByCode:     completedByCode,
		ExpiresAt:           expiresAt,
		CreatedAt:           o.CreatedAt,
		UpdatedAt:           o.UpdatedAt,
		Items:               []core.OrderItem{},
	}
}

func nullableString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

// OrderItemModel represents the order_items table structure
type OrderItemModel struct {
	ID          string  `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	OrderID     string  `gorm:"column:order_id;type:uuid;not null"`
	ProductID   string  `gorm:"column:product_id;type:uuid;not null"`
	Quantity    int     `gorm:"column:quantity;type:integer;not null"`
	PriceAtTime float64 `gorm:"column:price_at_time;type:decimal(10,2);not null"`
}

func (OrderItemModel) TableName() string {
	return "order_items"
}

// OrderItemModelFromDomain creates OrderItemModel from core.OrderItem
func OrderItemModelFromDomain(item *core.OrderItem) *OrderItemModel {
	return &OrderItemModel{
		ID:          item.ID,
		OrderID:     item.OrderID,
		ProductID:   item.ProductID,
		Quantity:    item.Quantity,
		PriceAtTime: item.PriceAtTime,
	}
}

// ToDomain converts OrderItemModel to core.OrderItem
func (oi *OrderItemModel) ToDomain() *core.OrderItem {
	return &core.OrderItem{
		ID:          oi.ID,
		OrderID:     oi.OrderID,
		ProductID:   oi.ProductID,
		Quantity:    oi.Quantity,
		PriceAtTime: oi.PriceAtTime,
	}
}

// UserRepository implementation

// UserModel represents the users table structure
type UserModel struct {
	ID          string    `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	PhoneNumber string    `gorm:"column:phone_number;type:varchar(20);not null;uniqueIndex"`
	Name        string    `gorm:"column:name;type:varchar(255)"`
	CreatedAt   time.Time `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (UserModel) TableName() string {
	return "users"
}

// ToDomain converts UserModel to core.User
func (u *UserModel) ToDomain() *core.User {
	return &core.User{
		ID:          u.ID,
		PhoneNumber: u.PhoneNumber,
		Name:        u.Name,
		CreatedAt:   u.CreatedAt,
	}
}

// GetByID retrieves a user by ID.
func (r *userRepository) GetByID(ctx context.Context, id string) (*core.User, error) {
	var userModel UserModel
	if err := r.db.WithContext(ctx).Table("users").Where("id = ?", id).First(&userModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return userModel.ToDomain(), nil
}

// GetByPhone retrieves a user by phone number
func (r *userRepository) GetByPhone(ctx context.Context, phone string) (*core.User, error) {
	var userModel UserModel
	patterns := buildPhoneSearchPatterns(canonicalizePhoneForStorage(phone))
	query := r.db.WithContext(ctx).Table("users")
	if len(patterns) > 0 {
		query = query.Where("phone_number IN ?", patterns)
	} else {
		query = query.Where("phone_number = ?", strings.TrimSpace(phone))
	}
	if err := query.First(&userModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return userModel.ToDomain(), nil
}

// Create creates a new user
func (r *userRepository) Create(ctx context.Context, user *core.User) error {
	phoneNumber := canonicalizePhoneForStorage(user.PhoneNumber)
	if phoneNumber == "" {
		phoneNumber = strings.TrimSpace(user.PhoneNumber)
	}
	userModel := &UserModel{
		ID:          user.ID,
		PhoneNumber: phoneNumber,
		Name:        user.Name,
		CreatedAt:   user.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Table("users").Create(userModel).Error; err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// GetOrCreateByPhone retrieves a user by phone or creates one if not found
func (r *userRepository) GetOrCreateByPhone(ctx context.Context, phone string) (*core.User, error) {
	phone = canonicalizePhoneForStorage(phone)
	user, err := r.GetByPhone(ctx, phone)
	if err == nil && user != nil {
		return user, nil
	}

	now := time.Now()
	newUser := &core.User{
		ID:          uuid.New().String(),
		PhoneNumber: phone,
		Name:        "",
		CreatedAt:   now,
	}

	userModel := &UserModel{
		ID:          newUser.ID,
		PhoneNumber: phone,
		Name:        "",
		CreatedAt:   now,
	}

	if err := r.db.WithContext(ctx).Table("users").
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "phone_number"}},
			DoNothing: true,
		}).
		Create(userModel).Error; err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	user, err = r.GetByPhone(ctx, phone)
	if err != nil {
		return nil, fmt.Errorf("failed to load user after upsert: %w", err)
	}

	return user, nil
}

// AdminUserRepository implementation

// AdminUserModel represents the admin_users table structure
type AdminUserModel struct {
	ID            string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	PhoneNumber   string         `gorm:"column:phone_number;type:varchar(20);not null;uniqueIndex"`
	Name          string         `gorm:"column:name;type:varchar(255);not null"`
	Role          string         `gorm:"column:role;type:varchar(20);not null;default:'MANAGER'"`
	BartenderCode sql.NullString `gorm:"column:bartender_code;type:varchar(4)"`
	PinHash       sql.NullString `gorm:"column:pin_hash;type:varchar(255)"`
	IsActive      bool           `gorm:"column:is_active;type:boolean;not null;default:true"`
	CreatedAt     time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (AdminUserModel) TableName() string {
	return "admin_users"
}

// ToDomain converts AdminUserModel to core.AdminUser
func (a *AdminUserModel) ToDomain() *core.AdminUser {
	pinHash := ""
	if a.PinHash.Valid {
		pinHash = a.PinHash.String
	}
	bartenderCode := ""
	if a.BartenderCode.Valid {
		bartenderCode = a.BartenderCode.String
	}

	return &core.AdminUser{
		ID:            a.ID,
		PhoneNumber:   a.PhoneNumber,
		Name:          a.Name,
		Role:          a.Role,
		BartenderCode: bartenderCode,
		PinHash:       pinHash,
		IsActive:      a.IsActive,
		CreatedAt:     a.CreatedAt,
	}
}

// GetByPhone retrieves an admin user by phone number
func (r *adminUserRepository) GetByPhone(ctx context.Context, phone string) (*core.AdminUser, error) {
	var adminModel AdminUserModel
	patterns := buildPhoneSearchPatterns(canonicalizePhoneForStorage(phone))
	query := r.db.WithContext(ctx).Table("admin_users")
	if len(patterns) > 0 {
		query = query.Where("phone_number IN ?", patterns)
	} else {
		query = query.Where("phone_number = ?", strings.TrimSpace(phone))
	}
	if err := query.First(&adminModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("admin user not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get admin user: %w", err)
	}
	return adminModel.ToDomain(), nil
}

// GetByID retrieves an admin user by ID.
func (r *adminUserRepository) GetByID(ctx context.Context, id string) (*core.AdminUser, error) {
	var adminModel AdminUserModel
	if err := r.db.WithContext(ctx).Table("admin_users").Where("id = ?", id).First(&adminModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("admin user not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get admin user: %w", err)
	}
	return adminModel.ToDomain(), nil
}

// GetActiveByRole retrieves active admin users by role.
func (r *adminUserRepository) GetActiveByRole(ctx context.Context, role string) ([]*core.AdminUser, error) {
	var adminModels []AdminUserModel
	if err := r.db.WithContext(ctx).Table("admin_users").
		Where("role = ? AND is_active = ?", role, true).
		Find(&adminModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get admin users by role: %w", err)
	}

	users := make([]*core.AdminUser, len(adminModels))
	for i := range adminModels {
		users[i] = adminModels[i].ToDomain()
	}

	return users, nil
}

// GetByRole retrieves all admin users by role, including inactive users.
func (r *adminUserRepository) GetByRole(ctx context.Context, role string) ([]*core.AdminUser, error) {
	var adminModels []AdminUserModel
	if err := r.db.WithContext(ctx).Table("admin_users").
		Where("role = ?", role).
		Order("name ASC").
		Find(&adminModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get admin users by role: %w", err)
	}

	users := make([]*core.AdminUser, len(adminModels))
	for i := range adminModels {
		users[i] = adminModels[i].ToDomain()
	}

	return users, nil
}

// GetActiveBartenders retrieves all active bartender accounts that have a PIN configured.
func (r *adminUserRepository) GetActiveBartenders(ctx context.Context) ([]*core.AdminUser, error) {
	var adminModels []AdminUserModel
	if err := r.db.WithContext(ctx).Table("admin_users").
		Where("role = ? AND is_active = ? AND pin_hash IS NOT NULL AND pin_hash <> ''", core.AdminRoleBartender, true).
		Order("name ASC").
		Find(&adminModels).Error; err != nil {
		return nil, fmt.Errorf("failed to get active bartenders: %w", err)
	}

	users := make([]*core.AdminUser, len(adminModels))
	for i := range adminModels {
		users[i] = adminModels[i].ToDomain()
	}

	return users, nil
}

// Create creates a new admin user
func (r *adminUserRepository) Create(ctx context.Context, user *core.AdminUser) error {
	phoneNumber := canonicalizePhoneForStorage(user.PhoneNumber)
	if phoneNumber == "" {
		phoneNumber = strings.TrimSpace(user.PhoneNumber)
	}
	pinHash := sql.NullString{}
	if user.PinHash != "" {
		pinHash = sql.NullString{
			String: user.PinHash,
			Valid:  true,
		}
	}
	bartenderCode := sql.NullString{}
	if strings.TrimSpace(user.BartenderCode) != "" {
		bartenderCode = sql.NullString{
			String: strings.TrimSpace(user.BartenderCode),
			Valid:  true,
		}
	}

	adminModel := &AdminUserModel{
		ID:            user.ID,
		PhoneNumber:   phoneNumber,
		Name:          user.Name,
		Role:          user.Role,
		BartenderCode: bartenderCode,
		PinHash:       pinHash,
		IsActive:      user.IsActive,
		CreatedAt:     user.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Table("admin_users").Create(adminModel).Error; err != nil {
		return fmt.Errorf("failed to create admin user: %w", err)
	}
	return nil
}

// Update updates mutable admin user fields.
func (r *adminUserRepository) Update(ctx context.Context, user *core.AdminUser) error {
	phoneNumber := canonicalizePhoneForStorage(user.PhoneNumber)
	if phoneNumber == "" {
		phoneNumber = strings.TrimSpace(user.PhoneNumber)
	}
	result := r.db.WithContext(ctx).Table("admin_users").
		Where("id = ?", user.ID).
		Updates(map[string]interface{}{
			"name":         user.Name,
			"phone_number": phoneNumber,
			"is_active":    user.IsActive,
		})

	if result.Error != nil {
		return fmt.Errorf("failed to update admin user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("admin user not found")
	}

	return nil
}

// UpdatePinHash updates an admin user's PIN hash.
func (r *adminUserRepository) UpdatePinHash(ctx context.Context, userID string, pinHash string) error {
	result := r.db.WithContext(ctx).Table("admin_users").
		Where("id = ?", userID).
		Update("pin_hash", pinHash)

	if result.Error != nil {
		return fmt.Errorf("failed to update admin user PIN: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("admin user not found")
	}

	return nil
}

// IsActive checks if an admin user is active
func (r *adminUserRepository) IsActive(ctx context.Context, phone string) (bool, error) {
	var adminModel AdminUserModel
	patterns := buildPhoneSearchPatterns(canonicalizePhoneForStorage(phone))
	query := r.db.WithContext(ctx).Table("admin_users").Select("is_active")
	if len(patterns) > 0 {
		query = query.Where("phone_number IN ?", patterns)
	} else {
		query = query.Where("phone_number = ?", strings.TrimSpace(phone))
	}
	if err := query.First(&adminModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check admin status: %w", err)
	}
	return adminModel.IsActive, nil
}

// OTPRepository implementation

// OTPCodeModel represents the otp_codes table structure
type OTPCodeModel struct {
	ID          string    `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	PhoneNumber string    `gorm:"column:phone_number;type:varchar(20);not null;index"`
	Code        string    `gorm:"column:code;type:varchar(6);not null"`
	ExpiresAt   time.Time `gorm:"column:expires_at;type:timestamp;not null"`
	Verified    bool      `gorm:"column:verified;type:boolean;not null;default:false"`
	CreatedAt   time.Time `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OTPCodeModel) TableName() string {
	return "otp_codes"
}

// ToDomain converts OTPCodeModel to core.OTPCode
func (o *OTPCodeModel) ToDomain() *core.OTPCode {
	return &core.OTPCode{
		ID:          o.ID,
		PhoneNumber: o.PhoneNumber,
		Code:        o.Code,
		ExpiresAt:   o.ExpiresAt,
		Verified:    o.Verified,
		CreatedAt:   o.CreatedAt,
	}
}

// Create creates a new OTP code
func (r *otpRepository) Create(ctx context.Context, otp *core.OTPCode) error {
	otpModel := &OTPCodeModel{
		ID:          otp.ID,
		PhoneNumber: otp.PhoneNumber,
		Code:        otp.Code,
		ExpiresAt:   otp.ExpiresAt,
		Verified:    otp.Verified,
		CreatedAt:   otp.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Table("otp_codes").Create(otpModel).Error; err != nil {
		return fmt.Errorf("failed to create OTP code: %w", err)
	}
	return nil
}

// GetLatestByPhone retrieves the latest unverified OTP code for a phone number
func (r *otpRepository) GetLatestByPhone(ctx context.Context, phone string) (*core.OTPCode, error) {
	var otpModel OTPCodeModel
	if err := r.db.WithContext(ctx).Table("otp_codes").
		Where("phone_number = ? AND verified = ?", phone, false).
		Order("created_at DESC").
		First(&otpModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("OTP code not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get OTP code: %w", err)
	}
	return otpModel.ToDomain(), nil
}

// MarkAsVerified marks an OTP code as verified
func (r *otpRepository) MarkAsVerified(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Table("otp_codes").
		Where("id = ?", id).
		Update("verified", true)

	if result.Error != nil {
		return fmt.Errorf("failed to mark OTP as verified: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("OTP code not found")
	}
	return nil
}

// CleanupExpired deletes expired OTP codes
func (r *otpRepository) CleanupExpired(ctx context.Context) error {
	result := r.db.WithContext(ctx).Table("otp_codes").
		Where("expires_at < ?", time.Now()).
		Delete(&OTPCodeModel{})

	if result.Error != nil {
		return fmt.Errorf("failed to cleanup expired OTP codes: %w", result.Error)
	}
	return nil
}

// AnalyticsRepository implementation

// GetOverview retrieves dashboard overview metrics for today
func (r *analyticsRepository) GetOverview(ctx context.Context) (*core.Analytics, error) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	settledStatuses := []string{"PAID", "PREPARING", "READY", "COMPLETED"}

	var analytics core.Analytics

	// Get today's revenue and order count
	type TodayStats struct {
		Revenue    float64
		OrderCount int
	}
	var todayStats TodayStats
	if err := r.db.WithContext(ctx).Table("orders").
		Select("COALESCE(SUM(total_amount), 0) as revenue, COUNT(*) as order_count").
		Where("status IN ? AND created_at >= ?", settledStatuses, startOfDay).
		Scan(&todayStats).Error; err != nil {
		return nil, fmt.Errorf("failed to get today's stats: %w", err)
	}

	analytics.TodayRevenue = todayStats.Revenue
	analytics.TodayOrders = todayStats.OrderCount

	// Calculate average order value
	if todayStats.OrderCount > 0 {
		analytics.AverageOrderValue = todayStats.Revenue / float64(todayStats.OrderCount)
	}

	// Get best seller for today
	type BestSellerResult struct {
		ProductName string
		Quantity    int
	}
	var bestSeller BestSellerResult
	if err := r.db.WithContext(ctx).Table("order_items").
		Select("products.name as product_name, SUM(order_items.quantity) as quantity").
		Joins("JOIN orders ON order_items.order_id = orders.id").
		Joins("JOIN products ON order_items.product_id = products.id").
		Where("orders.status IN ? AND orders.created_at >= ?", settledStatuses, startOfDay).
		Group("products.name").
		Order("quantity DESC").
		Limit(1).
		Scan(&bestSeller).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to get best seller: %w", err)
	}

	analytics.BestSeller = core.BestSeller{
		Name:     bestSeller.ProductName,
		Quantity: bestSeller.Quantity,
	}

	return &analytics, nil
}

// GetRevenueTrend retrieves daily revenue data for the specified number of days
func (r *analyticsRepository) GetRevenueTrend(ctx context.Context, days int) ([]*core.RevenueTrend, error) {
	startDate := time.Now().AddDate(0, 0, -days)
	settledStatuses := []string{"PAID", "PREPARING", "READY", "COMPLETED"}

	type TrendResult struct {
		Date       string
		Revenue    float64
		OrderCount int
	}

	var results []TrendResult
	if err := r.db.WithContext(ctx).Table("orders").
		Select("DATE(created_at) as date, COALESCE(SUM(total_amount), 0) as revenue, COUNT(*) as order_count").
		Where("status IN ? AND created_at >= ?", settledStatuses, startDate).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to get revenue trend: %w", err)
	}

	trends := make([]*core.RevenueTrend, len(results))
	for i, r := range results {
		trends[i] = &core.RevenueTrend{
			Date:       r.Date,
			Revenue:    r.Revenue,
			OrderCount: r.OrderCount,
		}
	}

	return trends, nil
}

// GetTopProducts retrieves top-selling products by revenue
func (r *analyticsRepository) GetTopProducts(ctx context.Context, limit int) ([]*core.TopProduct, error) {
	// Get data for last 30 days
	startDate := time.Now().AddDate(0, 0, -30)
	settledStatuses := []string{"PAID", "PREPARING", "READY", "COMPLETED"}

	type ProductResult struct {
		ProductName  string
		QuantitySold int
		Revenue      float64
	}

	var results []ProductResult
	if err := r.db.WithContext(ctx).Table("order_items").
		Select("products.name as product_name, SUM(order_items.quantity) as quantity_sold, SUM(order_items.quantity * order_items.price_at_time) as revenue").
		Joins("JOIN orders ON order_items.order_id = orders.id").
		Joins("JOIN products ON order_items.product_id = products.id").
		Where("orders.status IN ? AND orders.created_at >= ?", settledStatuses, startDate).
		Group("products.name").
		Order("revenue DESC").
		Limit(limit).
		Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to get top products: %w", err)
	}

	products := make([]*core.TopProduct, len(results))
	for i, r := range results {
		products[i] = &core.TopProduct{
			ProductName:  r.ProductName,
			QuantitySold: r.QuantitySold,
			Revenue:      r.Revenue,
		}
	}

	return products, nil
}
