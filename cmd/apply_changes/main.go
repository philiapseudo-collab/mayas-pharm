package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"github.com/google/uuid"
	"encoding/json"
	"strings"
)

// MenuItem represents a product in the seed data JSON
type MenuItem struct {
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Category string  `json:"category"`
	Stock    int     `json:"stock"`
}

// MenuData holds the Chasers products to be seeded
var ChasersData = []byte(`[
  { "name": "Ice Cubes (Packet)", "price": 20, "category": "Chasers", "stock": 100 },
  { "name": "Coca-Cola (Soda)", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Fanta Orange (Soda)", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Fanta Blackcurrant (Soda)", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Fanta Passion (Soda)", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Sprite (Soda)", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Krest Bitter Lemon", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Stoney Tangawizi", "price": 150, "category": "Chasers", "stock": 100 },
  { "name": "Schweppes Tonic Water", "price": 200, "category": "Chasers", "stock": 100 },
  { "name": "Power Play (Energy Drink)", "price": 250, "category": "Chasers", "stock": 100 },
  { "name": "Red Bull (Energy Drink)", "price": 300, "category": "Chasers", "stock": 100 },
  { "name": "Water (500ml)", "price": 50, "category": "Chasers", "stock": 100 }
]`)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Use DATABASE_URL if available (Railway standard), otherwise use DB_URL
	dbURL := cfg.DBURL
	
	// When running locally with Railway CLI, use DATABASE_PUBLIC_URL (external URL)
	// Railway's DATABASE_URL uses internal hostname (postgres.railway.internal) 
	// which only works inside Railway's network
	if publicURL := os.Getenv("DATABASE_PUBLIC_URL"); publicURL != "" {
		dbURL = publicURL
		log.Println("Using DATABASE_PUBLIC_URL (external) for local execution")
	} else if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dbURL = databaseURL
		log.Println("Using DATABASE_URL from environment")
		// Check if it's an internal URL (won't work locally)
		if strings.Contains(dbURL, "postgres.railway.internal") {
			log.Println("WARNING: DATABASE_URL uses internal hostname")
			log.Println("This may fail when running locally. Use DATABASE_PUBLIC_URL instead.")
		}
	} else {
		log.Println("Using DB_URL from config")
	}

	ctx := context.Background()

	// Step 1: Run Migration (using pgxpool for raw SQL)
	log.Println("=" + strings.Repeat("=", 60))
	log.Println("STEP 1: Running Migration (Archive Cognac products)")
	log.Println("=" + strings.Repeat("=", 60))

	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbpool.Close()

	// Verify connection
	if err := dbpool.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	log.Println("✓ Database connection established")

	// Read migration file
	migrationFile := "migrations/002_replace_cognac_with_chasers.sql"
	var migrationPath string
	if _, err := os.Stat(migrationFile); err == nil {
		migrationPath = migrationFile
	} else {
		wd, _ := os.Getwd()
		possiblePaths := []string{
			filepath.Join(wd, migrationFile),
			filepath.Join(wd, "..", migrationFile),
			filepath.Join(wd, "..", "..", migrationFile),
		}
		found := false
		for _, path := range possiblePaths {
			if _, err := os.Stat(path); err == nil {
				migrationPath = path
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("Migration file not found: %s (tried: %v)", migrationFile, possiblePaths)
		}
	}

	log.Printf("Reading migration file: %s", migrationPath)
	sqlContent, err := ioutil.ReadFile(migrationPath)
	if err != nil {
		log.Fatalf("Failed to read migration file: %v", err)
	}

	// Execute migration
	log.Println("Executing migration...")
	_, err = dbpool.Exec(ctx, string(sqlContent))
	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	log.Println("✓ Migration completed successfully")
	dbpool.Close()

	// Step 2: Run Seeder (using GORM for product insertion)
	log.Println("")
	log.Println("=" + strings.Repeat("=", 60))
	log.Println("STEP 2: Running Seeder (Add Chasers products)")
	log.Println("=" + strings.Repeat("=", 60))

	// Connect using GORM
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Parse Chasers data
	var menuItems []MenuItem
	if err := json.Unmarshal(ChasersData, &menuItems); err != nil {
		log.Fatalf("Failed to parse Chasers data: %v", err)
	}

	if len(menuItems) == 0 {
		log.Println("Chasers data is empty. No products to seed.")
		return
	}

	inserted := 0
	updated := 0

	// Upsert Chasers products
	for _, item := range menuItems {
		productID := uuid.New().String()

		// Check if product with this name already exists
		var existingID string
		result := db.WithContext(ctx).Table("products").
			Select("id").
			Where("name = ?", item.Name).
			Limit(1).
			Scan(&existingID)

		if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
			log.Fatalf("Failed to check existing product %s: %v", item.Name, result.Error)
		}

		if existingID != "" {
			// Update existing product
			if err := db.WithContext(ctx).Table("products").
				Where("id = ?", existingID).
				Updates(map[string]interface{}{
					"price":          item.Price,
					"stock_quantity": item.Stock,
					"category":       item.Category,
					"is_active":      true,
					"updated_at":     gorm.Expr("CURRENT_TIMESTAMP"),
				}).Error; err != nil {
				log.Fatalf("Failed to update product %s: %v", item.Name, err)
			}
			updated++
			log.Printf("  Updated: %s", item.Name)
		} else {
			// Insert new product
			productMap := map[string]interface{}{
				"id":             productID,
				"name":           item.Name,
				"description":    "",
				"price":          item.Price,
				"category":       item.Category,
				"stock_quantity": item.Stock,
				"image_url":      "",
				"is_active":      true,
			}
			if err := db.WithContext(ctx).Table("products").Create(productMap).Error; err != nil {
				log.Fatalf("Failed to insert product %s: %v", item.Name, err)
			}
			inserted++
			log.Printf("  Inserted: %s", item.Name)
		}
	}

	log.Println("")
	log.Println("=" + strings.Repeat("=", 60))
	log.Printf("✓ Seeder completed: %d products processed (%d inserted, %d updated)", 
		len(menuItems), inserted, updated)
	log.Println("=" + strings.Repeat("=", 60))
	log.Println("")
	log.Println("✅ All changes applied successfully!")
	log.Println("   - Cognac products have been archived")
	log.Println("   - Chasers products have been added")
}
