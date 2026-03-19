package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	paymentadapter "github.com/philia-technologies/mayas-pharm/internal/adapters/payment"
	"github.com/philia-technologies/mayas-pharm/internal/core"
)

func TestSelectionInstructionsText(t *testing.T) {
	want := "\n*Reply with item number*\nExample: 6\n\n*For multiple items*\n1,6\n\n*For quantities*\n1x2, 6x2"

	if got := selectionInstructionsText(); got != want {
		t.Fatalf("selectionInstructionsText() = %q, want %q", got, want)
	}
}

func TestCheckoutPromptText(t *testing.T) {
	want := "Your total is *KES 750*.\n\nWhich M-Pesa number should we charge?\n\nM-PESA prompt coming shortly...\nIf your *iOS version* is not up to date, kindly go to your Home Screen *NOW* to complete your payment"

	if got := checkoutPromptText(750); got != want {
		t.Fatalf("checkoutPromptText() = %q, want %q", got, want)
	}
}

func TestPaymentMethodPromptText(t *testing.T) {
	want := "Your total is *KES 750*.\n\nChoose a payment method:"

	if got := paymentMethodPromptText(750); got != want {
		t.Fatalf("paymentMethodPromptText() = %q, want %q", got, want)
	}
}

func TestSendBulkConfirmPromptFormatsPreviewAndButtons(t *testing.T) {
	service := NewBotService(&botTestProductRepo{}, newBotTestSessionRepo(), &botTestWhatsApp{}, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})
	whatsApp := &botTestWhatsApp{}
	service.WhatsApp = whatsApp

	session := &core.Session{
		PendingResolvedItems: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Coca-Cola (Soda)", Price: 150},
			{ProductID: "p2", Quantity: 3, Name: "Fanta Blackcurrant (Soda)", Price: 150},
			{ProductID: "p3", Quantity: 7, Name: "Fanta Passion (Soda)", Price: 150},
		},
	}

	if err := service.sendBulkConfirmPrompt(context.Background(), "254700000009", session); err != nil {
		t.Fatalf("sendBulkConfirmPrompt() error = %v", err)
	}

	if len(whatsApp.menuButtonTexts) != 1 {
		t.Fatalf("SendMenuButtons() calls = %d, want 1", len(whatsApp.menuButtonTexts))
	}

	wantText := "confirm your order\n\n1. Coca-Cola (Soda) x2 = KES 300\n2. Fanta Blackcurrant (Soda) x3 = KES 450\n3. Fanta Passion (Soda) x7 = KES 1050\n\nTOTAL=KES 1800"
	if got := whatsApp.menuButtonTexts[0]; got != wantText {
		t.Fatalf("confirm preview = %q, want %q", got, wantText)
	}

	wantTitles := []string{"Add more", "Cancel", "\u2705 Confirm"}
	if len(whatsApp.menuButtons) != 1 {
		t.Fatalf("button sets = %d, want 1", len(whatsApp.menuButtons))
	}
	for i, want := range wantTitles {
		if got := whatsApp.menuButtons[0][i].Title; got != want {
			t.Fatalf("button %d title = %q, want %q", i, got, want)
		}
	}
}

func TestHandleIncomingMessage_NewConversationShowsWelcomeMenu(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Cocktails": {},
			"Shots":     {},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	whatsApp := &botTestWhatsApp{}

	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000001", "margarita", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if repo.searchCalls != 0 {
		t.Fatalf("SearchProducts() called %d times, want 0", repo.searchCalls)
	}
	if len(whatsApp.categoryPrompts) != 1 {
		t.Fatalf("SendCategoryListWithText() calls = %d, want 1", len(whatsApp.categoryPrompts))
	}
	if got := whatsApp.categoryPrompts[0]; got != welcomeMenuPrompt {
		t.Fatalf("welcome prompt = %q, want %q", got, welcomeMenuPrompt)
	}

	session, err := sessionRepo.Get(context.Background(), "254700000001")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateBrowsing {
		t.Fatalf("session state = %s, want %s", session.State, StateBrowsing)
	}
}

func TestHandleIncomingMessage_GreetingResetsActiveSession(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Cocktails": {},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000002"] = &core.Session{
		State:            StateConfirmOrder,
		CurrentCategory:  "Cocktails",
		CurrentProductID: "old-product",
		Cart: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Negroni", Price: 900},
		},
		PendingResolvedItems: []core.CartItem{
			{ProductID: "p2", Quantity: 1, Name: "Mojito", Price: 850},
		},
	}
	whatsApp := &botTestWhatsApp{}

	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000002", "niaje boss", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	session, err := sessionRepo.Get(context.Background(), "254700000002")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateBrowsing {
		t.Fatalf("session state = %s, want %s", session.State, StateBrowsing)
	}
	if len(session.Cart) != 0 {
		t.Fatalf("cart length = %d, want 0", len(session.Cart))
	}
	if session.CurrentCategory != "" {
		t.Fatalf("current category = %q, want empty", session.CurrentCategory)
	}
	if session.CurrentProductID != "" {
		t.Fatalf("current product ID = %q, want empty", session.CurrentProductID)
	}
	if len(whatsApp.categoryPrompts) != 1 || whatsApp.categoryPrompts[0] != welcomeMenuPrompt {
		t.Fatalf("welcome prompt calls = %#v, want [%q]", whatsApp.categoryPrompts, welcomeMenuPrompt)
	}
}

