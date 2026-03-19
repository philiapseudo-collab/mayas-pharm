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
)

type UnmatchedPaymentModel struct {
	ID                string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	Provider          string         `gorm:"column:provider;type:varchar(20);not null"`
	Status            string         `gorm:"column:status;type:varchar(20);not null"`
	ProviderReference sql.NullString `gorm:"column:provider_reference;type:varchar(255)"`
	OrderIDHint       sql.NullString `gorm:"column:order_id_hint;type:varchar(255)"`
	Phone             sql.NullString `gorm:"column:phone;type:varchar(20)"`
	HashedPhone       sql.NullString `gorm:"column:hashed_phone;type:varchar(255)"`
	Amount            float64        `gorm:"column:amount;type:decimal(10,2);not null"`
	Payload           string         `gorm:"column:payload;type:text;not null"`
	ResolutionNote    sql.NullString `gorm:"column:resolution_note;type:text"`
	ResolvedOrderID   sql.NullString `gorm:"column:resolved_order_id;type:uuid"`
	CreatedAt         time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt         time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	ResolvedAt        sql.NullTime   `gorm:"column:resolved_at;type:timestamp"`
}

func (UnmatchedPaymentModel) TableName() string {
	return "unmatched_payments"
}

func unmatchedPaymentModelFromDomain(payment *core.UnmatchedPayment) *UnmatchedPaymentModel {
	providerReference := sql.NullString{}
	if strings.TrimSpace(payment.ProviderReference) != "" {
		providerReference = sql.NullString{String: strings.TrimSpace(payment.ProviderReference), Valid: true}
	}
	orderIDHint := sql.NullString{}
	if strings.TrimSpace(payment.OrderIDHint) != "" {
		orderIDHint = sql.NullString{String: strings.TrimSpace(payment.OrderIDHint), Valid: true}
	}
	phone := sql.NullString{}
	if strings.TrimSpace(payment.Phone) != "" {
		phone = sql.NullString{String: canonicalizePhoneForStorage(payment.Phone), Valid: true}
	}
	hashedPhone := sql.NullString{}
	if strings.TrimSpace(payment.HashedPhone) != "" {
		hashedPhone = sql.NullString{String: strings.TrimSpace(payment.HashedPhone), Valid: true}
	}
	resolutionNote := sql.NullString{}
	if strings.TrimSpace(payment.ResolutionNote) != "" {
		resolutionNote = sql.NullString{String: strings.TrimSpace(payment.ResolutionNote), Valid: true}
	}
	resolvedOrderID := sql.NullString{}
	if strings.TrimSpace(payment.ResolvedOrderID) != "" {
		resolvedOrderID = sql.NullString{String: strings.TrimSpace(payment.ResolvedOrderID), Valid: true}
	}
	resolvedAt := sql.NullTime{}
	if payment.ResolvedAt != nil {
		resolvedAt = sql.NullTime{Time: *payment.ResolvedAt, Valid: true}
	}

	return &UnmatchedPaymentModel{
		ID:                payment.ID,
		Provider:          strings.TrimSpace(payment.Provider),
		Status:            string(payment.Status),
		ProviderReference: providerReference,
		OrderIDHint:       orderIDHint,
		Phone:             phone,
		HashedPhone:       hashedPhone,
		Amount:            payment.Amount,
		Payload:           payment.Payload,
		ResolutionNote:    resolutionNote,
		ResolvedOrderID:   resolvedOrderID,
		CreatedAt:         payment.CreatedAt,
		UpdatedAt:         payment.UpdatedAt,
		ResolvedAt:        resolvedAt,
	}
}

