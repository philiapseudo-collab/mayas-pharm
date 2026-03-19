package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/events"
	"github.com/philia-technologies/mayas-pharm/internal/observability"
)

const paymentDispatchBackoffFloor = 5 * time.Second

func isPaidWorkflowStatus(status core.OrderStatus) bool {
	switch status {
	case core.OrderStatusPaid, core.OrderStatusPreparing, core.OrderStatusReady, core.OrderStatusCompleted:
		return true
	default:
		return false
	}
}

type PaymentService struct {
	orderRepo            core.OrderRepository
	paymentAttemptRepo   core.PaymentAttemptRepository
	unmatchedRepo        core.UnmatchedPaymentRepository
	userRepo             core.UserRepository
	paymentGateway       core.PaymentGateway
	whatsappGateway      core.WhatsAppGateway
	eventBus             *events.EventBus
	metrics              *observability.RuntimeMetrics
	dispatchInterval     time.Duration
	dispatchPollInterval time.Duration
	expirySweepInterval  time.Duration
}

func NewPaymentService(
	orderRepo core.OrderRepository,
	paymentAttemptRepo core.PaymentAttemptRepository,
	unmatchedRepo core.UnmatchedPaymentRepository,
	userRepo core.UserRepository,
	paymentGateway core.PaymentGateway,
	whatsappGateway core.WhatsAppGateway,
	eventBus *events.EventBus,
) *PaymentService {
	return &PaymentService{
		orderRepo:          orderRepo,
		paymentAttemptRepo: paymentAttemptRepo,
		unmatchedRepo:      unmatchedRepo,
		userRepo:           userRepo,
		paymentGateway:     paymentGateway,
		whatsappGateway:    whatsappGateway,
		eventBus:           eventBus,
	}
}

func (s *PaymentService) SetMetrics(metrics *observability.RuntimeMetrics) {
	s.metrics = metrics
}

func (s *PaymentService) ConfigureWorkerIntervals(dispatchInterval time.Duration, pollInterval time.Duration, expirySweepInterval time.Duration) {
	s.dispatchInterval = dispatchInterval
	s.dispatchPollInterval = pollInterval
	s.expirySweepInterval = expirySweepInterval
}

func (s *PaymentService) effectiveDispatchInterval() time.Duration {
	if s.dispatchInterval > 0 {
		return s.dispatchInterval
	}
	return 2100 * time.Millisecond
}

func (s *PaymentService) effectiveDispatchPollInterval() time.Duration {
	if s.dispatchPollInterval > 0 {
		return s.dispatchPollInterval
	}
	return 250 * time.Millisecond
}

func (s *PaymentService) effectiveExpirySweepInterval() time.Duration {
	if s.expirySweepInterval > 0 {
		return s.expirySweepInterval
	}
	return 5 * time.Second
}

func paymentRetryBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	backoff := time.Duration(attempts*attempts) * paymentDispatchBackoffFloor
	if backoff > time.Minute {
		return time.Minute
	}
	return backoff
}

