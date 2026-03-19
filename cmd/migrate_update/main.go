package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Use DATABASE_URL if available (Railway standard), otherwise use DB_URL
	dbURL := cfg.DBURL
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dbURL = databaseURL
		log.Println("Using DATABASE_URL from environment")
	} else {
		log.Println("Using DB_URL from config")
	}

	// Connect to database
	ctx := context.Background()
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
	
	// Get the project root (assuming we're running from project root or adjust path)
	// Try relative path first, then try to find it
	var migrationPath string
	if _, err := os.Stat(migrationFile); err == nil {
		migrationPath = migrationFile
	} else {
		// Try to find migrations directory
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

	// Execute the entire SQL file as one transaction
	// PostgreSQL allows multiple statements in a single Exec call
	log.Println("Executing migration...")
	_, err = dbpool.Exec(ctx, string(sqlContent))
	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	log.Println("✓ Migration completed successfully")
}
