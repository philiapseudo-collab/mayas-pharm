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

const mpesaDispatchInterval = 2100 * time.Millisecond

type PaymentAttemptModel struct {
	ID                string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	OrderID           string         `gorm:"column:order_id;type:uuid;not null"`
	Provider          string         `gorm:"column:provider;type:varchar(20);not null"`
	Status            string         `gorm:"column:status;type:varchar(32);not null"`
	IdempotencyKey    sql.NullString `gorm:"column:idempotency_key;type:varchar(255)"`
	RequestedPhone    sql.NullString `gorm:"column:requested_phone;type:varchar(20)"`
	Amount            float64        `gorm:"column:amount;type:decimal(10,2);not null"`
	ProviderReference sql.NullString `gorm:"column:provider_reference;type:varchar(255)"`
	CheckoutURL       sql.NullString `gorm:"column:checkout_url;type:text"`
	LastError         sql.NullString `gorm:"column:last_error;type:text"`
	Attempts          int            `gorm:"column:attempts;type:integer;not null;default:0"`
	NextRetryAt       time.Time      `gorm:"column:next_retry_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	DispatchedAt      sql.NullTime   `gorm:"column:dispatched_at;type:timestamp"`
	CompletedAt       sql.NullTime   `gorm:"column:completed_at;type:timestamp"`
	CreatedAt         time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt         time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (PaymentAttemptModel) TableName() string {
	return "payment_attempts"
}

func paymentAttemptModelFromDomain(attempt *core.PaymentAttempt) *PaymentAttemptModel {
	idempotencyKey := sql.NullString{}
	if strings.TrimSpace(attempt.IdempotencyKey) != "" {
		idempotencyKey = sql.NullString{String: strings.TrimSpace(attempt.IdempotencyKey), Valid: true}
	}

	requestedPhone := sql.NullString{}
	if strings.TrimSpace(attempt.RequestedPhone) != "" {
		requestedPhone = sql.NullString{String: canonicalizePhoneForStorage(attempt.RequestedPhone), Valid: true}
	}

	providerReference := sql.NullString{}
	if strings.TrimSpace(attempt.ProviderReference) != "" {
		providerReference = sql.NullString{String: strings.TrimSpace(attempt.ProviderReference), Valid: true}
	}

	checkoutURL := sql.NullString{}
	if strings.TrimSpace(attempt.CheckoutURL) != "" {
		checkoutURL = sql.NullString{String: strings.TrimSpace(attempt.CheckoutURL), Valid: true}
	}

	lastError := sql.NullString{}
	if strings.TrimSpace(attempt.LastError) != "" {
		lastError = sql.NullString{String: strings.TrimSpace(attempt.LastError), Valid: true}
	}

	dispatchedAt := sql.NullTime{}
	if attempt.DispatchedAt != nil {
		dispatchedAt = sql.NullTime{Time: *attempt.DispatchedAt, Valid: true}
	}

	completedAt := sql.NullTime{}
	if attempt.CompletedAt != nil {
		completedAt = sql.NullTime{Time: *attempt.CompletedAt, Valid: true}
	}

	nextRetryAt := attempt.NextRetryAt
	if nextRetryAt == nil {
		now := time.Now()
		nextRetryAt = &now
	}

	return &PaymentAttemptModel{
		ID:                attempt.ID,
		OrderID:           attempt.OrderID,
		Provider:          strings.TrimSpace(attempt.Provider),
		Status:            string(attempt.Status),
		IdempotencyKey:    idempotencyKey,
		RequestedPhone:    requestedPhone,
		Amount:            attempt.Amount,
		ProviderReference: providerReference,
		CheckoutURL:       checkoutURL,
		LastError:         lastError,
		Attempts:          attempt.Attempts,
		NextRetryAt:       *nextRetryAt,
		DispatchedAt:      dispatchedAt,
		CompletedAt:       completedAt,
		CreatedAt:         attempt.CreatedAt,
		UpdatedAt:         attempt.UpdatedAt,
	}
}

func (m *PaymentAttemptModel) ToDomain() *core.PaymentAttempt {
	idempotencyKey := ""
	if m.IdempotencyKey.Valid {
		idempotencyKey = m.IdempotencyKey.String
	}
	requestedPhone := ""
	if m.RequestedPhone.Valid {
		requestedPhone = m.RequestedPhone.String
	}
	providerReference := ""
	if m.ProviderReference.Valid {
		providerReference = m.ProviderReference.String
	}
	checkoutURL := ""
	if m.CheckoutURL.Valid {
		checkoutURL = m.CheckoutURL.String
	}
	lastError := ""
	if m.LastError.Valid {
		lastError = m.LastError.String
	}

	nextRetryAt := m.NextRetryAt
	var dispatchedAt *time.Time
	if m.DispatchedAt.Valid {
		t := m.DispatchedAt.Time
		dispatchedAt = &t
	}
	var completedAt *time.Time
	if m.CompletedAt.Valid {
		t := m.CompletedAt.Time
		completedAt = &t
	}

	return &core.PaymentAttempt{
		ID:                m.ID,
		OrderID:           m.OrderID,
		Provider:          m.Provider,
		Status:            core.PaymentAttemptStatus(m.Status),
		IdempotencyKey:    idempotencyKey,
		RequestedPhone:    requestedPhone,
		Amount:            m.Amount,
		ProviderReference: providerReference,
		CheckoutURL:       checkoutURL,
		LastError:         lastError,
		Attempts:          m.Attempts,
		NextRetryAt:       &nextRetryAt,
		DispatchedAt:      dispatchedAt,
		CompletedAt:       completedAt,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}
}

type ProviderThrottleModel struct {
	Provider         string       `gorm:"column:provider;type:varchar(20);primaryKey"`
	LastDispatchedAt sql.NullTime `gorm:"column:last_dispatched_at;type:timestamp"`
}

func (ProviderThrottleModel) TableName() string {
	return "provider_throttles"
}

func activeAttemptStatuses() []string {
	return []string{
		string(core.PaymentAttemptStatusQueued),
		string(core.PaymentAttemptStatusProcessing),
		string(core.PaymentAttemptStatusAwaitingCustomer),
	}
}

func (r *paymentAttemptRepository) Create(ctx context.Context, attempt *core.PaymentAttempt) (*core.PaymentAttempt, bool, error) {
	if attempt == nil {
		return nil, false, fmt.Errorf("payment attempt is required")
	}

	if strings.TrimSpace(attempt.OrderID) == "" {
		return nil, false, fmt.Errorf("order ID is required")
	}

	if strings.TrimSpace(attempt.Provider) == "" {
		return nil, false, fmt.Errorf("provider is required")
	}

	if attempt.ID == "" {
		attempt.ID = uuid.New().String()
	}
	if attempt.Status == "" {
		attempt.Status = core.PaymentAttemptStatusQueued
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now()
	}
	if attempt.UpdatedAt.IsZero() {
		attempt.UpdatedAt = attempt.CreatedAt
	}
	if attempt.NextRetryAt == nil {
		next := attempt.CreatedAt
		attempt.NextRetryAt = &next
	}
	attempt.RequestedPhone = canonicalizePhoneForStorage(attempt.RequestedPhone)

	if key := strings.TrimSpace(attempt.IdempotencyKey); key != "" {
		existing, err := r.GetByIdempotencyKey(ctx, key)
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			return existing, false, nil
		}
	}

	existingActive, err := r.GetActiveByOrderID(ctx, attempt.OrderID, attempt.Provider)
	if err != nil {
		return nil, false, err
	}
	if existingActive != nil {
		return existingActive, false, nil
	}

	model := paymentAttemptModelFromDomain(attempt)
	if err := r.db.WithContext(ctx).Table("payment_attempts").Create(model).Error; err != nil {
		if key := strings.TrimSpace(attempt.IdempotencyKey); key != "" && isUniqueViolation(err, "idx_payment_attempts_idempotency_key_unique") {
			existing, getErr := r.GetByIdempotencyKey(ctx, key)
			if getErr != nil {
				return nil, false, getErr
			}
			return existing, false, nil
		}
		if isUniqueViolation(err, "idx_payment_attempts_active_order_provider") {
			existing, getErr := r.GetActiveByOrderID(ctx, attempt.OrderID, attempt.Provider)
			if getErr != nil {
				return nil, false, getErr
			}
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("failed to create payment attempt: %w", err)
	}

	return model.ToDomain(), true, nil
}

func (r *paymentAttemptRepository) GetByID(ctx context.Context, id string) (*core.PaymentAttempt, error) {
	var model PaymentAttemptModel
	if err := r.db.WithContext(ctx).Table("payment_attempts").Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get payment attempt: %w", err)
	}
	return model.ToDomain(), nil
}

func (r *paymentAttemptRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*core.PaymentAttempt, error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return nil, nil
	}

	var model PaymentAttemptModel
	if err := r.db.WithContext(ctx).Table("payment_attempts").Where("idempotency_key = ?", key).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get payment attempt by idempotency key: %w", err)
	}
	return model.ToDomain(), nil
}

func (r *paymentAttemptRepository) GetByOrderID(ctx context.Context, orderID string) ([]*core.PaymentAttempt, error) {
	var models []PaymentAttemptModel
	if err := r.db.WithContext(ctx).Table("payment_attempts").
		Where("order_id = ?", orderID).
		Order("created_at DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to get payment attempts: %w", err)
	}

	attempts := make([]*core.PaymentAttempt, 0, len(models))
	for i := range models {
		attempts = append(attempts, models[i].ToDomain())
	}
	return attempts, nil
}

func (r *paymentAttemptRepository) GetLatestByOrderID(ctx context.Context, orderID string) (*core.PaymentAttempt, error) {
	var model PaymentAttemptModel
	if err := r.db.WithContext(ctx).Table("payment_attempts").
		Where("order_id = ?", orderID).
		Order("created_at DESC").
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get latest payment attempt: %w", err)
	}
	return model.ToDomain(), nil
}

func (r *paymentAttemptRepository) GetActiveByOrderID(ctx context.Context, orderID string, provider string) (*core.PaymentAttempt, error) {
	var model PaymentAttemptModel
	query := r.db.WithContext(ctx).Table("payment_attempts").
		Where("order_id = ? AND status IN ?", orderID, activeAttemptStatuses())
	if strings.TrimSpace(provider) != "" {
		query = query.Where("provider = ?", strings.TrimSpace(provider))
	}

	if err := query.Order("created_at DESC").First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get active payment attempt: %w", err)
	}
	return model.ToDomain(), nil
}

func (r *paymentAttemptRepository) GetByProviderReference(ctx context.Context, provider string, reference string) (*core.PaymentAttempt, error) {
	ref := strings.TrimSpace(reference)
	if ref == "" {
		return nil, nil
	}

	var model PaymentAttemptModel
	if err := r.db.WithContext(ctx).Table("payment_attempts").
		Where("provider = ? AND provider_reference = ?", strings.TrimSpace(provider), ref).
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get payment attempt by provider reference: %w", err)
	}
	return model.ToDomain(), nil
}

func (r *paymentAttemptRepository) ClaimNextQueued(ctx context.Context, provider string, throttleWindow time.Duration) (*core.PaymentAttempt, error) {
	attempt, err := r.claimNextQueued(ctx, provider, throttleWindow, true)
	if err != nil && isPaymentQueueCompatibilityError(err) {
		return r.claimNextQueued(ctx, provider, throttleWindow, false)
	}
	return attempt, err
}

func (r *paymentAttemptRepository) claimNextQueued(ctx context.Context, provider string, throttleWindow time.Duration, useHardeningSchema bool) (*core.PaymentAttempt, error) {
	now := time.Now()
	if throttleWindow <= 0 {
		throttleWindow = mpesaDispatchInterval
	}

	var claimed *PaymentAttemptModel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if useHardeningSchema {
			var throttle ProviderThrottleModel
			if err := tx.Table("provider_throttles").
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("provider = ?", provider).
				First(&throttle).Error; err != nil {
				return fmt.Errorf("failed to lock provider throttle: %w", err)
			}

			if throttle.LastDispatchedAt.Valid && throttle.LastDispatchedAt.Time.Add(throttleWindow).After(now) {
				return nil
			}
		}

		var model PaymentAttemptModel
		query := tx.Table("payment_attempts").
			Joins("JOIN orders ON orders.id = payment_attempts.order_id").
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("payment_attempts.provider = ?", provider).
			Where("payment_attempts.status = ?", string(core.PaymentAttemptStatusQueued)).
			Where("payment_attempts.next_retry_at <= ?", now).
			Where("orders.status = ?", string(core.OrderStatusPending))
		if useHardeningSchema {
			query = query.Where("(orders.expires_at IS NULL OR orders.expires_at > ?)", now)
		}

		err := query.Order("payment_attempts.created_at ASC").First(&model).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return fmt.Errorf("failed to claim queued payment attempt: %w", err)
		}

		if err := tx.Table("payment_attempts").Where("id = ?", model.ID).Updates(map[string]interface{}{
			"status":     string(core.PaymentAttemptStatusProcessing),
			"attempts":   gorm.Expr("attempts + 1"),
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error; err != nil {
			return fmt.Errorf("failed to mark attempt as processing: %w", err)
		}

		if useHardeningSchema {
			if err := tx.Table("provider_throttles").Where("provider = ?", provider).Updates(map[string]interface{}{
				"last_dispatched_at": now,
			}).Error; err != nil {
				return fmt.Errorf("failed to update provider throttle: %w", err)
			}
		}

		model.Status = string(core.PaymentAttemptStatusProcessing)
		model.Attempts++
		model.UpdatedAt = now
		claimed = &model
		return nil
	})
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return nil, nil
	}
	return claimed.ToDomain(), nil
}

func (r *paymentAttemptRepository) MarkAwaitingCustomer(ctx context.Context, id string, providerReference string) error {
	updates := map[string]interface{}{
		"status":        string(core.PaymentAttemptStatusAwaitingCustomer),
		"dispatched_at": gorm.Expr("COALESCE(dispatched_at, CURRENT_TIMESTAMP)"),
		"updated_at":    gorm.Expr("CURRENT_TIMESTAMP"),
	}
	if strings.TrimSpace(providerReference) != "" {
		updates["provider_reference"] = strings.TrimSpace(providerReference)
	}

	if err := r.db.WithContext(ctx).Table("payment_attempts").Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to mark payment attempt awaiting customer: %w", err)
	}
	return nil
}

func (r *paymentAttemptRepository) MarkRetryQueued(ctx context.Context, id string, nextRetryAt time.Time, lastError string) error {
	if err := r.db.WithContext(ctx).Table("payment_attempts").Where("id = ?", id).Updates(map[string]interface{}{
		"status":        string(core.PaymentAttemptStatusQueued),
		"next_retry_at": nextRetryAt,
		"last_error":    strings.TrimSpace(lastError),
		"updated_at":    gorm.Expr("CURRENT_TIMESTAMP"),
	}).Error; err != nil {
		return fmt.Errorf("failed to requeue payment attempt: %w", err)
	}
	return nil
}

func (r *paymentAttemptRepository) MarkFailed(ctx context.Context, id string, providerReference string, lastError string) error {
	updates := map[string]interface{}{
		"status":       string(core.PaymentAttemptStatusFailed),
		"last_error":   strings.TrimSpace(lastError),
		"completed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		"updated_at":   gorm.Expr("CURRENT_TIMESTAMP"),
	}
	if strings.TrimSpace(providerReference) != "" {
		updates["provider_reference"] = strings.TrimSpace(providerReference)
	}
	if err := r.db.WithContext(ctx).Table("payment_attempts").Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to mark payment attempt failed: %w", err)
	}
	return nil
}

func (r *paymentAttemptRepository) MarkSucceeded(ctx context.Context, id string, providerReference string) error {
	updates := map[string]interface{}{
		"status":       string(core.PaymentAttemptStatusSucceeded),
		"completed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		"updated_at":   gorm.Expr("CURRENT_TIMESTAMP"),
	}
	if strings.TrimSpace(providerReference) != "" {
		updates["provider_reference"] = strings.TrimSpace(providerReference)
	}
	if err := r.db.WithContext(ctx).Table("payment_attempts").Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to mark payment attempt succeeded: %w", err)
	}
	return nil
}

func (r *paymentAttemptRepository) ExpireActiveForOrder(ctx context.Context, orderID string, now time.Time) error {
	if err := r.db.WithContext(ctx).Table("payment_attempts").
		Where("order_id = ? AND status IN ?", orderID, activeAttemptStatuses()).
		Updates(map[string]interface{}{
			"status":       string(core.PaymentAttemptStatusExpired),
			"completed_at": now,
			"updated_at":   now,
		}).Error; err != nil {
		return fmt.Errorf("failed to expire payment attempts for order: %w", err)
	}
	return nil
}

func (r *paymentAttemptRepository) GetQueueMetrics(ctx context.Context, attemptID string, provider string, dispatchInterval time.Duration) (int, time.Duration, error) {
	attempt, err := r.GetByID(ctx, attemptID)
	if err != nil {
		return 0, 0, err
	}
	if attempt == nil {
		return 0, 0, nil
	}

	if attempt.Status != core.PaymentAttemptStatusQueued {
		return 0, 0, nil
	}
	if dispatchInterval <= 0 {
		dispatchInterval = mpesaDispatchInterval
	}

	var position int64
	if err := r.db.WithContext(ctx).Table("payment_attempts").
		Where("provider = ? AND status = ?", provider, string(core.PaymentAttemptStatusQueued)).
		Where("(created_at < ?) OR (created_at = ? AND id <= ?)", attempt.CreatedAt, attempt.CreatedAt, attempt.ID).
		Count(&position).Error; err != nil {
		return 0, 0, fmt.Errorf("failed to compute queue position: %w", err)
	}

	var throttle ProviderThrottleModel
	if err := r.db.WithContext(ctx).Table("provider_throttles").Where("provider = ?", provider).First(&throttle).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) && !isUndefinedRelationError(err, "provider_throttles") {
			return 0, 0, fmt.Errorf("failed to load provider throttle: %w", err)
		}
	}

	eta := time.Duration(maxInt64(position-1, 0)) * dispatchInterval
	if throttle.LastDispatchedAt.Valid {
		nextAllowed := throttle.LastDispatchedAt.Time.Add(dispatchInterval)
		if nextAllowed.After(time.Now()) {
			eta += nextAllowed.Sub(time.Now())
		}
	}

	return int(position), eta, nil
}

func (r *paymentAttemptRepository) GetQueueStats(ctx context.Context, provider string) (*core.PaymentQueueStats, error) {
	var row struct {
		QueuedCount    int64        `gorm:"column:queued_count"`
		OldestQueuedAt sql.NullTime `gorm:"column:oldest_queued_at"`
	}

	if err := r.db.WithContext(ctx).
		Table("payment_attempts").
		Select("COUNT(*) AS queued_count, MIN(created_at) AS oldest_queued_at").
		Where("provider = ? AND status = ?", strings.TrimSpace(provider), string(core.PaymentAttemptStatusQueued)).
		Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("failed to load payment queue stats: %w", err)
	}

	stats := &core.PaymentQueueStats{
		Provider:    strings.TrimSpace(provider),
		QueuedCount: int(row.QueuedCount),
	}
	if row.OldestQueuedAt.Valid {
		oldest := row.OldestQueuedAt.Time
		stats.OldestQueuedAt = &oldest
	}
	return stats, nil
}

func maxInt64(value int64, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}