func TestHandleIncomingMessage_StartStateIgnoresSearch(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Cocktails": {},
			"Gin":       {},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000003"] = &core.Session{
		State: StateStart,
		Cart:  []core.CartItem{},
	}
	whatsApp := &botTestWhatsApp{}

	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000003", "martini", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if repo.searchCalls != 0 {
		t.Fatalf("SearchProducts() called %d times, want 0", repo.searchCalls)
	}
	if len(whatsApp.texts) != 0 {
		t.Fatalf("SendText() calls = %#v, want none", whatsApp.texts)
	}
	if len(whatsApp.categoryPrompts) != 1 || whatsApp.categoryPrompts[0] != welcomeMenuPrompt {
		t.Fatalf("welcome prompt calls = %#v, want [%q]", whatsApp.categoryPrompts, welcomeMenuPrompt)
	}
}

func TestHandleIncomingMessage_BrowsingCategoryStillShowsProducts(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Cocktails": {
				{ID: "p2", Name: "Mojito", Price: 850, Category: "Cocktails"},
				{ID: "p1", Name: "Negroni", Price: 900, Category: "Cocktails"},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000004"] = &core.Session{
		State: StateBrowsing,
		Cart:  []core.CartItem{},
	}
	whatsApp := &botTestWhatsApp{}

	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000004", "Cocktails", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	if !strings.Contains(whatsApp.texts[0], "Products in *Cocktails*:") {
		t.Fatalf("product list text = %q, want category heading", whatsApp.texts[0])
	}
	if !strings.Contains(whatsApp.texts[0], "1. Mojito - KES 850") {
		t.Fatalf("product list text = %q, want sorted products", whatsApp.texts[0])
	}
	if !strings.Contains(whatsApp.texts[0], "*Reply with item number*\nExample: 6\n\n*For multiple items*\n1,6\n\n*For quantities*\n1x2, 6x2") {
		t.Fatalf("product list text = %q, want updated instructions", whatsApp.texts[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700000004")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateSelectingProduct {
		t.Fatalf("session state = %s, want %s", session.State, StateSelectingProduct)
	}
	if session.CurrentCategory != "Cocktails" {
		t.Fatalf("current category = %q, want %q", session.CurrentCategory, "Cocktails")
	}
}

func TestHandleIncomingMessage_WelcomeMenuPaginatesCategories(t *testing.T) {
	repo := &botTestProductRepo{menu: pagedPharmacyMenu()}
	sessionRepo := newBotTestSessionRepo()
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700001001", "hello", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.categoryPrompts) != 1 {
		t.Fatalf("SendCategoryListWithText() calls = %d, want 1", len(whatsApp.categoryPrompts))
	}
	if got := whatsApp.categoryPrompts[0]; !strings.Contains(got, "Page 1 of 2.") {
		t.Fatalf("welcome prompt = %q, want page indicator", got)
	}
	if len(whatsApp.categoryLists) != 1 {
		t.Fatalf("category lists = %d, want 1", len(whatsApp.categoryLists))
	}
	if got := len(whatsApp.categoryLists[0]); got != 9 {
		t.Fatalf("page 1 option count = %d, want 9", got)
	}
	if last := whatsApp.categoryLists[0][len(whatsApp.categoryLists[0])-1]; last != controlMoreCategories {
		t.Fatalf("last option = %q, want %q", last, controlMoreCategories)
	}

	session, err := sessionRepo.Get(context.Background(), "254700001001")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.CurrentCategoryPage != 0 {
		t.Fatalf("current category page = %d, want 0", session.CurrentCategoryPage)
	}
}

