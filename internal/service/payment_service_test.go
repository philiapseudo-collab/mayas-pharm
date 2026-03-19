package service

import (
	"context"
	"testing"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/core"
)

func TestPaymentServiceProcessWebhookMarksPendingOrderPaid(t *testing.T) {
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-1": {
				ID:            "order-1",
				UserID:        "user-1",
				CustomerPhone: "254700000001",
				Status:        core.OrderStatusPending,
				TotalAmount:   1200,
				PickupCode:    "000001",
			},
		},
	}
	attemptRepo := newPaymentAttemptTestRepo(
		&core.PaymentAttempt{
			ID:             "attempt-1",
			OrderID:        "order-1",
			Provider:       string(core.PaymentMethodMpesa),
			Status:         core.PaymentAttemptStatusAwaitingCustomer,
			RequestedPhone: "254700000001",
			Amount:         1200,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	)
	service := NewPaymentService(orderRepo, attemptRepo, newUnmatchedPaymentTestRepo(), botTestUserRepo{}, botTestPaymentGateway{}, nil, nil)

	order, err := service.ProcessKopoKopoWebhook(context.Background(), &core.PaymentWebhook{
		OrderID:   "order-1",
		Reference: "ref-success",
		Status:    "Success",
		Success:   true,
	}, []byte(`{"status":"Success"}`))
	if err != nil {
		t.Fatalf("ProcessKopoKopoWebhook() error = %v", err)
	}
	if order == nil {
		t.Fatalf("ProcessKopoKopoWebhook() order = nil")
	}
	if order.Status != core.OrderStatusPaid {
		t.Fatalf("order status = %s, want %s", order.Status, core.OrderStatusPaid)
	}

	attempt, _ := attemptRepo.GetByID(context.Background(), "attempt-1")
	if attempt.Status != core.PaymentAttemptStatusSucceeded {
		t.Fatalf("attempt status = %s, want %s", attempt.Status, core.PaymentAttemptStatusSucceeded)
	}
}

func TestPaymentServiceProcessWebhookMarksPendingOrderFailed(t *testing.T) {
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-2": {
				ID:            "order-2",
				UserID:        "user-1",
				CustomerPhone: "254700000002",
				Status:        core.OrderStatusPending,
				TotalAmount:   800,
				PickupCode:    "000002",
			},
		},
	}
	attemptRepo := newPaymentAttemptTestRepo(
		&core.PaymentAttempt{
			ID:             "attempt-2",
			OrderID:        "order-2",
			Provider:       string(core.PaymentMethodMpesa),
			Status:         core.PaymentAttemptStatusAwaitingCustomer,
			RequestedPhone: "254700000002",
			Amount:         800,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	)
	service := NewPaymentService(orderRepo, attemptRepo, newUnmatchedPaymentTestRepo(), botTestUserRepo{}, botTestPaymentGateway{}, nil, nil)

	order, err := service.ProcessKopoKopoWebhook(context.Background(), &core.PaymentWebhook{
		OrderID:   "order-2",
		Reference: "ref-failed",
		Status:    "Failed",
		Success:   false,
	}, []byte(`{"status":"Failed"}`))
	if err != nil {
		t.Fatalf("ProcessKopoKopoWebhook() error = %v", err)
	}
	if order == nil {
		t.Fatalf("ProcessKopoKopoWebhook() order = nil")
	}
	if order.Status != core.OrderStatusFailed {
		t.Fatalf("order status = %s, want %s", order.Status, core.OrderStatusFailed)
	}

	attempt, _ := attemptRepo.GetByID(context.Background(), "attempt-2")
	if attempt.Status != core.PaymentAttemptStatusFailed {
		t.Fatalf("attempt status = %s, want %s", attempt.Status, core.PaymentAttemptStatusFailed)
	}
}

