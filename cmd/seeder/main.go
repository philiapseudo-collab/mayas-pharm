package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = cfg.DBURL
	}

	if !shouldSeed(databaseURL, cfg.DBHost) {
		log.Println("Seeder skipped: set ALLOW_SEED=true or point DB_URL/DATABASE_URL to a non-localhost database.")
		log.Printf("DB_URL preview: %s", maskURL(databaseURL))
		return
	}

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	ctx := context.Background()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		categoriesBySlug, err := upsertCategories(ctx, tx, seedCategories())
		if err != nil {
			return err
		}
		if err := upsertProducts(ctx, tx, categoriesBySlug, seedProducts()); err != nil {
			return err
		}
		if err := upsertDeliveryZones(ctx, tx, seedDeliveryZones()); err != nil {
			return err
		}
		if err := upsertBusinessHours(ctx, tx, seedBusinessHours()); err != nil {
			return err
		}
		return nil
	}); err != nil {
		log.Fatalf("failed to seed data: %v", err)
	}

	log.Printf(
		"Seeder completed: %d categories, %d products, %d delivery zones, %d business-hours rows",
		len(seedCategories()),
		len(seedProducts()),
		len(seedDeliveryZones()),
		len(seedBusinessHours()),
	)
}

func shouldSeed(databaseURL string, dbHost string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_SEED")), "true") {
		return true
	}

	lowerURL := strings.ToLower(strings.TrimSpace(databaseURL))
	lowerHost := strings.ToLower(strings.TrimSpace(dbHost))

	if strings.Contains(lowerURL, "railway") ||
		strings.Contains(lowerURL, ".railway.internal") ||
		strings.Contains(lowerURL, ".proxy.rlwy.net") {
		return true
	}

	if lowerURL != "" &&
		!strings.Contains(lowerURL, "localhost") &&
		!strings.Contains(lowerURL, "127.0.0.1") {
		return true
	}

	return lowerHost != "" &&
		lowerHost != "localhost" &&
		lowerHost != "127.0.0.1"
}

func maskURL(url string) string {
	if strings.TrimSpace(url) == "" {
		return "<empty>"
	}
	if len(url) < 20 {
		return "<hidden>"
	}
	if len(url) < 50 {
		return url[:20] + "..." + url[len(url)-10:]
	}
	return url[:20] + "..." + url[len(url)-10:]
}

func upsertCategories(ctx context.Context, tx *gorm.DB, categories []seedCategory) (map[string]categoryRecord, error) {
	result := make(map[string]categoryRecord, len(categories))
	for _, category := range categories {
		slug := strings.TrimSpace(category.Slug)
		if slug == "" {
			return nil, fmt.Errorf("category slug is required for %q", category.Name)
		}

		values := map[string]any{
			"name":        strings.TrimSpace(category.Name),
			"slug":        slug,
			"description": strings.TrimSpace(category.Description),
			"sort_order":  category.SortOrder,
			"is_active":   true,
			"updated_at":  gorm.Expr("CURRENT_TIMESTAMP"),
		}

		var existingID string
		err := tx.WithContext(ctx).
			Table("categories").
			Select("id").
			Where("slug = ?", slug).
			Limit(1).
			Scan(&existingID).Error
		if err != nil {
			return nil, fmt.Errorf("failed to check category %s: %w", slug, err)
		}

		if strings.TrimSpace(existingID) == "" {
			existingID = newID()
			values["id"] = existingID
			values["created_at"] = gorm.Expr("CURRENT_TIMESTAMP")
			if err := tx.WithContext(ctx).Table("categories").Create(values).Error; err != nil {
				return nil, fmt.Errorf("failed to insert category %s: %w", slug, err)
			}
		} else {
			if err := tx.WithContext(ctx).Table("categories").Where("id = ?", existingID).Updates(values).Error; err != nil {
				return nil, fmt.Errorf("failed to update category %s: %w", slug, err)
			}
		}

		result[slug] = categoryRecord{ID: existingID, Name: strings.TrimSpace(category.Name), Slug: slug}
	}
	return result, nil
}

