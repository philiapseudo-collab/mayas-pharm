package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// DATABASE_URL is Railway's standard environment variable name

// Config holds all application configuration
type Config struct {
	AppPort        string `envconfig:"APP_PORT" default:"8080"`
	AppEnv         string `envconfig:"APP_ENV" default:"development"`
	AppRuntimeMode string `envconfig:"APP_RUNTIME_MODE" default:"all"`

	// Database
	DBHost                   string `envconfig:"DB_HOST" default:"localhost"`
	DBPort                   string `envconfig:"DB_PORT" default:"5432"`
	DBUser                   string `envconfig:"DB_USER" default:"postgres"`
	DBPassword               string `envconfig:"DB_PASSWORD" default:"postgres"`
	DBName                   string `envconfig:"DB_NAME" default:"mayas_pharm"`
	DBURL                    string `envconfig:"DB_URL"`
	DBMaxOpenConns           int    `envconfig:"DB_MAX_OPEN_CONNS" default:"25"`
	DBMaxIdleConns           int    `envconfig:"DB_MAX_IDLE_CONNS" default:"10"`
	DBConnMaxLifetimeMinutes int    `envconfig:"DB_CONN_MAX_LIFETIME_MINUTES" default:"30"`
	DBConnMaxIdleTimeMinutes int    `envconfig:"DB_CONN_MAX_IDLE_TIME_MINUTES" default:"10"`

	// Workers and operational limits
	PaymentDispatchIntervalMS     int `envconfig:"PAYMENT_DISPATCH_INTERVAL_MS" default:"2100"`
	PaymentDispatchPollIntervalMS int `envconfig:"PAYMENT_DISPATCH_POLL_INTERVAL_MS" default:"250"`
	OrderExpirySweepIntervalMS    int `envconfig:"ORDER_EXPIRY_SWEEP_INTERVAL_MS" default:"5000"`
	WhatsAppWebhookMaxConcurrent  int `envconfig:"WHATSAPP_WEBHOOK_MAX_CONCURRENT" default:"32"`
	OrdersAPIMaxLimit             int `envconfig:"ORDERS_API_MAX_LIMIT" default:"200"`
	AppShutdownTimeoutSeconds     int `envconfig:"APP_SHUTDOWN_TIMEOUT_SECONDS" default:"10"`

	// Redis
	RedisURL      string `envconfig:"REDIS_URL" default:"redis://localhost:6379"`
	RedisPassword string `envconfig:"REDIS_PASSWORD" default:""`

	// WhatsApp
	WhatsAppToken         string `envconfig:"WHATSAPP_TOKEN"`
	WhatsAppPhoneNumberID string `envconfig:"WHATSAPP_PHONE_NUMBER_ID"`
	WhatsAppVerifyToken   string `envconfig:"WHATSAPP_VERIFY_TOKEN"`

	// Operations
	OpsAlertPhone       string `envconfig:"OPS_ALERT_PHONE" default:""` // Internal WhatsApp number for paid-order alerts
	LegacyBarStaffPhone string `envconfig:"BAR_STAFF_PHONE" default:""` // Backward-compatible legacy env support
	BusinessTimezone    string `envconfig:"BUSINESS_TIMEZONE" default:"Africa/Nairobi"`

	// Dashboard
	JWTSecret      string `envconfig:"JWT_SECRET" default:"change-this-secret-in-production"`
	AllowedOrigin  string `envconfig:"ALLOWED_ORIGIN" default:"http://localhost:3000"`
	AllowedOrigins string `envconfig:"ALLOWED_ORIGINS" default:"http://localhost:3000,http://localhost:3001"`

	// Kopo Kopo (use Client ID + Secret for OAuth; or set Access Token for sandbox manual token)
	KopoKopoClientID      string `envconfig:"KOPOKOPO_CLIENT_ID"`
	KopoKopoClientSecret  string `envconfig:"KOPOKOPO_CLIENT_SECRET"`
	KopoKopoWebhookSecret string `envconfig:"KOPOKOPO_WEBHOOK_SECRET"` // Used to verify X-KopoKopo-Signature header
	KopoKopoBaseURL       string `envconfig:"KOPOKOPO_BASE_URL" default:"https://api.kopokopo.com"`
	KopoKopoTillNumber    string `envconfig:"KOPOKOPO_TILL_NUMBER"`
	KopoKopoAccessToken   string `envconfig:"KOPOKOPO_ACCESS_TOKEN"` // Optional: manual token (e.g. sandbox); else we use Client ID/Secret OAuth
	KopoKopoCallbackURL   string `envconfig:"KOPOKOPO_CALLBACK_URL"` // Full callback URL (e.g., https://your-app.railway.app/api/webhooks/payment)

	// Pesapal
	PesapalClientID           string `envconfig:"PESAPAL_CLIENT_ID"`
	PesapalClientSecret       string `envconfig:"PESAPAL_CLIENT_SECRET"`
	PesapalEnvironment        string `envconfig:"PESAPAL_ENVIRONMENT" default:"sandbox"`
	PesapalIPNURL             string `envconfig:"PESAPAL_IPN_URL"`
	PesapalIPNType            string `envconfig:"PESAPAL_IPN_TYPE" default:"GET"`
	PesapalNotificationID     string `envconfig:"PESAPAL_NOTIFICATION_ID"`
	PaymentReturnURL          string `envconfig:"PAYMENT_RETURN_URL" default:"http://localhost:3000/orders/status"`
	LegacyCustomerFrontendURL string `envconfig:"CUSTOMER_FRONTEND_URL" default:""`
}

var instance *Config

// Load initializes and returns the singleton Config instance
func Load() (*Config, error) {
	if instance != nil {
		return instance, nil
	}

	// Load .env file if it exists (for local development)
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			return nil, fmt.Errorf("error loading .env file: %w", err)
		}
	}

	cfg := &Config{}
	if err := envconfig.Process("", cfg); err != nil {
		return nil, fmt.Errorf("error processing environment variables: %w", err)
	}

	if _, exists := os.LookupEnv("APP_PORT"); !exists {
		if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
			cfg.AppPort = port
		}
	}

	// Check for Railway's DATABASE_URL if DB_URL is not set
	if cfg.DBURL == "" {
		if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
			cfg.DBURL = databaseURL
		}
	}

	// Build DBURL if still not provided
	if cfg.DBURL == "" {
		cfg.DBURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)
	}

	if cfg.OpsAlertPhone == "" {
		cfg.OpsAlertPhone = cfg.LegacyBarStaffPhone
	}

	if cfg.PaymentReturnURL == "" {
		cfg.PaymentReturnURL = cfg.LegacyCustomerFrontendURL
	}

	instance = cfg
	return instance, nil
}

// Get returns the singleton Config instance (must call Load first)
func Get() *Config {
	if instance == nil {
		panic("config not loaded: call config.Load() first")
	}
	return instance
}