func TestPaymentServiceProcessWebhookStoresUnknownSuccessAsUnmatched(t *testing.T) {
	unmatchedRepo := newUnmatchedPaymentTestRepo()
	service := NewPaymentService(botTestOrderRepo{}, newPaymentAttemptTestRepo(), unmatchedRepo, botTestUserRepo{}, botTestPaymentGateway{}, nil, nil)

	order, err := service.ProcessKopoKopoWebhook(context.Background(), &core.PaymentWebhook{
		Reference: "ref-orphan",
		Status:    "Success",
		Phone:     "254700000003",
		Amount:    600,
		Success:   true,
	}, []byte(`{"status":"Success"}`))
	if err != nil {
		t.Fatalf("ProcessKopoKopoWebhook() error = %v", err)
	}
	if order != nil {
		t.Fatalf("ProcessKopoKopoWebhook() order = %#v, want nil", order)
	}

	payments, err := unmatchedRepo.ListPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPending() error = %v", err)
	}
	if len(payments) != 1 {
		t.Fatalf("pending unmatched payments = %d, want 1", len(payments))
	}
	if payments[0].ProviderReference != "ref-orphan" {
		t.Fatalf("provider reference = %q, want %q", payments[0].ProviderReference, "ref-orphan")
	}
}

func TestPaymentServiceProcessWebhookDoesNotDowngradePaidOrderOnFailure(t *testing.T) {
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-3": {
				ID:            "order-3",
				UserID:        "user-1",
				CustomerPhone: "254700000004",
				Status:        core.OrderStatusPaid,
				TotalAmount:   950,
				PickupCode:    "000003",
			},
		},
	}
	attemptRepo := newPaymentAttemptTestRepo(
		&core.PaymentAttempt{
			ID:                "attempt-3",
			OrderID:           "order-3",
			Provider:          string(core.PaymentMethodMpesa),
			Status:            core.PaymentAttemptStatusSucceeded,
			ProviderReference: "ref-paid",
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	)
	service := NewPaymentService(orderRepo, attemptRepo, newUnmatchedPaymentTestRepo(), botTestUserRepo{}, botTestPaymentGateway{}, nil, nil)

	order, err := service.ProcessKopoKopoWebhook(context.Background(), &core.PaymentWebhook{
		OrderID:   "order-3",
		Reference: "ref-paid",
		Status:    "Failed",
		Success:   false,
	}, []byte(`{"status":"Failed"}`))
	if err != nil {
		t.Fatalf("ProcessKopoKopoWebhook() error = %v", err)
	}
	if order.Status != core.OrderStatusPaid {
		t.Fatalf("order status = %s, want %s", order.Status, core.OrderStatusPaid)
	}

	attempt, _ := attemptRepo.GetByID(context.Background(), "attempt-3")
	if attempt.Status != core.PaymentAttemptStatusSucceeded {
		t.Fatalf("attempt status = %s, want %s", attempt.Status, core.PaymentAttemptStatusSucceeded)
	}
}

func TestPaymentServiceResolveUnmatchedPaymentReopensFailedOrder(t *testing.T) {
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-4": {
				ID:            "order-4",
				UserID:        "user-1",
				CustomerPhone: "254700000005",
				Status:        core.OrderStatusFailed,
				TotalAmount:   1500,
				PickupCode:    "000004",
			},
		},
	}
	unmatchedRepo := newUnmatchedPaymentTestRepo(
		&core.UnmatchedPayment{
			ID:                "unmatched-1",
			Provider:          string(core.PaymentMethodMpesa),
			Status:            core.UnmatchedPaymentStatusPending,
			ProviderReference: "ref-manual",
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	)
	service := NewPaymentService(orderRepo, newPaymentAttemptTestRepo(), unmatchedRepo, botTestUserRepo{}, botTestPaymentGateway{}, nil, nil)

	payment, err := service.ResolveUnmatchedPayment(context.Background(), "unmatched-1", "order-4", "manual review")
	if err != nil {
		t.Fatalf("ResolveUnmatchedPayment() error = %v", err)
	}
	if payment.Status != core.UnmatchedPaymentStatusResolved {
		t.Fatalf("unmatched status = %s, want %s", payment.Status, core.UnmatchedPaymentStatusResolved)
	}

	order, err := orderRepo.GetByID(context.Background(), "order-4")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if order.Status != core.OrderStatusPaid {
		t.Fatalf("order status = %s, want %s", order.Status, core.OrderStatusPaid)
	}
}

type paymentAttemptTestRepo struct {
	attempts map[string]*core.PaymentAttempt
}

