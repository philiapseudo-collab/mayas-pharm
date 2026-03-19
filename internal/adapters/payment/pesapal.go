package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/config"
)

const (
	pesapalSandboxBaseURL    = "https://cybqa.pesapal.com/pesapalv3"
	pesapalProductionBaseURL = "https://pay.pesapal.com/v3"
)

type PesapalClient struct {
	baseURL          string
	clientID         string
	clientSecret     string
	ipnURL           string
	ipnType          string
	notificationID   string
	defaultReturnURL string
	httpClient       *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

type PesapalInitiateInput struct {
	OrderID         string
	Amount          float64
	Description     string
	CallbackURL     string
	CancellationURL string
	Phone           string
	Email           string
}

type PesapalInitiateResult struct {
	RedirectURL       string `json:"redirect_url"`
	OrderTrackingID   string `json:"order_tracking_id"`
	MerchantReference string `json:"merchant_reference"`
}

type PesapalTransactionStatus struct {
	StatusCode               int
	PaymentStatusDescription string
	OrderTrackingID          string
	MerchantReference        string
	PaymentMethod            string
	ConfirmationCode         string
	Amount                   float64
}

func NewPesapalClient() (*PesapalClient, error) {
	cfg := config.Get()
	clientID := strings.TrimSpace(cfg.PesapalClientID)
	clientSecret := strings.TrimSpace(cfg.PesapalClientSecret)
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("pesapal credentials are not configured")
	}

	return &PesapalClient{
		baseURL:          pesapalBaseURL(cfg.PesapalEnvironment),
		clientID:         clientID,
		clientSecret:     clientSecret,
		ipnURL:           strings.TrimSpace(cfg.PesapalIPNURL),
		ipnType:          strings.ToUpper(strings.TrimSpace(cfg.PesapalIPNType)),
		notificationID:   strings.TrimSpace(cfg.PesapalNotificationID),
		defaultReturnURL: strings.TrimSpace(cfg.PaymentReturnURL),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func pesapalBaseURL(environment string) string {
	if strings.EqualFold(strings.TrimSpace(environment), "production") {
		return pesapalProductionBaseURL
	}
	return pesapalSandboxBaseURL
}

func (c *PesapalClient) InitiatePayment(ctx context.Context, input PesapalInitiateInput) (*PesapalInitiateResult, error) {
	if strings.TrimSpace(input.OrderID) == "" {
		return nil, fmt.Errorf("order ID is required")
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}

	notificationID, err := c.ensureNotificationID(ctx)
	if err != nil {
		return nil, err
	}

	callbackURL := strings.TrimSpace(input.CallbackURL)
	if callbackURL == "" {
		base := strings.TrimSuffix(c.defaultReturnURL, "/")
		if base == "" {
			return nil, fmt.Errorf("callback URL is required")
		}
		callbackURL = fmt.Sprintf("%s/order/%s", base, input.OrderID)
	}

	type billingAddress struct {
		EmailAddress string `json:"email_address,omitempty"`
		PhoneNumber  string `json:"phone_number,omitempty"`
		CountryCode  string `json:"country_code,omitempty"`
		FirstName    string `json:"first_name,omitempty"`
		LastName     string `json:"last_name,omitempty"`
	}
	type submitOrderRequest struct {
		ID              string         `json:"id"`
		Currency        string         `json:"currency"`
		Amount          float64        `json:"amount"`
		Description     string         `json:"description"`
		CallbackURL     string         `json:"callback_url"`
		CancellationURL string         `json:"cancellation_url,omitempty"`
		NotificationID  string         `json:"notification_id"`
		BillingAddress  billingAddress `json:"billing_address"`
	}
	type submitOrderResponse struct {
		OrderTrackingID   string `json:"order_tracking_id"`
		MerchantReference string `json:"merchant_reference"`
		RedirectURL       string `json:"redirect_url"`
		Error             struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	body := submitOrderRequest{
		ID:              input.OrderID,
		Currency:        "KES",
		Amount:          input.Amount,
		Description:     strings.TrimSpace(input.Description),
		CallbackURL:     callbackURL,
		CancellationURL: strings.TrimSpace(input.CancellationURL),
		NotificationID:  notificationID,
		BillingAddress: billingAddress{
			EmailAddress: strings.TrimSpace(input.Email),
			PhoneNumber:  strings.TrimSpace(input.Phone),
			CountryCode:  "KE",
			FirstName:    "Guest",
			LastName:     "Customer",
		},
	}
	if body.Description == "" {
		body.Description = "Maya's Pharm order payment"
	}

	var resp submitOrderResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/Transactions/SubmitOrderRequest", body, &resp); err != nil {
		return nil, err
	}

	if strings.TrimSpace(resp.RedirectURL) == "" {
		if resp.Error.Message != "" {
			return nil, fmt.Errorf("pesapal submit order failed: %s", resp.Error.Message)
		}
		return nil, fmt.Errorf("pesapal submit order failed: missing redirect URL")
	}

	return &PesapalInitiateResult{
		RedirectURL:       resp.RedirectURL,
		OrderTrackingID:   resp.OrderTrackingID,
		MerchantReference: resp.MerchantReference,
	}, nil
}

func (c *PesapalClient) GetTransactionStatus(ctx context.Context, orderTrackingID string) (*PesapalTransactionStatus, error) {
	orderTrackingID = strings.TrimSpace(orderTrackingID)
	if orderTrackingID == "" {
		return nil, fmt.Errorf("order tracking ID is required")
	}

	type statusResponse struct {
		PaymentMethod            string      `json:"payment_method"`
		Amount                   interface{} `json:"amount"`
		CreatedDate              string      `json:"created_date"`
		ConfirmationCode         string      `json:"confirmation_code"`
		PaymentStatusDescription string      `json:"payment_status_description"`
		Description              string      `json:"description"`
		Message                  string      `json:"message"`
		PaymentAccount           string      `json:"payment_account"`
		CallBackURL              string      `json:"call_back_url"`
		StatusCode               interface{} `json:"status_code"`
		MerchantReference        string      `json:"merchant_reference"`
		OrderTrackingID          string      `json:"order_tracking_id"`
		Error                    struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	path := "/api/Transactions/GetTransactionStatus?orderTrackingId=" + url.QueryEscape(orderTrackingID)
	var resp statusResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	statusCode, err := intFromAny(resp.StatusCode)
	if err != nil {
		return nil, fmt.Errorf("invalid pesapal status code: %w", err)
	}

	amount, err := floatFromAny(resp.Amount)
	if err != nil {
		amount = 0
	}

	return &PesapalTransactionStatus{
		StatusCode:               statusCode,
		PaymentStatusDescription: strings.TrimSpace(resp.PaymentStatusDescription),
		OrderTrackingID:          strings.TrimSpace(resp.OrderTrackingID),
		MerchantReference:        strings.TrimSpace(resp.MerchantReference),
		PaymentMethod:            strings.TrimSpace(resp.PaymentMethod),
		ConfirmationCode:         strings.TrimSpace(resp.ConfirmationCode),
		Amount:                   amount,
	}, nil
}

func (c *PesapalClient) ensureNotificationID(ctx context.Context) (string, error) {
	c.mu.Lock()
	cached := strings.TrimSpace(c.notificationID)
	c.mu.Unlock()
	if cached != "" {
		return cached, nil
	}

	ipnURL := strings.TrimSpace(c.ipnURL)
	if ipnURL == "" {
		return "", fmt.Errorf("pesapal IPN URL is not configured")
	}
	ipnType := strings.TrimSpace(c.ipnType)
	if ipnType == "" {
		ipnType = "GET"
	}

	type registerIPNRequest struct {
		URL                 string `json:"url"`
		IPNNotificationType string `json:"ipn_notification_type"`
	}
	type registerIPNResponse struct {
		URL    string `json:"url"`
		IPNID  string `json:"ipn_id"`
		Status string `json:"status"`
		Error  struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	var resp registerIPNResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/URLSetup/RegisterIPN", registerIPNRequest{
		URL:                 ipnURL,
		IPNNotificationType: ipnType,
	}, &resp); err != nil {
		return "", err
	}

	if strings.TrimSpace(resp.IPNID) == "" {
		if resp.Error.Message != "" {
			return "", fmt.Errorf("pesapal IPN registration failed: %s", resp.Error.Message)
		}
		return "", fmt.Errorf("pesapal IPN registration failed: missing ipn_id")
	}

	c.mu.Lock()
	c.notificationID = strings.TrimSpace(resp.IPNID)
	c.mu.Unlock()

	return strings.TrimSpace(resp.IPNID), nil
}

func (c *PesapalClient) doJSON(ctx context.Context, method string, path string, body interface{}, out interface{}) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	url := strings.TrimSuffix(c.baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call pesapal: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read pesapal response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pesapal request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if out == nil || len(raw) == 0 {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("failed to parse pesapal response: %w", err)
	}

	return nil
}

func (c *PesapalClient) getAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	type tokenRequest struct {
		ConsumerKey    string `json:"consumer_key"`
		ConsumerSecret string `json:"consumer_secret"`
	}
	type tokenResponse struct {
		Token string `json:"token"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	rawBody, err := json.Marshal(tokenRequest{
		ConsumerKey:    c.clientID,
		ConsumerSecret: c.clientSecret,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal token request: %w", err)
	}

	endpoint := strings.TrimSuffix(c.baseURL, "/") + "/api/Auth/RequestToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request pesapal token: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("pesapal token request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed tokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}
	if strings.TrimSpace(parsed.Token) == "" {
		if parsed.Error.Message != "" {
			return "", fmt.Errorf("pesapal token request failed: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("pesapal token request failed: empty token")
	}

	c.accessToken = strings.TrimSpace(parsed.Token)
	c.tokenExpiry = time.Now().Add(4 * time.Minute)
	return c.accessToken, nil
}

func intFromAny(v interface{}) (int, error) {
	switch t := v.(type) {
	case float64:
		return int(t), nil
	case float32:
		return int(t), nil
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case json.Number:
		n, err := t.Int64()
		return int(n), err
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, err
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported numeric value: %T", v)
	}
}

func floatFromAny(v interface{}) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case json.Number:
		return t.Float64()
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, err
		}
		return f, nil
	default:
		return 0, fmt.Errorf("unsupported numeric value: %T", v)
	}
}
