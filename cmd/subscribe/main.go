package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/config"
)

// tokenResponse is the OAuth client_credentials token response
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds
	TokenType   string `json:"token_type"`
}

// subscriptionRequest represents the webhook subscription request
type subscriptionRequest struct {
	EventType      string `json:"event_type"`
	URL            string `json:"url"`
	Scope          string `json:"scope"`
	ScopeReference string `json:"scope_reference"`
}

// subscriptionResponse represents the webhook subscription response
type subscriptionResponse struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	URL       string `json:"url"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Println("===========================================")
	fmt.Println("Kopo Kopo Webhook Subscription Tool")
	fmt.Println("===========================================")
	fmt.Printf("Base URL: %s\n", cfg.KopoKopoBaseURL)
	fmt.Printf("Callback URL: %s\n", cfg.KopoKopoCallbackURL)
	fmt.Printf("Till Number: %s\n", cfg.KopoKopoTillNumber)
	fmt.Println()

	// Step 1: Get OAuth token
	fmt.Println("Step 1: Fetching OAuth token...")
	token, err := getOAuthToken(cfg)
	if err != nil {
		log.Fatalf("Failed to get OAuth token: %v", err)
	}
	fmt.Println("✓ OAuth token obtained successfully")
	fmt.Println()

	// Step 2: Subscribe to webhook
	fmt.Println("Step 2: Subscribing to buygoods_transaction_received webhook...")
	subscription, err := subscribeWebhook(cfg, token)
	if err != nil {
		log.Fatalf("Failed to subscribe to webhook: %v", err)
	}

	fmt.Println("✓ Webhook subscription created successfully!")
	fmt.Println()
	fmt.Println("Subscription Details:")
	fmt.Printf("  ID: %s\n", subscription.ID)
	fmt.Printf("  Event Type: %s\n", subscription.EventType)
	fmt.Printf("  URL: %s\n", subscription.URL)
	fmt.Printf("  Scope: %s\n", subscription.Scope)
	fmt.Printf("  Created: %s\n", subscription.CreatedAt)
	fmt.Println()
	fmt.Println("===========================================")
	fmt.Println("✅ Webhook subscription is now active!")
	fmt.Println("Kopo Kopo will send payment notifications to:")
	fmt.Printf("   %s\n", cfg.KopoKopoCallbackURL)
	fmt.Println("===========================================")
}

func getOAuthToken(cfg *config.Config) (string, error) {
	authURL := strings.TrimSuffix(cfg.KopoKopoBaseURL, "/") + "/oauth/token"
	
	form := url.Values{}
	form.Set("client_id", cfg.KopoKopoClientID)
	form.Set("client_secret", cfg.KopoKopoClientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(context.Background(), "POST", authURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "mayas-pharm/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth token error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	return tr.AccessToken, nil
}

func subscribeWebhook(cfg *config.Config, token string) (*subscriptionResponse, error) {
	subscribeURL := strings.TrimSuffix(cfg.KopoKopoBaseURL, "/") + "/api/v1/webhook_subscriptions"

	reqBody := subscriptionRequest{
		EventType:      "buygoods_transaction_received",
		URL:            cfg.KopoKopoCallbackURL,
		Scope:          "till",
		ScopeReference: cfg.KopoKopoTillNumber,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal subscription request: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", subscribeURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create subscription request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("User-Agent", "mayas-pharm/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscription request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read subscription response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscription error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Kopo Kopo may return Location header instead of body
	if len(body) == 0 {
		location := resp.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("empty response and no Location header")
		}
		// Return minimal response with location
		return &subscriptionResponse{
			ID:        extractIDFromLocation(location),
			EventType: reqBody.EventType,
			URL:       reqBody.URL,
			Scope:     reqBody.Scope,
			CreatedAt: time.Now().Format(time.RFC3339),
		}, nil
	}

	var subResp subscriptionResponse
	if err := json.Unmarshal(body, &subResp); err != nil {
		return nil, fmt.Errorf("parse subscription response: %w", err)
	}

	return &subResp, nil
}

func extractIDFromLocation(location string) string {
	parts := strings.Split(location, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return location
}
