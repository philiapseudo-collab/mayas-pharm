package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	paymentadapter "github.com/philia-technologies/mayas-pharm/internal/adapters/payment"
	"github.com/philia-technologies/mayas-pharm/internal/core"
)

type pesapalCheckoutGateway interface {
	InitiatePayment(ctx context.Context, input paymentadapter.PesapalInitiateInput) (*paymentadapter.PesapalInitiateResult, error)
}

// BotService handles the bot state machine and message processing
type BotService struct {
	Repo             core.ProductRepository
	Session          core.SessionRepository
	WhatsApp         core.WhatsAppGateway
	Payment          core.PaymentGateway
	Pesapal          pesapalCheckoutGateway
	PaymentService   *PaymentService
	OrderRepo        core.OrderRepository
	UserRepo         core.UserRepository
	DeliveryZoneRepo core.DeliveryZoneRepository
	PrescriptionRepo core.PrescriptionRepository
}

var fixedCategoryOrder = []string{
	"Pain & Fever",
	"Antibiotics",
	"Allergy",
	"Cough & Cold",
	"Gastro Care",
	"Vitamins & Supplements",
	"Dermatology",
	"First Aid",
	"Women's Health",
	"Chronic Care",
}

// State constants
const (
	StateStart                  = "START"
	StateBrowsing               = "BROWSING"
	StateBrowsingSubcategory    = "BROWSING_SUBCATEGORY"
	StateSelectingProduct       = "SELECTING_PRODUCT"
	StateResolveAmbiguous       = "RESOLVE_AMBIGUOUS"
	StateConfirmBulkAdd         = "CONFIRM_BULK_ADD"
	StateQuantity               = "QUANTITY"
	StateConfirmOrder           = "CONFIRM_ORDER"
	StateSelectingFulfillment   = "SELECTING_FULFILLMENT"
	StateSelectingDeliveryZone  = "SELECTING_DELIVERY_ZONE"
	StateWaitingForAddress      = "WAITING_FOR_ADDRESS"
	StateWaitingForPrescription = "WAITING_FOR_PRESCRIPTION"
	StateSelectingPaymentMethod = "SELECTING_PAYMENT_METHOD"
	StateSelectingMpesaPhone    = "SELECTING_MPESA_PHONE"
	StateWaitingForPaymentPhone = "WAITING_FOR_PAYMENT_PHONE"
	StateWaitingForRetryPhone   = "WAITING_FOR_RETRY_PAYMENT_PHONE"
	welcomeMenuPrompt           = "Welcome to Maya's Pharm. What essentials do you need today?"
	// Courtesy delay before sending STK push to reduce iPhone SIM prompt freeze
	// when the user is still in WhatsApp after choosing the payment number.
	// 3 seconds keeps checkout responsive while still giving older iOS devices
	// a brief window to surface the STK prompt after the user acts on the warning.
	stkPushCourtesyDelay = 3 * time.Second
)

var resetCommands = map[string]struct{}{
	"0":       {},
	"menu":    {},
	"reset":   {},
	"restart": {},
	"start":   {},
}

var greetingPrefixes = []string{
	"good afternoon",
	"good evening",
	"good morning",
	"habari",
	"hello",
	"hey",
	"hi",
	"mambo",
	"niaje",
	"sasa",
	"vipi",
}

// NewBotService creates a new bot service
func NewBotService(repo core.ProductRepository, session core.SessionRepository, whatsapp core.WhatsAppGateway, payment core.PaymentGateway, orderRepo core.OrderRepository, userRepo core.UserRepository) *BotService {
	return &BotService{
		Repo:      repo,
		Session:   session,
		WhatsApp:  whatsapp,
		Payment:   payment,
		OrderRepo: orderRepo,
		UserRepo:  userRepo,
	}
}

func (b *BotService) SetPaymentService(paymentService *PaymentService) {
	b.PaymentService = paymentService
}

func (b *BotService) SetPesapalGateway(pesapalGateway pesapalCheckoutGateway) {
	b.Pesapal = pesapalGateway
}

func (b *BotService) SetDeliveryZoneRepository(repo core.DeliveryZoneRepository) {
	b.DeliveryZoneRepo = repo
}

func (b *BotService) SetPrescriptionRepository(repo core.PrescriptionRepository) {
	b.PrescriptionRepo = repo
}

// sortProductsAlphabetically sorts products by name (A-Z, case-insensitive)
func sortProductsAlphabetically(products []*core.Product) []*core.Product {
	sorted := make([]*core.Product, len(products))
	copy(sorted, products)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	return sorted
}

// buildOrderedCategories returns categories in fixed order and appends unknown ones after.
func buildOrderedCategories(menu map[string][]*core.Product) []string {
	categories := make([]string, 0, len(fixedCategoryOrder)+len(menu))
	seen := make(map[string]struct{}, len(fixedCategoryOrder)+len(menu))

	for _, category := range fixedCategoryOrder {
		if _, exists := menu[category]; !exists {
			continue
		}
		categories = append(categories, category)
		seen[category] = struct{}{}
	}

	extraCategories := make([]string, 0, len(menu))
	for category := range menu {
		if _, exists := seen[category]; exists {
			continue
		}
		extraCategories = append(extraCategories, category)
	}

	sort.Slice(extraCategories, func(i, j int) bool {
		return strings.ToLower(extraCategories[i]) < strings.ToLower(extraCategories[j])
	})

	categories = append(categories, extraCategories...)

	return categories
}

func isCategoryInList(categories []string, target string) bool {
	for _, category := range categories {
		if category == target {
			return true
		}
	}
	return false
}

func normalizeConversationInput(message string) string {
	var builder strings.Builder
	lastWasSpace := true

	for _, r := range strings.ToLower(strings.TrimSpace(message)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			builder.WriteRune(r)
			lastWasSpace = false
			continue
		}

		if !lastWasSpace {
			builder.WriteByte(' ')
			lastWasSpace = true
		}
	}

	return strings.TrimSpace(builder.String())
}

func isResetMessage(message string) bool {
	normalized := normalizeConversationInput(message)
	if normalized == "" {
		return false
	}

	if _, ok := resetCommands[normalized]; ok {
		return true
	}

	for _, greeting := range greetingPrefixes {
		if normalized == greeting || strings.HasPrefix(normalized, greeting+" ") {
			return true
		}
	}

	return false
}

func newConversationSession() *core.Session {
	return &core.Session{
		State:            StateStart,
		Cart:             []core.CartItem{},
		CurrentCategory:  "",
		CurrentProductID: "",
	}
}

func (b *BotService) sendWelcomeMenu(ctx context.Context, phone string, session *core.Session) error {
	menu, err := b.Repo.GetMenu(ctx)
	if err != nil {
		return fmt.Errorf("failed to get menu: %w", err)
	}

	session.CurrentCategoryPage = 0
	session.CurrentCategory = ""
	session.CurrentSubcategory = ""
	session.CurrentProductID = ""
	clearPendingSelectionState(session)
	return b.sendCategoryPage(ctx, phone, session, menu, 0, welcomeMenuPrompt)
}

func selectionInstructionsText() string {
	return "\n*Reply with item number*\nExample: 6\n\n*For multiple items*\n1,6\n\n*For quantities*\n1x2, 6x2"
}

func checkoutPromptText(total float64) string {
	return fmt.Sprintf(
		"Your total is *KES %.0f*.\n\nWhich M-Pesa number should we charge?\n\nM-PESA prompt coming shortly...\nIf your *iOS version* is not up to date, kindly go to your Home Screen *NOW* to complete your payment",
		total,
	)
}

func paymentMethodPromptText(total float64) string {
	return fmt.Sprintf("Your total is *KES %.0f*.\n\nChoose a payment method:", total)
}

func buildSelectionListText(title string, products []*core.Product) string {
	productList := title + "\n\n"
	for i, product := range products {
		productList += fmt.Sprintf("%d. %s - KES %.0f\n", i+1, product.Name, product.Price)
	}
	productList += selectionInstructionsText()
	return productList
}

func (b *BotService) sendCurrentSelectionList(ctx context.Context, phone string, session *core.Session) error {
	if strings.TrimSpace(session.CurrentCategory) == "" {
		return b.sendWelcomeMenu(ctx, phone, session)
	}

	sortedProducts, isSearchMode, err := b.getCurrentSortedProducts(ctx, session)
	if err != nil {
		return b.WhatsApp.SendText(ctx, phone, "Menu changed. Please select items again.")
	}

	var productList string
	if isSearchMode {
		searchQuery := strings.TrimPrefix(session.CurrentCategory, "_SEARCH_")
		productList = buildSelectionListText(fmt.Sprintf("ðŸ” Search results for '*%s*':", searchQuery), sortedProducts)
	} else {
		title := fmt.Sprintf("Products in *%s*:", session.CurrentCategory)
		if strings.TrimSpace(session.CurrentSubcategory) != "" {
			title = fmt.Sprintf("Products in *%s* - *%s*:", session.CurrentCategory, session.CurrentSubcategory)
		}
		productList = buildSelectionListText(title, sortedProducts)
	}

	if err := b.WhatsApp.SendText(ctx, phone, productList); err != nil {
		return fmt.Errorf("failed to send products: %w", err)
	}

	session.State = StateSelectingProduct
	return b.Session.Set(ctx, phone, session, 7200)
}