func (m *UnmatchedPaymentModel) ToDomain() *core.UnmatchedPayment {
	providerReference := ""
	if m.ProviderReference.Valid {
		providerReference = m.ProviderReference.String
	}
	orderIDHint := ""
	if m.OrderIDHint.Valid {
		orderIDHint = m.OrderIDHint.String
	}
	phone := ""
	if m.Phone.Valid {
		phone = m.Phone.String
	}
	hashedPhone := ""
	if m.HashedPhone.Valid {
		hashedPhone = m.HashedPhone.String
	}
	resolutionNote := ""
	if m.ResolutionNote.Valid {
		resolutionNote = m.ResolutionNote.String
	}
	resolvedOrderID := ""
	if m.ResolvedOrderID.Valid {
		resolvedOrderID = m.ResolvedOrderID.String
	}
	var resolvedAt *time.Time
	if m.ResolvedAt.Valid {
		t := m.ResolvedAt.Time
		resolvedAt = &t
	}

	return &core.UnmatchedPayment{
		ID:                m.ID,
		Provider:          m.Provider,
		Status:            core.UnmatchedPaymentStatus(m.Status),
		ProviderReference: providerReference,
		OrderIDHint:       orderIDHint,
		Phone:             phone,
		HashedPhone:       hashedPhone,
		Amount:            m.Amount,
		Payload:           m.Payload,
		ResolutionNote:    resolutionNote,
		ResolvedOrderID:   resolvedOrderID,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
		ResolvedAt:        resolvedAt,
	}
}

func (r *unmatchedPaymentRepository) Create(ctx context.Context, payment *core.UnmatchedPayment) error {
	if payment == nil {
		return fmt.Errorf("unmatched payment is required")
	}

	if payment.ID == "" {
		payment.ID = uuid.New().String()
	}
	if payment.Status == "" {
		payment.Status = core.UnmatchedPaymentStatusPending
	}
	if payment.CreatedAt.IsZero() {
		payment.CreatedAt = time.Now()
	}
	if payment.UpdatedAt.IsZero() {
		payment.UpdatedAt = payment.CreatedAt
	}

	model := unmatchedPaymentModelFromDomain(payment)
	if err := r.db.WithContext(ctx).Table("unmatched_payments").Create(model).Error; err != nil {
		if isUniqueViolation(err, "idx_unmatched_payments_provider_reference_pending") {
			return nil
		}
		return fmt.Errorf("failed to create unmatched payment: %w", err)
	}
	return nil
}

func (r *unmatchedPaymentRepository) GetByID(ctx context.Context, id string) (*core.UnmatchedPayment, error) {
	var model UnmatchedPaymentModel
	if err := r.db.WithContext(ctx).Table("unmatched_payments").Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get unmatched payment: %w", err)
	}
	return model.ToDomain(), nil
}

func (r *unmatchedPaymentRepository) ListPending(ctx context.Context, limit int) ([]*core.UnmatchedPayment, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var models []UnmatchedPaymentModel
	if err := r.db.WithContext(ctx).Table("unmatched_payments").
		Where("status = ?", string(core.UnmatchedPaymentStatusPending)).
		Order("created_at DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list unmatched payments: %w", err)
	}

	payments := make([]*core.UnmatchedPayment, 0, len(models))
	for i := range models {
		payments = append(payments, models[i].ToDomain())
	}
	return payments, nil
}

func (r *unmatchedPaymentRepository) CountPending(ctx context.Context) (int, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Table("unmatched_payments").
		Where("status = ?", string(core.UnmatchedPaymentStatusPending)).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count unmatched payments: %w", err)
	}
	return int(count), nil
}

func (r *unmatchedPaymentRepository) Resolve(ctx context.Context, id string, orderID string, note string) (*core.UnmatchedPayment, error) {
	now := time.Now()
	if err := r.db.WithContext(ctx).Table("unmatched_payments").
		Where("id = ? AND status = ?", id, string(core.UnmatchedPaymentStatusPending)).
		Updates(map[string]interface{}{
			"status":            string(core.UnmatchedPaymentStatusResolved),
			"resolved_order_id": strings.TrimSpace(orderID),
			"resolution_note":   strings.TrimSpace(note),
			"resolved_at":       now,
			"updated_at":        now,
		}).Error; err != nil {
		return nil, fmt.Errorf("failed to resolve unmatched payment: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *unmatchedPaymentRepository) Reject(ctx context.Context, id string, note string) (*core.UnmatchedPayment, error) {
	now := time.Now()
	if err := r.db.WithContext(ctx).Table("unmatched_payments").
		Where("id = ? AND status = ?", id, string(core.UnmatchedPaymentStatusPending)).
		Updates(map[string]interface{}{
			"status":          string(core.UnmatchedPaymentStatusRejected),
			"resolution_note": strings.TrimSpace(note),
			"resolved_at":     now,
			"updated_at":      now,
		}).Error; err != nil {
		return nil, fmt.Errorf("failed to reject unmatched payment: %w", err)
	}
	return r.GetByID(ctx, id)
}