func (s *PaymentService) QueueMPESA(ctx context.Context, orderID string, phone string, idempotencyKey string) (*core.PaymentAttempt, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, fmt.Errorf("order not found")
	}

	if isPaidWorkflowStatus(order.Status) {
		return nil, fmt.Errorf("order is not in a payable state (status: %s)", order.Status)
	}
	if order.Status == core.OrderStatusExpired {
		return nil, fmt.Errorf("order has expired")
	}

	if order.Status == core.OrderStatusFailed || order.Status == core.OrderStatusCancelled {
		if err := s.orderRepo.UpdateStatus(ctx, orderID, core.OrderStatusPending); err != nil {
			return nil, fmt.Errorf("failed to reopen order: %w", err)
		}
		order, err = s.orderRepo.GetByID(ctx, orderID)
		if err != nil || order == nil {
			return nil, fmt.Errorf("order not found")
		}
	}

	if order.Status != core.OrderStatusPending {
		return nil, fmt.Errorf("order is not in a payable state (status: %s)", order.Status)
	}
	if order.ExpiresAt != nil && order.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("order has expired")
	}

	phone, err = normaliseCustomerPhone(phone)
	if err != nil {
		return nil, fmt.Errorf("invalid phone number: %w", err)
	}

	if strings.TrimSpace(order.CustomerPhone) != phone {
		if err := s.orderRepo.UpdateCustomerPhone(ctx, orderID, phone); err != nil {
			return nil, fmt.Errorf("failed to update payment phone: %w", err)
		}
	}
	if err := s.orderRepo.UpdatePaymentDetails(ctx, orderID, string(core.PaymentMethodMpesa), ""); err != nil {
		return nil, fmt.Errorf("failed to set payment method: %w", err)
	}

	attempt, _, err := s.paymentAttemptRepo.Create(ctx, &core.PaymentAttempt{
		OrderID:        orderID,
		Provider:       string(core.PaymentMethodMpesa),
		Status:         core.PaymentAttemptStatusQueued,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		RequestedPhone: phone,
		Amount:         order.TotalAmount,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to queue M-Pesa payment: %w", err)
	}

	return s.attachQueueMetrics(ctx, attempt)
}

func (s *PaymentService) attachQueueMetrics(ctx context.Context, attempt *core.PaymentAttempt) (*core.PaymentAttempt, error) {
	if attempt == nil {
		return nil, nil
	}

	position, eta, err := s.paymentAttemptRepo.GetQueueMetrics(ctx, attempt.ID, attempt.Provider, s.effectiveDispatchInterval())
	if err != nil {
		return nil, err
	}
	attempt.QueuePosition = position
	attempt.ETASeconds = int(eta.Seconds())
	return attempt, nil
}

func (s *PaymentService) latestAttemptForProvider(ctx context.Context, orderID string, provider string) (*core.PaymentAttempt, error) {
	attempts, err := s.paymentAttemptRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	for _, attempt := range attempts {
		if strings.EqualFold(attempt.Provider, provider) {
			return attempt, nil
		}
	}
	return nil, nil
}

func (s *PaymentService) GetLatestPaymentAttempt(ctx context.Context, orderID string) (*core.PaymentAttempt, error) {
	attempt, err := s.paymentAttemptRepo.GetLatestByOrderID(ctx, orderID)
	if err != nil || attempt == nil {
		return attempt, err
	}
	return s.attachQueueMetrics(ctx, attempt)
}

func (s *PaymentService) RunDispatcher(ctx context.Context) {
	ticker := time.NewTicker(s.effectiveDispatchPollInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attempt, err := s.paymentAttemptRepo.ClaimNextQueued(ctx, string(core.PaymentMethodMpesa), s.effectiveDispatchInterval())
			if err != nil {
				slog.Error("failed to claim queued payment attempt", "error", err)
				continue
			}
			if attempt == nil {
				continue
			}

			sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			dispatchStartedAt := time.Now()
			err = s.paymentGateway.InitiateSTKPush(sendCtx, attempt.OrderID, attempt.RequestedPhone, attempt.Amount)
			cancel()
			if err != nil {
				if s.metrics != nil {
					s.metrics.ObservePaymentDispatch(false, time.Since(dispatchStartedAt))
				}
				nextRetryAt := time.Now().Add(paymentRetryBackoff(attempt.Attempts))
				if requeueErr := s.paymentAttemptRepo.MarkRetryQueued(context.Background(), attempt.ID, nextRetryAt, err.Error()); requeueErr != nil {
					slog.Error("failed to requeue payment attempt", "attempt_id", attempt.ID, "error", requeueErr)
				}
				continue
			}
			if s.metrics != nil {
				s.metrics.ObservePaymentDispatch(true, time.Since(dispatchStartedAt))
			}

			if err := s.paymentAttemptRepo.MarkAwaitingCustomer(context.Background(), attempt.ID, ""); err != nil {
				slog.Error("failed to mark payment attempt awaiting customer", "attempt_id", attempt.ID, "error", err)
			}
		}
	}
}