func TestHandleIncomingMessage_BrowsingMoreCategoriesShowsNextPage(t *testing.T) {
	repo := &botTestProductRepo{menu: pagedPharmacyMenu()}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700001002"] = &core.Session{State: StateBrowsing}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700001002", controlMoreCategories, "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.categoryPrompts) != 1 {
		t.Fatalf("SendCategoryListWithText() calls = %d, want 1", len(whatsApp.categoryPrompts))
	}
	if got := whatsApp.categoryPrompts[0]; !strings.Contains(got, "Page 2 of 2.") {
		t.Fatalf("prompt = %q, want page 2 indicator", got)
	}
	if len(whatsApp.categoryLists) != 1 {
		t.Fatalf("category lists = %d, want 1", len(whatsApp.categoryLists))
	}
	if !containsString(whatsApp.categoryLists[0], controlPreviousCategories) {
		t.Fatalf("page 2 options = %#v, want previous-page control", whatsApp.categoryLists[0])
	}
	if containsString(whatsApp.categoryLists[0], controlMoreCategories) {
		t.Fatalf("page 2 options = %#v, did not expect next-page control", whatsApp.categoryLists[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700001002")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.CurrentCategoryPage != 1 {
		t.Fatalf("current category page = %d, want 1", session.CurrentCategoryPage)
	}
}

func TestHandleIncomingMessage_CategoryShowsSubcategoryList(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Pain & Fever": {
				{ID: "p1", Name: "Cetamol", Price: 70, Category: "Pain & Fever", DosageForm: "Tablet"},
				{ID: "p2", Name: "Paracetamol Syrup", Price: 180, Category: "Pain & Fever", DosageForm: "Syrup"},
				{ID: "p3", Name: "Voltaren Emulgel", Price: 980, Category: "Pain & Fever", DosageForm: "Gel"},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700001003"] = &core.Session{State: StateBrowsing}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700001003", "Pain & Fever", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 0 {
		t.Fatalf("SendText() calls = %d, want 0", len(whatsApp.texts))
	}
	if len(whatsApp.categoryPrompts) != 1 {
		t.Fatalf("SendCategoryListWithText() calls = %d, want 1", len(whatsApp.categoryPrompts))
	}
	if got := whatsApp.categoryPrompts[0]; got != "Choose a section in *Pain & Fever*:" {
		t.Fatalf("subcategory prompt = %q, want category section prompt", got)
	}
	wantOptions := []string{controlAllProducts, "Tablets & Capsules", "Liquids & Syrups", "Topicals", controlBackToCategories}
	if got := whatsApp.categoryLists[0]; strings.Join(got, "|") != strings.Join(wantOptions, "|") {
		t.Fatalf("subcategory options = %#v, want %#v", got, wantOptions)
	}

	session, err := sessionRepo.Get(context.Background(), "254700001003")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateBrowsingSubcategory {
		t.Fatalf("session state = %s, want %s", session.State, StateBrowsingSubcategory)
	}
	if session.CurrentCategory != "Pain & Fever" {
		t.Fatalf("current category = %q, want %q", session.CurrentCategory, "Pain & Fever")
	}
}

func TestHandleIncomingMessage_SubcategorySelectionFiltersProducts(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Pain & Fever": {
				{ID: "p1", Name: "Cetamol", Price: 70, Category: "Pain & Fever", DosageForm: "Tablet"},
				{ID: "p2", Name: "Paracetamol Syrup", Price: 180, Category: "Pain & Fever", DosageForm: "Syrup"},
				{ID: "p3", Name: "Voltaren Emulgel", Price: 980, Category: "Pain & Fever", DosageForm: "Gel"},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700001004"] = &core.Session{
		State:               StateBrowsingSubcategory,
		CurrentCategory:     "Pain & Fever",
		CurrentCategoryPage: 0,
	}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700001004", "Topicals", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	if !strings.Contains(whatsApp.texts[0], "Products in *Pain & Fever* - *Topicals*:") {
		t.Fatalf("product list = %q, want subcategory heading", whatsApp.texts[0])
	}
	if !strings.Contains(whatsApp.texts[0], "Voltaren Emulgel - KES 980") {
		t.Fatalf("product list = %q, want topical product", whatsApp.texts[0])
	}
	if strings.Contains(whatsApp.texts[0], "Cetamol - KES 70") || strings.Contains(whatsApp.texts[0], "Paracetamol Syrup - KES 180") {
		t.Fatalf("product list = %q, expected only topical products", whatsApp.texts[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700001004")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateSelectingProduct {
		t.Fatalf("session state = %s, want %s", session.State, StateSelectingProduct)
	}
	if session.CurrentSubcategory != "Topicals" {
		t.Fatalf("current subcategory = %q, want %q", session.CurrentSubcategory, "Topicals")
	}
}

func TestHandleIncomingMessage_InteractiveCategorySwitchWhileSelectingProduct(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Shots": {
				{ID: "p1", Name: "B52", Price: 500, Category: "Shots"},
			},
			"Vodka": {
				{ID: "p2", Name: "Absolut", Price: 450, Category: "Vodka"},
				{ID: "p3", Name: "Belvedere", Price: 900, Category: "Vodka"},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000014"] = &core.Session{
		State:           StateSelectingProduct,
		CurrentCategory: "Shots",
		Cart: []core.CartItem{
			{ProductID: "cart-1", Quantity: 1, Name: "Negroni", Price: 900},
		},
		PendingResolvedItems: []core.CartItem{
			{ProductID: "pending-1", Quantity: 2, Name: "Soda", Price: 150},
		},
	}
	whatsApp := &botTestWhatsApp{}

	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000014", "Vodka", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	if !strings.Contains(whatsApp.texts[0], "Products in *Vodka*:") {
		t.Fatalf("product list text = %q, want vodka heading", whatsApp.texts[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700000014")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateSelectingProduct {
		t.Fatalf("session state = %s, want %s", session.State, StateSelectingProduct)
	}
	if session.CurrentCategory != "Vodka" {
		t.Fatalf("current category = %q, want %q", session.CurrentCategory, "Vodka")
	}
	if len(session.Cart) != 1 {
		t.Fatalf("cart length = %d, want 1", len(session.Cart))
	}
	if len(session.PendingResolvedItems) != 1 {
		t.Fatalf("pending item count = %d, want 1", len(session.PendingResolvedItems))
	}
}

func TestHandleIncomingMessage_TextCategoryDoesNotSwitchWhileSelectingProduct(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Shots": {
				{ID: "p1", Name: "B52", Price: 500, Category: "Shots"},
			},
			"Vodka": {
				{ID: "p2", Name: "Absolut", Price: 450, Category: "Vodka"},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000015"] = &core.Session{
		State:           StateSelectingProduct,
		CurrentCategory: "Shots",
	}
	whatsApp := &botTestWhatsApp{}

	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000015", "Vodka", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	if got := whatsApp.texts[0]; got != "Invalid option. Use product numbers only (e.g., 7 or 7x3)." {
		t.Fatalf("error text = %q, want invalid product-number guidance", got)
	}

	session, err := sessionRepo.Get(context.Background(), "254700000015")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.CurrentCategory != "Shots" {
		t.Fatalf("current category = %q, want %q", session.CurrentCategory, "Shots")
	}
}

