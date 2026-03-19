package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/philia-technologies/mayas-pharm/internal/core"
)

// Client handles Kopo Kopo payment operations.
type Client struct {
	baseURL       string
	webhookSecret string
	tillNumber    string
	callbackURL   string
	httpClient    *http.Client
	// OAuth: used when KOPOKOPO_ACCESS_TOKEN is not set
	clientID     string
	clientSecret string
	accessToken  string
	tokenExpiry  time.Time
	tokenMu      sync.Mutex
}

// tokenResponse is the OAuth client_credentials token response
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds
	TokenType   string `json:"token_type"`
}

// NewClient creates a new Kopo Kopo payment client.
// Dispatch rate limiting is handled by the durable payment_attempts worker, not in memory here.
func NewClient() (*Client, error) {
	cfg := config.Get()
	c := &Client{
		baseURL:       cfg.KopoKopoBaseURL,
		webhookSecret: cfg.KopoKopoWebhookSecret,
		tillNumber:    cfg.KopoKopoTillNumber,
		callbackURL:   cfg.KopoKopoCallbackURL,
		clientID:      cfg.KopoKopoClientID,
		clientSecret:  cfg.KopoKopoClientSecret,
		accessToken:   cfg.KopoKopoAccessToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	return c, nil
}

func (c *Client) fetchOAuthToken(ctx context.Context) (accessToken string, expiresIn int, err error) {
	authURL := strings.TrimSuffix(c.baseURL, "/") + "/oauth/token"
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("grant_type", "client_credentials")
	req, err := http.NewRequestWithContext(ctx, "POST", authURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "mayas-pharm/1.0")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("oauth token error: status %d, body: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("parse token response: %w", err)
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	return tr.AccessToken, tr.ExpiresIn, nil
}

// STKPushRequest represents the Kopo Kopo STK Push request payload
type STKPushRequest struct {
	PaymentChannel string `json:"payment_channel"`
	TillNumber     string `json:"till_number"`
	Subscriber     struct {
		FirstName   string `json:"first_name,omitempty"`
		LastName    string `json:"last_name,omitempty"`
		PhoneNumber string `json:"phone_number"`
	} `json:"subscriber"`
	Amount struct {
		Currency string `json:"currency"`
		Value    string `json:"value"`
	} `json:"amount"`
	Metadata struct {
		OrderID string `json:"order_id"`
	} `json:"metadata"`
	Links struct {
		CallbackURL string `json:"callback_url"`
	} `json:"_links"`
}

// STKPushResponse represents the Kopo Kopo STK Push response
type STKPushResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Reference   string `json:"reference,omitempty"`
	Description string `json:"description,omitempty"`
}

// InitiateSTKPush sends an M-Pesa STK Push request immediately.
// Callers are expected to provide their own durable queueing and retry semantics.
func (c *Client) InitiateSTKPush(ctx context.Context, orderID string, phone string, amount float64) error {
	return c.sendSTKPush(ctx, orderID, phone, amount)
}

// sendSTKPush sends an M-Pesa STK Push request to Kopo Kopo API (internal worker method).
func (c *Client) sendSTKPush(ctx context.Context, orderID string, phone string, amount float64) error {
	// Validate and sanitize phone number
	// Use format WITHOUT + prefix (254xxxxxxxxx) as this is more compatible with M-Pesa STK
	// Some phones/SIM cards have issues with the + prefix causing PIN dialog freezes
	phone, err := sanitizeAndValidatePhoneWithoutPlus(phone)
	if err != nil {
		return fmt.Errorf("invalid phone number: %w", err)
	}

	// Format amount as integer string (Kopo Kopo expects whole numbers for KES)
	amountStr := fmt.Sprintf("%.0f", amount)

	// Build request payload (Kopo Kopo incoming_payments format)
	// Use minimal values (".") for optional name fields to reduce SIM Toolkit payload size
	// This helps prevent processing issues on older SIMs and iPhones
	payload := STKPushRequest{
		PaymentChannel: "M-PESA STK Push",
		TillNumber:     c.tillNumber,
	}
	payload.Subscriber.FirstName = "." // Minimal value - reduces SIM command bytes
	payload.Subscriber.LastName = "."  // Minimal value - reduces SIM command bytes
	payload.Subscriber.PhoneNumber = phone
	payload.Amount.Currency = "KES"
	payload.Amount.Value = amountStr
	payload.Metadata.OrderID = orderID
	payload.Links.CallbackURL = c.callbackURL

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal STK push request: %w", err)
	}

	slog.Info("Sending STK push request",
		"order_id", orderID,
		"amount", amountStr,
		"till", c.tillNumber,
		"phone_suffix", phone[max(len(phone)-4, 0):],
		"callback_configured", c.callbackURL != "")

	// Get fresh OAuth token (force refresh if needed)
	token, err := c.getAccessTokenWithRefresh(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// Make API request (correct Kopo Kopo endpoint)
	apiURL := fmt.Sprintf("%s/api/v1/incoming_payments", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send STK push request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Handle API errors with retry on 401 (token expired)
	if resp.StatusCode == http.StatusUnauthorized {
		slog.Warn("Token expired, refreshing and retrying", "order_id", orderID)
		c.clearCachedToken()
		return c.sendSTKPush(ctx, orderID, phone, amount) // Retry once with fresh token
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		slog.Error("Kopo Kopo API error",
			"status", resp.StatusCode,
			"order_id", orderID,
			"phone_suffix", phone[max(len(phone)-4, 0):])
		return fmt.Errorf("kopokopo API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Kopo Kopo may return empty body on success (HTTP 201 with Location header)
	if len(body) > 0 {
		var stkResponse STKPushResponse
		if err := json.Unmarshal(body, &stkResponse); err != nil {
			slog.Warn("Failed to parse Kopo Kopo response (request was successful)", "error", err.Error(), "body", string(body))
		} else {
			slog.Info("Kopo Kopo STK response", "reference", stkResponse.Reference, "status", stkResponse.Status)
		}
	} else {
		slog.Info("Kopo Kopo STK push accepted", "order_id", orderID, "status_code", resp.StatusCode)
	}

	return nil
}

// clearCachedToken clears the cached OAuth token to force refresh
func (c *Client) clearCachedToken() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	// Only clear if using OAuth (not static token from env)
	if c.clientID != "" && c.clientSecret != "" {
		c.accessToken = ""
		c.tokenExpiry = time.Time{}
	}
}

// getAccessTokenWithRefresh gets a valid token, forcing refresh if close to expiry
func (c *Client) getAccessTokenWithRefresh(ctx context.Context) (string, error) {
	c.tokenMu.Lock()

	// Check if we have OAuth credentials
	hasOAuth := c.clientID != "" && c.clientSecret != ""

	// If we have a static token and no OAuth credentials, use the static token
	// But warn if it might be expired (we can't know without trying)
	staticToken := c.accessToken
	if !hasOAuth && staticToken != "" {
		c.tokenMu.Unlock()
		return staticToken, nil
	}

	// If using OAuth, check if token needs refresh (within 10 minutes of expiry for safety)
	if hasOAuth {
		if c.accessToken != "" && time.Now().Add(10*time.Minute).Before(c.tokenExpiry) {
			token := c.accessToken
			c.tokenMu.Unlock()
			return token, nil
		}
	}
	c.tokenMu.Unlock()

	// Fetch new OAuth token
	if hasOAuth {
		token, expiresIn, err := c.fetchOAuthToken(ctx)
		if err != nil {
			return "", err
		}
		c.tokenMu.Lock()
		c.accessToken = token
		c.tokenExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
		c.tokenMu.Unlock()
		slog.Info("OAuth token refreshed", "expires_in_seconds", expiresIn)
		return token, nil
	}

	return "", errors.New("no valid authentication method configured")
}

// VerifyWebhook verifies the X-KopoKopo-Signature header
func (c *Client) VerifyWebhook(ctx context.Context, signature string, payload []byte) bool {
	if c.webhookSecret == "" {
		slog.Warn("kopokopo webhook secret missing; skipping signature verification")
		return true
	}

	// Signature format: sha256=<hex_string>
	parts := strings.Split(signature, "=")
	if len(parts) != 2 || parts[0] != "sha256" {
		// Try without prefix (some implementations don't use sha256= prefix)
		parts = []string{"sha256", signature}
	}

	expectedSig, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(c.webhookSecret))
	mac.Write(payload)
	computedSig := mac.Sum(nil)

	isValid := hmac.Equal(expectedSig, computedSig)
	if !isValid {
		slog.Warn("kopokopo webhook signature mismatch")
	}

	return isValid
}

// PaymentWebhookPayload represents the buygoods_transaction_received webhook format
type PaymentWebhookPayload struct {
	Topic     string `json:"topic"` // e.g., "buygoods_transaction_received"
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Event     struct {
		Type     string `json:"type"`
		Resource struct {
			ID                string `json:"id"`
			Amount            string `json:"amount"`
			Status            string `json:"status"`
			System            string `json:"system"`
			Currency          string `json:"currency"`
			Reference         string `json:"reference"`
			TillNumber        string `json:"till_number"`
			SenderPhoneNumber string `json:"sender_phone_number"`
			HashedSenderPhone string `json:"hashed_sender_phone"` // SHA256 hash of sender phone (for buygoods webhooks)
			OriginationTime   string `json:"origination_time"`
			SenderFirstName   string `json:"sender_first_name"`
			SenderLastName    string `json:"sender_last_name"`
		} `json:"resource"`
	} `json:"event"`
	Links struct {
		Self     string `json:"self"`
		Resource string `json:"resource"`
	} `json:"_links"`
}

// IncomingPaymentWebhook represents the incoming_payment callback format (STK push result)
type IncomingPaymentWebhook struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"` // "incoming_payment"
		Attributes struct {
			InitiationTime string `json:"initiation_time"`
			Status         string `json:"status"` // "Success" or "Failed"
			Event          struct {
				Type     string `json:"type"`
				Resource *struct {
					ID                string `json:"id"`
					Amount            string `json:"amount"`
					Status            string `json:"status"`
					System            string `json:"system"`
					Currency          string `json:"currency"`
					Reference         string `json:"reference"`
					TillNumber        string `json:"till_number"`
					SenderPhoneNumber string `json:"sender_phone_number"`
					OriginationTime   string `json:"origination_time"`
				} `json:"resource"`
				Errors interface{} `json:"errors"` // Can be string or null
			} `json:"event"`
			Metadata struct {
				OrderID string `json:"order_id"`
			} `json:"metadata"`
			Links struct {
				CallbackURL string `json:"callback_url"`
				Self        string `json:"self"`
			} `json:"_links"`
		} `json:"attributes"`
	} `json:"data"`
}

