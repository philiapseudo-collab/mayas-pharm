package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"golang.org/x/crypto/bcrypt"
)

type StaffAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	StaffCode string `json:"staff_code"`
}

type CreateStaffInput struct {
	Name  string
	Phone string
	PIN   string
	Role  string
}

type UpdateStaffInput struct {
	ID       string
	Name     string
	Phone    string
	Role     string
	IsActive bool
}

func allowedStaffRoles() map[string]struct{} {
	return map[string]struct{}{
		core.AdminRoleOwner:      {},
		core.AdminRolePharmacist: {},
		core.AdminRoleDispatcher: {},
	}
}

func normalizeStaffRole(role string) string {
	role = strings.ToUpper(strings.TrimSpace(role))
	switch role {
	case "", "MANAGER":
		return core.AdminRoleOwner
	case "BARTENDER":
		return core.AdminRoleDispatcher
	default:
		return role
	}
}

func isPINRole(role string) bool {
	role = normalizeStaffRole(role)
	return role == core.AdminRolePharmacist || role == core.AdminRoleDispatcher
}

func (s *DashboardService) GetStaffAccounts(ctx context.Context) ([]StaffAccount, error) {
	var users []*core.AdminUser
	for _, role := range []string{core.AdminRolePharmacist, core.AdminRoleDispatcher} {
		group, err := s.adminUserRepo.GetActiveByRole(ctx, role)
		if err != nil {
			return nil, err
		}
		users = append(users, group...)
	}

	accounts := make([]StaffAccount, 0, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.PinHash) == "" {
			continue
		}
		accounts = append(accounts, StaffAccount{
			ID:        user.ID,
			Name:      user.Name,
			Role:      normalizeStaffRole(user.Role),
			StaffCode: user.BartenderCode,
		})
	}
	return accounts, nil
}

func (s *DashboardService) VerifyStaffPIN(ctx context.Context, pin string) (string, error) {
	if !isValidFourDigitPIN(pin) {
		return "", fmt.Errorf("PIN must be exactly 4 digits")
	}

	accounts, err := s.GetStaffAccounts(ctx)
	if err != nil {
		return "", err
	}

	for _, account := range accounts {
		user, getErr := s.adminUserRepo.GetByID(ctx, account.ID)
		if getErr != nil || user == nil {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PinHash), []byte(pin)); err == nil {
			user.Role = normalizeStaffRole(user.Role)
			return s.generateJWT(user)
		}
	}

	return "", fmt.Errorf("invalid PIN")
}

func (s *DashboardService) VerifyStaffPINForUser(ctx context.Context, userID string, pin string) (string, error) {
	if !isValidFourDigitPIN(pin) {
		return "", fmt.Errorf("PIN must be exactly 4 digits")
	}

	user, err := s.adminUserRepo.GetByID(ctx, strings.TrimSpace(userID))
	if err != nil || user == nil || !user.IsActive {
		return "", fmt.Errorf("invalid staff account")
	}
	role := normalizeStaffRole(user.Role)
	if !isPINRole(role) {
		return "", fmt.Errorf("invalid staff account")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PinHash), []byte(pin)); err != nil {
		return "", fmt.Errorf("invalid PIN")
	}
	user.Role = role
	return s.generateJWT(user)
}

func (s *DashboardService) ListStaff(ctx context.Context) ([]*core.AdminUser, error) {
	var users []*core.AdminUser
	for _, role := range []string{core.AdminRoleOwner, core.AdminRolePharmacist, core.AdminRoleDispatcher} {
		group, err := s.adminUserRepo.GetByRole(ctx, role)
		if err != nil {
			return nil, err
		}
		users = append(users, group...)
	}
	return users, nil
}

