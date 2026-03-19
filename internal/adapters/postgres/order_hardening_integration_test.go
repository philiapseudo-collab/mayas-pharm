package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/core"
)

func TestCreatePendingOrderConcurrentReservations(t *testing.T) {
	repo, sqlDB, cleanup := setupIntegrationRepository(t)
	defer cleanup()

	ctx := context.Background()
	productID := "00000000-0000-0000-0000-000000000101"
	userID := "00000000-0000-0000-0000-000000000201"

	seedIntegrationUser(t, sqlDB, userID, "254700000111")
	seedIntegrationProduct(t, sqlDB, productID, "Stress SKU", 100, 250)

	var (
		successes int32
		failures  int32
		wg        sync.WaitGroup
	)

	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			_, _, err := repo.OrderRepository().CreatePendingOrder(ctx, core.CreatePendingOrderInput{
				UserID:         userID,
				CustomerPhone:  "254700000111",
				TableNumber:    "PICKUP",
				PaymentMethod:  string(core.PaymentMethodMpesa),
				IdempotencyKey: fmt.Sprintf("order-%03d", index),
				ExpiresAt:      time.Now().Add(20 * time.Minute),
				Items: []core.PendingOrderItemInput{
					{ProductID: productID, Quantity: 1},
				},
			})
			if err == nil {
				atomic.AddInt32(&successes, 1)
				return
			}
			if strings.Contains(err.Error(), "insufficient stock") {
				atomic.AddInt32(&failures, 1)
				return
			}
			t.Errorf("unexpected CreatePendingOrder error: %v", err)
		}(i)
	}
	wg.Wait()

	if got := successes; got != 100 {
		t.Fatalf("successful reservations = %d, want 100", got)
	}
	if got := failures; got != 200 {
		t.Fatalf("failed reservations = %d, want 200", got)
	}

	product, err := repo.ProductRepository().GetByID(ctx, productID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if product.StockQuantity != 0 {
		t.Fatalf("stock quantity = %d, want 0", product.StockQuantity)
	}

	var orderCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders").Scan(&orderCount); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orderCount != 100 {
		t.Fatalf("stored orders = %d, want 100", orderCount)
	}
}

func TestPendingFailureReleasesStockAndReopenReservesAgain(t *testing.T) {
	repo, sqlDB, cleanup := setupIntegrationRepository(t)
	defer cleanup()

	ctx := context.Background()
	productID := "00000000-0000-0000-0000-000000000102"
	userID := "00000000-0000-0000-0000-000000000202"

	seedIntegrationUser(t, sqlDB, userID, "254700000112")
	seedIntegrationProduct(t, sqlDB, productID, "Retry SKU", 5, 400)

	order, _, err := repo.OrderRepository().CreatePendingOrder(ctx, core.CreatePendingOrderInput{
		UserID:         userID,
		CustomerPhone:  "254700000112",
		TableNumber:    "PICKUP",
		PaymentMethod:  string(core.PaymentMethodMpesa),
		IdempotencyKey: "retry-order",
		ExpiresAt:      time.Now().Add(20 * time.Minute),
		Items: []core.PendingOrderItemInput{
			{ProductID: productID, Quantity: 2},
		},
	})
	if err != nil {
		t.Fatalf("CreatePendingOrder() error = %v", err)
	}

	assertIntegrationStock(t, repo, productID, 3)

	if err := repo.OrderRepository().UpdateStatus(ctx, order.ID, core.OrderStatusFailed); err != nil {
		t.Fatalf("UpdateStatus(FAILED) error = %v", err)
	}
	assertIntegrationStock(t, repo, productID, 5)

	if err := repo.OrderRepository().UpdateStatus(ctx, order.ID, core.OrderStatusFailed); err != nil {
		t.Fatalf("second UpdateStatus(FAILED) error = %v", err)
	}
	assertIntegrationStock(t, repo, productID, 5)

	if err := repo.OrderRepository().UpdateStatus(ctx, order.ID, core.OrderStatusPending); err != nil {
		t.Fatalf("UpdateStatus(PENDING) error = %v", err)
	}
	assertIntegrationStock(t, repo, productID, 3)
}