// ProcessWebhook processes the payment webhook and extracts order information
// Handles both buygoods_transaction_received and incoming_payment formats
func (c *Client) ProcessWebhook(ctx context.Context, payload []byte) (*core.PaymentWebhook, error) {
	// Try to detect which format this is by checking for "data" or "topic" field
	var detector map[string]interface{}
	if err := json.Unmarshal(payload, &detector); err != nil {
		return nil, fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	// Check if this is an incoming_payment webhook (has "data" field)
	if _, hasData := detector["data"]; hasData {
		return c.processIncomingPaymentWebhook(payload)
	}

	// Otherwise, try buygoods_transaction_received format
	return c.processBuygoodsWebhook(payload)
}

// processIncomingPaymentWebhook handles the STK push callback format
func (c *Client) processIncomingPaymentWebhook(payload []byte) (*core.PaymentWebhook, error) {
	var webhook IncomingPaymentWebhook
	if err := json.Unmarshal(payload, &webhook); err != nil {
		return nil, fmt.Errorf("failed to parse incoming_payment webhook: %w", err)
	}

	attrs := webhook.Data.Attributes

	// Check if payment was successful
	isSuccess := strings.ToLower(attrs.Status) == "success"

	result := &core.PaymentWebhook{
		OrderID: attrs.Metadata.OrderID, // We have the order ID directly!
		Status:  attrs.Status,
		Success: isSuccess,
	}

	// Extract phone and amount from event.resource if available
	if attrs.Event.Resource != nil {
		result.Phone = attrs.Event.Resource.SenderPhoneNumber
		result.Reference = attrs.Event.Resource.Reference

		if attrs.Event.Resource.Amount != "" {
			var amount float64
			if _, err := fmt.Sscanf(attrs.Event.Resource.Amount, "%f", &amount); err == nil {
				result.Amount = amount
			}
		}

	}

	return result, nil
}

// processBuygoodsWebhook handles the buygoods_transaction_received format
func (c *Client) processBuygoodsWebhook(payload []byte) (*core.PaymentWebhook, error) {
	var webhook PaymentWebhookPayload
	if err := json.Unmarshal(payload, &webhook); err != nil {
		return nil, fmt.Errorf("failed to parse buygoods webhook: %w", err)
	}

	// Check if this is a successful transaction.
	// CRITICAL: For buygoods_transaction_received webhooks:
	// - "Received" means the payment was successfully received by the till
	// - "Success" also means success
	// Both should trigger order fulfillment.
	status := strings.ToLower(webhook.Event.Resource.Status)
	isSuccess := (webhook.Topic == "buygoods_transaction_received" ||
		strings.Contains(strings.ToLower(webhook.Topic), "transaction")) &&
		(status == "success" || status == "received")

	result := &core.PaymentWebhook{
		OrderID:     "", // Will be matched in handler using phone + amount, or amount alone
		Status:      webhook.Event.Resource.Status,
		Reference:   webhook.Event.Resource.Reference,
		Phone:       webhook.Event.Resource.SenderPhoneNumber,
		HashedPhone: webhook.Event.Resource.HashedSenderPhone, // For buygoods webhooks
		Success:     isSuccess,
	}

	// Parse amount if available
	if webhook.Event.Resource.Amount != "" {
		var amount float64
		if _, err := fmt.Sscanf(webhook.Event.Resource.Amount, "%f", &amount); err == nil {
			result.Amount = amount
		}
	}

	return result, nil
}

// sanitizeAndValidatePhoneWithoutPlus converts phone number to format 254xxxxxxxxx (WITHOUT + prefix)
// Some M-Pesa STK implementations work better without the + prefix, avoiding PIN dialog freezes.
func sanitizeAndValidatePhoneWithoutPlus(phone string) (string, error) {
	// Remove all spaces, dashes, and other common separators
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	phone = strings.TrimSpace(phone)

	// Remove leading + if present
	phone = strings.TrimPrefix(phone, "+")

	// Handle different input formats
	if strings.HasPrefix(phone, "0") {
		// 07xxxxxxxx or 01xxxxxxxx -> 254xxxxxxxxx
		phone = "254" + phone[1:]
	} else if !strings.HasPrefix(phone, "254") {
		// 7xxxxxxxx -> 2547xxxxxxxx
		phone = "254" + phone
	}

	// Validate: must be exactly 12 digits (254 + 9 digits)
	if len(phone) != 12 {
		return "", fmt.Errorf("invalid phone number length: expected 12 digits (254xxxxxxxxx), got %d", len(phone))
	}

	// Validate: must be all digits
	for _, c := range phone {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("phone number contains non-numeric characters")
		}
	}

	// Validate: must be a valid Kenyan mobile prefix (7xx or 1xx)
	prefix := phone[3:4]
	if prefix != "7" && prefix != "1" {
		return "", fmt.Errorf("invalid Kenyan mobile prefix: must start with 2547 or 2541, got 254%s", prefix)
	}

	// Return WITHOUT + prefix (helps avoid STK PIN dialog freezes on some phones)
	return phone, nil
}

// sanitizeAndValidatePhone converts phone number to E.164 format (+254xxxxxxxxx)
// and validates it's a valid Kenyan mobile number.
// Note: For STK push, use sanitizeAndValidatePhoneWithoutPlus instead.
func sanitizeAndValidatePhone(phone string) (string, error) {
	phone, err := sanitizeAndValidatePhoneWithoutPlus(phone)
	if err != nil {
		return "", err
	}
	// Return in E.164 format WITH + prefix
	return "+" + phone, nil
}
