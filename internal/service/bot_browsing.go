package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/core"
)

const (
	categoryPageSize          = 8
	controlMoreCategories     = "More Categories"
	controlPreviousCategories = "Previous Categories"
	controlBackToCategories   = "Back to Categories"
	controlAllProducts        = "All Products"
)

type subcategoryGroup struct {
	Label    string
	SortRank int
	Products []*core.Product
}

func categoryPageCount(categories []string) int {
	if len(categories) == 0 {
		return 1
	}
	return (len(categories) + categoryPageSize - 1) / categoryPageSize
}

func normalizeCategoryPage(page int, categories []string) int {
	totalPages := categoryPageCount(categories)
	if page < 0 {
		return 0
	}
	if page >= totalPages {
		return totalPages - 1
	}
	return page
}

func buildCategoryPageOptions(categories []string, page int) []string {
	page = normalizeCategoryPage(page, categories)
	start := page * categoryPageSize
	if start > len(categories) {
		start = len(categories)
	}
	end := start + categoryPageSize
	if end > len(categories) {
		end = len(categories)
	}

	options := append([]string{}, categories[start:end]...)
	if page > 0 {
		options = append(options, controlPreviousCategories)
	}
	if end < len(categories) {
		options = append(options, controlMoreCategories)
	}
	return options
}

func categoryPagePrompt(base string, page int, categories []string) string {
	totalPages := categoryPageCount(categories)
	if totalPages <= 1 {
		return base
	}
	return fmt.Sprintf("%s\n\nPage %d of %d.", base, page+1, totalPages)
}

func clearBrowseContext(session *core.Session) {
	session.CurrentCategory = ""
	session.CurrentSubcategory = ""
	session.CurrentProductID = ""
}

func (b *BotService) sendCategoryPage(ctx context.Context, phone string, session *core.Session, menu map[string][]*core.Product, page int, prompt string) error {
	categories := buildOrderedCategories(menu)
	page = normalizeCategoryPage(page, categories)

	if err := b.WhatsApp.SendCategoryListWithText(ctx, phone, categoryPagePrompt(prompt, page, categories), buildCategoryPageOptions(categories, page)); err != nil {
		return fmt.Errorf("failed to send categories: %w", err)
	}

	clearBrowseContext(session)
	session.CurrentCategoryPage = page
	session.State = StateBrowsing
	return b.Session.Set(ctx, phone, session, 7200)
}

func isCategoryPagingSelection(selection string) bool {
	switch strings.TrimSpace(selection) {
	case controlMoreCategories, controlPreviousCategories:
		return true
	default:
		return false
	}
}

func (b *BotService) handleCategoryBrowseSelection(ctx context.Context, phone string, session *core.Session, menu map[string][]*core.Product, selection string, prompt string) error {
	selection = strings.TrimSpace(selection)
	switch selection {
	case controlMoreCategories:
		return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage+1, prompt)
	case controlPreviousCategories:
		return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage-1, prompt)
	}

	if !isCategoryInList(buildOrderedCategories(menu), selection) {
		if err := b.WhatsApp.SendText(ctx, phone, "That menu is expired. Here is the latest one."); err != nil {
			return fmt.Errorf("failed to send error message: %w", err)
		}
		return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage, prompt)
	}

	return b.sendCategoryEntryPoint(ctx, phone, session, menu, selection)
}

func (b *BotService) sendCategoryEntryPoint(ctx context.Context, phone string, session *core.Session, menu map[string][]*core.Product, selectedCategory string) error {
	products := sortProductsAlphabetically(menu[selectedCategory])
	if len(products) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "No products available in this category.")
	}

	groups := buildSubcategoryGroups(selectedCategory, products)
	if len(groups) <= 1 {
		return b.sendFilteredCategoryProducts(ctx, phone, session, selectedCategory, "", products)
	}

	options := make([]string, 0, len(groups)+2)
	options = append(options, controlAllProducts)
	for _, group := range groups {
		options = append(options, group.Label)
	}
	options = append(options, controlBackToCategories)

	if err := b.WhatsApp.SendCategoryListWithText(ctx, phone, fmt.Sprintf("Choose a section in *%s*:", selectedCategory), options); err != nil {
		return fmt.Errorf("failed to send subcategories: %w", err)
	}

	session.CurrentCategory = selectedCategory
	session.CurrentSubcategory = ""
	session.CurrentProductID = ""
	session.State = StateBrowsingSubcategory
	return b.Session.Set(ctx, phone, session, 7200)
}

