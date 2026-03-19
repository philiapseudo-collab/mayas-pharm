package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"log/slog"
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/adapters/whatsapp"
	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/observability"
	"github.com/gofiber/fiber/v2"
)

// Handler handles HTTP requests for WhatsApp and payment webhooks.
type Handler struct {
	verifyToken       string
	appSecret         string
	botService        BotServiceHandler
	paymentGateway    PaymentGatewayHandler
	paymentService    PaymentWebhookService
	whatsappSemaphore chan struct{}
	metrics           *observability.RuntimeMetrics
}

// PaymentGatewayHandler defines the interface for payment gateway.
type PaymentGatewayHandler interface {
	VerifyWebhook(ctx context.Context, signature string, payload []byte) bool
	ProcessWebhook(ctx context.Context, payload []byte) (*core.PaymentWebhook, error)
}

// PaymentWebhookService defines the application service used to persist webhook outcomes.
type PaymentWebhookService interface {
	ProcessKopoKopoWebhook(ctx context.Context, result *core.PaymentWebhook, rawPayload []byte) (*core.Order, error)
}

// BotServiceHandler defines the interface for bot service.
type BotServiceHandler interface {
	HandleIncomingMessage(phone string, message string, messageType string) error
}

// NewHandler creates a new HTTP handler.
func NewHandler(botService BotServiceHandler, paymentGateway PaymentGatewayHandler, paymentService PaymentWebhookService, whatsappMaxConcurrent int, metrics *observability.RuntimeMetrics) *Handler {
	cfg := config.Get()
	verifyToken := strings.TrimSpace(cfg.WhatsAppVerifyToken)
	if whatsappMaxConcurrent <= 0 {
		whatsappMaxConcurrent = 32
	}

	if verifyToken == "" {
		log.Printf("WARNING: WHATSAPP_VERIFY_TOKEN is not set or empty!")
	}

	return &Handler{
		verifyToken:       verifyToken,
		appSecret:         "",
		botService:        botService,
		paymentGateway:    paymentGateway,
		paymentService:    paymentService,
		whatsappSemaphore: make(chan struct{}, whatsappMaxConcurrent),
		metrics:           metrics,
	}
}

// VerifyWebhook handles GET requests for webhook verification.
func (h *Handler) VerifyWebhook(c *fiber.Ctx) error {
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")

	if mode != "subscribe" {
		slog.Warn("whatsapp webhook verification failed", "reason", "invalid_mode")
		return c.Status(fiber.StatusBadRequest).SendString("Invalid mode")
	}

	providedToken := strings.TrimSpace(token)
	expectedToken := strings.TrimSpace(h.verifyToken)
	if providedToken != expectedToken {
		slog.Warn("whatsapp webhook verification failed", "reason", "token_mismatch", "provided", maskToken(providedToken))
		return c.Status(fiber.StatusForbidden).SendString("Invalid verify token")
	}

	slog.Info("whatsapp webhook verified")
	return c.SendString(challenge)
}

// maskToken masks a token for logging.
func maskToken(token string) string {
	if token == "" {
		return "<empty>"
	}
	if len(token) <= 6 {
		return "***"
	}
	return token[:3] + "***" + token[len(token)-3:]
}

// ReceiveMessage handles POST requests for incoming WhatsApp messages.
func (h *Handler) ReceiveMessage(c *fiber.Ctx) error {
	if h.appSecret != "" {
		signature := c.Get("X-Hub-Signature-256")
		if signature == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing signature",
			})
		}

		body := c.Body()
		if !h.verifySignature(signature, body) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Invalid signature",
			})
		}
	}

	var payload whatsapp.WebhookPayload
	if err := c.BodyParser(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid payload",
		})
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}

			value := change.Value
			for _, msg := range value.Messages {
				phone := msg.From
				messageType := msg.Type

				var messageText string
				var interactiveID string

				switch messageType {
				case "text":
					messageText = msg.Text.Body
				case "interactive":
					switch msg.Interactive.Type {
					case "button_reply":
						interactiveID = msg.Interactive.ButtonReply.ID
						messageText = msg.Interactive.ButtonReply.Title
					case "list_reply":
						interactiveID = msg.Interactive.ListReply.ID
						messageText = msg.Interactive.ListReply.Title
					}
				case "image":
					messageText = strings.TrimSpace(msg.Image.ID)
					if messageText == "" {
						messageText = strings.TrimSpace(msg.Image.Caption)
					}
				case "document":
					messageText = strings.TrimSpace(msg.Document.ID)
					if messageText == "" {
						messageText = strings.TrimSpace(msg.Document.Filename)
					}
				default:
					continue
				}

				messageToProcess := interactiveID
				if messageToProcess == "" {
					messageToProcess = messageText
				}

				h.whatsappSemaphore <- struct{}{}
				if h.metrics != nil {
					h.metrics.IncWhatsAppAccepted()
				}
				go func(phoneNum, msgText, msgType string) {
					defer func() {
						<-h.whatsappSemaphore
					}()
					if err := h.botService.HandleIncomingMessage(phoneNum, msgText, msgType); err != nil {
						slog.Warn("failed to handle whatsapp message", "message_type", msgType, "error", err)
					}
				}(phone, messageToProcess, messageType)
			}
		}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status": "ok",
	})
}

// verifySignature verifies the X-Hub-Signature-256 header using HMAC-SHA256.
func (h *Handler) verifySignature(signature string, body []byte) bool {
	parts := strings.Split(signature, "=")
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}

	expectedSig, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.appSecret))
	mac.Write(body)
	computedSig := mac.Sum(nil)

	return hmac.Equal(expectedSig, computedSig)
}

// HandlePaymentWebhook handles POST requests for Kopo Kopo payment webhooks.
func (h *Handler) HandlePaymentWebhook(c *fiber.Ctx) error {
	ctx := c.Context()

	signature := c.Get("X-KopoKopo-Signature")
	if signature == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Missing signature",
		})
	}

	body := c.Body()
	if !h.paymentGateway.VerifyWebhook(ctx, signature, body) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid signature",
		})
	}

	result, err := h.paymentGateway.ProcessWebhook(ctx, body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Failed to process webhook",
		})
	}

	if h.paymentService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "payment service unavailable",
		})
	}

	order, err := h.paymentService.ProcessKopoKopoWebhook(ctx, result, body)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to persist webhook",
		})
	}

	response := fiber.Map{
		"status": "ok",
	}
	if order != nil {
		response["order_id"] = order.ID
		response["order_status"] = order.Status
	}

	return c.Status(fiber.StatusOK).JSON(response)
}