func TestCreatePendingOrderConcurrentMixedCartReservations(t *testing.T) {
	repo, sqlDB, cleanup := setupIntegrationRepository(t)
	defer cleanup()

	ctx := context.Background()
	userID := "00000000-0000-0000-0000-000000000203"
	productA := "00000000-0000-0000-0000-000000000103"
	productB := "00000000-0000-0000-0000-000000000104"

	seedIntegrationUser(t, sqlDB, userID, "254700000113")
	seedIntegrationProduct(t, sqlDB, productA, "Mixed A", 80, 300)
	seedIntegrationProduct(t, sqlDB, productB, "Mixed B", 80, 450)

	var (
		successes int32
		failures  int32
		wg        sync.WaitGroup
	)

	for i := 0; i < 120; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			items := []core.PendingOrderItemInput{
				{ProductID: productA, Quantity: 1},
				{ProductID: productB, Quantity: 1},
			}
			if index%2 == 1 {
				items[0], items[1] = items[1], items[0]
			}

			_, _, err := repo.OrderRepository().CreatePendingOrder(ctx, core.CreatePendingOrderInput{
				UserID:         userID,
				CustomerPhone:  "254700000113",
				TableNumber:    "PICKUP",
				PaymentMethod:  string(core.PaymentMethodMpesa),
				IdempotencyKey: fmt.Sprintf("mixed-%03d", index),
				ExpiresAt:      time.Now().Add(20 * time.Minute),
				Items:          items,
			})
			if err == nil {
				atomic.AddInt32(&successes, 1)
				return
			}
			if strings.Contains(err.Error(), "insufficient stock") {
				atomic.AddInt32(&failures, 1)
				return
			}
			t.Errorf("unexpected CreatePendingOrder error: %v", err)
		}(i)
	}
	wg.Wait()

	if got := successes; got != 80 {
		t.Fatalf("successful mixed-cart reservations = %d, want 80", got)
	}
	if got := failures; got != 40 {
		t.Fatalf("failed mixed-cart reservations = %d, want 40", got)
	}

	assertIntegrationStock(t, repo, productA, 0)
	assertIntegrationStock(t, repo, productB, 0)
}

func setupIntegrationRepository(t *testing.T) (*Repository, *sql.DB, func()) {
	t.Helper()

	baseURL := strings.Trim(strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")), `"'`)
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "" && host != "localhost" && host != "127.0.0.1" && os.Getenv("TEST_DATABASE_URL_ALLOW_REMOTE") != "1" {
		t.Skip("TEST_DATABASE_URL points to a non-local database; set TEST_DATABASE_URL_ALLOW_REMOTE=1 to override")
	}

	schemaName := fmt.Sprintf("it_%d", time.Now().UnixNano())

	adminRepo, err := NewRepository(baseURL)
	if err != nil {
		t.Fatalf("NewRepository(admin) error = %v", err)
	}
	adminSQL, err := adminRepo.SQLDB()
	if err != nil {
		t.Fatalf("SQLDB(admin) error = %v", err)
	}
	adminSQL.SetMaxOpenConns(4)
	adminSQL.SetMaxIdleConns(2)
	if _, err := adminSQL.ExecContext(context.Background(), fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	schemaURL := withSearchPath(t, baseURL, schemaName)
	repo, err := NewRepository(schemaURL)
	if err != nil {
		t.Fatalf("NewRepository(schema) error = %v", err)
	}
	sqlDB, err := repo.SQLDB()
	if err != nil {
		t.Fatalf("SQLDB(schema) error = %v", err)
	}
	sqlDB.SetMaxOpenConns(24)
	sqlDB.SetMaxIdleConns(12)

	applyAllMigrations(t, sqlDB)

	cleanup := func() {
		_, _ = adminSQL.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
		_ = sqlDB.Close()
		_ = adminSQL.Close()
	}

	return repo, sqlDB, cleanup
}

func withSearchPath(t *testing.T, databaseURL string, schemaName string) string {
	t.Helper()

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName+",public")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func applyAllMigrations(t *testing.T, db *sql.DB) {
	t.Helper()

	root := projectRoot(t)
	migrationsDir := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("ReadDir(migrations): %v", err)
	}

	var migrationFiles []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		migrationFiles = append(migrationFiles, filepath.Join(migrationsDir, entry.Name()))
	}
	sort.Strings(migrationFiles)

	for _, migrationPath := range migrationFiles {
		sqlBytes, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", migrationPath, err)
		}
		if _, err := db.ExecContext(context.Background(), string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", filepath.Base(migrationPath), err)
		}
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("failed to resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func seedIntegrationUser(t *testing.T, db *sql.DB, userID string, phone string) {
	t.Helper()

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO users (id, phone_number, name)
		VALUES ($1, $2, $3)
	`, userID, phone, "Integration User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func seedIntegrationProduct(t *testing.T, db *sql.DB, productID string, name string, stock int, price float64) {
	t.Helper()

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO products (id, name, description, price, category, stock_quantity, image_url, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, productID, name, "integration", price, "Cocktails", stock, "", true); err != nil {
		t.Fatalf("insert product: %v", err)
	}
}

func assertIntegrationStock(t *testing.T, repo *Repository, productID string, want int) {
	t.Helper()

	product, err := repo.ProductRepository().GetByID(context.Background(), productID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if product.StockQuantity != want {
		t.Fatalf("stock quantity = %d, want %d", product.StockQuantity, want)
	}
}