// HandleIncomingMessage processes incoming WhatsApp messages
func (b *BotService) HandleIncomingMessage(phone string, message string, messageType string) error {
	ctx := context.Background()
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))

	// Greeting/reset commands always restart the conversation.
	if isResetMessage(message) {
		return b.restartToInitialMenu(ctx, phone)
	}

	// Retry actions must work even if the session expired.
	if strings.HasPrefix(normalizedMessage, "retry_pay_") {
		orderID := strings.TrimPrefix(strings.TrimSpace(message), "retry_pay_")
		return b.handleRetryPayment(ctx, phone, nil, orderID)
	}
	if strings.HasPrefix(normalizedMessage, "retry_other_") {
		orderID := strings.TrimPrefix(strings.TrimSpace(message), "retry_other_")
		return b.startRetryWithDifferentNumber(ctx, phone, orderID)
	}
	if strings.HasPrefix(normalizedMessage, "retry_cancel_") {
		orderID := strings.TrimPrefix(strings.TrimSpace(message), "retry_cancel_")
		return b.handleRetryCancel(ctx, phone, orderID)
	}
	if strings.HasPrefix(normalizedMessage, "switch_pesapal_") {
		orderID := strings.TrimPrefix(strings.TrimSpace(message), "switch_pesapal_")
		return b.handleSwitchPendingOrderToPesapal(ctx, phone, orderID)
	}

	// Get or create session
	session, err := b.Session.Get(ctx, phone)
	if err != nil {
		return b.sendWelcomeMenu(ctx, phone, newConversationSession())
	}

	// Route based on state
	switch session.State {
	case "START", "":
		return b.handleStart(ctx, phone, session, message)
	case "MENU":
		return b.handleMenu(ctx, phone, session, message)
	case "BROWSING":
		return b.handleBrowsing(ctx, phone, session, message)
	case StateBrowsingSubcategory:
		return b.handleBrowsingSubcategory(ctx, phone, session, message)
	case "SELECTING_PRODUCT":
		return b.handleSelectingProduct(ctx, phone, session, message, messageType)
	case StateResolveAmbiguous:
		return b.handleResolveAmbiguous(ctx, phone, session, message)
	case StateConfirmBulkAdd:
		return b.handleConfirmBulkAdd(ctx, phone, session, message)
	case StateSelectingPaymentMethod:
		return b.handleSelectingPaymentMethod(ctx, phone, session, message)
	case StateSelectingMpesaPhone:
		return b.handleSelectingMpesaPhone(ctx, phone, session, message)
	case "QUANTITY":
		return b.handleQuantity(ctx, phone, session, message)
	case "CONFIRM_ORDER":
		return b.handleConfirmOrder(ctx, phone, session, message)
	case StateSelectingFulfillment:
		return b.handleSelectingFulfillment(ctx, phone, session, message)
	case StateSelectingDeliveryZone:
		return b.handleSelectingDeliveryZone(ctx, phone, session, message)
	case StateWaitingForAddress:
		return b.handleDeliveryAddress(ctx, phone, session, message)
	case StateWaitingForPrescription:
		return b.handlePrescriptionUpload(ctx, phone, session, message, messageType)
	case StateWaitingForPaymentPhone:
		return b.handlePaymentPhoneInput(ctx, phone, session, message)
	case StateWaitingForRetryPhone:
		return b.handleRetryPaymentPhoneInput(ctx, phone, session, message)
	default:
		// Unknown state, reset to START
		session.State = "START"
		b.Session.Set(ctx, phone, session, 7200)
		return b.handleStart(ctx, phone, session, message)
	}
}

// handleStart handles the START state by always welcoming the user and showing the menu.
func (b *BotService) handleStart(ctx context.Context, phone string, session *core.Session, message string) error {
	return b.sendWelcomeMenu(ctx, phone, session)

	messageLower := strings.ToLower(strings.TrimSpace(message))

	// If message is empty (from reset command), show welcome with categories
	if messageLower == "" {
		// Get menu (grouped by category)
		menu, err := b.Repo.GetMenu(ctx)
		if err != nil {
			return fmt.Errorf("failed to get menu: %w", err)
		}

		categories := buildOrderedCategories(menu)

		// Send category list directly
		if err := b.WhatsApp.SendCategoryList(ctx, phone, categories); err != nil {
			return fmt.Errorf("failed to send categories: %w", err)
		}

		// Set state to BROWSING
		session.State = "BROWSING"
		return b.Session.Set(ctx, phone, session, 7200)
	}

	// If message is the order button or contains "order", directly show the catalog.
	if messageLower == "order_essentials" || messageLower == "order essentials" || messageLower == "order_drinks" || messageLower == "order drinks" || strings.Contains(messageLower, "order") {
		// Get menu (grouped by category)
		menu, err := b.Repo.GetMenu(ctx)
		if err != nil {
			return fmt.Errorf("failed to get menu: %w", err)
		}

		categories := buildOrderedCategories(menu)

		// Send category list directly (no welcome message needed)
		if err := b.WhatsApp.SendCategoryList(ctx, phone, categories); err != nil {
			return fmt.Errorf("failed to send categories: %w", err)
		}

		// Set state to BROWSING (skip MENU state)
		session.State = "BROWSING"
		return b.Session.Set(ctx, phone, session, 7200)
	}

	// Otherwise, treat the message as a search query
	searchQuery := strings.TrimSpace(message)

	// Improved search: allow partial matches, handle multiple words
	products, err := b.Repo.SearchProducts(ctx, searchQuery)
	if err != nil {
		return fmt.Errorf("failed to search products: %w", err)
	}

	// If no results found, send error message and a button back to the full catalog.
	if len(products) == 0 {
		noResultsMsg := fmt.Sprintf("No products found for '%s'.\n\nTry:\n- Typing one keyword such as 'paracetamol' or 'vitamin'\n- Browsing the full catalog below", searchQuery)
		buttons := []core.Button{
			{
				ID:    "order_essentials",
				Title: "View Catalog",
			},
		}

		if err := b.WhatsApp.SendMenuButtons(ctx, phone, noResultsMsg, buttons); err != nil {
			return fmt.Errorf("failed to send no results message: %w", err)
		}

		// Stay in START state
		return b.Session.Set(ctx, phone, session, 7200)
	}

	// Sort products alphabetically
	sortedProducts := sortProductsAlphabetically(products)

	// Build formatted text message with numbered list
	productList := fmt.Sprintf("🔍 Search results for '*%s*':\n\n", searchQuery)
	for i, product := range sortedProducts {
		productList += fmt.Sprintf("%d. %s - KES %.0f\n", i+1, product.Name, product.Price)
	}
	productList += selectionInstructionsText()

	// Send product list as text message
	if err := b.WhatsApp.SendText(ctx, phone, productList); err != nil {
		return fmt.Errorf("failed to send search results: %w", err)
	}

	// Set a pseudo-category for search results so SELECTING_PRODUCT state can work
	// We'll use a special category name that includes all search results
	session.CurrentCategory = "_SEARCH_" + searchQuery
	session.State = "SELECTING_PRODUCT"
	return b.Session.Set(ctx, phone, session, 7200)
}

// handleMenu handles the MENU state - shows categories
func (b *BotService) handleMenu(ctx context.Context, phone string, session *core.Session, message string) error {
	messageLower := strings.ToLower(strings.TrimSpace(message))

	// Accept button ID or text containing "order".
	if messageLower != "order_essentials" && messageLower != "order essentials" && messageLower != "order_drinks" && messageLower != "order drinks" && !strings.Contains(messageLower, "order") {
		// Invalid input - resend the category list
		menu, err := b.Repo.GetMenu(ctx)
		if err != nil {
			return fmt.Errorf("failed to get menu: %w", err)
		}

		errorMsg := "That menu is expired. Here is the latest one."
		// Send error message first, then the list
		if err := b.WhatsApp.SendText(ctx, phone, errorMsg); err != nil {
			return fmt.Errorf("failed to send error message: %w", err)
		}
		return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage, "Select a category to browse:")
	}

	// Get menu (grouped by category)
	menu, err := b.Repo.GetMenu(ctx)
	if err != nil {
		return fmt.Errorf("failed to get menu: %w", err)
	}

	return b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage, "Select a category to browse:")
}

// handleBrowsing handles the BROWSING state - shows products in a category
func (b *BotService) handleBrowsing(ctx context.Context, phone string, session *core.Session, message string) error {
	// Get menu (grouped by category)
	menu, err := b.Repo.GetMenu(ctx)
	if err != nil {
		return fmt.Errorf("failed to get menu: %w", err)
	}

	return b.handleCategoryBrowseSelection(ctx, phone, session, menu, message, welcomeMenuPrompt)
}

func (b *BotService) handleBrowsingSubcategory(ctx context.Context, phone string, session *core.Session, message string) error {
	return b.handleSubcategoryBrowseSelection(ctx, phone, session, message)
}

