package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultPendingOrderTTL = 20 * time.Minute

func canonicalizePhoneForStorage(phone string) string {
	digits := extractDigits(phone)
	switch {
	case len(digits) == 12 && strings.HasPrefix(digits, "254"):
		return digits
	case len(digits) == 10 && strings.HasPrefix(digits, "0"):
		return "254" + digits[1:]
	case len(digits) == 9:
		return "254" + digits
	default:
		return strings.TrimSpace(phone)
	}
}

func isUniqueViolation(err error, token string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "duplicate key") || strings.Contains(lower, "unique constraint") {
		if token == "" {
			return true
		}
		return strings.Contains(lower, strings.ToLower(token))
	}
	return false
}

func generateNextPickupCodeTx(ctx context.Context, tx *gorm.DB) (string, error) {
	var pickupCode string
	if err := tx.WithContext(ctx).
		Raw("SELECT LPAD(nextval('pickup_code_seq')::text, 6, '0')").
		Scan(&pickupCode).Error; err != nil {
		return "", fmt.Errorf("failed to generate pickup code: %w", err)
	}
	if pickupCode == "" {
		return "", fmt.Errorf("failed to generate pickup code")
	}
	return pickupCode, nil
}

func mergePendingOrderItems(items []core.PendingOrderItemInput) []core.PendingOrderItemInput {
	merged := make([]core.PendingOrderItemInput, 0, len(items))
	indexByProduct := make(map[string]int, len(items))
	for _, item := range items {
		productID := strings.TrimSpace(item.ProductID)
		if productID == "" {
			continue
		}
		if idx, ok := indexByProduct[productID]; ok {
			merged[idx].Quantity += item.Quantity
			continue
		}
		indexByProduct[productID] = len(merged)
		merged = append(merged, core.PendingOrderItemInput{
			ProductID: productID,
			Quantity:  item.Quantity,
		})
	}
	return merged
}

func (r *orderRepository) fetchOrderItemsForOrders(ctx context.Context, orderIDs []string) (map[string][]core.OrderItem, error) {
	result := make(map[string][]core.OrderItem, len(orderIDs))
	if len(orderIDs) == 0 {
		return result, nil
	}

	type orderItemWithProduct struct {
		OrderItemModel
		ProductName string `gorm:"column:product_name"`
	}

	var rows []orderItemWithProduct
	if err := r.db.WithContext(ctx).
		Table("order_items").
		Select("order_items.*, products.name AS product_name").
		Joins("LEFT JOIN products ON products.id = order_items.product_id").
		Where("order_items.order_id IN ?", orderIDs).
		Order("order_items.created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to batch load order items: %w", err)
	}

	for _, row := range rows {
		item := row.OrderItemModel.ToDomain()
		result[row.OrderID] = append(result[row.OrderID], core.OrderItem{
			ID:          item.ID,
			OrderID:     item.OrderID,
			ProductID:   item.ProductID,
			Quantity:    item.Quantity,
			PriceAtTime: item.PriceAtTime,
			ProductName: row.ProductName,
		})
	}

	for _, orderID := range orderIDs {
		if _, ok := result[orderID]; !ok {
			result[orderID] = []core.OrderItem{}
		}
	}

	return result, nil
}

func (r *orderRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*core.Order, error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return nil, nil
	}

	var orderModel OrderModel
	if err := r.orderQueryWithAdminNames(ctx).
		Where("orders.idempotency_key = ?", key).
		First(&orderModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if isUndefinedColumnError(err, "idempotency_key") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get order by idempotency key: %w", err)
	}

	itemsByOrder, err := r.fetchOrderItemsForOrders(ctx, []string{orderModel.ID})
	if err != nil {
		return nil, err
	}

	order := orderModel.ToDomain()
	order.Items = itemsByOrder[orderModel.ID]
	return order, nil
}

