package http

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/service"
)

type mockCustomerService struct {
	initPesapalFn      func(ctx context.Context, orderID string, callbackURL string) (*service.CardPaymentInitResult, error)
	reconcilePesapalFn func(ctx context.Context, orderTrackingID string, merchantReference string) (*core.Order, error)
}

func (m *mockCustomerService) GetActiveProducts(ctx context.Context) (map[string][]*core.Product, error) {
	return map[string][]*core.Product{}, nil
}

func (m *mockCustomerService) GetActiveCategories(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

func (m *mockCustomerService) GetActiveDeliveryZones(ctx context.Context) ([]*core.DeliveryZone, error) {
	return []*core.DeliveryZone{}, nil
}

func (m *mockCustomerService) CreateOrder(ctx context.Context, input service.CreateOrderInput) (*core.Order, bool, error) {
	return nil, false, nil
}

func (m *mockCustomerService) UploadPrescription(ctx context.Context, orderID string, input service.UploadPrescriptionInput) (*core.Prescription, error) {
	return nil, nil
}

func (m *mockCustomerService) InitiateSTKPush(ctx context.Context, orderID string, phone string, idempotencyKey string) (*core.PaymentAttempt, bool, error) {
	return nil, false, nil
}

func (m *mockCustomerService) InitiatePesapalPayment(ctx context.Context, orderID string, callbackURL string) (*service.CardPaymentInitResult, error) {
	if m.initPesapalFn != nil {
		return m.initPesapalFn(ctx, orderID, callbackURL)
	}
	return &service.CardPaymentInitResult{}, nil
}

func (m *mockCustomerService) ReconcilePesapalPayment(ctx context.Context, orderTrackingID string, merchantReference string) (*core.Order, error) {
	if m.reconcilePesapalFn != nil {
		return m.reconcilePesapalFn(ctx, orderTrackingID, merchantReference)
	}
	return &core.Order{ID: merchantReference, Status: core.OrderStatusPaid}, nil
}

func (m *mockCustomerService) GetOrderStatus(ctx context.Context, orderID string) (*core.Order, error) {
	return &core.Order{ID: orderID, Status: core.OrderStatusPending}, nil
}

func (m *mockCustomerService) GetLatestPaymentAttempt(ctx context.Context, orderID string) (*core.PaymentAttempt, error) {
	return nil, nil
}

func TestPayWithPesapalReturnsRedirect(t *testing.T) {
	app := fiber.New()
	handler := NewCustomerHandler(&mockCustomerService{
		initPesapalFn: func(ctx context.Context, orderID string, callbackURL string) (*service.CardPaymentInitResult, error) {
			if orderID != "order-1" {
				t.Fatalf("unexpected order ID: %s", orderID)
			}
			return &service.CardPaymentInitResult{
				RedirectURL:       "https://cybqa.pesapal.com/redirect",
				OrderTrackingID:   "trk_123",
				MerchantReference: "order-1",
			}, nil
		},
	})

	app.Post("/api/customer/orders/:id/pay/pesapal", handler.PayWithPesapal)

	req := httptest.NewRequest("POST", "/api/customer/orders/order-1/pay/pesapal", strings.NewReader(`{"callback_url":"https://example.com/order/order-1"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload["redirect_url"] != "https://cybqa.pesapal.com/redirect" {
		t.Fatalf("redirect_url = %v", payload["redirect_url"])
	}
}

func TestHandlePesapalWebhookValidatesRequiredParams(t *testing.T) {
	app := fiber.New()
	handler := NewCustomerHandler(&mockCustomerService{})
	app.Get("/api/webhooks/pesapal", handler.HandlePesapalWebhook)

	req := httptest.NewRequest("GET", "/api/webhooks/pesapal", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func TestHandlePesapalWebhookReconcilesOrder(t *testing.T) {
	app := fiber.New()
	handler := NewCustomerHandler(&mockCustomerService{
		reconcilePesapalFn: func(ctx context.Context, orderTrackingID string, merchantReference string) (*core.Order, error) {
			if orderTrackingID != "trk_123" {
				t.Fatalf("unexpected tracking ID: %s", orderTrackingID)
			}
			return &core.Order{ID: merchantReference, Status: core.OrderStatusPaid}, nil
		},
	})
	app.Get("/api/webhooks/pesapal", handler.HandlePesapalWebhook)

	req := httptest.NewRequest("GET", "/api/webhooks/pesapal?OrderTrackingId=trk_123&OrderMerchantReference=order-1&OrderNotificationType=IPNCHANGE", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}
