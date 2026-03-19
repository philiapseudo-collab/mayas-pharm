package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/events"
	"golang.org/x/crypto/bcrypt"
)

// DashboardService handles dashboard business logic
type DashboardService struct {
	adminUserRepo       core.AdminUserRepository
	otpRepo             core.OTPRepository
	productRepo         core.ProductRepository
	orderRepo           core.OrderRepository
	paymentService      *PaymentService
	userRepo            core.UserRepository
	analyticsRepo       core.AnalyticsRepository
	prescriptionRepo    core.PrescriptionRepository
	deliveryZoneRepo    core.DeliveryZoneRepository
	businessHoursRepo   core.BusinessHoursRepository
	outboundMessageRepo core.OutboundMessageRepository
	auditLogRepo        core.AuditLogRepository
	whatsappGateway     core.WhatsAppGateway
	eventBus            *events.EventBus
	jwtSecret           string
}

const AuthTokenVersion = 2

type BartenderAccount struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BartenderCode string `json:"bartender_code"`
}

type CreateBartenderInput struct {
	Name  string
	Phone string
	PIN   string
}

type UpdateBartenderInput struct {
	ID       string
	Name     string
	Phone    string
	IsActive bool
}

// NewDashboardService creates a new dashboard service
func NewDashboardService(
	adminUserRepo core.AdminUserRepository,
	otpRepo core.OTPRepository,
	productRepo core.ProductRepository,
	orderRepo core.OrderRepository,
	paymentService *PaymentService,
	userRepo core.UserRepository,
	analyticsRepo core.AnalyticsRepository,
	prescriptionRepo core.PrescriptionRepository,
	deliveryZoneRepo core.DeliveryZoneRepository,
	businessHoursRepo core.BusinessHoursRepository,
	outboundMessageRepo core.OutboundMessageRepository,
	auditLogRepo core.AuditLogRepository,
	whatsappGateway core.WhatsAppGateway,
	eventBus *events.EventBus,
	jwtSecret string,
) *DashboardService {
	return &DashboardService{
		adminUserRepo:       adminUserRepo,
		otpRepo:             otpRepo,
		productRepo:         productRepo,
		orderRepo:           orderRepo,
		paymentService:      paymentService,
		userRepo:            userRepo,
		analyticsRepo:       analyticsRepo,
		prescriptionRepo:    prescriptionRepo,
		deliveryZoneRepo:    deliveryZoneRepo,
		businessHoursRepo:   businessHoursRepo,
		outboundMessageRepo: outboundMessageRepo,
		auditLogRepo:        auditLogRepo,
		whatsappGateway:     whatsappGateway,
		eventBus:            eventBus,
		jwtSecret:           jwtSecret,
	}
}

// RequestOTP generates and sends an OTP code via WhatsApp
func (s *DashboardService) RequestOTP(ctx context.Context, phone string) error {
	// OTP flow is manager-only.
	adminUser, err := s.adminUserRepo.GetByPhone(ctx, phone)
	if err != nil || !adminUser.IsActive {
		return fmt.Errorf("unauthorized: admin user not found or inactive")
	}

	if adminUser.Role != core.AdminRoleManager {
		return fmt.Errorf("unauthorized: OTP login is manager-only")
	}

	// Generate OTP code (hardcoded for test admin, random for others)
	var code string
	if phone == "254700000000" {
		code = "123456" // Hardcoded for test admin
	} else {
		code, err = generateOTP()
		if err != nil {
			return fmt.Errorf("failed to generate OTP: %w", err)
		}
	}

	// Create OTP record
	otp := &core.OTPCode{
		ID:          uuid.New().String(),
		PhoneNumber: phone,
		Code:        code,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		Verified:    false,
		CreatedAt:   time.Now(),
	}

	if err := s.otpRepo.Create(ctx, otp); err != nil {
		return fmt.Errorf("failed to save OTP: %w", err)
	}

	// Send OTP via WhatsApp
	message := fmt.Sprintf("Your Maya's Pharm Dashboard login code is: *%s*\n\nThis code expires in 5 minutes.", code)
	if err := s.whatsappGateway.SendText(ctx, phone, message); err != nil {
		return fmt.Errorf("failed to send OTP via WhatsApp: %w", err)
	}

	return nil
}