func (r *orderRepository) CreatePendingOrder(ctx context.Context, input core.CreatePendingOrderInput) (*core.Order, bool, error) {
	order, created, err := r.createPendingOrder(ctx, input, true)
	if err != nil && isOrderHardeningCompatibilityError(err) {
		return r.createPendingOrder(ctx, input, false)
	}
	return order, created, err
}

func (r *orderRepository) createPendingOrder(ctx context.Context, input core.CreatePendingOrderInput, useHardeningColumns bool) (*core.Order, bool, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return nil, false, fmt.Errorf("user ID is required")
	}

	mergedItems := mergePendingOrderItems(input.Items)
	if len(mergedItems) == 0 {
		return nil, false, fmt.Errorf("cart is empty")
	}

	orderPhone := canonicalizePhoneForStorage(input.CustomerPhone)
	if strings.TrimSpace(orderPhone) == "" {
		return nil, false, fmt.Errorf("customer phone is required")
	}

	tableNumber := strings.TrimSpace(input.TableNumber)
	if tableNumber == "" {
		tableNumber = "PICKUP"
	}

	expiresAt := input.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(defaultPendingOrderTTL)
	}

	var (
		resultOrder *core.Order
		created     bool
	)

	err := withRetryableTransaction(ctx, r.db, r.metrics, func(tx *gorm.DB) error {
		key := strings.TrimSpace(input.IdempotencyKey)
		if useHardeningColumns && key != "" {
			var existing OrderModel
			if err := tx.Table("orders").Where("idempotency_key = ?", key).First(&existing).Error; err == nil {
				resultOrder = existing.ToDomain()
				created = false
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to check existing order idempotency key: %w", err)
			}
		}

		productIDs := make([]string, 0, len(mergedItems))
		for _, item := range mergedItems {
			if item.Quantity <= 0 {
				return fmt.Errorf("invalid quantity for product %s", item.ProductID)
			}
			productIDs = append(productIDs, item.ProductID)
		}

		var productModels []ProductModel
		if err := tx.Table("products").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ?", productIDs).
			Find(&productModels).Error; err != nil {
			return fmt.Errorf("failed to load products for reservation: %w", err)
		}

		productsByID := make(map[string]ProductModel, len(productModels))
		for _, product := range productModels {
			productsByID[product.ID] = product
		}

		orderID := uuid.New().String()
		pickupCode, err := generateNextPickupCodeTx(ctx, tx)
		if err != nil {
			return err
		}

		orderItems := make([]core.OrderItem, 0, len(mergedItems))
		var totalAmount float64
		reviewRequired := input.ReviewRequired
		for _, item := range mergedItems {
			product, ok := productsByID[item.ProductID]
			if !ok {
				return fmt.Errorf("product not found: %s", item.ProductID)
			}
			if !product.IsActive {
				return fmt.Errorf("product is no longer available: %s", product.Name)
			}
			if product.StockQuantity < item.Quantity {
				return fmt.Errorf("insufficient stock for %s (available: %d)", product.Name, product.StockQuantity)
			}

			if err := tx.Table("products").
				Where("id = ?", product.ID).
				UpdateColumn("stock_quantity", gorm.Expr("stock_quantity - ?", item.Quantity)).Error; err != nil {
				return fmt.Errorf("failed to reserve stock for %s: %w", product.Name, err)
			}

			lineTotal := product.Price * float64(item.Quantity)
			totalAmount += lineTotal
			if product.RequiresPrescription {
				reviewRequired = true
			}
			orderItems = append(orderItems, core.OrderItem{
				ID:                   uuid.New().String(),
				OrderID:              orderID,
				ProductID:            product.ID,
				Quantity:             item.Quantity,
				PriceAtTime:          product.Price,
				ProductName:          product.Name,
				RequiresPrescription: product.RequiresPrescription,
			})
		}
		totalAmount += input.DeliveryFee

		status := input.Status
		if status == "" {
			if reviewRequired {
				status = core.OrderStatusPendingReview
			} else {
				status = core.OrderStatusPending
			}
		}

		fulfillmentType := strings.TrimSpace(input.FulfillmentType)
		if fulfillmentType == "" {
			fulfillmentType = string(core.FulfillmentTypePickup)
		}

		now := time.Now()
		orderModel := &OrderModel{
			ID:                 orderID,
			UserID:             input.UserID,
			CustomerPhone:      orderPhone,
			TableNumber:        tableNumber,
			TotalAmount:        totalAmount,
			Status:             string(status),
			PaymentMethod:      input.PaymentMethod,
			PaymentRef:         "",
			PickupCode:         pickupCode,
			FulfillmentType:    fulfillmentType,
			DeliveryZoneID:     nullableString(input.DeliveryZoneID),
			DeliveryFee:        input.DeliveryFee,
			DeliveryAddress:    nullableString(input.DeliveryAddress),
			DeliveryContactName: nullableString(input.DeliveryContactName),
			DeliveryNotes:      nullableString(input.DeliveryNotes),
			ReviewRequired:     reviewRequired,
			ReviewNotes:        sql.NullString{},
			PrescriptionCount:  0,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if useHardeningColumns {
			orderModel.IdempotencyKey = key
			orderModel.ExpiresAt = sql.NullTime{Time: expiresAt, Valid: true}
			orderModel.StockReleased = false
		}

		insertValues := map[string]interface{}{
			"id":                    orderModel.ID,
			"user_id":               orderModel.UserID,
			"customer_phone":        orderModel.CustomerPhone,
			"table_number":          orderModel.TableNumber,
			"total_amount":          orderModel.TotalAmount,
			"status":                orderModel.Status,
			"payment_method":        orderModel.PaymentMethod,
			"payment_reference":     orderModel.PaymentRef,
			"pickup_code":           orderModel.PickupCode,
			"fulfillment_type":      orderModel.FulfillmentType,
			"delivery_zone_id":      orderModel.DeliveryZoneID,
			"delivery_fee":          orderModel.DeliveryFee,
			"delivery_address":      orderModel.DeliveryAddress,
			"delivery_contact_name": orderModel.DeliveryContactName,
			"delivery_notes":        orderModel.DeliveryNotes,
			"review_required":       orderModel.ReviewRequired,
			"review_notes":          orderModel.ReviewNotes,
			"prescription_count":    orderModel.PrescriptionCount,
			"created_at":            orderModel.CreatedAt,
			"updated_at":            orderModel.UpdatedAt,
		}
		if useHardeningColumns {
			insertValues["idempotency_key"] = orderModel.IdempotencyKey
			insertValues["expires_at"] = orderModel.ExpiresAt
			insertValues["stock_released"] = orderModel.StockReleased
		}

		if err := tx.Table("orders").Create(insertValues).Error; err != nil {
			if useHardeningColumns && key != "" && isUniqueViolation(err, "idx_orders_idempotency_key_unique") {
				var existing OrderModel
				if getErr := tx.Table("orders").Where("idempotency_key = ?", key).First(&existing).Error; getErr != nil {
					return fmt.Errorf("failed to load existing idempotent order: %w", getErr)
				}
				resultOrder = existing.ToDomain()
				created = false
				return nil
			}
			return fmt.Errorf("failed to create order: %w", err)
		}

		for _, item := range orderItems {
			itemModel := OrderItemModelFromDomain(&item)
			if err := tx.Table("order_items").Create(itemModel).Error; err != nil {
				return fmt.Errorf("failed to create order item: %w", err)
			}
		}

		resultOrder = orderModel.ToDomain()
		resultOrder.Items = orderItems
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	if resultOrder != nil && len(resultOrder.Items) == 0 {
		loaded, loadErr := r.GetByID(ctx, resultOrder.ID)
		if loadErr != nil {
			return nil, created, loadErr
		}
		resultOrder = loaded
	}

	return resultOrder, created, nil
}

func withRetryableTransaction(ctx context.Context, db *gorm.DB, metrics interface{ IncStockRetry() }, fn func(tx *gorm.DB) error) error {
	const maxAttempts = 3

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return fn(tx)
		})
		if !isRetryableTransactionError(err) || attempt == maxAttempts {
			return err
		}
		if metrics != nil {
			metrics.IncStockRetry()
		}
		time.Sleep(time.Duration(attempt*attempt) * 25 * time.Millisecond)
	}

	return err
}