func TestHandleIncomingMessage_AddMoreReturnsFullMenuAndKeepsPending(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Cocktails": {
				{ID: "p1", Name: "Coca-Cola (Soda)", Price: 150, Category: "Cocktails", StockQuantity: 20},
				{ID: "p2", Name: "Fanta Blackcurrant (Soda)", Price: 150, Category: "Cocktails", StockQuantity: 20},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000010"] = &core.Session{
		State:           StateConfirmBulkAdd,
		CurrentCategory: "Cocktails",
		PendingResolvedItems: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Coca-Cola (Soda)", Price: 150},
		},
	}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000010", "add_more_pending", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 0 {
		t.Fatalf("SendText() calls = %d, want 0", len(whatsApp.texts))
	}
	if len(whatsApp.categoryPrompts) != 1 {
		t.Fatalf("SendCategoryList() calls = %d, want 1", len(whatsApp.categoryPrompts))
	}
	if got := whatsApp.categoryPrompts[0]; got != "Select a category to browse:" {
		t.Fatalf("category prompt = %q, want default category prompt", got)
	}

	session, err := sessionRepo.Get(context.Background(), "254700000010")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateBrowsing {
		t.Fatalf("session state = %s, want %s", session.State, StateBrowsing)
	}
	if len(session.PendingResolvedItems) != 1 {
		t.Fatalf("pending item count = %d, want 1", len(session.PendingResolvedItems))
	}
}

func TestHandleIncomingMessage_AddMorePreservesPendingItemsInNextConfirmation(t *testing.T) {
	repo := &botTestProductRepo{
		menu: map[string][]*core.Product{
			"Cocktails": {
				{ID: "p1", Name: "Coca-Cola (Soda)", Price: 150, Category: "Cocktails", StockQuantity: 20},
				{ID: "p2", Name: "Fanta Blackcurrant (Soda)", Price: 150, Category: "Cocktails", StockQuantity: 20},
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000011"] = &core.Session{
		State:           StateSelectingProduct,
		CurrentCategory: "Cocktails",
		PendingResolvedItems: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Coca-Cola (Soda)", Price: 150},
		},
	}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000011", "2x3", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	session, err := sessionRepo.Get(context.Background(), "254700000011")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateConfirmBulkAdd {
		t.Fatalf("session state = %s, want %s", session.State, StateConfirmBulkAdd)
	}
	if len(session.PendingResolvedItems) != 2 {
		t.Fatalf("pending item count = %d, want 2", len(session.PendingResolvedItems))
	}
	if len(whatsApp.menuButtonTexts) != 1 {
		t.Fatalf("SendMenuButtons() calls = %d, want 1", len(whatsApp.menuButtonTexts))
	}
	if !strings.Contains(whatsApp.menuButtonTexts[0], "1. Coca-Cola (Soda) x2 = KES 300") {
		t.Fatalf("confirm preview = %q, want preserved first item", whatsApp.menuButtonTexts[0])
	}
	if !strings.Contains(whatsApp.menuButtonTexts[0], "2. Fanta Blackcurrant (Soda) x3 = KES 450") {
		t.Fatalf("confirm preview = %q, want added second item", whatsApp.menuButtonTexts[0])
	}
	if !strings.Contains(whatsApp.menuButtonTexts[0], "TOTAL=KES 750") {
		t.Fatalf("confirm preview = %q, want merged total", whatsApp.menuButtonTexts[0])
	}
}