// VerifyOTP verifies an OTP code and returns a JWT token
func (s *DashboardService) VerifyOTP(ctx context.Context, phone string, code string) (string, error) {
	// Get latest OTP for phone
	otp, err := s.otpRepo.GetLatestByPhone(ctx, phone)
	if err != nil {
		return "", fmt.Errorf("invalid or expired OTP")
	}

	// Check if OTP is expired
	if time.Now().After(otp.ExpiresAt) {
		return "", fmt.Errorf("OTP has expired")
	}

	// Check if OTP code matches
	if otp.Code != code {
		return "", fmt.Errorf("invalid OTP code")
	}

	// Mark OTP as verified
	if err := s.otpRepo.MarkAsVerified(ctx, otp.ID); err != nil {
		return "", fmt.Errorf("failed to verify OTP: %w", err)
	}

	// Get admin user details
	adminUser, err := s.adminUserRepo.GetByPhone(ctx, phone)
	if err != nil {
		return "", fmt.Errorf("admin user not found: %w", err)
	}

	if !adminUser.IsActive {
		return "", fmt.Errorf("unauthorized: admin user inactive")
	}

	if adminUser.Role != core.AdminRoleManager {
		return "", fmt.Errorf("unauthorized: OTP login is manager-only")
	}

	// OTP login always issues MANAGER role per RBAC contract.
	adminUser.Role = core.AdminRoleManager

	// Generate JWT token
	token, err := s.generateJWT(adminUser)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	return token, nil
}

// VerifyBartenderPIN verifies a bartender PIN and returns a JWT token.
func (s *DashboardService) VerifyBartenderPIN(ctx context.Context, pin string) (string, error) {
	if !isValidFourDigitPIN(pin) {
		return "", fmt.Errorf("PIN must be exactly 4 digits")
	}

	users, err := s.adminUserRepo.GetActiveByRole(ctx, core.AdminRoleBartender)
	if err != nil {
		return "", fmt.Errorf("failed to fetch bartender accounts: %w", err)
	}

	for _, user := range users {
		if user.PinHash == "" {
			continue
		}
		if !isValidBartenderCode(user.BartenderCode) {
			continue
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PinHash), []byte(pin)); err == nil {
			// PIN login always issues BARTENDER role.
			user.Role = core.AdminRoleBartender
			token, tokenErr := s.generateJWT(user)
			if tokenErr != nil {
				return "", fmt.Errorf("failed to generate token: %w", tokenErr)
			}
			return token, nil
		}
	}

	return "", fmt.Errorf("invalid PIN")
}

// VerifyBartenderPINForUser verifies a bartender PIN against a specific account.
func (s *DashboardService) VerifyBartenderPINForUser(ctx context.Context, userID string, pin string) (string, error) {
	if !isValidFourDigitPIN(pin) {
		return "", fmt.Errorf("PIN must be exactly 4 digits")
	}

	user, err := s.adminUserRepo.GetByID(ctx, strings.TrimSpace(userID))
	if err != nil {
		return "", fmt.Errorf("invalid bartender account")
	}

	if !user.IsActive {
		return "", fmt.Errorf("invalid bartender account")
	}

	if user.Role != core.AdminRoleBartender {
		return "", fmt.Errorf("invalid bartender account")
	}
	if !isValidBartenderCode(user.BartenderCode) {
		return "", fmt.Errorf("invalid bartender account")
	}

	if strings.TrimSpace(user.PinHash) == "" {
		return "", fmt.Errorf("invalid bartender account")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PinHash), []byte(pin)); err != nil {
		return "", fmt.Errorf("invalid PIN")
	}

	// PIN login always issues BARTENDER role.
	user.Role = core.AdminRoleBartender
	token, tokenErr := s.generateJWT(user)
	if tokenErr != nil {
		return "", fmt.Errorf("failed to generate token: %w", tokenErr)
	}

	return token, nil
}

// GetBartenderAccounts retrieves active bartender identities for account selection on login.
func (s *DashboardService) GetBartenderAccounts(ctx context.Context) ([]BartenderAccount, error) {
	users, err := s.adminUserRepo.GetActiveBartenders(ctx)
	if err != nil {
		return nil, err
	}

	accounts := make([]BartenderAccount, 0, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.ID) == "" || strings.TrimSpace(user.Name) == "" || !isValidBartenderCode(user.BartenderCode) {
			continue
		}
		accounts = append(accounts, BartenderAccount{
			ID:            user.ID,
			Name:          user.Name,
			BartenderCode: user.BartenderCode,
		})
	}

	return accounts, nil
}

// ListBartenders returns all bartender users (active and inactive) for manager administration.
func (s *DashboardService) ListBartenders(ctx context.Context) ([]*core.AdminUser, error) {
	return s.adminUserRepo.GetByRole(ctx, core.AdminRoleBartender)
}