func isPaidWorkflowStatus(status core.OrderStatus) bool {
	switch status {
	case core.OrderStatusPaid, core.OrderStatusPreparing, core.OrderStatusReady, core.OrderStatusCompleted:
		return true
	default:
		return false
	}
}

func releaseReservedStockTx(ctx context.Context, tx *gorm.DB, orderID string) error {
	type orderQty struct {
		ProductID string
		Quantity  int
	}

	var items []orderQty
	if err := tx.WithContext(ctx).
		Table("order_items").
		Select("product_id, SUM(quantity) AS quantity").
		Where("order_id = ?", orderID).
		Group("product_id").
		Scan(&items).Error; err != nil {
		return fmt.Errorf("failed to load reserved stock for order: %w", err)
	}

	for _, item := range items {
		if err := tx.WithContext(ctx).Table("products").
			Where("id = ?", item.ProductID).
			UpdateColumn("stock_quantity", gorm.Expr("stock_quantity + ?", item.Quantity)).Error; err != nil {
			return fmt.Errorf("failed to release reserved stock: %w", err)
		}
	}

	return nil
}

func reserveStockForExistingOrderTx(ctx context.Context, tx *gorm.DB, orderID string) error {
	type orderQty struct {
		ProductID string
		Quantity  int
	}

	var items []orderQty
	if err := tx.WithContext(ctx).
		Table("order_items").
		Select("product_id, SUM(quantity) AS quantity").
		Where("order_id = ?", orderID).
		Group("product_id").
		Scan(&items).Error; err != nil {
		return fmt.Errorf("failed to load order items for reservation: %w", err)
	}

	if len(items) == 0 {
		return fmt.Errorf("order has no items to reserve")
	}

	productIDs := make([]string, 0, len(items))
	requiredByProduct := make(map[string]int, len(items))
	for _, item := range items {
		productIDs = append(productIDs, item.ProductID)
		requiredByProduct[item.ProductID] = item.Quantity
	}

	var products []ProductModel
	if err := tx.WithContext(ctx).
		Table("products").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ?", productIDs).
		Find(&products).Error; err != nil {
		return fmt.Errorf("failed to lock products for reservation: %w", err)
	}

	productByID := make(map[string]ProductModel, len(products))
	for _, product := range products {
		productByID[product.ID] = product
	}

	for _, item := range items {
		product, ok := productByID[item.ProductID]
		if !ok {
			return fmt.Errorf("product not found: %s", item.ProductID)
		}
		if !product.IsActive {
			return fmt.Errorf("product is no longer available: %s", product.Name)
		}
		if product.StockQuantity < item.Quantity {
			return fmt.Errorf("insufficient stock for %s (available: %d)", product.Name, product.StockQuantity)
		}

		if err := tx.WithContext(ctx).Table("products").
			Where("id = ?", item.ProductID).
			UpdateColumn("stock_quantity", gorm.Expr("stock_quantity - ?", item.Quantity)).Error; err != nil {
			return fmt.Errorf("failed to reserve stock for %s: %w", product.Name, err)
		}
	}

	return nil
}