func (s *DashboardService) CreateStaff(ctx context.Context, input CreateStaffInput) (*core.AdminUser, error) {
	name := strings.TrimSpace(input.Name)
	phone := normalizePhoneInput(input.Phone)
	role := normalizeStaffRole(input.Role)
	pin := strings.TrimSpace(input.PIN)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if phone == "" {
		return nil, fmt.Errorf("phone number is required")
	}
	if _, ok := allowedStaffRoles()[role]; !ok {
		return nil, fmt.Errorf("invalid staff role")
	}
	if isPINRole(role) && !isValidFourDigitPIN(pin) {
		return nil, fmt.Errorf("PIN must be exactly 4 digits")
	}
	if existing, err := s.adminUserRepo.GetByPhone(ctx, phone); err == nil && existing != nil {
		return nil, fmt.Errorf("phone number is already used by another account")
	}

	pinHash := ""
	if pin != "" {
		pinHashBytes, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash PIN: %w", err)
		}
		pinHash = string(pinHashBytes)
	}

	staffCode := ""
	if isPINRole(role) {
		nextCode, err := s.generateNextBartenderCode(ctx)
		if err != nil {
			return nil, err
		}
		staffCode = nextCode
	}

	user := &core.AdminUser{
		ID:            uuid.New().String(),
		PhoneNumber:   phone,
		Name:          name,
		Role:          role,
		BartenderCode: staffCode,
		PinHash:       pinHash,
		IsActive:      true,
		CreatedAt:     time.Now(),
	}
	if err := s.adminUserRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	s.writeAuditLog(ctx, "staff_user", user.ID, "created", "", fmt.Sprintf("{\"role\":\"%s\"}", role))
	return s.adminUserRepo.GetByID(ctx, user.ID)
}

func (s *DashboardService) UpdateStaff(ctx context.Context, input UpdateStaffInput) (*core.AdminUser, error) {
	userID := strings.TrimSpace(input.ID)
	name := strings.TrimSpace(input.Name)
	phone := normalizePhoneInput(input.Phone)
	role := normalizeStaffRole(input.Role)
	if userID == "" {
		return nil, fmt.Errorf("staff ID is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if phone == "" {
		return nil, fmt.Errorf("phone number is required")
	}
	if _, ok := allowedStaffRoles()[role]; !ok {
		return nil, fmt.Errorf("invalid staff role")
	}

	user, err := s.adminUserRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("staff account not found")
	}
	if existing, err := s.adminUserRepo.GetByPhone(ctx, phone); err == nil && existing != nil && existing.ID != userID {
		return nil, fmt.Errorf("phone number is already used by another account")
	}

	user.Name = name
	user.PhoneNumber = phone
	user.Role = role
	user.IsActive = input.IsActive
	if err := s.adminUserRepo.Update(ctx, user); err != nil {
		return nil, err
	}
	s.writeAuditLog(ctx, "staff_user", userID, "updated", "", fmt.Sprintf("{\"role\":\"%s\"}", role))
	return s.adminUserRepo.GetByID(ctx, userID)
}

func (s *DashboardService) ResetStaffPIN(ctx context.Context, staffID string, pin string) error {
	if !isValidFourDigitPIN(pin) {
		return fmt.Errorf("PIN must be exactly 4 digits")
	}
	user, err := s.adminUserRepo.GetByID(ctx, strings.TrimSpace(staffID))
	if err != nil {
		return fmt.Errorf("staff account not found")
	}
	if !isPINRole(user.Role) {
		return fmt.Errorf("only pharmacist or dispatcher accounts can have PIN reset")
	}
	pinHashBytes, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash PIN: %w", err)
	}
	if err := s.adminUserRepo.UpdatePinHash(ctx, user.ID, string(pinHashBytes)); err != nil {
		return err
	}
	s.writeAuditLog(ctx, "staff_user", user.ID, "pin_reset", "", "")
	return nil
}

func (s *DashboardService) ListPendingPrescriptions(ctx context.Context, limit int) ([]*core.Prescription, error) {
	if s.prescriptionRepo == nil {
		return nil, fmt.Errorf("prescription repository unavailable")
	}
	return s.prescriptionRepo.ListPending(ctx, limit)
}