// handleSelectingProduct handles the SELECTING_PRODUCT state - user selects a product
func (b *BotService) handleSelectingProduct(ctx context.Context, phone string, session *core.Session, message string, messageType string) error {
	if handled, err := b.tryHandleInteractiveCategorySwitch(ctx, phone, session, message, messageType); handled || err != nil {
		return err
	}

	sortedProducts, isSearchMode, err := b.getCurrentSortedProducts(ctx, session)
	if err != nil {
		return b.WhatsApp.SendText(ctx, phone, "Menu changed. Please select items again.")
	}

	messageTrimmed := strings.TrimSpace(message)
	if messageTrimmed == "" {
		return b.WhatsApp.SendText(ctx, phone, "Please reply with a product number.")
	}

	entries := splitSelectionEntries(messageTrimmed)
	if len(entries) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "Please reply with a product number.")
	}

	hasExplicitQuantity := false
	for _, entry := range entries {
		if _, _, hasOperator := splitQuantityExpression(entry); hasOperator {
			hasExplicitQuantity = true
			break
		}
	}

	pendingResolvedItems := append([]core.CartItem(nil), session.PendingResolvedItems...)
	pendingSelectionErrors := append([]string(nil), session.PendingSelectionErrors...)

	// Keep classic flow when user picks one item without quantity syntax and there is no pending bulk confirmation.
	if len(entries) == 1 && !hasExplicitQuantity && len(pendingResolvedItems) == 0 {
		resolution := b.resolveSelectionEntry(ctx, entries[0], sortedProducts, session.CurrentCategory, isSearchMode)
		if resolution.InvalidReason != "" {
			errorMsg := "Invalid option. Use product numbers only (e.g., 7 or 7x3)."
			if err := b.WhatsApp.SendText(ctx, phone, errorMsg); err != nil {
				return fmt.Errorf("failed to send error message: %w", err)
			}
			return b.Session.Set(ctx, phone, session, 7200)
		}

		if len(resolution.Ambiguous) > 0 {
			clearPendingSelectionState(session)
			session.PendingAmbiguousInput = entries[0]
			session.PendingAmbiguousQty = 0 // 0 means ask quantity after user chooses exact item.
			session.PendingAmbiguousOptions = buildPendingAmbiguousOptions(resolution.Ambiguous)
			session.State = StateResolveAmbiguous
			if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
				return fmt.Errorf("failed to save ambiguous state: %w", err)
			}
			return b.sendAmbiguousPrompt(ctx, phone, session)
		}

		if resolution.Product.StockQuantity <= 0 {
			return b.WhatsApp.SendText(ctx, phone, fmt.Sprintf("Sorry, %s is out of stock. Please select another product.", resolution.Product.Name))
		}

		clearPendingSelectionState(session)
		session.CurrentProductID = resolution.Product.ID
		session.State = StateQuantity
		if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
			return fmt.Errorf("failed to save quantity state: %w", err)
		}

		quantityMsg := fmt.Sprintf("You selected: *%s*\nPrice: KES %.0f\n\nHow many would you like? (Enter a number)",
			resolution.Product.Name, resolution.Product.Price)
		return b.WhatsApp.SendText(ctx, phone, quantityMsg)
	}

	// Bulk/quantity flow with confirmation step.
	session.PendingRawSelections = nil
	session.PendingAmbiguousInput = ""
	session.PendingAmbiguousQty = 0
	session.PendingAmbiguousOptions = nil
	return b.processBulkSelections(ctx, phone, session, sortedProducts, isSearchMode, entries, pendingResolvedItems, pendingSelectionErrors)
}

func (b *BotService) tryHandleInteractiveCategorySwitch(ctx context.Context, phone string, session *core.Session, message string, messageType string) (bool, error) {
	if messageType != "interactive" {
		return false, nil
	}

	menu, err := b.Repo.GetMenu(ctx)
	if err != nil {
		return true, fmt.Errorf("failed to get menu: %w", err)
	}

	selected := strings.TrimSpace(message)
	if isCategoryPagingSelection(selected) {
		return true, b.handleCategoryBrowseSelection(ctx, phone, session, menu, selected, "Select a category to browse:")
	}
	if selected == controlBackToCategories {
		return true, b.sendCategoryPage(ctx, phone, session, menu, session.CurrentCategoryPage, "Select a category to browse:")
	}
	if isCategoryInList(buildOrderedCategories(menu), selected) {
		return true, b.sendCategoryEntryPoint(ctx, phone, session, menu, selected)
	}
	if strings.TrimSpace(session.CurrentCategory) == "" {
		return false, nil
	}
	for _, group := range buildSubcategoryGroups(session.CurrentCategory, menu[session.CurrentCategory]) {
		if strings.EqualFold(group.Label, selected) || selected == controlAllProducts {
			return true, b.handleSubcategoryBrowseSelection(ctx, phone, session, selected)
		}
	}
	return false, nil
}

func (b *BotService) sendCategoryProducts(ctx context.Context, phone string, session *core.Session, menu map[string][]*core.Product, selectedCategory string) error {
	return b.sendFilteredCategoryProducts(ctx, phone, session, selectedCategory, "", menu[selectedCategory])
}

type selectionResolution struct {
	Product       *core.Product
	Quantity      int
	Ambiguous     []*core.Product
	InvalidReason string
}

func (b *BotService) getCurrentSortedProducts(ctx context.Context, session *core.Session) ([]*core.Product, bool, error) {
	// Check if we're in search mode (category starts with "_SEARCH_")
	isSearchMode := strings.HasPrefix(session.CurrentCategory, "_SEARCH_")

	if isSearchMode {
		searchQuery := strings.TrimPrefix(session.CurrentCategory, "_SEARCH_")
		products, err := b.Repo.SearchProducts(ctx, searchQuery)
		if err != nil {
			return nil, false, fmt.Errorf("failed to search products: %w", err)
		}
		if len(products) == 0 {
			return nil, false, fmt.Errorf("no products in search")
		}
		return sortProductsAlphabetically(products), true, nil
	}

	menu, err := b.Repo.GetMenu(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get menu: %w", err)
	}

	products := menu[session.CurrentCategory]
	if len(products) == 0 {
		return nil, false, fmt.Errorf("no products in category")
	}

	if strings.TrimSpace(session.CurrentSubcategory) != "" {
		products = filterProductsBySubcategory(session.CurrentCategory, products, session.CurrentSubcategory)
		if len(products) == 0 {
			return nil, false, fmt.Errorf("no products in subcategory")
		}
	}

	return sortProductsAlphabetically(products), false, nil
}

func splitSelectionEntries(message string) []string {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}

	segments := strings.FieldsFunc(message, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})

	entries := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}

		tokens := strings.Fields(segment)
		if len(tokens) > 1 {
			allSimpleTokens := true
			for _, token := range tokens {
				_, isNumber := parsePositiveInt(token)
				if !isNumber && !isCompactQtyToken(token) {
					allSimpleTokens = false
					break
				}
			}
			// Expand mixed numeric tokens like "5x2 7".
			if allSimpleTokens {
				entries = append(entries, tokens...)
				continue
			}

		}

		entries = append(entries, segment)
	}

	return entries
}

func isCompactQtyToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	for i, r := range token {
		if r != 'x' && r != 'X' && r != '*' {
			continue
		}

		left := strings.TrimSpace(token[:i])
		right := strings.TrimSpace(token[i+1:])
		_, leftOK := parsePositiveInt(left)
		_, rightOK := parsePositiveInt(right)
		return leftOK && rightOK
	}

	return false
}

func splitQuantityExpression(entry string) (string, string, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", "", false
	}

	for i, r := range entry {
		if r != 'x' && r != 'X' && r != '*' {
			continue
		}

		left := strings.TrimSpace(entry[:i])
		right := strings.TrimSpace(entry[i+1:])
		if left == "" || right == "" {
			continue
		}

		_, leftNumber := parsePositiveInt(left)
		_, rightNumber := parsePositiveInt(right)
		if leftNumber || rightNumber {
			return left, right, true
		}
	}

	return "", "", false
}

func parsePositiveInt(value string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func (b *BotService) matchProducts(ctx context.Context, selection string, sortedProducts []*core.Product, currentCategory string, isSearchMode bool) []*core.Product {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return nil
	}

	// UUID support for backward compatibility.
	if productID, err := uuid.Parse(selection); err == nil {
		product, err := b.Repo.GetByID(ctx, productID.String())
		if err == nil && product != nil {
			if isSearchMode {
				for _, p := range sortedProducts {
					if p.ID == product.ID {
						return []*core.Product{product}
					}
				}
			} else if product.Category == currentCategory {
				return []*core.Product{product}
			}
		}
		return nil
	}

	// Number mapping (display order).
	if num, err := strconv.Atoi(selection); err == nil {
		if num > 0 && num <= len(sortedProducts) {
			return []*core.Product{sortedProducts[num-1]}
		}
		return nil
	}

	// Names are intentionally not supported in ordering flow.
	return nil
}