func (r *orderRepository) MarkPaid(ctx context.Context, id string, paymentMethod string, paymentRef string) (*core.Order, bool, error) {
	order, changed, err := r.markPaid(ctx, id, paymentMethod, paymentRef, true)
	if err != nil && isOrderHardeningCompatibilityError(err) {
		return r.markPaid(ctx, id, paymentMethod, paymentRef, false)
	}
	return order, changed, err
}

func (r *orderRepository) markPaid(ctx context.Context, id string, paymentMethod string, paymentRef string, useHardeningColumns bool) (*core.Order, bool, error) {
	var changed bool

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		if isPaidWorkflowStatus(currentStatus) {
			changed = false
			return nil
		}

		if currentStatus != core.OrderStatusPending {
			return fmt.Errorf("order is not payable from status %s", currentStatus)
		}

		updates := map[string]interface{}{
			"status":                     string(core.OrderStatusPaid),
			"payment_method":             paymentMethod,
			"payment_reference":          paymentRef,
			"paid_at":                    gorm.Expr("COALESCE(paid_at, CURRENT_TIMESTAMP)"),
			"updated_at":                 gorm.Expr("CURRENT_TIMESTAMP"),
			"preparing_at":               nil,
			"preparing_by_admin_user_id": nil,
		}
		if useHardeningColumns {
			updates["expires_at"] = nil
		}
		if err := tx.Table("orders").Where("id = ?", id).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to mark order as paid: %w", err)
		}

		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	order, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, changed, err
	}
	return order, changed, nil
}