func upsertProducts(ctx context.Context, tx *gorm.DB, categoriesBySlug map[string]categoryRecord, products []seedProduct) error {
	for _, product := range products {
		category, ok := categoriesBySlug[product.CategorySlug]
		if !ok {
			return fmt.Errorf("category %q not found for product %q", product.CategorySlug, product.Name)
		}

		sku := buildSKU(product.CategorySlug, product.Name)
		values := map[string]any{
			"name":                  strings.TrimSpace(product.Name),
			"description":           strings.TrimSpace(product.Description),
			"price":                 product.Price,
			"category":              category.Name,
			"category_id":           category.ID,
			"sku":                   sku,
			"brand_name":            emptyToNil(product.BrandName),
			"generic_name":          emptyToNil(product.GenericName),
			"strength":              emptyToNil(product.Strength),
			"dosage_form":           emptyToNil(product.DosageForm),
			"pack_size":             emptyToNil(product.PackSize),
			"unit":                  emptyToNil(product.Unit),
			"active_ingredient":     emptyToNil(product.ActiveIngredient),
			"stock_quantity":        product.Stock,
			"image_url":             nil,
			"is_active":             true,
			"requires_prescription": product.RequiresPrescription,
			"is_controlled":         false,
			"price_source":          emptyToNil(product.PriceSource),
			"updated_at":            gorm.Expr("CURRENT_TIMESTAMP"),
		}

		var existingID string
		err := tx.WithContext(ctx).
			Table("products").
			Select("id").
			Where("sku = ? OR (name = ? AND category = ?)", sku, strings.TrimSpace(product.Name), category.Name).
			Limit(1).
			Scan(&existingID).Error
		if err != nil {
			return fmt.Errorf("failed to check product %s: %w", product.Name, err)
		}

		if strings.TrimSpace(existingID) == "" {
			existingID = newID()
			values["id"] = existingID
			values["created_at"] = gorm.Expr("CURRENT_TIMESTAMP")
			if err := tx.WithContext(ctx).Table("products").Create(values).Error; err != nil {
				return fmt.Errorf("failed to insert product %s: %w", product.Name, err)
			}
			continue
		}

		if err := tx.WithContext(ctx).Table("products").Where("id = ?", existingID).Updates(values).Error; err != nil {
			return fmt.Errorf("failed to update product %s: %w", product.Name, err)
		}
	}
	return nil
}

func upsertDeliveryZones(ctx context.Context, tx *gorm.DB, zones []seedDeliveryZone) error {
	for _, zone := range zones {
		values := map[string]any{
			"name":           zone.Name,
			"slug":           zone.Slug,
			"fee":            zone.Fee,
			"estimated_mins": zone.EstimatedMins,
			"sort_order":     zone.SortOrder,
			"is_active":      true,
			"updated_at":     gorm.Expr("CURRENT_TIMESTAMP"),
		}

		var existingID string
		err := tx.WithContext(ctx).
			Table("delivery_zones").
			Select("id").
			Where("slug = ?", zone.Slug).
			Limit(1).
			Scan(&existingID).Error
		if err != nil {
			return fmt.Errorf("failed to check delivery zone %s: %w", zone.Slug, err)
		}

		if strings.TrimSpace(existingID) == "" {
			values["id"] = newID()
			values["created_at"] = gorm.Expr("CURRENT_TIMESTAMP")
			if err := tx.WithContext(ctx).Table("delivery_zones").Create(values).Error; err != nil {
				return fmt.Errorf("failed to insert delivery zone %s: %w", zone.Slug, err)
			}
			continue
		}

		if err := tx.WithContext(ctx).Table("delivery_zones").Where("id = ?", existingID).Updates(values).Error; err != nil {
			return fmt.Errorf("failed to update delivery zone %s: %w", zone.Slug, err)
		}
	}
	return nil
}

func upsertBusinessHours(ctx context.Context, tx *gorm.DB, rows []seedBusinessHour) error {
	for _, row := range rows {
		values := map[string]any{
			"day_of_week": row.DayOfWeek,
			"open_time":   row.OpenTime,
			"close_time":  row.CloseTime,
			"is_open":     row.IsOpen,
			"updated_at":  gorm.Expr("CURRENT_TIMESTAMP"),
		}

		var existingID string
		err := tx.WithContext(ctx).
			Table("business_hours").
			Select("id").
			Where("day_of_week = ?", row.DayOfWeek).
			Limit(1).
			Scan(&existingID).Error
		if err != nil {
			return fmt.Errorf("failed to check business hours for day %d: %w", row.DayOfWeek, err)
		}

		if strings.TrimSpace(existingID) == "" {
			values["id"] = newID()
			values["created_at"] = gorm.Expr("CURRENT_TIMESTAMP")
			if err := tx.WithContext(ctx).Table("business_hours").Create(values).Error; err != nil {
				return fmt.Errorf("failed to insert business hours for day %d: %w", row.DayOfWeek, err)
			}
			continue
		}

		if err := tx.WithContext(ctx).Table("business_hours").Where("id = ?", existingID).Updates(values).Error; err != nil {
			return fmt.Errorf("failed to update business hours for day %d: %w", row.DayOfWeek, err)
		}
	}
	return nil
}
