package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"gorm.io/gorm"
)

type categoryRepository struct {
	*Repository
}

type prescriptionRepository struct {
	*Repository
}

type deliveryZoneRepository struct {
	*Repository
}

type businessHoursRepository struct {
	*Repository
}

type outboundMessageRepository struct {
	*Repository
}

type auditLogRepository struct {
	*Repository
}

func (r *Repository) CategoryRepository() core.CategoryRepository {
	return &categoryRepository{Repository: r}
}

func (r *Repository) PrescriptionRepository() core.PrescriptionRepository {
	return &prescriptionRepository{Repository: r}
}

func (r *Repository) DeliveryZoneRepository() core.DeliveryZoneRepository {
	return &deliveryZoneRepository{Repository: r}
}

func (r *Repository) BusinessHoursRepository() core.BusinessHoursRepository {
	return &businessHoursRepository{Repository: r}
}

func (r *Repository) OutboundMessageRepository() core.OutboundMessageRepository {
	return &outboundMessageRepository{Repository: r}
}

func (r *Repository) AuditLogRepository() core.AuditLogRepository {
	return &auditLogRepository{Repository: r}
}

type CategoryModel struct {
	ID          string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	Name        string         `gorm:"column:name;type:varchar(100);not null"`
	Slug        string         `gorm:"column:slug;type:varchar(100);not null"`
	Description sql.NullString `gorm:"column:description;type:text"`
	SortOrder   int            `gorm:"column:sort_order;type:integer;not null;default:0"`
	IsActive    bool           `gorm:"column:is_active;type:boolean;not null;default:true"`
	CreatedAt   time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (CategoryModel) TableName() string { return "categories" }

func (m *CategoryModel) ToDomain() *core.Category {
	category := &core.Category{
		ID:        m.ID,
		Name:      m.Name,
		Slug:      m.Slug,
		SortOrder: m.SortOrder,
		IsActive:  m.IsActive,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
	if m.Description.Valid {
		category.Description = m.Description.String
	}
	return category
}

func (r *categoryRepository) ListActive(ctx context.Context) ([]*core.Category, error) {
	var rows []CategoryModel
	if err := r.db.WithContext(ctx).Table("categories").Where("is_active = ?", true).Order("sort_order ASC, name ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list categories: %w", err)
	}
	items := make([]*core.Category, len(rows))
	for i := range rows {
		items[i] = rows[i].ToDomain()
	}
	return items, nil
}

type DeliveryZoneModel struct {
	ID            string    `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	Name          string    `gorm:"column:name;type:varchar(100);not null"`
	Slug          string    `gorm:"column:slug;type:varchar(100);not null"`
	Fee           float64   `gorm:"column:fee;type:decimal(10,2);not null;default:0"`
	EstimatedMins int       `gorm:"column:estimated_mins;type:integer;not null;default:60"`
	SortOrder     int       `gorm:"column:sort_order;type:integer;not null;default:0"`
	IsActive      bool      `gorm:"column:is_active;type:boolean;not null;default:true"`
	CreatedAt     time.Time `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt     time.Time `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (DeliveryZoneModel) TableName() string { return "delivery_zones" }

func (m *DeliveryZoneModel) ToDomain() *core.DeliveryZone {
	return &core.DeliveryZone{
		ID:            m.ID,
		Name:          m.Name,
		Slug:          m.Slug,
		Fee:           m.Fee,
		EstimatedMins: m.EstimatedMins,
		SortOrder:     m.SortOrder,
		IsActive:      m.IsActive,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func (r *deliveryZoneRepository) ListActive(ctx context.Context) ([]*core.DeliveryZone, error) {
	var rows []DeliveryZoneModel
	if err := r.db.WithContext(ctx).Table("delivery_zones").Where("is_active = ?", true).Order("sort_order ASC, name ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list delivery zones: %w", err)
	}
	items := make([]*core.DeliveryZone, len(rows))
	for i := range rows {
		items[i] = rows[i].ToDomain()
	}
	return items, nil
}

func (r *deliveryZoneRepository) GetByID(ctx context.Context, id string) (*core.DeliveryZone, error) {
	var row DeliveryZoneModel
	if err := r.db.WithContext(ctx).Table("delivery_zones").Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("delivery zone not found")
		}
		return nil, fmt.Errorf("failed to get delivery zone: %w", err)
	}
	return row.ToDomain(), nil
}

func (r *deliveryZoneRepository) Upsert(ctx context.Context, zone *core.DeliveryZone) error {
	if zone == nil {
		return fmt.Errorf("delivery zone is required")
	}
	if strings.TrimSpace(zone.ID) == "" {
		zone.ID = uuid.New().String()
	}
	model := &DeliveryZoneModel{
		ID:            zone.ID,
		Name:          strings.TrimSpace(zone.Name),
		Slug:          strings.TrimSpace(zone.Slug),
		Fee:           zone.Fee,
		EstimatedMins: zone.EstimatedMins,
		SortOrder:     zone.SortOrder,
		IsActive:      zone.IsActive,
	}
	return r.db.WithContext(ctx).Table("delivery_zones").Save(model).Error
}

type BusinessHoursModel struct {
	ID        string    `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	DayOfWeek int       `gorm:"column:day_of_week;type:integer;not null"`
	OpenTime  string    `gorm:"column:open_time;type:varchar(5);not null"`
	CloseTime string    `gorm:"column:close_time;type:varchar(5);not null"`
	IsOpen    bool      `gorm:"column:is_open;type:boolean;not null;default:true"`
	CreatedAt time.Time `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (BusinessHoursModel) TableName() string { return "business_hours" }

func (m *BusinessHoursModel) ToDomain() *core.BusinessHours {
	return &core.BusinessHours{
		ID:        m.ID,
		DayOfWeek: m.DayOfWeek,
		OpenTime:  m.OpenTime,
		CloseTime: m.CloseTime,
		IsOpen:    m.IsOpen,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func (r *businessHoursRepository) List(ctx context.Context) ([]*core.BusinessHours, error) {
	var rows []BusinessHoursModel
	if err := r.db.WithContext(ctx).Table("business_hours").Order("day_of_week ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list business hours: %w", err)
	}
	items := make([]*core.BusinessHours, len(rows))
	for i := range rows {
		items[i] = rows[i].ToDomain()
	}
	return items, nil
}

func (r *businessHoursRepository) Upsert(ctx context.Context, hours *core.BusinessHours) error {
	if hours == nil {
		return fmt.Errorf("business hours are required")
	}
	if strings.TrimSpace(hours.ID) == "" {
		hours.ID = uuid.New().String()
	}
	model := &BusinessHoursModel{
		ID:        hours.ID,
		DayOfWeek: hours.DayOfWeek,
		OpenTime:  strings.TrimSpace(hours.OpenTime),
		CloseTime: strings.TrimSpace(hours.CloseTime),
		IsOpen:    hours.IsOpen,
	}
	return r.db.WithContext(ctx).Table("business_hours").Save(model).Error
}

type PrescriptionModel struct {
	ID                  string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	OrderID             string         `gorm:"column:order_id;type:uuid;not null;index"`
	CustomerPhone       string         `gorm:"column:customer_phone;type:varchar(20);not null"`
	MediaID             string         `gorm:"column:media_id;type:varchar(255);not null"`
	MediaType           string         `gorm:"column:media_type;type:varchar(50);not null"`
	FileName            sql.NullString `gorm:"column:file_name;type:varchar(255)"`
	Caption             sql.NullString `gorm:"column:caption;type:text"`
	Status              string         `gorm:"column:status;type:varchar(20);not null;default:'PENDING'"`
	ReviewNotes         sql.NullString `gorm:"column:review_notes;type:text"`
	ReviewedByAdminUserID sql.NullString `gorm:"column:reviewed_by_admin_user_id;type:uuid"`
	ReviewedAt          sql.NullTime   `gorm:"column:reviewed_at;type:timestamp"`
	CreatedAt           time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt           time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (PrescriptionModel) TableName() string { return "prescriptions" }

func (m *PrescriptionModel) ToDomain() *core.Prescription {
	item := &core.Prescription{
		ID:            m.ID,
		OrderID:       m.OrderID,
		CustomerPhone: m.CustomerPhone,
		MediaID:       m.MediaID,
		MediaType:     m.MediaType,
		Status:        m.Status,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
	if m.FileName.Valid {
		item.FileName = m.FileName.String
	}
	if m.Caption.Valid {
		item.Caption = m.Caption.String
	}
	if m.ReviewNotes.Valid {
		item.ReviewNotes = m.ReviewNotes.String
	}
	if m.ReviewedByAdminUserID.Valid {
		item.ReviewedByUserID = m.ReviewedByAdminUserID.String
	}
	if m.ReviewedAt.Valid {
		t := m.ReviewedAt.Time
		item.ReviewedAt = &t
	}
	return item
}

type PrescriptionReviewModel struct {
	ID                 string    `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	PrescriptionID     string    `gorm:"column:prescription_id;type:uuid;not null"`
	OrderID            string    `gorm:"column:order_id;type:uuid;not null"`
	ReviewerAdminUserID string   `gorm:"column:reviewer_admin_user_id;type:uuid;not null"`
	Decision           string    `gorm:"column:decision;type:varchar(20);not null"`
	Notes              string    `gorm:"column:notes;type:text"`
	CreatedAt          time.Time `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (PrescriptionReviewModel) TableName() string { return "prescription_reviews" }

func (r *prescriptionRepository) Create(ctx context.Context, prescription *core.Prescription) error {
	if prescription == nil {
		return fmt.Errorf("prescription is required")
	}
	if strings.TrimSpace(prescription.ID) == "" {
		prescription.ID = uuid.New().String()
	}
	now := time.Now()
	model := &PrescriptionModel{
		ID:            prescription.ID,
		OrderID:       prescription.OrderID,
		CustomerPhone: prescription.CustomerPhone,
		MediaID:       strings.TrimSpace(prescription.MediaID),
		MediaType:     strings.TrimSpace(prescription.MediaType),
		FileName:      nullableString(prescription.FileName),
		Caption:       nullableString(prescription.Caption),
		Status:        core.PrescriptionStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("prescriptions").Create(model).Error; err != nil {
			return fmt.Errorf("failed to create prescription: %w", err)
		}
		return tx.Table("orders").
			Where("id = ?", prescription.OrderID).
			Updates(map[string]any{
				"review_required":    true,
				"status":             string(core.OrderStatusPendingReview),
				"prescription_count": gorm.Expr("prescription_count + 1"),
				"updated_at":         gorm.Expr("CURRENT_TIMESTAMP"),
			}).Error
	})
}

func (r *prescriptionRepository) GetByID(ctx context.Context, id string) (*core.Prescription, error) {
	var row PrescriptionModel
	if err := r.db.WithContext(ctx).Table("prescriptions").Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("prescription not found")
		}
		return nil, fmt.Errorf("failed to get prescription: %w", err)
	}
	return row.ToDomain(), nil
}

func (r *prescriptionRepository) ListByOrderID(ctx context.Context, orderID string) ([]*core.Prescription, error) {
	var rows []PrescriptionModel
	if err := r.db.WithContext(ctx).Table("prescriptions").Where("order_id = ?", orderID).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list prescriptions: %w", err)
	}
	items := make([]*core.Prescription, len(rows))
	for i := range rows {
		items[i] = rows[i].ToDomain()
	}
	return items, nil
}

func (r *prescriptionRepository) ListPending(ctx context.Context, limit int) ([]*core.Prescription, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []PrescriptionModel
	if err := r.db.WithContext(ctx).Table("prescriptions").Where("status = ?", core.PrescriptionStatusPending).Order("created_at ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list pending prescriptions: %w", err)
	}
	items := make([]*core.Prescription, len(rows))
	for i := range rows {
		items[i] = rows[i].ToDomain()
	}
	return items, nil
}

func (r *prescriptionRepository) UpdateStatus(ctx context.Context, id string, status string, reviewedBy string, notes string) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
	}
	if strings.TrimSpace(reviewedBy) != "" {
		updates["reviewed_by_admin_user_id"] = reviewedBy
	}
	if strings.TrimSpace(notes) != "" {
		updates["review_notes"] = notes
	}
	if status == core.PrescriptionStatusApproved || status == core.PrescriptionStatusRejected {
		updates["reviewed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}
	result := r.db.WithContext(ctx).Table("prescriptions").Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to update prescription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("prescription not found")
	}
	return nil
}

func (r *prescriptionRepository) CreateReview(ctx context.Context, review *core.PrescriptionReview) error {
	if review == nil {
		return fmt.Errorf("prescription review is required")
	}
	if strings.TrimSpace(review.ID) == "" {
		review.ID = uuid.New().String()
	}
	model := &PrescriptionReviewModel{
		ID:                  review.ID,
		PrescriptionID:      review.PrescriptionID,
		OrderID:             review.OrderID,
		ReviewerAdminUserID: review.ReviewerUserID,
		Decision:            review.Decision,
		Notes:               review.Notes,
		CreatedAt:           time.Now(),
	}
	if err := r.db.WithContext(ctx).Table("prescription_reviews").Create(model).Error; err != nil {
		return fmt.Errorf("failed to create prescription review: %w", err)
	}
	return nil
}

type OutboundMessageModel struct {
	ID          string         `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	PhoneNumber string         `gorm:"column:phone_number;type:varchar(20);not null"`
	MessageType string         `gorm:"column:message_type;type:varchar(50);not null"`
	Payload     string         `gorm:"column:payload;type:text;not null"`
	Status      string         `gorm:"column:status;type:varchar(20);not null;default:'PENDING'"`
	LastError   sql.NullString `gorm:"column:last_error;type:text"`
	SentAt      sql.NullTime   `gorm:"column:sent_at;type:timestamp"`
	CreatedAt   time.Time      `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time      `gorm:"column:updated_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OutboundMessageModel) TableName() string { return "outbound_messages" }

func (r *outboundMessageRepository) Create(ctx context.Context, message *core.OutboundMessage) error {
	if message == nil {
		return fmt.Errorf("outbound message is required")
	}
	if strings.TrimSpace(message.ID) == "" {
		message.ID = uuid.New().String()
	}
	model := &OutboundMessageModel{
		ID:          message.ID,
		PhoneNumber: message.PhoneNumber,
		MessageType: message.MessageType,
		Payload:     message.Payload,
		Status:      message.Status,
		LastError:   nullableString(message.LastError),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	return r.db.WithContext(ctx).Table("outbound_messages").Create(model).Error
}

type AuditLogModel struct {
	ID         string    `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	EntityType string    `gorm:"column:entity_type;type:varchar(50);not null"`
	EntityID   string    `gorm:"column:entity_id;type:varchar(64);not null"`
	Action     string    `gorm:"column:action;type:varchar(100);not null"`
	ActorID    string    `gorm:"column:actor_id;type:varchar(64)"`
	Metadata   string    `gorm:"column:metadata;type:text"`
	CreatedAt  time.Time `gorm:"column:created_at;type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (AuditLogModel) TableName() string { return "audit_logs" }

func (r *auditLogRepository) Create(ctx context.Context, logEntry *core.AuditLog) error {
	if logEntry == nil {
		return fmt.Errorf("audit log entry is required")
	}
	if strings.TrimSpace(logEntry.ID) == "" {
		logEntry.ID = uuid.New().String()
	}
	model := &AuditLogModel{
		ID:         logEntry.ID,
		EntityType: logEntry.EntityType,
		EntityID:   logEntry.EntityID,
		Action:     logEntry.Action,
		ActorID:    logEntry.ActorID,
		Metadata:   logEntry.Metadata,
		CreatedAt:  time.Now(),
	}
	return r.db.WithContext(ctx).Table("audit_logs").Create(model).Error
}