func (s *PaymentService) RunOrderExpiryLoop(ctx context.Context) {
	ticker := time.NewTicker(s.effectiveExpirySweepInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expiredOrders, err := s.orderRepo.ExpirePendingOrders(ctx, time.Now(), 100)
			if err != nil {
				slog.Error("failed to expire pending orders", "error", err)
				continue
			}
			for _, order := range expiredOrders {
				if s.eventBus != nil {
					s.eventBus.PublishOrderUpdated(order)
				}
				s.notifyOrderExpired(order)
			}
		}
	}
}

func (s *PaymentService) ProcessKopoKopoWebhook(ctx context.Context, result *core.PaymentWebhook, rawPayload []byte) (*core.Order, error) {
	if result == nil {
		return nil, fmt.Errorf("payment webhook result is required")
	}

	var (
		order   *core.Order
		attempt *core.PaymentAttempt
		err     error
	)

	if strings.TrimSpace(result.OrderID) != "" {
		order, err = s.orderRepo.GetByID(ctx, result.OrderID)
		if err != nil {
			return nil, fmt.Errorf("failed to load webhook order: %w", err)
		}
		attempt, err = s.latestAttemptForProvider(ctx, result.OrderID, string(core.PaymentMethodMpesa))
		if err != nil {
			return nil, err
		}
	}

	if attempt == nil && strings.TrimSpace(result.Reference) != "" {
		attempt, err = s.paymentAttemptRepo.GetByProviderReference(ctx, string(core.PaymentMethodMpesa), result.Reference)
		if err != nil {
			return nil, err
		}
		if attempt != nil && order == nil {
			order, err = s.orderRepo.GetByID(ctx, attempt.OrderID)
			if err != nil {
				return nil, err
			}
		}
	}

	if order == nil && result.Success && strings.TrimSpace(result.HashedPhone) != "" {
		order, err = s.orderRepo.FindPendingByHashedPhoneAndAmount(ctx, result.HashedPhone, result.Amount)
		if err != nil {
			return nil, err
		}
		if order != nil && attempt == nil {
			attempt, err = s.latestAttemptForProvider(ctx, order.ID, string(core.PaymentMethodMpesa))
			if err != nil {
				return nil, err
			}
		}
	}

	if result.Success {
		if order == nil {
			if err := s.unmatchedRepo.Create(ctx, &core.UnmatchedPayment{
				Provider:          string(core.PaymentMethodMpesa),
				Status:            core.UnmatchedPaymentStatusPending,
				ProviderReference: strings.TrimSpace(result.Reference),
				OrderIDHint:       strings.TrimSpace(result.OrderID),
				Phone:             strings.TrimSpace(result.Phone),
				HashedPhone:       strings.TrimSpace(result.HashedPhone),
				Amount:            result.Amount,
				Payload:           string(rawPayload),
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}); err != nil {
				return nil, err
			}
			if s.metrics != nil {
				s.metrics.IncUnmatchedCreated()
			}
			return nil, nil
		}

		if order.Status != core.OrderStatusPending && !isPaidWorkflowStatus(order.Status) {
			if err := s.unmatchedRepo.Create(ctx, &core.UnmatchedPayment{
				Provider:          string(core.PaymentMethodMpesa),
				Status:            core.UnmatchedPaymentStatusPending,
				ProviderReference: strings.TrimSpace(result.Reference),
				OrderIDHint:       order.ID,
				Phone:             strings.TrimSpace(result.Phone),
				HashedPhone:       strings.TrimSpace(result.HashedPhone),
				Amount:            result.Amount,
				Payload:           string(rawPayload),
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}); err != nil {
				return nil, err
			}
			if s.metrics != nil {
				s.metrics.IncUnmatchedCreated()
			}
			return nil, nil
		}

		if attempt != nil {
			if err := s.paymentAttemptRepo.MarkSucceeded(ctx, attempt.ID, result.Reference); err != nil {
				return nil, err
			}
		}

		updatedOrder, changed, err := s.orderRepo.MarkPaid(ctx, order.ID, string(core.PaymentMethodMpesa), result.Reference)
		if err != nil {
			return nil, err
		}

		if changed {
			s.notifyPaymentReceived(updatedOrder)
			if s.eventBus != nil {
				s.eventBus.PublishNewOrder(updatedOrder)
				s.eventBus.PublishOrderUpdated(updatedOrder)
			}
		}

		return updatedOrder, nil
	}

	if order == nil {
		return nil, nil
	}

	if isPaidWorkflowStatus(order.Status) {
		return order, nil
	}

	if attempt != nil {
		if err := s.paymentAttemptRepo.MarkFailed(ctx, attempt.ID, result.Reference, strings.TrimSpace(result.Status)); err != nil {
			return nil, err
		}
	}

	if order.Status == core.OrderStatusPending {
		if err := s.orderRepo.UpdateStatus(ctx, order.ID, core.OrderStatusFailed); err != nil {
			return nil, err
		}
		refreshed, err := s.orderRepo.GetByID(ctx, order.ID)
		if err == nil && refreshed != nil {
			order = refreshed
		}
		if s.eventBus != nil {
			s.eventBus.PublishOrderUpdated(order)
		}
	}

	s.notifyPaymentFailed(order)
	return order, nil
}