func (b *BotService) handleSubcategoryBrowseSelection(ctx context.Context, phone string, session *core.Session, message string) error {
	menu, err := b.Repo.GetMenu(ctx)
	if err != nil {
		return fmt.Errorf("failed to get menu: %w", err)
	}

	selectedCategory := strings.TrimSpace(session.CurrentCategory)
	if selectedCategory == "" {
		return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage, welcomeMenuPrompt)
	}

	message = strings.TrimSpace(message)
	if isCategoryPagingSelection(message) {
		return b.handleCategoryBrowseSelection(ctx, phone, session, menu, message, welcomeMenuPrompt)
	}
	if message == controlBackToCategories {
		return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage, welcomeMenuPrompt)
	}
	if isCategoryInList(buildOrderedCategories(menu), message) {
		return b.sendCategoryEntryPoint(ctx, phone, session, menu, message)
	}

	products := sortProductsAlphabetically(menu[selectedCategory])
	if len(products) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "No products available in this category.")
	}

	if message == controlAllProducts {
		return b.sendFilteredCategoryProducts(ctx, phone, session, selectedCategory, "", products)
	}

	groups := buildSubcategoryGroups(selectedCategory, products)
	for _, group := range groups {
		if strings.EqualFold(group.Label, message) {
			return b.sendFilteredCategoryProducts(ctx, phone, session, selectedCategory, group.Label, group.Products)
		}
	}

	if err := b.WhatsApp.SendText(ctx, phone, "That section is expired. Here is the latest one."); err != nil {
		return fmt.Errorf("failed to send error message: %w", err)
	}
	return b.sendCategoryEntryPoint(ctx, phone, session, menu, selectedCategory)
}

func (b *BotService) sendFilteredCategoryProducts(ctx context.Context, phone string, session *core.Session, selectedCategory string, selectedSubcategory string, products []*core.Product) error {
	if len(products) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "No products available in this section.")
	}

	title := fmt.Sprintf("Products in *%s*:", selectedCategory)
	if selectedSubcategory != "" {
		title = fmt.Sprintf("Products in *%s* - *%s*:", selectedCategory, selectedSubcategory)
	}
	productList := buildSelectionListText(title, sortProductsAlphabetically(products))

	if err := b.WhatsApp.SendText(ctx, phone, productList); err != nil {
		return fmt.Errorf("failed to send products: %w", err)
	}

	session.CurrentCategory = selectedCategory
	session.CurrentSubcategory = selectedSubcategory
	session.CurrentProductID = ""
	session.State = StateSelectingProduct
	return b.Session.Set(ctx, phone, session, 7200)
}

func buildSubcategoryGroups(category string, products []*core.Product) []subcategoryGroup {
	type groupEntry struct {
		label string
		rank  int
	}

	groups := make(map[string]*subcategoryGroup)
	for _, product := range products {
		entry := classifySubcategory(category, product)
		group, ok := groups[entry.label]
		if !ok {
			group = &subcategoryGroup{Label: entry.label, SortRank: entry.rank}
			groups[entry.label] = group
		}
		group.Products = append(group.Products, product)
	}

	items := make([]subcategoryGroup, 0, len(groups))
	for _, group := range groups {
		group.Products = sortProductsAlphabetically(group.Products)
		items = append(items, *group)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].SortRank == items[j].SortRank {
			return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
		}
		return items[i].SortRank < items[j].SortRank
	})

	return items
}

func filterProductsBySubcategory(category string, products []*core.Product, selectedSubcategory string) []*core.Product {
	if strings.TrimSpace(selectedSubcategory) == "" {
		return sortProductsAlphabetically(products)
	}

	filtered := make([]*core.Product, 0, len(products))
	for _, product := range products {
		if classifySubcategory(category, product).label == selectedSubcategory {
			filtered = append(filtered, product)
		}
	}
	return sortProductsAlphabetically(filtered)
}