func TestHandleIncomingMessage_ConfirmFromBulkGoesDirectToCheckoutFlow(t *testing.T) {
	repo := &botTestProductRepo{}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000012"] = &core.Session{
		State: StateConfirmBulkAdd,
		PendingResolvedItems: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Coca-Cola (Soda)", Price: 150},
			{ProductID: "p2", Quantity: 3, Name: "Fanta Blackcurrant (Soda)", Price: 150},
		},
		Cart: []core.CartItem{},
	}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000012", "confirm_add", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.menuButtonTexts) != 1 {
		t.Fatalf("SendMenuButtons() calls = %d, want 1", len(whatsApp.menuButtonTexts))
	}
	if got := whatsApp.menuButtonTexts[0]; got != "How should we fulfil this order?" {
		t.Fatalf("checkout prompt = %q, want %q", got, "How should we fulfil this order?")
	}

	if len(whatsApp.menuButtons) != 1 {
		t.Fatalf("button sets = %d, want 1", len(whatsApp.menuButtons))
	}
	if len(whatsApp.menuButtons[0]) != 2 {
		t.Fatalf("checkout button count = %d, want 2", len(whatsApp.menuButtons[0]))
	}
	if whatsApp.menuButtons[0][0].ID != "fulfillment_pickup" || whatsApp.menuButtons[0][1].ID != "fulfillment_delivery" {
		t.Fatalf("checkout buttons = %#v, want fulfillment_pickup/fulfillment_delivery", whatsApp.menuButtons[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700000012")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateSelectingFulfillment {
		t.Fatalf("session state = %s, want %s", session.State, StateSelectingFulfillment)
	}
	if len(session.Cart) != 2 {
		t.Fatalf("cart length = %d, want 2", len(session.Cart))
	}
	if len(session.PendingResolvedItems) != 0 {
		t.Fatalf("pending item count = %d, want 0", len(session.PendingResolvedItems))
	}
}

func TestHandleIncomingMessage_SelectMpesaAfterCheckoutShowsNumberButtons(t *testing.T) {
	repo := &botTestProductRepo{}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000016"] = &core.Session{
		State: StateSelectingPaymentMethod,
		Cart: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Coca-Cola (Soda)", Price: 150},
			{ProductID: "p2", Quantity: 3, Name: "Fanta Blackcurrant (Soda)", Price: 150},
		},
	}
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000016", "pay_mpesa", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.menuButtonTexts) != 1 {
		t.Fatalf("SendMenuButtons() calls = %d, want 1", len(whatsApp.menuButtonTexts))
	}
	if got := whatsApp.menuButtonTexts[0]; got != checkoutPromptText(750) {
		t.Fatalf("mpesa prompt = %q, want %q", got, checkoutPromptText(750))
	}
	if len(whatsApp.menuButtons) != 1 {
		t.Fatalf("button sets = %d, want 1", len(whatsApp.menuButtons))
	}
	if whatsApp.menuButtons[0][0].ID != "pay_self" || whatsApp.menuButtons[0][1].ID != "pay_other" {
		t.Fatalf("checkout buttons = %#v, want pay_self/pay_other", whatsApp.menuButtons[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700000016")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateSelectingMpesaPhone {
		t.Fatalf("session state = %s, want %s", session.State, StateSelectingMpesaPhone)
	}
}

func TestHandleIncomingMessage_SelectPesapalAfterCheckoutSendsLink(t *testing.T) {
	repo := &botTestProductRepo{}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000017"] = &core.Session{
		State: StateSelectingPaymentMethod,
		Cart: []core.CartItem{
			{ProductID: "p1", Quantity: 1, Name: "Ice Cubes (Packet)", Price: 10},
		},
	}
	whatsApp := &botTestWhatsApp{}
	orderRepo := botTestOrderRepo{orders: map[string]*core.Order{}}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, orderRepo, botTestUserRepo{})
	service.SetPesapalGateway(&botTestPesapalGateway{
		redirectURL:       "https://pay.pesapal.com/checkout/order-created",
		orderTrackingID:   "trk_123",
		merchantReference: "order-created",
	})

	if err := service.HandleIncomingMessage("254700000017", "pay_pesapal", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	if !strings.Contains(whatsApp.texts[0], "https://pay.pesapal.com/checkout/order-created") {
		t.Fatalf("pesapal message = %q, want redirect link", whatsApp.texts[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700000017")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateStart {
		t.Fatalf("session state = %s, want %s", session.State, StateStart)
	}
	if session.PendingOrderID != "order-created" {
		t.Fatalf("pending order ID = %q, want %q", session.PendingOrderID, "order-created")
	}
	if len(session.Cart) != 0 {
		t.Fatalf("cart length = %d, want 0", len(session.Cart))
	}
}

func TestHandleIncomingMessage_SwitchPendingOrderToPesapalUsesSameOrder(t *testing.T) {
	repo := &botTestProductRepo{}
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-1": {
				ID:            "order-1",
				Status:        core.OrderStatusPending,
				CustomerPhone: "254700000018",
				PaymentMethod: string(core.PaymentMethodMpesa),
				TotalAmount:   1350,
				PickupCode:    "000321",
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, orderRepo, botTestUserRepo{})
	service.SetPesapalGateway(&botTestPesapalGateway{
		redirectURL:       "https://pay.pesapal.com/checkout/order-1",
		orderTrackingID:   "trk_existing",
		merchantReference: "order-1",
	})

	if err := service.HandleIncomingMessage("254700000018", "switch_pesapal_order-1", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	if !strings.Contains(whatsApp.texts[0], "https://pay.pesapal.com/checkout/order-1") {
		t.Fatalf("switch message = %q, want redirect link", whatsApp.texts[0])
	}

	session, err := sessionRepo.Get(context.Background(), "254700000018")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.PendingOrderID != "order-1" {
		t.Fatalf("pending order ID = %q, want %q", session.PendingOrderID, "order-1")
	}
}

func TestSendCartSummary_NumbersItems(t *testing.T) {
	repo := &botTestProductRepo{}
	sessionRepo := newBotTestSessionRepo()
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, botTestOrderRepo{}, botTestUserRepo{})

	session := &core.Session{
		Cart: []core.CartItem{
			{ProductID: "p1", Quantity: 2, Name: "Coca-Cola (Soda)", Price: 150},
			{ProductID: "p2", Quantity: 3, Name: "Fanta Blackcurrant (Soda)", Price: 150},
			{ProductID: "p3", Quantity: 7, Name: "Fanta Passion (Soda)", Price: 150},
		},
	}

	if err := service.sendCartSummary(context.Background(), "254700000013", session); err != nil {
		t.Fatalf("sendCartSummary() error = %v", err)
	}

	if len(whatsApp.menuButtonTexts) != 1 {
		t.Fatalf("SendMenuButtons() calls = %d, want 1", len(whatsApp.menuButtonTexts))
	}
	got := whatsApp.menuButtonTexts[0]
	if !strings.Contains(got, "1. Coca-Cola (Soda) x2 = KES 300") {
		t.Fatalf("cart summary = %q, missing line 1", got)
	}
	if !strings.Contains(got, "2. Fanta Blackcurrant (Soda) x3 = KES 450") {
		t.Fatalf("cart summary = %q, missing line 2", got)
	}
	if !strings.Contains(got, "3. Fanta Passion (Soda) x7 = KES 1050") {
		t.Fatalf("cart summary = %q, missing line 3", got)
	}
}

func TestHandleIncomingMessage_RetryOtherAsksForPhone(t *testing.T) {
	repo := &botTestProductRepo{}
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-1": {
				ID:            "order-1",
				Status:        core.OrderStatusFailed,
				CustomerPhone: "+254700000099",
				TotalAmount:   1200,
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	whatsApp := &botTestWhatsApp{}
	service := NewBotService(repo, sessionRepo, whatsApp, botTestPaymentGateway{}, orderRepo, botTestUserRepo{})

	if err := service.HandleIncomingMessage("254700000020", "retry_other_order-1", "interactive"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(whatsApp.texts) != 1 {
		t.Fatalf("SendText() calls = %d, want 1", len(whatsApp.texts))
	}
	wantPrompt := "Please type the Safaricom M-Pesa number you want to use (e.g., 0712345678)."
	if whatsApp.texts[0] != wantPrompt {
		t.Fatalf("prompt = %q, want %q", whatsApp.texts[0], wantPrompt)
	}

	session, err := sessionRepo.Get(context.Background(), "254700000020")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateWaitingForRetryPhone {
		t.Fatalf("session state = %s, want %s", session.State, StateWaitingForRetryPhone)
	}
	if session.PendingRetryOrderID != "order-1" {
		t.Fatalf("PendingRetryOrderID = %q, want %q", session.PendingRetryOrderID, "order-1")
	}
}

func TestHandleIncomingMessage_RetryPhoneInputUpdatesPhoneAndRetriesImmediately(t *testing.T) {
	repo := &botTestProductRepo{}
	orderRepo := botTestOrderRepo{
		orders: map[string]*core.Order{
			"order-2": {
				ID:            "order-2",
				Status:        core.OrderStatusFailed,
				CustomerPhone: "+254700000199",
				TotalAmount:   1350,
			},
		},
	}
	sessionRepo := newBotTestSessionRepo()
	sessionRepo.sessions["254700000021"] = &core.Session{
		State:               StateWaitingForRetryPhone,
		PendingRetryOrderID: "order-2",
	}
	whatsApp := &botTestWhatsApp{}
	payment := &botRetryPaymentGateway{}
	service := NewBotService(repo, sessionRepo, whatsApp, payment, orderRepo, botTestUserRepo{})

	start := time.Now()
	if err := service.HandleIncomingMessage("254700000021", "0712345678", "text"); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("retry took too long, expected immediate resend for failed order")
	}

	if len(payment.calls) != 1 {
		t.Fatalf("InitiateSTKPush() calls = %d, want 1", len(payment.calls))
	}
	call := payment.calls[0]
	if call.orderID != "order-2" {
		t.Fatalf("orderID = %q, want %q", call.orderID, "order-2")
	}
	if call.phone != "+254712345678" {
		t.Fatalf("phone = %q, want %q", call.phone, "+254712345678")
	}

	updatedOrder, err := orderRepo.GetByID(context.Background(), "order-2")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedOrder.Status != core.OrderStatusPending {
		t.Fatalf("order status = %s, want %s", updatedOrder.Status, core.OrderStatusPending)
	}
	if updatedOrder.CustomerPhone != "+254712345678" {
		t.Fatalf("customer phone = %q, want %q", updatedOrder.CustomerPhone, "+254712345678")
	}

	session, err := sessionRepo.Get(context.Background(), "254700000021")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.State != StateStart {
		t.Fatalf("session state = %s, want %s", session.State, StateStart)
	}
	if session.PendingRetryOrderID != "" {
		t.Fatalf("PendingRetryOrderID = %q, want empty", session.PendingRetryOrderID)
	}
}

type botTestProductRepo struct {
	menu        map[string][]*core.Product
	searchCalls int
}

func (r *botTestProductRepo) GetByID(ctx context.Context, id string) (*core.Product, error) {
	for _, products := range r.menu {
		for _, product := range products {
			if product.ID == id {
				return product, nil
			}
		}
	}
	return nil, errors.New("not found")
}

func (r *botTestProductRepo) GetByCategory(ctx context.Context, category string) ([]*core.Product, error) {
	return r.menu[category], nil
}

func (r *botTestProductRepo) GetAll(ctx context.Context) ([]*core.Product, error) {
	var products []*core.Product
	for _, group := range r.menu {
		products = append(products, group...)
	}
	return products, nil
}

func (r *botTestProductRepo) GetMenu(ctx context.Context) (map[string][]*core.Product, error) {
	return r.menu, nil
}

func (r *botTestProductRepo) UpdateStock(ctx context.Context, id string, quantity int) error {
	return nil
}

func (r *botTestProductRepo) UpdatePrice(ctx context.Context, id string, price float64) error {
	return nil
}

func (r *botTestProductRepo) SearchProducts(ctx context.Context, query string) ([]*core.Product, error) {
	r.searchCalls++
	return nil, nil
}

type botTestSessionRepo struct {
	sessions map[string]*core.Session
}

func newBotTestSessionRepo() *botTestSessionRepo {
	return &botTestSessionRepo{
		sessions: make(map[string]*core.Session),
	}
}

func (r *botTestSessionRepo) Get(ctx context.Context, phone string) (*core.Session, error) {
	session, ok := r.sessions[phone]
	if !ok {
		return nil, errors.New("not found")
	}
	return cloneBotTestSession(session), nil
}

func (r *botTestSessionRepo) Set(ctx context.Context, phone string, session *core.Session, ttl int) error {
	r.sessions[phone] = cloneBotTestSession(session)
	return nil
}

func (r *botTestSessionRepo) Delete(ctx context.Context, phone string) error {
	delete(r.sessions, phone)
	return nil
}

func (r *botTestSessionRepo) UpdateStep(ctx context.Context, phone string, step string) error {
	session, ok := r.sessions[phone]
	if !ok {
		return errors.New("not found")
	}
	session.State = step
	return nil
}

func (r *botTestSessionRepo) UpdateCart(ctx context.Context, phone string, cartItems string) error {
	return nil
}

func cloneBotTestSession(session *core.Session) *core.Session {
	if session == nil {
		return nil
	}

	clone := *session
	clone.Cart = append([]core.CartItem(nil), session.Cart...)
	clone.PendingResolvedItems = append([]core.CartItem(nil), session.PendingResolvedItems...)
	clone.PendingRawSelections = append([]string(nil), session.PendingRawSelections...)
	clone.PendingSelectionErrors = append([]string(nil), session.PendingSelectionErrors...)
	clone.PendingAmbiguousOptions = append([]core.PendingAmbiguousOption(nil), session.PendingAmbiguousOptions...)
	return &clone
}

func pagedPharmacyMenu() map[string][]*core.Product {
	return map[string][]*core.Product{
		"Pain & Fever":           {},
		"Antibiotics":            {},
		"Allergy":                {},
		"Cough & Cold":           {},
		"Gastro Care":            {},
		"Vitamins & Supplements": {},
		"Dermatology":            {},
		"First Aid":              {},
		"Women's Health":         {},
		"Baby Care":              {},
		"Chronic Care":           {},
		"Eye & Ear":              {},
		"Oral Care":              {},
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type botTestWhatsApp struct {
	texts           []string
	categoryPrompts []string
	categoryLists   [][]string
	menuButtonTexts []string
	menuButtons     [][]core.Button
}

func (w *botTestWhatsApp) SendText(ctx context.Context, phone string, message string) error {
	w.texts = append(w.texts, message)
	return nil
}

func (w *botTestWhatsApp) SendMenu(ctx context.Context, phone string, products []*core.Product) error {
	return nil
}

func (w *botTestWhatsApp) SendCategoryList(ctx context.Context, phone string, categories []string) error {
	return w.SendCategoryListWithText(ctx, phone, "Select a category to browse:", categories)
}

func (w *botTestWhatsApp) SendCategoryListWithText(ctx context.Context, phone string, text string, categories []string) error {
	w.categoryPrompts = append(w.categoryPrompts, text)
	w.categoryLists = append(w.categoryLists, append([]string(nil), categories...))
	return nil
}

func (w *botTestWhatsApp) SendProductList(ctx context.Context, phone string, category string, products []*core.Product) error {
	return nil
}

func (w *botTestWhatsApp) SendMenuButtons(ctx context.Context, phone string, text string, buttons []core.Button) error {
	w.menuButtonTexts = append(w.menuButtonTexts, text)
	w.menuButtons = append(w.menuButtons, append([]core.Button(nil), buttons...))
	return nil
}

type botTestPaymentGateway struct{}

func (botTestPaymentGateway) InitiateSTKPush(ctx context.Context, orderID string, phone string, amount float64) error {
	return nil
}

func (botTestPaymentGateway) VerifyWebhook(ctx context.Context, signature string, payload []byte) bool {
	return true
}

func (botTestPaymentGateway) ProcessWebhook(ctx context.Context, payload []byte) (*core.PaymentWebhook, error) {
	return nil, nil
}

type botTestPesapalGateway struct {
	redirectURL       string
	orderTrackingID   string
	merchantReference string
}

func (g *botTestPesapalGateway) InitiatePayment(ctx context.Context, input paymentadapter.PesapalInitiateInput) (*paymentadapter.PesapalInitiateResult, error) {
	return &paymentadapter.PesapalInitiateResult{
		RedirectURL:       g.redirectURL,
		OrderTrackingID:   g.orderTrackingID,
		MerchantReference: g.merchantReference,
	}, nil
}

type botRetryPaymentGateway struct {
	calls []struct {
		orderID string
		phone   string
		amount  float64
	}
}

func (m *botRetryPaymentGateway) InitiateSTKPush(ctx context.Context, orderID string, phone string, amount float64) error {
	m.calls = append(m.calls, struct {
		orderID string
		phone   string
		amount  float64
	}{
		orderID: orderID,
		phone:   phone,
		amount:  amount,
	})
	return nil
}

func (*botRetryPaymentGateway) VerifyWebhook(ctx context.Context, signature string, payload []byte) bool {
	return true
}

func (*botRetryPaymentGateway) ProcessWebhook(ctx context.Context, payload []byte) (*core.PaymentWebhook, error) {
	return nil, nil
}

type botTestOrderRepo struct {
	orders map[string]*core.Order
}

func (botTestOrderRepo) CreateOrder(ctx context.Context, order *core.Order) error {
	return nil
}

func (r botTestOrderRepo) CreatePendingOrder(ctx context.Context, input core.CreatePendingOrderInput) (*core.Order, bool, error) {
	order := &core.Order{
		ID:            "order-created",
		UserID:        input.UserID,
		CustomerPhone: input.CustomerPhone,
		TableNumber:   input.TableNumber,
		Status:        core.OrderStatusPending,
		PaymentMethod: input.PaymentMethod,
		PickupCode:    "000001",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	for _, item := range input.Items {
		order.Items = append(order.Items, core.OrderItem{
			ID:        "item-" + item.ProductID,
			OrderID:   order.ID,
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		})
	}
	if r.orders != nil {
		r.orders[order.ID] = order
	}
	return order, true, nil
}

func (botTestOrderRepo) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*core.Order, error) {
	return nil, nil
}

func (botTestOrderRepo) GenerateNextPickupCode(ctx context.Context) (string, error) {
	return "0001", nil
}

func (r botTestOrderRepo) GetByID(ctx context.Context, id string) (*core.Order, error) {
	if r.orders == nil {
		return nil, errors.New("not found")
	}
	order, ok := r.orders[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *order
	return &clone, nil
}

func (botTestOrderRepo) GetByUserID(ctx context.Context, userID string) ([]*core.Order, error) {
	return nil, nil
}

func (botTestOrderRepo) GetByPhone(ctx context.Context, phone string) ([]*core.Order, error) {
	return nil, nil
}

func (botTestOrderRepo) GetByDateRangeAndStatuses(ctx context.Context, startTime, endTime time.Time, statuses []core.OrderStatus) ([]*core.Order, error) {
	return nil, nil
}

func (r botTestOrderRepo) UpdateStatus(ctx context.Context, id string, status core.OrderStatus) error {
	if r.orders != nil {
		if order, ok := r.orders[id]; ok {
			order.Status = status
		}
	}
	return nil
}

func (botTestOrderRepo) UpdateStatusWithActor(ctx context.Context, id string, status core.OrderStatus, actorUserID string) error {
	return nil
}

func (r botTestOrderRepo) MarkPaid(ctx context.Context, id string, paymentMethod string, paymentRef string) (*core.Order, bool, error) {
	if r.orders != nil {
		if order, ok := r.orders[id]; ok {
			order.Status = core.OrderStatusPaid
			order.PaymentMethod = paymentMethod
			order.PaymentRef = paymentRef
			return order, true, nil
		}
	}
	return nil, false, errors.New("not found")
}

func (botTestOrderRepo) ExpirePendingOrders(ctx context.Context, now time.Time, limit int) ([]*core.Order, error) {
	return nil, nil
}

func (r botTestOrderRepo) UpdateCustomerPhone(ctx context.Context, id string, phone string) error {
	if r.orders != nil {
		if order, ok := r.orders[id]; ok {
			order.CustomerPhone = phone
		}
	}
	return nil
}

func (r botTestOrderRepo) UpdatePaymentDetails(ctx context.Context, id string, paymentMethod string, paymentRef string) error {
	if r.orders != nil {
		if order, ok := r.orders[id]; ok {
			order.PaymentMethod = paymentMethod
			order.PaymentRef = paymentRef
		}
	}
	return nil
}

func (botTestOrderRepo) GetAllWithFilters(ctx context.Context, status string, limit int, updatedAfter *time.Time) ([]*core.Order, error) {
	return nil, nil
}

func (botTestOrderRepo) GetCompletedHistory(ctx context.Context, pickupCode string, phone string, limit int) ([]*core.Order, error) {
	return nil, nil
}

func (botTestOrderRepo) FindPendingByPhoneAndAmount(ctx context.Context, phone string, amount float64) (*core.Order, error) {
	return nil, errors.New("not found")
}

func (botTestOrderRepo) FindPendingByHashedPhoneAndAmount(ctx context.Context, hashedPhone string, amount float64) (*core.Order, error) {
	return nil, errors.New("not found")
}

func (botTestOrderRepo) FindPendingByAmount(ctx context.Context, amount float64) (*core.Order, error) {
	return nil, errors.New("not found")
}

func (botTestOrderRepo) ClaimPreparing(ctx context.Context, id string, actorUserID string) error {
	return nil
}

func (botTestOrderRepo) ForceTakeoverPreparing(ctx context.Context, id string, actorUserID string) error {
	return nil
}

func (botTestOrderRepo) UnlockPreparing(ctx context.Context, id string) error {
	return nil
}

func (botTestOrderRepo) MarkReadyFromPreparing(ctx context.Context, id string, actorUserID string) error {
	return nil
}

type botTestUserRepo struct{}

func (botTestUserRepo) GetByID(ctx context.Context, id string) (*core.User, error) {
	return nil, errors.New("not found")
}

func (botTestUserRepo) GetByPhone(ctx context.Context, phone string) (*core.User, error) {
	return nil, errors.New("not found")
}

func (botTestUserRepo) Create(ctx context.Context, user *core.User) error {
	return nil
}

func (botTestUserRepo) GetOrCreateByPhone(ctx context.Context, phone string) (*core.User, error) {
	return &core.User{ID: "user-1", PhoneNumber: phone}, nil
}