func newPaymentAttemptTestRepo(seed ...*core.PaymentAttempt) *paymentAttemptTestRepo {
	repo := &paymentAttemptTestRepo{attempts: make(map[string]*core.PaymentAttempt)}
	for _, attempt := range seed {
		repo.attempts[attempt.ID] = clonePaymentAttempt(attempt)
	}
	return repo
}

func (r *paymentAttemptTestRepo) Create(ctx context.Context, attempt *core.PaymentAttempt) (*core.PaymentAttempt, bool, error) {
	if attempt.ID == "" {
		attempt.ID = "attempt-created"
	}
	r.attempts[attempt.ID] = clonePaymentAttempt(attempt)
	return clonePaymentAttempt(r.attempts[attempt.ID]), true, nil
}

func (r *paymentAttemptTestRepo) GetByID(ctx context.Context, id string) (*core.PaymentAttempt, error) {
	attempt, ok := r.attempts[id]
	if !ok {
		return nil, nil
	}
	return clonePaymentAttempt(attempt), nil
}

func (r *paymentAttemptTestRepo) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*core.PaymentAttempt, error) {
	for _, attempt := range r.attempts {
		if attempt.IdempotencyKey == idempotencyKey {
			return clonePaymentAttempt(attempt), nil
		}
	}
	return nil, nil
}

func (r *paymentAttemptTestRepo) GetByOrderID(ctx context.Context, orderID string) ([]*core.PaymentAttempt, error) {
	var attempts []*core.PaymentAttempt
	for _, attempt := range r.attempts {
		if attempt.OrderID == orderID {
			attempts = append(attempts, clonePaymentAttempt(attempt))
		}
	}
	return attempts, nil
}

func (r *paymentAttemptTestRepo) GetLatestByOrderID(ctx context.Context, orderID string) (*core.PaymentAttempt, error) {
	attempts, _ := r.GetByOrderID(ctx, orderID)
	if len(attempts) == 0 {
		return nil, nil
	}
	return attempts[0], nil
}

func (r *paymentAttemptTestRepo) GetActiveByOrderID(ctx context.Context, orderID string, provider string) (*core.PaymentAttempt, error) {
	for _, attempt := range r.attempts {
		if attempt.OrderID != orderID {
			continue
		}
		if provider != "" && attempt.Provider != provider {
			continue
		}
		if attempt.Status == core.PaymentAttemptStatusQueued || attempt.Status == core.PaymentAttemptStatusProcessing || attempt.Status == core.PaymentAttemptStatusAwaitingCustomer {
			return clonePaymentAttempt(attempt), nil
		}
	}
	return nil, nil
}

func (r *paymentAttemptTestRepo) GetByProviderReference(ctx context.Context, provider string, reference string) (*core.PaymentAttempt, error) {
	for _, attempt := range r.attempts {
		if attempt.Provider == provider && attempt.ProviderReference == reference {
			return clonePaymentAttempt(attempt), nil
		}
	}
	return nil, nil
}

func (r *paymentAttemptTestRepo) ClaimNextQueued(ctx context.Context, provider string, throttleWindow time.Duration) (*core.PaymentAttempt, error) {
	return nil, nil
}

func (r *paymentAttemptTestRepo) MarkAwaitingCustomer(ctx context.Context, id string, providerReference string) error {
	if attempt, ok := r.attempts[id]; ok {
		attempt.Status = core.PaymentAttemptStatusAwaitingCustomer
		attempt.ProviderReference = providerReference
	}
	return nil
}

func (r *paymentAttemptTestRepo) MarkRetryQueued(ctx context.Context, id string, nextRetryAt time.Time, lastError string) error {
	if attempt, ok := r.attempts[id]; ok {
		attempt.Status = core.PaymentAttemptStatusQueued
		attempt.LastError = lastError
		attempt.NextRetryAt = &nextRetryAt
	}
	return nil
}

func (r *paymentAttemptTestRepo) MarkFailed(ctx context.Context, id string, providerReference string, lastError string) error {
	if attempt, ok := r.attempts[id]; ok {
		attempt.Status = core.PaymentAttemptStatusFailed
		attempt.ProviderReference = providerReference
		attempt.LastError = lastError
	}
	return nil
}