func (s *DashboardService) ReviewPrescription(ctx context.Context, prescriptionID string, actorUserID string, decision string, notes string) (*core.Order, error) {
	if s.prescriptionRepo == nil {
		return nil, fmt.Errorf("prescription repository unavailable")
	}
	prescription, err := s.prescriptionRepo.GetByID(ctx, strings.TrimSpace(prescriptionID))
	if err != nil {
		return nil, err
	}
	order, err := s.orderRepo.GetByID(ctx, prescription.OrderID)
	if err != nil {
		return nil, err
	}

	decision = strings.ToUpper(strings.TrimSpace(decision))
	nextOrderStatus := core.OrderStatusApprovedAwaitingPayment
	nextPrescriptionStatus := core.PrescriptionStatusApproved
	customerMessage := "Your prescription has been approved. You can now complete payment on WhatsApp."
	if decision == core.PrescriptionStatusRejected {
		nextOrderStatus = core.OrderStatusRejected
		nextPrescriptionStatus = core.PrescriptionStatusRejected
		customerMessage = "Your prescription could not be approved. A pharmacist will contact you if more detail is needed."
	}

	if err := s.prescriptionRepo.UpdateStatus(ctx, prescription.ID, nextPrescriptionStatus, actorUserID, notes); err != nil {
		return nil, err
	}
	_ = s.prescriptionRepo.CreateReview(ctx, &core.PrescriptionReview{
		ID:             uuid.New().String(),
		PrescriptionID: prescription.ID,
		OrderID:        prescription.OrderID,
		ReviewerUserID: actorUserID,
		Decision:       nextPrescriptionStatus,
		Notes:          notes,
		CreatedAt:      time.Now(),
	})
	if err := s.orderRepo.UpdateStatusWithActor(ctx, order.ID, nextOrderStatus, actorUserID); err != nil {
		return nil, err
	}

	updatedOrder, err := s.orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	s.writeAuditLog(ctx, "order", order.ID, "prescription_reviewed", actorUserID, fmt.Sprintf("{\"decision\":\"%s\"}", nextPrescriptionStatus))
	if s.whatsappGateway != nil {
		_ = s.whatsappGateway.SendText(ctx, updatedOrder.CustomerPhone, customerMessage)
	}
	if s.eventBus != nil {
		s.eventBus.PublishOrderUpdated(updatedOrder)
	}
	return updatedOrder, nil
}

func (s *DashboardService) ListDeliveryZones(ctx context.Context) ([]*core.DeliveryZone, error) {
	if s.deliveryZoneRepo == nil {
		return nil, fmt.Errorf("delivery zone repository unavailable")
	}
	return s.deliveryZoneRepo.ListActive(ctx)
}

func (s *DashboardService) SaveDeliveryZone(ctx context.Context, zone *core.DeliveryZone) error {
	if s.deliveryZoneRepo == nil {
		return fmt.Errorf("delivery zone repository unavailable")
	}
	if err := s.deliveryZoneRepo.Upsert(ctx, zone); err != nil {
		return err
	}
	s.writeAuditLog(ctx, "delivery_zone", zone.ID, "upserted", "", "")
	return nil
}

func (s *DashboardService) ListBusinessHours(ctx context.Context) ([]*core.BusinessHours, error) {
	if s.businessHoursRepo == nil {
		return nil, fmt.Errorf("business hours repository unavailable")
	}
	return s.businessHoursRepo.List(ctx)
}

func (s *DashboardService) SaveBusinessHours(ctx context.Context, hours *core.BusinessHours) error {
	if s.businessHoursRepo == nil {
		return fmt.Errorf("business hours repository unavailable")
	}
	if err := s.businessHoursRepo.Upsert(ctx, hours); err != nil {
		return err
	}
	s.writeAuditLog(ctx, "business_hours", hours.ID, "upserted", "", "")
	return nil
}

func (s *DashboardService) MarkOrderOutForDelivery(ctx context.Context, orderID string, actorUserID string) error {
	if err := s.orderRepo.UpdateStatusWithActor(ctx, orderID, core.OrderStatusOutForDelivery, actorUserID); err != nil {
		return err
	}
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return err
	}
	if s.whatsappGateway != nil {
		_ = s.whatsappGateway.SendText(ctx, order.CustomerPhone, "Your order is now out for delivery.")
	}
	if s.eventBus != nil {
		s.eventBus.PublishOrderUpdated(order)
	}
	return nil
}

func (s *DashboardService) writeAuditLog(ctx context.Context, entityType string, entityID string, action string, actorID string, metadata string) {
	if s.auditLogRepo == nil {
		return
	}
	_ = s.auditLogRepo.Create(ctx, &core.AuditLog{
		ID:         uuid.New().String(),
		EntityType: entityType,
		EntityID:   entityID,
		Action:     action,
		ActorID:    actorID,
		Metadata:   metadata,
		CreatedAt:  time.Now(),
	})
}