// CreateBartender creates a new bartender account with an initial PIN.
func (s *DashboardService) CreateBartender(ctx context.Context, input CreateBartenderInput) (*core.AdminUser, error) {
	name := strings.TrimSpace(input.Name)
	phone := normalizePhoneInput(input.Phone)
	pin := strings.TrimSpace(input.PIN)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if phone == "" {
		return nil, fmt.Errorf("phone number is required")
	}
	if !isValidFourDigitPIN(pin) {
		return nil, fmt.Errorf("PIN must be exactly 4 digits")
	}

	if existing, err := s.adminUserRepo.GetByPhone(ctx, phone); err == nil && existing != nil {
		return nil, fmt.Errorf("phone number is already used by another account")
	}

	pinHashBytes, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash PIN: %w", err)
	}

	bartenderCode, err := s.generateNextBartenderCode(ctx)
	if err != nil {
		return nil, err
	}

	user := &core.AdminUser{
		ID:            uuid.New().String(),
		PhoneNumber:   phone,
		Name:          name,
		Role:          core.AdminRoleBartender,
		BartenderCode: bartenderCode,
		PinHash:       string(pinHashBytes),
		IsActive:      true,
		CreatedAt:     time.Now(),
	}

	if err := s.adminUserRepo.Create(ctx, user); err != nil {
		lowerErr := strings.ToLower(err.Error())
		if strings.Contains(lowerErr, "phone_number") || strings.Contains(lowerErr, "phone number") {
			return nil, fmt.Errorf("phone number is already used by another account")
		}
		if strings.Contains(lowerErr, "bartender_code") {
			return nil, fmt.Errorf("failed to allocate unique bartender code, please retry")
		}
		if strings.Contains(lowerErr, "duplicate") {
			return nil, fmt.Errorf("phone number is already used by another account")
		}
		return nil, err
	}

	return s.adminUserRepo.GetByID(ctx, user.ID)
}

// UpdateBartender updates bartender identity and active status.
func (s *DashboardService) UpdateBartender(ctx context.Context, input UpdateBartenderInput) (*core.AdminUser, error) {
	userID := strings.TrimSpace(input.ID)
	name := strings.TrimSpace(input.Name)
	phone := normalizePhoneInput(input.Phone)

	if userID == "" {
		return nil, fmt.Errorf("bartender ID is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if phone == "" {
		return nil, fmt.Errorf("phone number is required")
	}

	user, err := s.adminUserRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("bartender not found")
	}
	if user.Role != core.AdminRoleBartender {
		return nil, fmt.Errorf("only bartender accounts can be updated")
	}

	if existing, err := s.adminUserRepo.GetByPhone(ctx, phone); err == nil && existing != nil && existing.ID != userID {
		return nil, fmt.Errorf("phone number is already used by another account")
	}

	user.Name = name
	user.PhoneNumber = phone
	user.IsActive = input.IsActive

	if err := s.adminUserRepo.Update(ctx, user); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return nil, fmt.Errorf("phone number is already used by another account")
		}
		return nil, err
	}

	return s.adminUserRepo.GetByID(ctx, userID)
}

// ResetBartenderPIN updates PIN for a bartender account.
func (s *DashboardService) ResetBartenderPIN(ctx context.Context, bartenderID string, pin string) error {
	userID := strings.TrimSpace(bartenderID)
	if userID == "" {
		return fmt.Errorf("bartender ID is required")
	}

	if !isValidFourDigitPIN(pin) {
		return fmt.Errorf("PIN must be exactly 4 digits")
	}

	user, err := s.adminUserRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("bartender not found")
	}
	if user.Role != core.AdminRoleBartender {
		return fmt.Errorf("only bartender accounts can have PIN reset")
	}

	pinHashBytes, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash PIN: %w", err)
	}

	return s.adminUserRepo.UpdatePinHash(ctx, userID, string(pinHashBytes))
}

// MarkOrderPreparing claims an order for preparation and emits a sync event.
func (s *DashboardService) MarkOrderPreparing(ctx context.Context, orderID string, actorUserID string) error {
	if strings.TrimSpace(actorUserID) == "" {
		return fmt.Errorf("actor user ID is required")
	}

	if err := s.orderRepo.ClaimPreparing(ctx, orderID, actorUserID); err != nil {
		return err
	}

	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get updated order: %w", err)
	}

	s.eventBus.PublishOrderUpdated(order)

	return nil
}

// ForceTakeoverPreparing reassigns a PREPARING order to the acting manager.
func (s *DashboardService) ForceTakeoverPreparing(ctx context.Context, orderID string, actorUserID string) error {
	if strings.TrimSpace(actorUserID) == "" {
		return fmt.Errorf("actor user ID is required")
	}

	if err := s.orderRepo.ForceTakeoverPreparing(ctx, orderID, actorUserID); err != nil {
		return err
	}

	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get updated order: %w", err)
	}

	s.eventBus.PublishOrderUpdated(order)

	return nil
}