func (b *BotService) resolveSelectionEntry(ctx context.Context, entry string, sortedProducts []*core.Product, currentCategory string, isSearchMode bool) selectionResolution {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return selectionResolution{InvalidReason: "empty selection"}
	}

	left, right, hasQuantityOperator := splitQuantityExpression(entry)
	if hasQuantityOperator {
		// Rule: interpret as item x qty unless impossible.
		if rightQty, rightIsQty := parsePositiveInt(right); rightIsQty {
			leftMatches := b.matchProducts(ctx, left, sortedProducts, currentCategory, isSearchMode)
			if len(leftMatches) == 1 {
				return selectionResolution{
					Product:  leftMatches[0],
					Quantity: rightQty,
				}
			}
			if len(leftMatches) > 1 {
				return selectionResolution{
					Quantity:  rightQty,
					Ambiguous: leftMatches,
				}
			}
		}

		// Fallback: qty x item.
		if leftQty, leftIsQty := parsePositiveInt(left); leftIsQty {
			rightMatches := b.matchProducts(ctx, right, sortedProducts, currentCategory, isSearchMode)
			if len(rightMatches) == 1 {
				return selectionResolution{
					Product:  rightMatches[0],
					Quantity: leftQty,
				}
			}
			if len(rightMatches) > 1 {
				return selectionResolution{
					Quantity:  leftQty,
					Ambiguous: rightMatches,
				}
			}
		}

		return selectionResolution{InvalidReason: "use numbers only (e.g., 7x3 or 3x7)"}
	}

	matches := b.matchProducts(ctx, entry, sortedProducts, currentCategory, isSearchMode)
	if len(matches) == 1 {
		return selectionResolution{
			Product:  matches[0],
			Quantity: 1,
		}
	}
	if len(matches) > 1 {
		return selectionResolution{
			Quantity:  1,
			Ambiguous: matches,
		}
	}

	return selectionResolution{InvalidReason: "use product numbers only (e.g., 7)"}
}

func buildPendingAmbiguousOptions(matches []*core.Product) []core.PendingAmbiguousOption {
	options := make([]core.PendingAmbiguousOption, 0, len(matches))
	for _, match := range matches {
		options = append(options, core.PendingAmbiguousOption{
			ProductID:            match.ID,
			Name:                 match.Name,
			Price:                match.Price,
			RequiresPrescription: match.RequiresPrescription,
		})
	}
	return options
}

func clearPendingSelectionState(session *core.Session) {
	session.PendingResolvedItems = nil
	session.PendingRawSelections = nil
	session.PendingSelectionErrors = nil
	session.PendingAmbiguousInput = ""
	session.PendingAmbiguousQty = 0
	session.PendingAmbiguousOptions = nil
}

func (b *BotService) restartToInitialMenu(ctx context.Context, phone string) error {
	return b.sendWelcomeMenu(ctx, phone, newConversationSession())
}

func mergeCartItem(items []core.CartItem, incoming core.CartItem) []core.CartItem {
	if incoming.Quantity <= 0 {
		return items
	}

	for i := range items {
		if items[i].ProductID == incoming.ProductID {
			items[i].Quantity += incoming.Quantity
			items[i].RequiresPrescription = items[i].RequiresPrescription || incoming.RequiresPrescription
			return items
		}
	}

	return append(items, incoming)
}

func (b *BotService) sendAmbiguousPrompt(ctx context.Context, phone string, session *core.Session) error {
	if len(session.PendingAmbiguousOptions) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "That selection expired. Please send your order again.")
	}

	prompt := fmt.Sprintf("I found multiple matches for '%s'.\nReply with one option number:\n\n", session.PendingAmbiguousInput)
	for i, option := range session.PendingAmbiguousOptions {
		prompt += fmt.Sprintf("%d. %s - KES %.0f\n", i+1, option.Name, option.Price)
	}
	prompt += "\nType 'cancel' to stop."

	return b.WhatsApp.SendText(ctx, phone, prompt)
}

func (b *BotService) handleResolveAmbiguous(ctx context.Context, phone string, session *core.Session, message string) error {
	if len(session.PendingAmbiguousOptions) == 0 {
		clearPendingSelectionState(session)
		session.State = StateSelectingProduct
		b.Session.Set(ctx, phone, session, 7200)
		return b.WhatsApp.SendText(ctx, phone, "That selection expired. Please send your order again.")
	}

	messageTrimmed := strings.TrimSpace(message)
	messageLower := strings.ToLower(messageTrimmed)

	if messageLower == "cancel" || messageLower == "cancel_add" {
		return b.restartToInitialMenu(ctx, phone)
	}

	optionIndex := -1
	if idx, err := strconv.Atoi(messageTrimmed); err == nil && idx >= 1 && idx <= len(session.PendingAmbiguousOptions) {
		optionIndex = idx - 1
	} else {
		exactMatches := make([]int, 0, 1)
		for i, option := range session.PendingAmbiguousOptions {
			if strings.EqualFold(option.Name, messageTrimmed) {
				exactMatches = append(exactMatches, i)
			}
		}
		if len(exactMatches) == 1 {
			optionIndex = exactMatches[0]
		}
	}

	if optionIndex < 0 {
		return b.sendAmbiguousPrompt(ctx, phone, session)
	}

	chosen := session.PendingAmbiguousOptions[optionIndex]

	// Qty == 0 indicates the single-item flow where quantity is asked next.
	if session.PendingAmbiguousQty == 0 {
		product, err := b.Repo.GetByID(ctx, chosen.ProductID)
		if err != nil || product == nil {
			return b.WhatsApp.SendText(ctx, phone, "That item is no longer available. Please pick another one.")
		}
		if product.StockQuantity <= 0 {
			return b.WhatsApp.SendText(ctx, phone, fmt.Sprintf("Sorry, %s is out of stock. Please select another product.", product.Name))
		}

		clearPendingSelectionState(session)
		session.CurrentProductID = product.ID
		session.State = StateQuantity
		if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
			return fmt.Errorf("failed to save quantity state: %w", err)
		}

		quantityMsg := fmt.Sprintf("You selected: *%s*\nPrice: KES %.0f\n\nHow many would you like? (Enter a number)",
			product.Name, product.Price)
		return b.WhatsApp.SendText(ctx, phone, quantityMsg)
	}

	// Bulk flow: merge chosen ambiguous item, then continue with remaining entries.
	resolved := append([]core.CartItem{}, session.PendingResolvedItems...)
	resolved = mergeCartItem(resolved, core.CartItem{
		ProductID:            chosen.ProductID,
		Quantity:             session.PendingAmbiguousQty,
		Name:                 chosen.Name,
		Price:                chosen.Price,
		RequiresPrescription: chosen.RequiresPrescription,
	})
	remaining := append([]string{}, session.PendingRawSelections...)
	errors := append([]string{}, session.PendingSelectionErrors...)

	session.PendingResolvedItems = resolved
	session.PendingRawSelections = remaining
	session.PendingSelectionErrors = errors
	session.PendingAmbiguousInput = ""
	session.PendingAmbiguousQty = 0
	session.PendingAmbiguousOptions = nil

	sortedProducts, isSearchMode, err := b.getCurrentSortedProducts(ctx, session)
	if err != nil {
		return b.WhatsApp.SendText(ctx, phone, "Menu changed. Please select your items again.")
	}

	return b.processBulkSelections(ctx, phone, session, sortedProducts, isSearchMode, remaining, resolved, errors)
}

func (b *BotService) processBulkSelections(ctx context.Context, phone string, session *core.Session, sortedProducts []*core.Product, isSearchMode bool, entries []string, resolved []core.CartItem, selectionErrors []string) error {
	for i, entry := range entries {
		resolution := b.resolveSelectionEntry(ctx, entry, sortedProducts, session.CurrentCategory, isSearchMode)
		if resolution.InvalidReason != "" {
			selectionErrors = append(selectionErrors, fmt.Sprintf("%s: %s", entry, resolution.InvalidReason))
			continue
		}

		if len(resolution.Ambiguous) > 0 {
			session.PendingResolvedItems = resolved
			if i+1 < len(entries) {
				session.PendingRawSelections = append([]string{}, entries[i+1:]...)
			} else {
				session.PendingRawSelections = nil
			}
			session.PendingSelectionErrors = selectionErrors
			session.PendingAmbiguousInput = entry
			session.PendingAmbiguousQty = resolution.Quantity
			session.PendingAmbiguousOptions = buildPendingAmbiguousOptions(resolution.Ambiguous)
			session.State = StateResolveAmbiguous
			if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
				return fmt.Errorf("failed to save ambiguous selection state: %w", err)
			}
			return b.sendAmbiguousPrompt(ctx, phone, session)
		}

		resolved = mergeCartItem(resolved, core.CartItem{
			ProductID:            resolution.Product.ID,
			Quantity:             resolution.Quantity,
			Name:                 resolution.Product.Name,
			Price:                resolution.Product.Price,
			RequiresPrescription: resolution.Product.RequiresPrescription,
		})
	}

	return b.prepareBulkConfirmation(ctx, phone, session, resolved, selectionErrors)
}

