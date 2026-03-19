package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// Check if migration file is provided
	if len(os.Args) < 2 {
		log.Fatal("Usage: go run cmd/run_migration/main.go <migration_file>")
	}

	migrationFile := os.Args[1]

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Use DATABASE_PUBLIC_URL for local runs (Railway CLI), DATABASE_URL otherwise
	dbURL := cfg.DBURL
	if publicURL := os.Getenv("DATABASE_PUBLIC_URL"); publicURL != "" {
		dbURL = publicURL
		log.Println("Using DATABASE_PUBLIC_URL (external) for local execution")
	} else if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dbURL = databaseURL
		log.Println("Using DATABASE_URL from environment")
		if strings.Contains(dbURL, "postgres.railway.internal") {
			log.Println("WARNING: DATABASE_URL uses internal hostname - use DATABASE_PUBLIC_URL for local runs")
		}
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

	// Find migration file
	var migrationPath string
	if _, err := os.Stat(migrationFile); err == nil {
		migrationPath = migrationFile
	} else {
		// Try to find it relative to project root
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

	// Execute the migration
	log.Println("Executing migration...")
	_, err = dbpool.Exec(ctx, string(sqlContent))
	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	log.Println("✓ Migration completed successfully")
}