// UnlockPreparing releases a PREPARING order back to PAID so another bartender can claim it.
func (s *DashboardService) UnlockPreparing(ctx context.Context, orderID string) error {
	if err := s.orderRepo.UnlockPreparing(ctx, orderID); err != nil {
		return err
	}

	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get updated order: %w", err)
	}

	s.eventBus.PublishOrderUpdated(order)

	return nil
}

// MarkOrderReady transitions an order from PREPARING to READY and notifies the customer.
func (s *DashboardService) MarkOrderReady(ctx context.Context, orderID string, actorUserID string) error {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get order: %w", err)
	}

	if order.Status == core.OrderStatusReady {
		return nil
	}

	if order.Status != core.OrderStatusPreparing {
		return fmt.Errorf("only PREPARING orders can be marked READY")
	}

	if order.PreparingByUserID != actorUserID {
		return fmt.Errorf("only the assigned bartender can notify customer")
	}

	if err := s.orderRepo.MarkReadyFromPreparing(ctx, orderID, actorUserID); err != nil {
		return fmt.Errorf("failed to mark order ready: %w", err)
	}

	order, err = s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("order marked ready but failed to fetch updated order: %w", err)
	}

	recipientPhone := s.resolveOrderNotificationPhone(ctx, order)
	if recipientPhone == "" {
		return fmt.Errorf("order marked ready but failed to resolve customer phone")
	}

	if err := s.whatsappGateway.SendText(ctx, recipientPhone, "Your order is ready for pickup. Please show this message when collecting at Maya's Pharm."); err != nil {
		return fmt.Errorf("order marked ready but failed to notify customer: %w", err)
	}

	s.eventBus.PublishOrderReady(order)

	return nil
}

// MarkOrderCompleted transitions an order from READY to COMPLETED and emits SSE.
func (s *DashboardService) MarkOrderCompleted(ctx context.Context, orderID string, actorUserID string) error {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get order: %w", err)
	}

	if order.Status == core.OrderStatusCompleted {
		return nil
	}

	if order.Status != core.OrderStatusReady {
		return fmt.Errorf("only READY orders can be marked COMPLETED")
	}

	if err := s.orderRepo.UpdateStatusWithActor(ctx, orderID, core.OrderStatusCompleted, actorUserID); err != nil {
		return fmt.Errorf("failed to mark order completed: %w", err)
	}

	s.eventBus.PublishOrderCompleted(orderID)

	return nil
}

// GetProducts retrieves all products
func (s *DashboardService) GetProducts(ctx context.Context) ([]*core.Product, error) {
	return s.productRepo.GetAll(ctx)
}

// UpdateStock updates product stock and emits event
func (s *DashboardService) UpdateStock(ctx context.Context, productID string, stock int) error {
	if err := s.productRepo.UpdateStock(ctx, productID, stock); err != nil {
		return err
	}

	// Emit stock updated event
	s.eventBus.PublishStockUpdated(productID, stock)

	return nil
}

// UpdatePrice updates product price and emits event
func (s *DashboardService) UpdatePrice(ctx context.Context, productID string, price float64) error {
	if err := s.productRepo.UpdatePrice(ctx, productID, price); err != nil {
		return err
	}

	// Emit price updated event
	s.eventBus.PublishPriceUpdated(productID, price)

	return nil
}

// GetOrders retrieves orders with optional filters
func (s *DashboardService) GetOrders(ctx context.Context, status string, limit int, updatedAfter *time.Time) ([]*core.Order, error) {
	return s.orderRepo.GetAllWithFilters(ctx, status, limit, updatedAfter)
}

// GetOrderHistory retrieves completed orders for dispute lookup.
func (s *DashboardService) GetOrderHistory(ctx context.Context, pickupCode string, phone string, limit int) ([]*core.Order, error) {
	return s.orderRepo.GetCompletedHistory(ctx, pickupCode, phone, limit)
}

// ListPendingUnmatchedPayments retrieves Kopo Kopo payments that could not be linked safely.
func (s *DashboardService) ListPendingUnmatchedPayments(ctx context.Context, limit int) ([]*core.UnmatchedPayment, error) {
	if s.paymentService == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	return s.paymentService.ListPendingUnmatchedPayments(ctx, limit)
}