func (b *BotService) prepareBulkConfirmation(ctx context.Context, phone string, session *core.Session, resolved []core.CartItem, selectionErrors []string) error {
	finalItems := make([]core.CartItem, 0, len(resolved))
	notes := append([]string{}, selectionErrors...)

	for _, item := range resolved {
		product, err := b.Repo.GetByID(ctx, item.ProductID)
		if err != nil || product == nil {
			notes = append(notes, fmt.Sprintf("%s is no longer available.", item.Name))
			continue
		}

		if product.StockQuantity <= 0 {
			notes = append(notes, fmt.Sprintf("%s is out of stock and was skipped.", product.Name))
			continue
		}

		allowedQty := item.Quantity
		if item.Quantity > product.StockQuantity {
			allowedQty = product.StockQuantity
			notes = append(notes,
				fmt.Sprintf("%s requested x%d, only x%d available. Will add x%d.", product.Name, item.Quantity, product.StockQuantity, allowedQty))
		}

		finalItems = mergeCartItem(finalItems, core.CartItem{
			ProductID:            product.ID,
			Quantity:             allowedQty,
			Name:                 product.Name,
			Price:                product.Price,
			RequiresPrescription: product.RequiresPrescription,
		})
	}

	if len(finalItems) == 0 {
		msg := "No items could be added from that message."
		if len(notes) > 0 {
			msg += "\n\nIssues:\n"
			for _, note := range notes {
				msg += fmt.Sprintf("- %s\n", note)
			}
		}

		clearPendingSelectionState(session)
		session.State = StateSelectingProduct
		if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
			return fmt.Errorf("failed to save session: %w", err)
		}
		return b.WhatsApp.SendText(ctx, phone, strings.TrimSpace(msg))
	}

	session.PendingResolvedItems = finalItems
	session.PendingRawSelections = nil
	session.PendingSelectionErrors = notes
	session.PendingAmbiguousInput = ""
	session.PendingAmbiguousQty = 0
	session.PendingAmbiguousOptions = nil
	session.State = StateConfirmBulkAdd

	if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
		return fmt.Errorf("failed to save bulk confirm state: %w", err)
	}

	return b.sendBulkConfirmPrompt(ctx, phone, session)
}

func (b *BotService) sendBulkConfirmPrompt(ctx context.Context, phone string, session *core.Session) error {
	if len(session.PendingResolvedItems) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "No pending items to confirm. Please send your order again.")
	}

	preview := "confirm your order\n\n"
	addOnTotal := 0.0
	for i, item := range session.PendingResolvedItems {
		lineTotal := item.Price * float64(item.Quantity)
		addOnTotal += lineTotal
		preview += fmt.Sprintf("%d. %s x%d = KES %.0f\n", i+1, item.Name, item.Quantity, lineTotal)
	}
	preview += fmt.Sprintf("\nTOTAL=KES %.0f", addOnTotal)

	if len(session.PendingSelectionErrors) > 0 {
		preview += "\n\nNotes:\n"
		for _, note := range session.PendingSelectionErrors {
			preview += fmt.Sprintf("- %s\n", note)
		}
	}

	buttons := []core.Button{
		{
			ID:    "add_more_pending",
			Title: "Add more",
		},
		{
			ID:    "cancel_add",
			Title: "Cancel",
		},
		{
			ID:    "confirm_add",
			Title: "\u2705 Confirm",
		},
	}

	return b.WhatsApp.SendMenuButtons(ctx, phone, strings.TrimSpace(preview), buttons)
}

func (b *BotService) handleConfirmBulkAdd(ctx context.Context, phone string, session *core.Session, message string) error {
	messageLower := strings.ToLower(strings.TrimSpace(message))

	if messageLower == "add_more_pending" || strings.Contains(messageLower, "add more") {
		return b.handleMenu(ctx, phone, session, "Order Essentials")
	}

	if messageLower == "confirm_add" || strings.Contains(messageLower, "confirm") {
		if len(session.PendingResolvedItems) == 0 {
			session.State = StateSelectingProduct
			b.Session.Set(ctx, phone, session, 7200)
			return b.WhatsApp.SendText(ctx, phone, "No pending items to confirm. Please send your order again.")
		}

		for _, item := range session.PendingResolvedItems {
			session.Cart = mergeCartItem(session.Cart, item)
		}

		clearPendingSelectionState(session)
		return b.handleCheckout(ctx, phone, session)
	}

	if messageLower == "cancel_add" || messageLower == "cancel" || strings.Contains(messageLower, "cancel") {
		return b.restartToInitialMenu(ctx, phone)
	}

	if err := b.WhatsApp.SendText(ctx, phone, "Please use the buttons: Add more, Cancel, or \u2705 Confirm."); err != nil {
		return fmt.Errorf("failed to send instruction: %w", err)
	}
	return b.sendBulkConfirmPrompt(ctx, phone, session)
}

func (b *BotService) sendCartSummary(ctx context.Context, phone string, session *core.Session) error {
	// Normalize duplicate lines in cart.
	normalizedCart := make([]core.CartItem, 0, len(session.Cart))
	for _, item := range session.Cart {
		normalizedCart = mergeCartItem(normalizedCart, item)
	}
	session.Cart = normalizedCart

	total := 0.0
	for _, item := range session.Cart {
		total += item.Price * float64(item.Quantity)
	}

	cartSummary := "Added to cart.\n\nYour cart:\n"
	for i, item := range session.Cart {
		itemTotal := item.Price * float64(item.Quantity)
		cartSummary += fmt.Sprintf("%d. %s x%d = KES %.0f\n", i+1, item.Name, item.Quantity, itemTotal)
	}
	cartSummary += fmt.Sprintf("\nCart total: KES %.0f", total)

	buttons := []core.Button{
		{
			ID:    "add_more",
			Title: "Add More",
		},
		{
			ID:    "checkout",
			Title: "Checkout",
		},
	}

	if err := b.WhatsApp.SendMenuButtons(ctx, phone, cartSummary, buttons); err != nil {
		return fmt.Errorf("failed to send confirmation: %w", err)
	}

	clearPendingSelectionState(session)
	session.State = StateConfirmOrder
	return b.Session.Set(ctx, phone, session, 7200)
}

// handleQuantity handles the QUANTITY state - user enters quantity
func (b *BotService) handleQuantity(ctx context.Context, phone string, session *core.Session, message string) error {
	// Parse quantity
	quantity, err := strconv.Atoi(strings.TrimSpace(message))
	if err != nil || quantity <= 0 {
		// Invalid input - forgiving state: keep in QUANTITY
		return b.WhatsApp.SendText(ctx, phone, "Please enter a valid number (e.g., 2)")
	}

	// Get product details
	product, err := b.Repo.GetByID(ctx, session.CurrentProductID)
	if err != nil {
		return fmt.Errorf("failed to get product: %w", err)
	}

	// Check stock
	if product.StockQuantity < quantity {
		return b.WhatsApp.SendText(ctx, phone,
			fmt.Sprintf("Sorry, only %d available in stock. Please enter a smaller quantity.", product.StockQuantity))
	}

	// Add to cart
	session.Cart = mergeCartItem(session.Cart, core.CartItem{
		ProductID:            product.ID,
		Quantity:             quantity,
		Name:                 product.Name,
		Price:                product.Price,
		RequiresPrescription: product.RequiresPrescription,
	})

	return b.sendCartSummary(ctx, phone, session)
}

// handleConfirmOrder handles the CONFIRM_ORDER state - user can add more or proceed to payment method selection.
func (b *BotService) handleConfirmOrder(ctx context.Context, phone string, session *core.Session, message string) error {
	messageLower := strings.ToLower(strings.TrimSpace(message))

	if messageLower == "add_more" || strings.Contains(messageLower, "add more") || strings.Contains(messageLower, "continue") {
		return b.handleMenu(ctx, phone, session, "Order Essentials")
	}

	if messageLower == "checkout" || strings.Contains(messageLower, "checkout") {
		return b.handleCheckout(ctx, phone, session)
	}

	confirmMsg := "Please select an option:"
	buttons := []core.Button{
		{
			ID:    "add_more",
			Title: "Add More",
		},
		{
			ID:    "checkout",
			Title: "Checkout",
		},
	}
	return b.WhatsApp.SendMenuButtons(ctx, phone, confirmMsg, buttons)
}

func (b *BotService) handleSelectingPaymentMethod(ctx context.Context, phone string, session *core.Session, message string) error {
	messageLower := strings.ToLower(strings.TrimSpace(message))

	if messageLower == "pay_mpesa" {
		return b.handleMpesaCheckout(ctx, phone, session)
	}
	if messageLower == "pay_pesapal" {
		return b.handlePesapalCheckout(ctx, phone, session)
	}

	return b.sendPaymentMethodPrompt(ctx, phone, session)
}

func (b *BotService) handleSelectingMpesaPhone(ctx context.Context, phone string, session *core.Session, message string) error {
	messageLower := strings.ToLower(strings.TrimSpace(message))

	if messageLower == "pay_self" {
		return b.handlePaySelf(ctx, phone, session)
	}
	if messageLower == "pay_other" {
		return b.handlePayOther(ctx, phone, session)
	}

	total, err := b.checkoutTotal(ctx, session)
	if err != nil {
		return b.WhatsApp.SendText(ctx, phone, "Your cart is empty. Please add items first.")
	}

	return b.sendMpesaPhonePrompt(ctx, phone, session, total)
}

func (b *BotService) checkoutPendingOrder(ctx context.Context, session *core.Session) (*core.Order, error) {
	orderID := strings.TrimSpace(session.PendingOrderID)
	if orderID == "" {
		return nil, nil
	}

	order, err := b.OrderRepo.GetByID(ctx, orderID)
	if err != nil || order == nil {
		session.PendingOrderID = ""
		return nil, nil
	}

	if isPaidWorkflowStatus(order.Status) || order.Status == core.OrderStatusExpired {
		session.PendingOrderID = ""
		return nil, nil
	}

	return order, nil
}

func cartTotal(items []core.CartItem) float64 {
	total := 0.0
	for _, item := range items {
		total += item.Price * float64(item.Quantity)
	}
	return total
}

func cartRequiresPrescription(items []core.CartItem) bool {
	for _, item := range items {
		if item.RequiresPrescription {
			return true
		}
	}
	return false
}