func (r *paymentAttemptTestRepo) MarkSucceeded(ctx context.Context, id string, providerReference string) error {
	if attempt, ok := r.attempts[id]; ok {
		attempt.Status = core.PaymentAttemptStatusSucceeded
		attempt.ProviderReference = providerReference
	}
	return nil
}

func (r *paymentAttemptTestRepo) ExpireActiveForOrder(ctx context.Context, orderID string, now time.Time) error {
	for _, attempt := range r.attempts {
		if attempt.OrderID == orderID && (attempt.Status == core.PaymentAttemptStatusQueued || attempt.Status == core.PaymentAttemptStatusProcessing || attempt.Status == core.PaymentAttemptStatusAwaitingCustomer) {
			attempt.Status = core.PaymentAttemptStatusExpired
			attempt.CompletedAt = &now
		}
	}
	return nil
}

func (r *paymentAttemptTestRepo) GetQueueMetrics(ctx context.Context, attemptID string, provider string, dispatchInterval time.Duration) (int, time.Duration, error) {
	return 1, 2 * time.Second, nil
}

func (r *paymentAttemptTestRepo) GetQueueStats(ctx context.Context, provider string) (*core.PaymentQueueStats, error) {
	return &core.PaymentQueueStats{Provider: provider, QueuedCount: 1}, nil
}

type unmatchedPaymentTestRepo struct {
	payments map[string]*core.UnmatchedPayment
}

func newUnmatchedPaymentTestRepo(seed ...*core.UnmatchedPayment) *unmatchedPaymentTestRepo {
	repo := &unmatchedPaymentTestRepo{payments: make(map[string]*core.UnmatchedPayment)}
	for _, payment := range seed {
		repo.payments[payment.ID] = cloneUnmatchedPayment(payment)
	}
	return repo
}

func (r *unmatchedPaymentTestRepo) Create(ctx context.Context, payment *core.UnmatchedPayment) error {
	if payment.ID == "" {
		payment.ID = "unmatched-created"
	}
	r.payments[payment.ID] = cloneUnmatchedPayment(payment)
	return nil
}

func (r *unmatchedPaymentTestRepo) GetByID(ctx context.Context, id string) (*core.UnmatchedPayment, error) {
	payment, ok := r.payments[id]
	if !ok {
		return nil, nil
	}
	return cloneUnmatchedPayment(payment), nil
}

func (r *unmatchedPaymentTestRepo) ListPending(ctx context.Context, limit int) ([]*core.UnmatchedPayment, error) {
	var payments []*core.UnmatchedPayment
	for _, payment := range r.payments {
		if payment.Status == core.UnmatchedPaymentStatusPending {
			payments = append(payments, cloneUnmatchedPayment(payment))
		}
	}
	return payments, nil
}

func (r *unmatchedPaymentTestRepo) CountPending(ctx context.Context) (int, error) {
	count := 0
	for _, payment := range r.payments {
		if payment.Status == core.UnmatchedPaymentStatusPending {
			count++
		}
	}
	return count, nil
}

func (r *unmatchedPaymentTestRepo) Resolve(ctx context.Context, id string, orderID string, note string) (*core.UnmatchedPayment, error) {
	payment, ok := r.payments[id]
	if !ok {
		return nil, nil
	}
	now := time.Now()
	payment.Status = core.UnmatchedPaymentStatusResolved
	payment.ResolvedOrderID = orderID
	payment.ResolutionNote = note
	payment.ResolvedAt = &now
	return cloneUnmatchedPayment(payment), nil
}

func (r *unmatchedPaymentTestRepo) Reject(ctx context.Context, id string, note string) (*core.UnmatchedPayment, error) {
	payment, ok := r.payments[id]
	if !ok {
		return nil, nil
	}
	now := time.Now()
	payment.Status = core.UnmatchedPaymentStatusRejected
	payment.ResolutionNote = note
	payment.ResolvedAt = &now
	return cloneUnmatchedPayment(payment), nil
}

func clonePaymentAttempt(attempt *core.PaymentAttempt) *core.PaymentAttempt {
	if attempt == nil {
		return nil
	}
	clone := *attempt
	return &clone
}

func cloneUnmatchedPayment(payment *core.UnmatchedPayment) *core.UnmatchedPayment {
	if payment == nil {
		return nil
	}
	clone := *payment
	return &clone
}