func (r *orderRepository) ExpirePendingOrders(ctx context.Context, now time.Time, limit int) ([]*core.Order, error) {
	if limit <= 0 {
		limit = 100
	}

	var orderIDs []string
	if err := r.db.WithContext(ctx).
		Table("orders").
		Select("id").
		Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", string(core.OrderStatusPending), now).
		Order("expires_at ASC").
		Limit(limit).
		Pluck("id", &orderIDs).Error; err != nil {
		if isUndefinedColumnError(err, "expires_at") {
			return []*core.Order{}, nil
		}
		return nil, fmt.Errorf("failed to list expired pending orders: %w", err)
	}

	expiredOrders := make([]*core.Order, 0, len(orderIDs))
	for _, orderID := range orderIDs {
		var expired bool
		if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var orderModel OrderModel
			if err := tx.Table("orders").
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", orderID).
				First(&orderModel).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}

			if core.OrderStatus(orderModel.Status) != core.OrderStatusPending {
				return nil
			}
			if !orderModel.ExpiresAt.Valid || orderModel.ExpiresAt.Time.After(now) {
				return nil
			}

			if !orderModel.StockReleased {
				if err := releaseReservedStockTx(ctx, tx, orderID); err != nil {
					return err
				}
			}

			if err := tx.Table("orders").
				Where("id = ?", orderID).
				Updates(map[string]interface{}{
					"status":         string(core.OrderStatusExpired),
					"stock_released": true,
					"updated_at":     gorm.Expr("CURRENT_TIMESTAMP"),
				}).Error; err != nil {
				return fmt.Errorf("failed to expire order: %w", err)
			}

			if err := tx.Table("payment_attempts").
				Where("order_id = ? AND status IN ?", orderID, []string{
					string(core.PaymentAttemptStatusQueued),
					string(core.PaymentAttemptStatusProcessing),
					string(core.PaymentAttemptStatusAwaitingCustomer),
				}).
				Updates(map[string]interface{}{
					"status":       string(core.PaymentAttemptStatusExpired),
					"completed_at": gorm.Expr("CURRENT_TIMESTAMP"),
					"updated_at":   gorm.Expr("CURRENT_TIMESTAMP"),
				}).Error; err != nil {
				return fmt.Errorf("failed to expire payment attempts: %w", err)
			}

			expired = true
			return nil
		}); err != nil {
			return nil, err
		}

		if !expired {
			continue
		}

		order, err := r.GetByID(ctx, orderID)
		if err != nil {
			return nil, err
		}
		expiredOrders = append(expiredOrders, order)
	}

	return expiredOrders, nil
}