// ResolveUnmatchedPayment manually applies an unmatched payment to an order.
func (s *DashboardService) ResolveUnmatchedPayment(ctx context.Context, unmatchedPaymentID string, orderID string, note string) (*core.UnmatchedPayment, error) {
	if s.paymentService == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	return s.paymentService.ResolveUnmatchedPayment(ctx, unmatchedPaymentID, orderID, note)
}

// RejectUnmatchedPayment dismisses an unmatched payment without applying it to an order.
func (s *DashboardService) RejectUnmatchedPayment(ctx context.Context, unmatchedPaymentID string, note string) (*core.UnmatchedPayment, error) {
	if s.paymentService == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	return s.paymentService.RejectUnmatchedPayment(ctx, unmatchedPaymentID, note)
}

// GetAnalyticsOverview retrieves dashboard overview metrics
func (s *DashboardService) GetAnalyticsOverview(ctx context.Context) (*core.Analytics, error) {
	return s.analyticsRepo.GetOverview(ctx)
}

// GetRevenueTrend retrieves revenue trend data
func (s *DashboardService) GetRevenueTrend(ctx context.Context, days int) ([]*core.RevenueTrend, error) {
	return s.analyticsRepo.GetRevenueTrend(ctx, days)
}

// GetTopProducts retrieves top-selling products
func (s *DashboardService) GetTopProducts(ctx context.Context, limit int) ([]*core.TopProduct, error) {
	return s.analyticsRepo.GetTopProducts(ctx, limit)
}

// GetEventBus returns the event bus for SSE subscriptions
func (s *DashboardService) GetEventBus() *events.EventBus {
	return s.eventBus
}

// GetAdminUserByPhone retrieves an admin user by phone number
func (s *DashboardService) GetAdminUserByPhone(ctx context.Context, phone string) (*core.AdminUser, error) {
	return s.adminUserRepo.GetByPhone(ctx, phone)
}

// generateOTP generates a random 6-digit OTP code
func generateOTP() (string, error) {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func isValidFourDigitPIN(pin string) bool {
	if len(pin) != 4 {
		return false
	}

	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func isValidBartenderCode(code string) bool {
	if len(code) != 4 {
		return false
	}

	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func (s *DashboardService) generateNextBartenderCode(ctx context.Context) (string, error) {
	var users []*core.AdminUser
	for _, role := range []string{core.AdminRolePharmacist, core.AdminRoleDispatcher} {
		group, err := s.adminUserRepo.GetByRole(ctx, role)
		if err != nil {
			return "", fmt.Errorf("failed to allocate staff code")
		}
		users = append(users, group...)
	}

	usedCodes := make(map[string]struct{}, len(users))
	for _, user := range users {
		code := strings.TrimSpace(user.BartenderCode)
		if isValidBartenderCode(code) {
			usedCodes[code] = struct{}{}
		}
	}

	for i := 1; i <= 9999; i++ {
		code := fmt.Sprintf("%04d", i)
		if _, exists := usedCodes[code]; !exists {
			return code, nil
		}
	}

	return "", fmt.Errorf("all bartender codes are in use")
}

func normalizePhoneInput(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if trimmed == "" {
		return ""
	}

	var digits strings.Builder
	digits.Grow(len(trimmed))
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}

	normalized := digits.String()
	if len(normalized) < 10 {
		return ""
	}

	return normalized
}

func (s *DashboardService) resolveOrderNotificationPhone(ctx context.Context, order *core.Order) string {
	if order == nil {
		return ""
	}

	fallback := strings.TrimSpace(order.CustomerPhone)
	if s.userRepo == nil || strings.TrimSpace(order.UserID) == "" {
		return fallback
	}

	user, err := s.userRepo.GetByID(ctx, order.UserID)
	if err != nil || user == nil {
		return fallback
	}

	resolved := strings.TrimSpace(user.PhoneNumber)
	if resolved == "" {
		return fallback
	}

	return resolved
}

// generateJWT generates a JWT token for an admin user
func (s *DashboardService) generateJWT(user *core.AdminUser) (string, error) {
	claims := jwt.MapClaims{
		"user_id":      user.ID,
		"phone":        user.PhoneNumber,
		"name":         user.Name,
		"role":         user.Role,
		"auth_version": AuthTokenVersion,
		"exp":          time.Now().Add(7 * 24 * time.Hour).Unix(), // 7 days
		"iat":          time.Now().Unix(),
	}
	if isValidBartenderCode(user.BartenderCode) {
		claims["bartender_code"] = user.BartenderCode
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}

// ValidateJWT validates a JWT token and returns the claims
func (s *DashboardService) ValidateJWT(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.jwtSecret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}
