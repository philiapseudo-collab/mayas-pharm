package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/core"
)

// Client handles WhatsApp Cloud API communication
type Client struct {
	baseURL       string
	phoneNumberID string
	token         string
	httpClient    *http.Client
}

// NewClient creates a new WhatsApp client
func NewClient(phoneNumberID, token string) *Client {
	if phoneNumberID == "" {
		panic("WHATSAPP_PHONE_NUMBER_ID is required but not set")
	}
	if token == "" {
		panic("WHATSAPP_TOKEN is required but not set")
	}

	return &Client{
		baseURL:       "https://graph.facebook.com/v19.0",
		phoneNumberID: phoneNumberID,
		token:         token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendMessage sends a generic message payload to WhatsApp
func (c *Client) SendMessage(ctx context.Context, to string, payload interface{}) error {
	url := fmt.Sprintf("%s/%s/messages", c.baseURL, c.phoneNumberID)

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	// Log request details (masked for security)
	fmt.Printf("WhatsApp API Request: POST %s (to: %s, phone_id: %s)\n",
		url, to, c.phoneNumberID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whatsapp API error: status %d, url: %s, phone_number_id: %s, body: %s",
			resp.StatusCode, url, c.phoneNumberID, string(body))
	}

	return nil
}

// maskToken masks a token for logging (shows first 3 and last 3 chars)
func maskToken(token string) string {
	if token == "" {
		return "<empty>"
	}
	if len(token) <= 6 {
		return "***"
	}
	return token[:3] + "***" + token[len(token)-3:]
}

// truncateTitle truncates a title to WhatsApp's max length of 24 characters
func truncateTitle(title string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 24 // WhatsApp's default max length for row titles
	}
	if len(title) <= maxLen {
		return title
	}
	// Truncate and add ellipsis if needed
	if maxLen > 3 {
		return title[:maxLen-3] + "..."
	}
	return title[:maxLen]
}

// SendText sends a simple text message
func (c *Client) SendText(ctx context.Context, phone string, message string) error {
	payload := TextMessage{
		MessagingProduct: "whatsapp",
		To:               phone,
		Type:             "text",
	}
	payload.Text.Body = message

	return c.SendMessage(ctx, phone, payload)
}

// SendMenuButtons sends an interactive button message (for quick replies)
func (c *Client) SendMenuButtons(ctx context.Context, phone string, text string, buttons []core.Button) error {
	payload := InteractiveButtonMessage{
		MessagingProduct: "whatsapp",
		To:               phone,
		Type:             "interactive",
	}
	payload.Interactive.Type = "button"
	payload.Interactive.Body.Text = text

	// WhatsApp allows max 3 buttons
	maxButtons := 3
	if len(buttons) > maxButtons {
		buttons = buttons[:maxButtons]
	}

	payload.Interactive.Action.Buttons = make([]struct {
		Type  string `json:"type"`
		Reply struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"reply"`
	}, len(buttons))

	for i, btn := range buttons {
		payload.Interactive.Action.Buttons[i].Type = "reply"
		payload.Interactive.Action.Buttons[i].Reply.ID = btn.ID
		payload.Interactive.Action.Buttons[i].Reply.Title = btn.Title
	}

	return c.SendMessage(ctx, phone, payload)
}

// sendInteractiveList sends an interactive list message (internal helper)
func (c *Client) sendInteractiveList(ctx context.Context, phone string, text string, buttonText string, items []struct {
	ID          string
	Title       string
	Description string
}) error {
	payload := InteractiveListMessage{
		MessagingProduct: "whatsapp",
		To:               phone,
		Type:             "interactive",
	}
	payload.Interactive.Type = "list"
	payload.Interactive.Body.Text = text
	payload.Interactive.Action.Button = buttonText

	// WhatsApp allows max 10 rows per list
	maxItems := 10
	if len(items) > maxItems {
		items = items[:maxItems]
	}

	// Create a single section with all items
	payload.Interactive.Action.Sections = []struct {
		Title string `json:"title,omitempty"`
		Rows  []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description,omitempty"`
		} `json:"rows"`
	}{
		{
			Rows: make([]struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				Description string `json:"description,omitempty"`
			}, len(items)),
		},
	}

	for i, item := range items {
		payload.Interactive.Action.Sections[0].Rows[i].ID = item.ID
		payload.Interactive.Action.Sections[0].Rows[i].Title = item.Title
		payload.Interactive.Action.Sections[0].Rows[i].Description = item.Description
	}

	return c.SendMessage(ctx, phone, payload)
}

// SendMenu sends a menu of products (implements WhatsAppGateway interface)
func (c *Client) SendMenu(ctx context.Context, phone string, products []*core.Product) error {
	// Send as a list
	items := make([]struct {
		ID          string
		Title       string
		Description string
	}, len(products))

	for i, p := range products {
		items[i].ID = p.ID
		// Format title and truncate to 24 chars (WhatsApp limit)
		fullTitle := fmt.Sprintf("%s - KES %.0f", p.Name, p.Price)
		items[i].Title = truncateTitle(fullTitle, 24)
		items[i].Description = p.Description
	}

	return c.sendInteractiveList(ctx, phone, "Select a product:", "View Products", items)
}

// SendCategoryList sends a list of categories (implements WhatsAppGateway interface)
func (c *Client) SendCategoryList(ctx context.Context, phone string, categories []string) error {
	return c.SendCategoryListWithText(ctx, phone, "Select a category to browse:", categories)
}

// SendCategoryListWithText sends a list of categories with a custom prompt.
func (c *Client) SendCategoryListWithText(ctx context.Context, phone string, text string, categories []string) error {
	items := make([]struct {
		ID          string
		Title       string
		Description string
	}, len(categories))

	for i, cat := range categories {
		items[i].ID = cat
		// Truncate category name to 24 chars (WhatsApp limit)
		items[i].Title = truncateTitle(cat, 24)
	}

	return c.sendInteractiveList(ctx, phone, text, "View Menu", items)
}

// SendProductList sends a list of products (implements WhatsAppGateway interface)
func (c *Client) SendProductList(ctx context.Context, phone string, category string, products []*core.Product) error {
	items := make([]struct {
		ID          string
		Title       string
		Description string
	}, len(products))

	for i, p := range products {
		items[i].ID = p.ID
		// Format title and truncate to 24 chars (WhatsApp limit)
		fullTitle := fmt.Sprintf("%s - KES %.0f", p.Name, p.Price)
		items[i].Title = truncateTitle(fullTitle, 24)
		if p.Description != "" {
			items[i].Description = p.Description
		}
	}

	text := fmt.Sprintf("Products in *%s*:", category)
	return c.sendInteractiveList(ctx, phone, text, "View Products", items)
}