func (s *PaymentService) ListPendingUnmatchedPayments(ctx context.Context, limit int) ([]*core.UnmatchedPayment, error) {
	return s.unmatchedRepo.ListPending(ctx, limit)
}

func (s *PaymentService) ResolveUnmatchedPayment(ctx context.Context, unmatchedPaymentID string, orderID string, note string) (*core.UnmatchedPayment, error) {
	unmatched, err := s.unmatchedRepo.GetByID(ctx, unmatchedPaymentID)
	if err != nil {
		return nil, err
	}
	if unmatched == nil {
		return nil, fmt.Errorf("unmatched payment not found")
	}

	order, err := s.orderRepo.GetByID(ctx, strings.TrimSpace(orderID))
	if err != nil || order == nil {
		return nil, fmt.Errorf("order not found")
	}
	if order.Status == core.OrderStatusFailed || order.Status == core.OrderStatusCancelled {
		if err := s.orderRepo.UpdateStatus(ctx, order.ID, core.OrderStatusPending); err != nil {
			return nil, err
		}
	}

	if _, _, err := s.orderRepo.MarkPaid(ctx, strings.TrimSpace(orderID), string(core.PaymentMethodMpesa), strings.TrimSpace(unmatched.ProviderReference)); err != nil {
		return nil, err
	}

	resolved, err := s.unmatchedRepo.Resolve(ctx, unmatchedPaymentID, orderID, note)
	if err != nil {
		return nil, err
	}

	if order, getErr := s.orderRepo.GetByID(ctx, orderID); getErr == nil && order != nil {
		s.notifyPaymentReceived(order)
		if s.eventBus != nil {
			s.eventBus.PublishNewOrder(order)
			s.eventBus.PublishOrderUpdated(order)
		}
	}

	return resolved, nil
}

func (s *PaymentService) RejectUnmatchedPayment(ctx context.Context, unmatchedPaymentID string, note string) (*core.UnmatchedPayment, error) {
	return s.unmatchedRepo.Reject(ctx, unmatchedPaymentID, note)
}

func (s *PaymentService) QueueStats(ctx context.Context, provider string) (*core.PaymentQueueStats, error) {
	return s.paymentAttemptRepo.GetQueueStats(ctx, provider)
}

