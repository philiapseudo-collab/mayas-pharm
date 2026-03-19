package main

import (
	"strings"
	"unicode"

	"github.com/google/uuid"
)

type seedCategory struct {
	Name        string
	Slug        string
	Description string
	SortOrder   int
}

type seedProduct struct {
	Name                 string
	CategorySlug         string
	Description          string
	Price                float64
	Stock                int
	BrandName            string
	GenericName          string
	Strength             string
	DosageForm           string
	PackSize             string
	Unit                 string
	ActiveIngredient     string
	RequiresPrescription bool
	PriceSource          string
}

type seedDeliveryZone struct {
	Name          string
	Slug          string
	Fee           float64
	EstimatedMins int
	SortOrder     int
}

type seedBusinessHour struct {
	DayOfWeek int
	OpenTime  string
	CloseTime string
	IsOpen    bool
}

type categoryRecord struct {
	ID   string
	Name string
	Slug string
}

func newID() string {
	return uuid.New().String()
}

func emptyToNil(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func buildSKU(categorySlug string, name string) string {
	raw := strings.ToUpper(strings.TrimSpace(categorySlug) + "-" + strings.TrimSpace(name))
	var builder strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteRune('-')
				lastDash = true
			}
		}
	}
	sku := strings.Trim(builder.String(), "-")
	if len(sku) > 96 {
		sku = strings.Trim(sku[:96], "-")
	}
	return sku
}