func (b *BotService) checkoutTotal(ctx context.Context, session *core.Session) (float64, error) {
	order, err := b.checkoutPendingOrder(ctx, session)
	if err != nil {
		return 0, err
	}
	if order != nil {
		return order.TotalAmount, nil
	}

	if len(session.Cart) == 0 {
		return 0, fmt.Errorf("cart is empty")
	}

	return cartTotal(session.Cart), nil
}

func (b *BotService) sendPendingOrderOptions(ctx context.Context, phone string, session *core.Session, order *core.Order) error {
	if order.Status == core.OrderStatusPendingReview {
		return b.WhatsApp.SendText(ctx, phone, "Your order is waiting for pharmacist review. Please send your prescription image or PDF in this chat if you have not already.")
	}
	message := fmt.Sprintf(
		"Your order for *KES %.0f* is still awaiting payment.\n\nChoose how you want to continue.",
		order.TotalAmount,
	)
	buttons := []core.Button{
		{
			ID:    "retry_pay_" + order.ID,
			Title: "Retry M-Pesa",
		},
		{
			ID:    "switch_pesapal_" + order.ID,
			Title: "Airtel Money/Card",
		},
	}
	return b.WhatsApp.SendMenuButtons(ctx, phone, message, buttons)
}

func (b *BotService) sendPaymentMethodPrompt(ctx context.Context, phone string, session *core.Session) error {
	total, err := b.checkoutTotal(ctx, session)
	if err != nil {
		return b.WhatsApp.SendText(ctx, phone, "Your cart is empty. Please add items first.")
	}

	promptMsg := paymentMethodPromptText(total)
	buttons := []core.Button{
		{
			ID:    "pay_mpesa",
			Title: "M-Pesa",
		},
		{
			ID:    "pay_pesapal",
			Title: "Airtel Money/Card",
		},
	}

	if err := b.WhatsApp.SendMenuButtons(ctx, phone, promptMsg, buttons); err != nil {
		return fmt.Errorf("failed to send payment method prompt: %w", err)
	}

	session.State = StateSelectingPaymentMethod
	return b.Session.Set(ctx, phone, session, 7200)
}

func (b *BotService) sendFulfillmentPrompt(ctx context.Context, phone string, session *core.Session) error {
	buttons := []core.Button{
		{ID: "fulfillment_pickup", Title: "Pickup"},
		{ID: "fulfillment_delivery", Title: "Delivery"},
	}
	if err := b.WhatsApp.SendMenuButtons(ctx, phone, "How should we fulfil this order?", buttons); err != nil {
		return fmt.Errorf("failed to send fulfilment prompt: %w", err)
	}
	session.State = StateSelectingFulfillment
	return b.Session.Set(ctx, phone, session, 7200)
}

func (b *BotService) sendDeliveryZonePrompt(ctx context.Context, phone string, session *core.Session) error {
	if b.DeliveryZoneRepo == nil {
		return b.WhatsApp.SendText(ctx, phone, "Delivery is unavailable right now. Please choose pickup.")
	}
	zones, err := b.DeliveryZoneRepo.ListActive(ctx)
	if err != nil || len(zones) == 0 {
		return b.WhatsApp.SendText(ctx, phone, "Delivery is unavailable right now. Please choose pickup.")
	}

	labels := make([]string, 0, len(zones))
	for _, zone := range zones {
		labels = append(labels, zone.ID)
	}
	if err := b.WhatsApp.SendCategoryListWithText(ctx, phone, "Choose your Nairobi delivery zone:", labels); err != nil {
		return err
	}
	session.State = StateSelectingDeliveryZone
	return b.Session.Set(ctx, phone, session, 7200)
}

func (b *BotService) sendMpesaPhonePrompt(ctx context.Context, phone string, session *core.Session, total float64) error {
	promptMsg := checkoutPromptText(total)
	buttons := []core.Button{
		{
			ID:    "pay_self",
			Title: "Use My Number",
		},
		{
			ID:    "pay_other",
			Title: "Different Number",
		},
	}

	if err := b.WhatsApp.SendMenuButtons(ctx, phone, promptMsg, buttons); err != nil {
		return fmt.Errorf("failed to send payment prompt: %w", err)
	}

	session.State = StateSelectingMpesaPhone
	return b.Session.Set(ctx, phone, session, 7200)
}

func (b *BotService) handleMpesaCheckout(ctx context.Context, phone string, session *core.Session) error {
	total, err := b.checkoutTotal(ctx, session)
	if err != nil {
		return b.WhatsApp.SendText(ctx, phone, "Your cart is empty. Please add items first.")
	}

	return b.sendMpesaPhonePrompt(ctx, phone, session, total)
}

// handleCheckout initiates the checkout process by asking for fulfilment first.
func (b *BotService) handleCheckout(ctx context.Context, phone string, session *core.Session) error {
	order, err := b.checkoutPendingOrder(ctx, session)
	if err != nil {
		return err
	}
	if order != nil {
		return b.sendPendingOrderOptions(ctx, phone, session, order)
	}

	return b.sendFulfillmentPrompt(ctx, phone, session)
}

func (b *BotService) handleSelectingFulfillment(ctx context.Context, phone string, session *core.Session, message string) error {
	switch strings.ToLower(strings.TrimSpace(message)) {
	case "fulfillment_pickup":
		session.FulfillmentType = string(core.FulfillmentTypePickup)
		session.DeliveryZoneID = ""
		session.DeliveryZoneName = ""
		session.DeliveryFee = 0
		session.DeliveryAddress = ""
		if cartRequiresPrescription(session.Cart) {
			order, err := b.createPendingOrderFromCart(ctx, phone, session, phone, string(core.PaymentMethodMpesa), core.OrderStatusPendingReview)
			if err != nil {
				return b.WhatsApp.SendText(ctx, phone, "We could not start your review. Please try again.")
			}
			session.PendingPrescriptionOrderID = order.ID
			session.PendingOrderID = order.ID
			session.State = StateWaitingForPrescription
			if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
				return err
			}
			return b.WhatsApp.SendText(ctx, phone, "This order includes prescription medicine. Please reply with a prescription image or PDF for pharmacist review.")
		}
		return b.sendPaymentMethodPrompt(ctx, phone, session)
	case "fulfillment_delivery":
		session.FulfillmentType = string(core.FulfillmentTypeDelivery)
		return b.sendDeliveryZonePrompt(ctx, phone, session)
	default:
		return b.sendFulfillmentPrompt(ctx, phone, session)
	}
}

func (b *BotService) handleSelectingDeliveryZone(ctx context.Context, phone string, session *core.Session, message string) error {
	if b.DeliveryZoneRepo == nil {
		return b.WhatsApp.SendText(ctx, phone, "Delivery is unavailable right now. Please choose pickup.")
	}
	zone, err := b.DeliveryZoneRepo.GetByID(ctx, strings.TrimSpace(message))
	if err != nil || zone == nil {
		return b.sendDeliveryZonePrompt(ctx, phone, session)
	}
	session.DeliveryZoneID = zone.ID
	session.DeliveryZoneName = zone.Name
	session.DeliveryFee = zone.Fee
	session.State = StateWaitingForAddress
	if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
		return err
	}
	return b.WhatsApp.SendText(ctx, phone, fmt.Sprintf("Send your delivery address and landmark for *%s* (delivery fee KES %.0f).", zone.Name, zone.Fee))
}

func (b *BotService) handleDeliveryAddress(ctx context.Context, phone string, session *core.Session, message string) error {
	address := strings.TrimSpace(message)
	if address == "" {
		return b.WhatsApp.SendText(ctx, phone, "Please send your delivery address and landmark.")
	}
	session.DeliveryAddress = address
	if cartRequiresPrescription(session.Cart) {
		order, err := b.createPendingOrderFromCart(ctx, phone, session, phone, string(core.PaymentMethodMpesa), core.OrderStatusPendingReview)
		if err != nil {
			return b.WhatsApp.SendText(ctx, phone, "We could not start your review. Please try again.")
		}
		session.PendingPrescriptionOrderID = order.ID
		session.PendingOrderID = order.ID
		session.State = StateWaitingForPrescription
		if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
			return err
		}
		return b.WhatsApp.SendText(ctx, phone, "This order includes prescription medicine. Please reply with a prescription image or PDF for pharmacist review.")
	}
	return b.sendPaymentMethodPrompt(ctx, phone, session)
}

func (b *BotService) handlePrescriptionUpload(ctx context.Context, phone string, session *core.Session, message string, messageType string) error {
	orderID := strings.TrimSpace(session.PendingPrescriptionOrderID)
	if orderID == "" {
		orderID = strings.TrimSpace(session.PendingOrderID)
	}
	if orderID == "" {
		session.State = StateStart
		_ = b.Session.Set(ctx, phone, session, 7200)
		return b.WhatsApp.SendText(ctx, phone, "No order is waiting for prescription review. Please start a new order.")
	}
	if messageType != "image" && messageType != "document" {
		return b.WhatsApp.SendText(ctx, phone, "Please reply with the prescription image or PDF in this chat.")
	}
	if b.PrescriptionRepo == nil {
		return b.WhatsApp.SendText(ctx, phone, "Prescription uploads are unavailable right now. Please try again later.")
	}
	if err := b.PrescriptionRepo.Create(ctx, &core.Prescription{
		ID:            uuid.New().String(),
		OrderID:       orderID,
		CustomerPhone: phone,
		MediaID:       strings.TrimSpace(message),
		MediaType:     messageType,
		Status:        core.PrescriptionStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}); err != nil {
		return b.WhatsApp.SendText(ctx, phone, "We could not save the prescription. Please try again.")
	}
	session.Cart = []core.CartItem{}
	session.PendingPrescriptionOrderID = ""
	session.State = StateStart
	if err := b.Session.Set(ctx, phone, session, 7200); err != nil {
		return err
	}
	return b.WhatsApp.SendText(ctx, phone, "Prescription received. A pharmacist will review it and send the next step here on WhatsApp.")
}