func (s *PaymentService) PendingUnmatchedCount(ctx context.Context) (int, error) {
	return s.unmatchedRepo.CountPending(ctx)
}

func (s *PaymentService) resolveOrderNotificationPhone(ctx context.Context, order *core.Order) string {
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

func (s *PaymentService) notifyPaymentReceived(order *core.Order) {
	if order == nil || s.whatsappGateway == nil {
		return
	}

	go func(snapshot *core.Order) {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		recipientPhone := s.resolveOrderNotificationPhone(sendCtx, snapshot)
		if recipientPhone != "" {
			label := "Order Code"
			if strings.EqualFold(snapshot.FulfillmentType, string(core.FulfillmentTypePickup)) || strings.TrimSpace(snapshot.FulfillmentType) == "" {
				label = "Pickup Code"
			}
			message := fmt.Sprintf("\u2705 *Payment Received!*\n\nYour order has been confirmed.\n\n*%s:* %s\n*Total:* KES %.0f",
				label, snapshot.PickupCode, snapshot.TotalAmount)
			if err := s.whatsappGateway.SendText(sendCtx, recipientPhone, message); err != nil {
				slog.Warn("failed to notify customer about successful payment", "order_id", snapshot.ID, "error", err)
			}
		}

		opsAlertPhone := strings.TrimSpace(config.Get().OpsAlertPhone)
		if opsAlertPhone == "" {
			return
		}

		builder := strings.Builder{}
		builder.WriteString("\U0001F6A8 *New Paid Order*\n\n")
		builder.WriteString(fmt.Sprintf("*Order #%s*\n", snapshot.PickupCode))
		builder.WriteString("*Items:*\n")
		for _, item := range snapshot.Items {
			name := item.ProductName
			if name == "" {
				name = "Unknown Item"
			}
			builder.WriteString(fmt.Sprintf("- %d x %s\n", item.Quantity, name))
		}
		builder.WriteString(fmt.Sprintf("\n*Total:* KES %.0f\n", snapshot.TotalAmount))
		_ = s.whatsappGateway.SendText(sendCtx, opsAlertPhone, builder.String())
	}(order)
}

func (s *PaymentService) notifyPaymentFailed(order *core.Order) {
	if order == nil || s.whatsappGateway == nil {
		return
	}

	go func(snapshot *core.Order) {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		recipientPhone := s.resolveOrderNotificationPhone(sendCtx, snapshot)
		if recipientPhone == "" {
			return
		}

		message := fmt.Sprintf("*Payment Not Completed*\n\nYour M-Pesa payment for *KES %.0f* was cancelled or timed out.\n\nTap *Resend Prompt* to try again.", snapshot.TotalAmount)
		buttons := []core.Button{
			{ID: "retry_pay_" + snapshot.ID, Title: "Resend Prompt"},
			{ID: "retry_other_" + snapshot.ID, Title: "Use Different Number"},
			{ID: "retry_cancel_" + snapshot.ID, Title: "Cancel"},
		}

		if err := s.whatsappGateway.SendMenuButtons(sendCtx, recipientPhone, message, buttons); err != nil {
			slog.Warn("failed to notify customer about payment failure", "order_id", snapshot.ID, "error", err)
			_ = s.whatsappGateway.SendText(sendCtx, recipientPhone, message)
		}
	}(order)
}

func (s *PaymentService) notifyOrderExpired(order *core.Order) {
	if order == nil || s.whatsappGateway == nil {
		return
	}

	go func(snapshot *core.Order) {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		recipientPhone := s.resolveOrderNotificationPhone(sendCtx, snapshot)
		if recipientPhone == "" {
			return
		}

		message := fmt.Sprintf("*Order Expired*\n\nOrder #%s expired before payment was completed. You can start a new order any time.", snapshot.PickupCode)
		_ = s.whatsappGateway.SendText(sendCtx, recipientPhone, message)
	}(order)
}