func classifySubcategory(category string, product *core.Product) struct {
	label string
	rank  int
} {
	dosageForm := strings.ToLower(strings.TrimSpace(product.DosageForm))
	name := strings.ToLower(strings.TrimSpace(product.Name + " " + product.GenericName))

	switch category {
	case "First Aid":
		switch {
		case strings.Contains(dosageForm, "liquid"):
			return struct {
				label string
				rank  int
			}{label: "Antiseptics & Liquids", rank: 1}
		case strings.Contains(dosageForm, "bandage"),
			strings.Contains(dosageForm, "swab"),
			strings.Contains(dosageForm, "tape"),
			strings.Contains(dosageForm, "plaster"):
			return struct {
				label string
				rank  int
			}{label: "Dressings & Bandages", rank: 2}
		default:
			return struct {
				label string
				rank  int
			}{label: "Topicals & Relief", rank: 3}
		}
	case "Women's Health":
		switch {
		case strings.Contains(dosageForm, "test kit"):
			return struct {
				label string
				rank  int
			}{label: "Pregnancy Tests", rank: 1}
		case strings.Contains(dosageForm, "pads"):
			return struct {
				label string
				rank  int
			}{label: "Pads & Period Care", rank: 2}
		case strings.Contains(dosageForm, "cream"), strings.Contains(dosageForm, "vaginal"):
			return struct {
				label string
				rank  int
			}{label: "Intimate Care", rank: 3}
		default:
			return struct {
				label string
				rank  int
			}{label: "Supplements & Wellness", rank: 4}
		}
	case "Baby Care":
		switch {
		case strings.Contains(name, "sterilis"), strings.Contains(name, "steriliz"):
			return struct {
				label string
				rank  int
			}{label: "Feeding & Sterilising", rank: 1}
		case strings.Contains(dosageForm, "drops"), strings.Contains(dosageForm, "sachet"):
			return struct {
				label string
				rank  int
			}{label: "Digestion & Drops", rank: 2}
		default:
			return struct {
				label string
				rank  int
			}{label: "Skin & Teething", rank: 3}
		}
	case "Chronic Care":
		if strings.Contains(dosageForm, "inhaler") {
			return struct {
				label string
				rank  int
			}{label: "Inhalers", rank: 2}
		}
		return struct {
			label string
			rank  int
		}{label: "Tablets & Capsules", rank: 1}
	case "Eye & Ear":
		switch {
		case strings.Contains(dosageForm, "eye"):
			return struct {
				label string
				rank  int
			}{label: "Eye Care", rank: 1}
		case strings.Contains(dosageForm, "ear"):
			return struct {
				label string
				rank  int
			}{label: "Ear Care", rank: 2}
		case strings.Contains(dosageForm, "accessory"), strings.Contains(name, "buds"):
			return struct {
				label string
				rank  int
			}{label: "Accessories", rank: 3}
		default:
			return struct {
				label string
				rank  int
			}{label: "Drops & Gels", rank: 4}
		}
	case "Oral Care":
		switch {
		case strings.Contains(dosageForm, "mouthwash"):
			return struct {
				label string
				rank  int
			}{label: "Mouthwash", rank: 1}
		case strings.Contains(dosageForm, "paste"):
			return struct {
				label string
				rank  int
			}{label: "Toothpaste", rank: 2}
		case strings.Contains(dosageForm, "gel"):
			return struct {
				label string
				rank  int
			}{label: "Dental Gels", rank: 3}
		default:
			return struct {
				label string
				rank  int
			}{label: "Brushes & Floss", rank: 4}
		}
	}

	switch {
	case strings.Contains(dosageForm, "tablet"),
		strings.Contains(dosageForm, "capsule"),
		strings.Contains(dosageForm, "chewable"),
		strings.Contains(dosageForm, "effervescent"),
		strings.Contains(dosageForm, "vaginal tablet"):
		return struct {
			label string
			rank  int
		}{label: "Tablets & Capsules", rank: 1}
	case strings.Contains(dosageForm, "syrup"),
		strings.Contains(dosageForm, "suspension"),
		strings.Contains(dosageForm, "liquid"):
		return struct {
			label string
			rank  int
		}{label: "Liquids & Syrups", rank: 2}
	case strings.Contains(dosageForm, "spray"),
		strings.Contains(dosageForm, "drops"),
		strings.Contains(dosageForm, "eye wash"):
		return struct {
			label string
			rank  int
		}{label: "Drops & Sprays", rank: 3}
	case strings.Contains(dosageForm, "cream"),
		strings.Contains(dosageForm, "gel"),
		strings.Contains(dosageForm, "ointment"),
		strings.Contains(dosageForm, "lotion"),
		strings.Contains(dosageForm, "powder"),
		strings.Contains(dosageForm, "shampoo"),
		strings.Contains(dosageForm, "jelly"):
		return struct {
			label string
			rank  int
		}{label: "Topicals", rank: 4}
	case strings.Contains(dosageForm, "sachet"):
		return struct {
			label string
			rank  int
		}{label: "Sachets & Powders", rank: 5}
	case strings.Contains(dosageForm, "inhaler"):
		return struct {
			label string
			rank  int
		}{label: "Inhalers", rank: 6}
	case strings.Contains(dosageForm, "accessory"),
		strings.Contains(dosageForm, "test kit"),
		strings.Contains(dosageForm, "bandage"),
		strings.Contains(dosageForm, "swab"),
		strings.Contains(dosageForm, "tape"),
		strings.Contains(dosageForm, "plaster"),
		strings.Contains(dosageForm, "pads"):
		return struct {
			label string
			rank  int
		}{label: "Supplies & Accessories", rank: 7}
	default:
		return struct {
			label string
			rank  int
		}{label: "Other Essentials", rank: 8}
	}
}