// handlePaySelf handles when user chooses to use their own WhatsApp number
func (b *BotService) handlePaySelf(ctx context.Context, phone string, session *core.Session) error {
	// Use the WhatsApp phone number
	return b.processPayment(ctx, phone, session, phone)
}

// handlePayOther handles when user chooses to use a different number
func (b *BotService) handlePayOther(ctx context.Context, phone string, session *core.Session) error {
	// Prompt for phone number
	promptMsg := "Please type the Safaricom M-Pesa number you want to use (e.g., 0712345678)."

	if err := b.WhatsApp.SendText(ctx, phone, promptMsg); err != nil {
		return fmt.Errorf("failed to send phone prompt: %w", err)
	}

	// Set state to wait for phone input
	session.State = StateWaitingForPaymentPhone
	return b.Session.Set(ctx, phone, session, 7200)
}

func (b *BotService) createPendingOrderFromCart(ctx context.Context, whatsappPhone string, session *core.Session, paymentPhone string, paymentMethod string, status core.OrderStatus) (*core.Order, error) {
	if len(session.Cart) == 0 {
		return nil, fmt.Errorf("cart is empty")
	}

	user, err := b.UserRepo.GetOrCreateByPhone(ctx, whatsappPhone)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create user: %w", err)
	}

	items := make([]core.PendingOrderItemInput, 0, len(session.Cart))
	for _, cartItem := range session.Cart {
		items = append(items, core.PendingOrderItemInput{
			ProductID: cartItem.ProductID,
			Quantity:  cartItem.Quantity,
		})
	}

	fulfillmentType := strings.TrimSpace(session.FulfillmentType)
	if fulfillmentType == "" {
		fulfillmentType = string(core.FulfillmentTypePickup)
	}

	tableNumber := "PICKUP"
	if fulfillmentType == string(core.FulfillmentTypeDelivery) {
		tableNumber = "DELIVERY"
	}

	order, _, err := b.OrderRepo.CreatePendingOrder(ctx, core.CreatePendingOrderInput{
		UserID:              user.ID,
		CustomerPhone:       paymentPhone,
		TableNumber:         tableNumber,
		PaymentMethod:       paymentMethod,
		IdempotencyKey:      "",
		ExpiresAt:           time.Now().Add(20 * time.Minute),
		Items:               items,
		FulfillmentType:     fulfillmentType,
		DeliveryZoneID:      strings.TrimSpace(session.DeliveryZoneID),
		DeliveryFee:         session.DeliveryFee,
		DeliveryAddress:     strings.TrimSpace(session.DeliveryAddress),
		DeliveryContactName: strings.TrimSpace(session.DeliveryContactName),
		DeliveryNotes:       strings.TrimSpace(session.DeliveryNotes),
		ReviewRequired:      cartRequiresPrescription(session.Cart),
		Status:              status,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create order: %w", err)
	}

	session.PendingOrderID = order.ID
	return order, nil
}

func (b *BotService) sendPesapalLink(ctx context.Context, whatsappPhone string, order *core.Order, session *core.Session) error {
	if b.Pesapal == nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Airtel Money/Card is currently unavailable. Please use M-Pesa.")
	}

	result, err := b.Pesapal.InitiatePayment(ctx, paymentadapter.PesapalInitiateInput{
		OrderID:     order.ID,
		Amount:      order.TotalAmount,
		Description: fmt.Sprintf("Order %s at Maya's Pharm", order.PickupCode),
		Phone:       order.CustomerPhone,
	})
	if err != nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Airtel Money/Card is unavailable right now. Please try again or use M-Pesa.")
	}

	paymentRef := strings.TrimSpace(result.OrderTrackingID)
	if paymentRef == "" {
		paymentRef = strings.TrimSpace(result.MerchantReference)
	}
	if err := b.OrderRepo.UpdatePaymentDetails(ctx, order.ID, pesapalAttemptProvider, paymentRef); err != nil {
		return fmt.Errorf("failed to save Pesapal payment details: %w", err)
	}

	session.PendingOrderID = order.ID
	session.Cart = []core.CartItem{}
	session.State = StateStart
	if err := b.Session.Set(ctx, whatsappPhone, session, 7200); err != nil {
		return fmt.Errorf("failed to save Pesapal checkout state: %w", err)
	}

	message := fmt.Sprintf(
		"Tap this secure payment link to complete payment with Airtel Money or Card:\n%s\n\nWe’ll confirm your order once payment succeeds.\n\nIf you previously requested an M-Pesa prompt, ignore it.",
		result.RedirectURL,
	)
	return b.WhatsApp.SendText(ctx, whatsappPhone, message)
}

func (b *BotService) handlePesapalCheckout(ctx context.Context, whatsappPhone string, session *core.Session) error {
	if b.Pesapal == nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Airtel Money/Card is currently unavailable. Please use M-Pesa.")
	}

	orderStatus := core.OrderStatusPending
	if cartRequiresPrescription(session.Cart) {
		orderStatus = core.OrderStatusPendingReview
	}
	order, err := b.createPendingOrderFromCart(ctx, whatsappPhone, session, whatsappPhone, pesapalAttemptProvider, orderStatus)
	if err != nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Your cart is empty. Please add items first.")
	}

	return b.sendPesapalLink(ctx, whatsappPhone, order, session)
}

// handlePaymentPhoneInput handles user input when waiting for alternative payment phone
func (b *BotService) handlePaymentPhoneInput(ctx context.Context, phone string, session *core.Session, message string) error {
	// Normalize and validate the phone number
	normalizedPhone, err := normalizePhone(message)
	if err != nil || !isValidKenyanMobile(normalizedPhone) {
		// Invalid phone number - ask to try again (keep state)
		errorMsg := "That doesn't look like a valid phone number. Please try again (e.g., 0712345678)."
		return b.WhatsApp.SendText(ctx, phone, errorMsg)
	}

	// Process payment with the normalized phone
	return b.processPayment(ctx, phone, session, normalizedPhone)
}

// handleRetryPayment handles the Retry Payment action for existing orders.
// It supports both pending retries and immediate retries for failed payments.
func (b *BotService) handleRetryPayment(ctx context.Context, whatsappPhone string, session *core.Session, orderID string) error {
	// Fetch the existing order
	order, err := b.OrderRepo.GetByID(ctx, orderID)
	if err != nil {
		b.WhatsApp.SendText(ctx, whatsappPhone, "Order not found. Please start a new order.")
		return nil
	}

	// PAID workflow statuses should not be retried.
	if order.Status == core.OrderStatusPaid ||
		order.Status == core.OrderStatusPreparing ||
		order.Status == core.OrderStatusReady ||
		order.Status == core.OrderStatusCompleted {
		b.WhatsApp.SendText(ctx, whatsappPhone, "This order has already been processed.")
		return nil
	}

	// Failed orders can be retried immediately.
	skipCourtesy := order.Status == core.OrderStatusFailed

	if !skipCourtesy {
		// Courtesy window: brief pause before the next STK attempt.
		if err := waitForSTKCourtesyWindow(ctx); err != nil {
			return nil
		}
	}

	// Re-initiate STK Push to the payment phone (SILENT - no confirmation message)
	if b.PaymentService != nil {
		_, err = b.PaymentService.QueueMPESA(ctx, orderID, order.CustomerPhone, "")
	} else {
		if order.Status == core.OrderStatusFailed {
			if err := b.OrderRepo.UpdateStatus(ctx, orderID, core.OrderStatusPending); err != nil {
				b.WhatsApp.SendText(ctx, whatsappPhone, "⚠️ Couldn't retry payment right now. Please try again.")
				return nil
			}
		}
		err = b.Payment.InitiateSTKPush(ctx, orderID, order.CustomerPhone, order.TotalAmount)
	}
	if err != nil {
		// Send error message - safe because no STK push was sent
		b.WhatsApp.SendText(ctx, whatsappPhone, "⚠️ Payment system busy. Please try again in a moment.")
		return nil
	}

	// SAFETY NET: Launch goroutine to check order status after 45 seconds
	// Note: M-Pesa STK prompts can take 20-40 seconds to arrive, so we wait longer
	go func(oID string, waPhone string) {
		time.Sleep(45 * time.Second)

		checkCtx := context.Background()
		order, err := b.OrderRepo.GetByID(checkCtx, oID)
		if err != nil {
			return
		}

		if order.Status == core.OrderStatusPending {
			// Order still pending - send retry button again
			timeoutMsg := "⏳ *Waiting for M-Pesa*\n\n" +
				"The payment prompt can take up to 60 seconds to appear.\n\n" +
				"*If it hasn't appeared yet:*\n" +
				"• Check your phone for the M-Pesa prompt\n" +
				"• Make sure you have network signal\n" +
				"• Tap 'Retry' below if needed\n\n" +
				"_If you already completed payment, please wait for confirmation._"
			buttons := []core.Button{
				{
					ID:    "retry_pay_" + oID,
					Title: "Retry Payment",
				},
				{
					ID:    "switch_pesapal_" + oID,
					Title: "Airtel Money/Card",
				},
			}
			b.WhatsApp.SendMenuButtons(checkCtx, waPhone, timeoutMsg, buttons)
		}
	}(orderID, whatsappPhone)

	return nil
}

func (b *BotService) startRetryWithDifferentNumber(ctx context.Context, whatsappPhone string, orderID string) error {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Order not found. Please start a new order.")
	}

	order, err := b.OrderRepo.GetByID(ctx, orderID)
	if err != nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Order not found. Please start a new order.")
	}

	if order.Status == core.OrderStatusPaid ||
		order.Status == core.OrderStatusPreparing ||
		order.Status == core.OrderStatusReady ||
		order.Status == core.OrderStatusCompleted {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "This order has already been processed.")
	}

	session, err := b.Session.Get(ctx, whatsappPhone)
	if err != nil {
		session = newConversationSession()
	}

	session.PendingOrderID = orderID
	session.PendingRetryOrderID = orderID
	session.State = StateWaitingForRetryPhone
	if err := b.Session.Set(ctx, whatsappPhone, session, 7200); err != nil {
		return fmt.Errorf("failed to save retry session: %w", err)
	}

	promptMsg := "Please type the Safaricom M-Pesa number you want to use (e.g., 0712345678)."
	return b.WhatsApp.SendText(ctx, whatsappPhone, promptMsg)
}

func (b *BotService) handleSwitchPendingOrderToPesapal(ctx context.Context, whatsappPhone string, orderID string) error {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Order not found. Please start a new order.")
	}

	order, err := b.OrderRepo.GetByID(ctx, orderID)
	if err != nil || order == nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Order not found. Please start a new order.")
	}

	if isPaidWorkflowStatus(order.Status) || order.Status == core.OrderStatusExpired {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "This order has already been processed.")
	}

	session, err := b.Session.Get(ctx, whatsappPhone)
	if err != nil {
		session = newConversationSession()
	}
	session.PendingOrderID = orderID

	return b.sendPesapalLink(ctx, whatsappPhone, order, session)
}

func (b *BotService) handleRetryCancel(ctx context.Context, whatsappPhone string, orderID string) error {
	session, err := b.Session.Get(ctx, whatsappPhone)
	if err != nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "Payment retry cancelled.")
	}

	if strings.TrimSpace(session.PendingRetryOrderID) == strings.TrimSpace(orderID) {
		session.PendingRetryOrderID = ""
	}
	session.State = StateStart

	if err := b.Session.Set(ctx, whatsappPhone, session, 7200); err != nil {
		return fmt.Errorf("failed to save retry cancel state: %w", err)
	}

	return b.WhatsApp.SendText(ctx, whatsappPhone, "Payment retry cancelled.")
}

func (b *BotService) handleRetryPaymentPhoneInput(ctx context.Context, whatsappPhone string, session *core.Session, message string) error {
	normalizedPhone, err := normalizePhone(message)
	if err != nil || !isValidKenyanMobile(normalizedPhone) {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "That doesn't look like a valid phone number. Please try again (e.g., 0712345678).")
	}

	orderID := strings.TrimSpace(session.PendingRetryOrderID)
	if orderID == "" {
		orderID = strings.TrimSpace(session.PendingOrderID)
	}
	if orderID == "" {
		session.State = StateStart
		_ = b.Session.Set(ctx, whatsappPhone, session, 7200)
		return b.WhatsApp.SendText(ctx, whatsappPhone, "No order found to retry. Please start a new order.")
	}

	if err := b.OrderRepo.UpdateCustomerPhone(ctx, orderID, normalizedPhone); err != nil {
		return b.WhatsApp.SendText(ctx, whatsappPhone, "⚠️ Couldn't update payment number right now. Please try again.")
	}

	session.PendingRetryOrderID = ""
	session.PendingOrderID = orderID
	session.State = StateStart
	if err := b.Session.Set(ctx, whatsappPhone, session, 7200); err != nil {
		return fmt.Errorf("failed to save retry phone state: %w", err)
	}

	return b.handleRetryPayment(ctx, whatsappPhone, session, orderID)
}

// processPayment creates the order and initiates STK push
// SILENT CHECKOUT: No WhatsApp messages are sent during STK push to prevent iPhone UI freeze
func (b *BotService) processPayment(ctx context.Context, whatsappPhone string, session *core.Session, paymentPhone string) error {
	order, err := b.createPendingOrderFromCart(ctx, whatsappPhone, session, paymentPhone, string(core.PaymentMethodMpesa), core.OrderStatusPending)
	if err != nil {
		return err
	}

	// CRITICAL: Store pending order ID in session for duplicate checkout prevention
	session.PendingOrderID = order.ID

	// Courtesy window: brief pause before queueing the STK prompt.
	if err := waitForSTKCourtesyWindow(ctx); err != nil {
		return err
	}

	// Initiate STK Push to the payment phone
	// SILENT MODE: No success message is sent - this prevents iPhone UI freeze
	if b.PaymentService != nil {
		_, err = b.PaymentService.QueueMPESA(ctx, order.ID, paymentPhone, "")
	} else {
		err = b.Payment.InitiateSTKPush(ctx, order.ID, paymentPhone, order.TotalAmount)
	}
	if err != nil {
		// If queueing fails (system busy), update order status to FAILED and clear pending ID
		b.OrderRepo.UpdateStatus(ctx, order.ID, core.OrderStatusFailed)
		session.PendingOrderID = ""
		b.Session.Set(ctx, whatsappPhone, session, 7200)
		// Send error message - safe because no STK push was sent to freeze the phone
		b.WhatsApp.SendText(ctx, whatsappPhone, "⚠️ Payment system busy. Please try again in a moment.")
		return fmt.Errorf("failed to initiate STK push: %w", err)
	}

	// Clear cart and reset state, but KEEP PendingOrderID until payment is processed
	session.Cart = []core.CartItem{}
	session.State = "START"
	b.Session.Set(ctx, whatsappPhone, session, 7200)

	// SAFETY NET: Launch goroutine to check order status after 45 seconds
	// If order is still PENDING, send a Retry button to the user
	// Note: M-Pesa STK prompts can take 20-40 seconds to arrive, so we wait longer
	go func(oID string, waPhone string) {
		time.Sleep(45 * time.Second)

		// Check if order is still PENDING
		checkCtx := context.Background()
		order, err := b.OrderRepo.GetByID(checkCtx, oID)
		if err != nil {
			return // Order not found or error, skip
		}

		if order.Status == core.OrderStatusPending {
			// Order still pending after 45 seconds - send retry button
			timeoutMsg := "⏳ *Waiting for M-Pesa*\n\n" +
				"The payment prompt can take up to 60 seconds to appear.\n\n" +
				"*If it hasn't appeared yet:*\n" +
				"• Check your phone for the M-Pesa prompt\n" +
				"• Make sure you have network signal\n" +
				"• Tap 'Retry' below if needed\n\n" +
				"_If you already completed payment, please wait for confirmation._"
			buttons := []core.Button{
				{
					ID:    "retry_pay_" + oID,
					Title: "Retry Payment",
				},
				{
					ID:    "switch_pesapal_" + oID,
					Title: "Airtel Money/Card",
				},
			}
			b.WhatsApp.SendMenuButtons(checkCtx, waPhone, timeoutMsg, buttons)
		}
	}(order.ID, whatsappPhone)

	return nil
}

// normalizePhone normalizes a Kenyan phone number to +254xxxxxxxxx format
// Supports: 07..., 01..., 254..., +254..., 7..., 1...
func normalizePhone(phone string) (string, error) {
	// Remove spaces and dashes
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.TrimSpace(phone)

	// Remove leading +
	phone = strings.TrimPrefix(phone, "+")

	// Handle different formats
	if strings.HasPrefix(phone, "254") {
		// Already in 254xxxxxxxxx format
		if len(phone) == 12 {
			return "+" + phone, nil
		}
		return "", fmt.Errorf("invalid phone number format")
	} else if strings.HasPrefix(phone, "07") || strings.HasPrefix(phone, "01") {
		// 07xxxxxxxx or 01xxxxxxxx -> +2547xxxxxxxx or +2541xxxxxxxx
		if len(phone) == 10 {
			return "+254" + phone[1:], nil
		}
		return "", fmt.Errorf("invalid phone number format")
	} else if strings.HasPrefix(phone, "7") || strings.HasPrefix(phone, "1") {
		// 7xxxxxxxx or 1xxxxxxxx -> +2547xxxxxxxx or +2541xxxxxxxx
		if len(phone) == 9 {
			return "+254" + phone, nil
		}
		return "", fmt.Errorf("invalid phone number format")
	}

	return "", fmt.Errorf("unsupported phone number format")
}

// isValidKenyanMobile validates that a normalized phone starts with +2547 or +2541
func isValidKenyanMobile(normalizedPhone string) bool {
	return strings.HasPrefix(normalizedPhone, "+2547") || strings.HasPrefix(normalizedPhone, "+2541")
}

func waitForSTKCourtesyWindow(ctx context.Context) error {
	timer := time.NewTimer(stkPushCourtesyDelay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
