package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type scenarioType string

const (
	scenarioOTCPickup     scenarioType = "otc-pickup"
	scenarioOTCDelivery   scenarioType = "otc-delivery"
	scenarioRXPickup      scenarioType = "rx-pickup"
	scenarioMixedDelivery scenarioType = "mixed-delivery"
)

type loadConfig struct {
	baseURL               string
	total                 int
	concurrency           int
	quantity              int
	itemsPerOrder         int
	phoneStart            string
	queueMPESA            bool
	pollStatus            bool
	statusPollDuration    time.Duration
	statusPollInterval    time.Duration
	dashboardClients      int
	dashboardToken        string
	dashboardDuration     time.Duration
	timeout               time.Duration
	scenarioProfile       string
	otcProductIDs         []string
	rxProductIDs          []string
	deliveryZoneIDs       []string
	prescriptionMediaID   string
	prescriptionMediaType string
	prescriptionFileName  string
	prescriptionCaption   string
}

type catalogProduct struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Category             string `json:"category"`
	RequiresPrescription bool   `json:"requires_prescription"`
}

type deliveryZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type catalogSnapshot struct {
	otcProducts     []string
	rxProducts      []string
	deliveryZoneIDs []string
}

type scenarioPlan struct {
	kind                 scenarioType
	items                []map[string]any
	fulfillmentType      string
	tableNumber          string
	deliveryZoneID       string
	deliveryAddress      string
	deliveryContactName  string
	deliveryNotes        string
	requiresPrescription bool
}

type customerOrderResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	ReviewRequired  bool   `json:"review_required"`
	FulfillmentType string `json:"fulfillment_type"`
}

type loadStats struct {
	createdOrders         int64
	deliveryOrders        int64
	prescriptionOrders    int64
	mixedOrders           int64
	uploadedPrescriptions int64
	queuedPayments        int64
	polledStatuses        int64
	dashboardEvents       int64
	createErrors          int64
	prescriptionErrors    int64
	paymentErrors         int64
	statusPollErrors      int64
	dashboardErrors       int64
}

func main() {
	cfg := parseFlags()

	client := &http.Client{Timeout: cfg.timeout}
	snapshot, err := loadCatalogSnapshot(client, cfg)
	if err != nil {
		log.Fatalf("load catalog snapshot: %v", err)
	}

	scenarioCycle := buildScenarioCycle(cfg.scenarioProfile)
	start := time.Now()
	jobs := make(chan int)

	var (
		stats       loadStats
		dashboardWG sync.WaitGroup
		wg          sync.WaitGroup
		latencies   sync.Map
	)

	if cfg.dashboardClients > 0 && cfg.dashboardToken != "" {
		for i := 0; i < cfg.dashboardClients; i++ {
			dashboardWG.Add(1)
			go func(index int) {
				defer dashboardWG.Done()
				if err := runDashboardClient(client, cfg, index, &stats); err != nil {
					atomic.AddInt64(&stats.dashboardErrors, 1)
					log.Printf("dashboard client %d failed: %v", index, err)
				}
			}(i)
		}
	}

	for worker := 0; worker < cfg.concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				runScenario(client, cfg, snapshot, scenarioCycle, index, &stats, &latencies)
			}
		}()
	}

	for i := 0; i < cfg.total; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	dashboardWG.Wait()

	var allLatencies []time.Duration
	latencies.Range(func(_, value any) bool {
		allLatencies = append(allLatencies, value.(time.Duration))
		return true
	})

	log.Printf("load test profile: %s", cfg.scenarioProfile)
	log.Printf("load test finished in %s", time.Since(start))
	log.Printf("orders created: %d / %d", stats.createdOrders, cfg.total)
	log.Printf("delivery orders created: %d", stats.deliveryOrders)
	log.Printf("prescription review orders created: %d", stats.prescriptionOrders)
	log.Printf("mixed carts created: %d", stats.mixedOrders)
	log.Printf("prescriptions uploaded: %d", stats.uploadedPrescriptions)
	log.Printf("payments queued: %d", stats.queuedPayments)
	log.Printf("status polls completed: %d", stats.polledStatuses)
	log.Printf("dashboard SSE clients: %d", cfg.dashboardClients)
	log.Printf("dashboard SSE events observed: %d", stats.dashboardEvents)
	log.Printf("create errors: %d", stats.createErrors)
	log.Printf("prescription upload errors: %d", stats.prescriptionErrors)
	log.Printf("payment errors: %d", stats.paymentErrors)
	log.Printf("status poll errors: %d", stats.statusPollErrors)
	log.Printf("dashboard SSE errors: %d", stats.dashboardErrors)
	if len(allLatencies) > 0 {
		log.Printf("scenario p50: %s", percentile(allLatencies, 0.50))
		log.Printf("scenario p95: %s", percentile(allLatencies, 0.95))
		log.Printf("scenario p99: %s", percentile(allLatencies, 0.99))
	}
}

func parseFlags() loadConfig {
	var (
		otcProductIDsCSV string
		rxProductIDsCSV  string
		deliveryZonesCSV string
	)

	cfg := loadConfig{}
	flag.StringVar(&cfg.baseURL, "base-url", "http://localhost:8080", "Base URL for the API server")
	flag.IntVar(&cfg.total, "orders", 500, "Total number of order scenarios to execute")
	flag.IntVar(&cfg.concurrency, "concurrency", 500, "Maximum number of concurrent workers")
	flag.IntVar(&cfg.quantity, "quantity", 1, "Quantity per item in each order")
	flag.IntVar(&cfg.itemsPerOrder, "items-per-order", 2, "How many products to include in each order")
	flag.StringVar(&cfg.phoneStart, "phone-start", "254700100000", "Starting customer phone in 254xxxxxxxxx format")
	flag.BoolVar(&cfg.queueMPESA, "queue-mpesa", true, "Queue M-Pesa payment attempts after order creation when the order is payable")
	flag.BoolVar(&cfg.pollStatus, "poll-status", true, "Poll order status after create/payment to simulate customer refresh load")
	flag.DurationVar(&cfg.statusPollDuration, "status-poll-duration", 15*time.Second, "Maximum duration to keep polling a customer order")
	flag.DurationVar(&cfg.statusPollInterval, "status-poll-interval", 2*time.Second, "Interval between customer status polls")
	flag.IntVar(&cfg.dashboardClients, "dashboard-clients", 0, "Number of admin SSE clients to attach during the run")
	flag.StringVar(&cfg.dashboardToken, "dashboard-token", "", "Bearer token used by dashboard SSE clients")
	flag.DurationVar(&cfg.dashboardDuration, "dashboard-duration", 45*time.Second, "How long each dashboard SSE client should stay connected")
	flag.DurationVar(&cfg.timeout, "timeout", 20*time.Second, "HTTP client timeout")
	flag.StringVar(&cfg.scenarioProfile, "scenario-profile", "pharmacy-500", "Scenario profile: pharmacy-500, otc-only, prescription-heavy")
	flag.StringVar(&otcProductIDsCSV, "product-ids", "", "Comma-separated OTC product IDs to rotate across orders")
	flag.StringVar(&rxProductIDsCSV, "rx-product-ids", "", "Comma-separated prescription product IDs to rotate across orders")
	flag.StringVar(&deliveryZonesCSV, "delivery-zone-ids", "", "Comma-separated delivery zone IDs to rotate across delivery orders")
	flag.StringVar(&cfg.prescriptionMediaID, "prescription-media-id", "loadtest-prescription-media", "Synthetic media ID used when uploading prescriptions")
	flag.StringVar(&cfg.prescriptionMediaType, "prescription-media-type", "image", "Prescription media type to upload (image or document)")
	flag.StringVar(&cfg.prescriptionFileName, "prescription-file-name", "loadtest-prescription.jpg", "Synthetic file name for prescription uploads")
	flag.StringVar(&cfg.prescriptionCaption, "prescription-caption", "Automated load-test prescription upload", "Caption to include with prescription uploads")
	flag.Parse()

	cfg.otcProductIDs = parseCSV(otcProductIDsCSV)
	cfg.rxProductIDs = parseCSV(rxProductIDsCSV)
	cfg.deliveryZoneIDs = parseCSV(deliveryZonesCSV)

	if cfg.total <= 0 {
		log.Fatal("-orders must be greater than 0")
	}
	if cfg.concurrency <= 0 {
		log.Fatal("-concurrency must be greater than 0")
	}
	if cfg.quantity <= 0 {
		log.Fatal("-quantity must be greater than 0")
	}
	if cfg.itemsPerOrder <= 0 {
		log.Fatal("-items-per-order must be greater than 0")
	}
	if _, err := strconv.ParseInt(strings.TrimSpace(cfg.phoneStart), 10, 64); err != nil {
		log.Fatalf("invalid -phone-start: %v", err)
	}
	cfg.scenarioProfile = strings.ToLower(strings.TrimSpace(cfg.scenarioProfile))
	if cfg.scenarioProfile == "" {
		cfg.scenarioProfile = "pharmacy-500"
	}

	return cfg
}

func parseCSV(input string) []string {
	parts := strings.Split(input, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func loadCatalogSnapshot(client *http.Client, cfg loadConfig) (catalogSnapshot, error) {
	snapshot := catalogSnapshot{
		otcProducts:     append([]string(nil), cfg.otcProductIDs...),
		rxProducts:      append([]string(nil), cfg.rxProductIDs...),
		deliveryZoneIDs: append([]string(nil), cfg.deliveryZoneIDs...),
	}

	needsCatalog := len(snapshot.otcProducts) == 0 || (profileNeedsPrescription(cfg.scenarioProfile) && len(snapshot.rxProducts) == 0)
	if needsCatalog {
		menu, err := fetchMenu(client, cfg.baseURL)
		if err != nil {
			return snapshot, err
		}
		for _, products := range menu {
			for _, product := range products {
				if strings.TrimSpace(product.ID) == "" {
					continue
				}
				if product.RequiresPrescription {
					snapshot.rxProducts = append(snapshot.rxProducts, product.ID)
				} else {
					snapshot.otcProducts = append(snapshot.otcProducts, product.ID)
				}
			}
		}
	}

	if len(snapshot.otcProducts) == 0 {
		return snapshot, fmt.Errorf("no OTC products available for load testing")
	}
	if profileNeedsPrescription(cfg.scenarioProfile) && len(snapshot.rxProducts) == 0 {
		return snapshot, fmt.Errorf("no prescription products available for profile %s", cfg.scenarioProfile)
	}
	if profileNeedsDelivery(cfg.scenarioProfile) && len(snapshot.deliveryZoneIDs) == 0 {
		zones, err := fetchDeliveryZones(client, cfg.baseURL)
		if err != nil {
			return snapshot, err
		}
		for _, zone := range zones {
			if strings.TrimSpace(zone.ID) != "" {
				snapshot.deliveryZoneIDs = append(snapshot.deliveryZoneIDs, zone.ID)
			}
		}
	}
	if profileNeedsDelivery(cfg.scenarioProfile) && len(snapshot.deliveryZoneIDs) == 0 {
		return snapshot, fmt.Errorf("no delivery zones available for profile %s", cfg.scenarioProfile)
	}

	return snapshot, nil
}

func fetchMenu(client *http.Client, baseURL string) (map[string][]catalogProduct, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/menu", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch menu status %d", resp.StatusCode)
	}

	var menu map[string][]catalogProduct
	if err := json.NewDecoder(resp.Body).Decode(&menu); err != nil {
		return nil, err
	}
	return menu, nil
}

func fetchDeliveryZones(client *http.Client, baseURL string) ([]deliveryZone, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/customer/delivery-zones", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch delivery zones status %d", resp.StatusCode)
	}

	var zones []deliveryZone
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return nil, err
	}
	return zones, nil
}

func buildScenarioCycle(profile string) []scenarioType {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "otc-only":
		return []scenarioType{scenarioOTCPickup, scenarioOTCDelivery}
	case "prescription-heavy":
		return []scenarioType{
			scenarioRXPickup, scenarioMixedDelivery, scenarioRXPickup, scenarioOTCDelivery, scenarioMixedDelivery,
		}
	default:
		return []scenarioType{
			scenarioOTCPickup, scenarioOTCPickup, scenarioOTCPickup, scenarioOTCPickup, scenarioOTCPickup,
			scenarioOTCPickup, scenarioOTCPickup, scenarioOTCPickup, scenarioOTCPickup, scenarioOTCPickup,
			scenarioOTCPickup, scenarioOTCDelivery, scenarioOTCDelivery, scenarioOTCDelivery, scenarioOTCDelivery,
			scenarioRXPickup, scenarioRXPickup, scenarioRXPickup,
			scenarioMixedDelivery, scenarioMixedDelivery,
		}
	}
}

func profileNeedsPrescription(profile string) bool {
	for _, scenario := range buildScenarioCycle(profile) {
		if scenario == scenarioRXPickup || scenario == scenarioMixedDelivery {
			return true
		}
	}
	return false
}

func profileNeedsDelivery(profile string) bool {
	for _, scenario := range buildScenarioCycle(profile) {
		if scenario == scenarioOTCDelivery || scenario == scenarioMixedDelivery {
			return true
		}
	}
	return false
}

func runScenario(client *http.Client, cfg loadConfig, snapshot catalogSnapshot, scenarioCycle []scenarioType, index int, stats *loadStats, latencies *sync.Map) {
	start := time.Now()
	phone := incrementPhone(cfg.phoneStart, index)

	plan, err := buildScenarioPlan(cfg, snapshot, scenarioCycle[index%len(scenarioCycle)], index, phone)
	if err != nil {
		atomic.AddInt64(&stats.createErrors, 1)
		log.Printf("build scenario %d failed: %v", index, err)
		return
	}

	order, err := createOrder(client, cfg, index, phone, plan)
	if err != nil {
		atomic.AddInt64(&stats.createErrors, 1)
		log.Printf("create order %d failed: %v", index, err)
		return
	}

	atomic.AddInt64(&stats.createdOrders, 1)
	switch plan.kind {
	case scenarioOTCDelivery:
		atomic.AddInt64(&stats.deliveryOrders, 1)
	case scenarioRXPickup:
		atomic.AddInt64(&stats.prescriptionOrders, 1)
	case scenarioMixedDelivery:
		atomic.AddInt64(&stats.deliveryOrders, 1)
		atomic.AddInt64(&stats.prescriptionOrders, 1)
		atomic.AddInt64(&stats.mixedOrders, 1)
	}

	if plan.requiresPrescription || order.ReviewRequired || strings.EqualFold(order.Status, "PENDING_REVIEW") {
		if err := uploadPrescription(client, cfg, order.ID); err != nil {
			atomic.AddInt64(&stats.prescriptionErrors, 1)
			log.Printf("upload prescription %d failed: %v", index, err)
		} else {
			atomic.AddInt64(&stats.uploadedPrescriptions, 1)
		}
	}

	if cfg.queueMPESA && isPayableStatus(order.Status) {
		if err := queuePayment(client, cfg, index, order.ID, phone); err != nil {
			atomic.AddInt64(&stats.paymentErrors, 1)
			log.Printf("queue payment %d failed: %v", index, err)
		} else {
			atomic.AddInt64(&stats.queuedPayments, 1)
		}
	}

	if cfg.pollStatus {
		if err := pollOrderStatus(client, cfg, order.ID); err != nil {
			atomic.AddInt64(&stats.statusPollErrors, 1)
			log.Printf("status polling %d failed: %v", index, err)
		} else {
			atomic.AddInt64(&stats.polledStatuses, 1)
		}
	}

	latencies.Store(index, time.Since(start))
}

func buildScenarioPlan(cfg loadConfig, snapshot catalogSnapshot, scenario scenarioType, index int, phone string) (scenarioPlan, error) {
	plan := scenarioPlan{
		kind:            scenario,
		fulfillmentType: "PICKUP",
		tableNumber:     "PICKUP",
	}

	switch scenario {
	case scenarioOTCPickup:
		plan.items = buildItems(snapshot.otcProducts, cfg.itemsPerOrder, cfg.quantity, index)
	case scenarioOTCDelivery:
		if len(snapshot.deliveryZoneIDs) == 0 {
			return plan, fmt.Errorf("delivery zone IDs are required for delivery scenarios")
		}
		plan.items = buildItems(snapshot.otcProducts, cfg.itemsPerOrder, cfg.quantity, index)
		plan.fulfillmentType = "DELIVERY"
		plan.tableNumber = "DELIVERY"
		plan.deliveryZoneID = snapshot.deliveryZoneIDs[index%len(snapshot.deliveryZoneIDs)]
		plan.deliveryAddress = fmt.Sprintf("Apartment %03d, Nairobi load-test route", index%200)
		plan.deliveryContactName = fmt.Sprintf("Load Test %03d", index%1000)
		plan.deliveryNotes = "Automated OTC delivery scenario"
	case scenarioRXPickup:
		if len(snapshot.rxProducts) == 0 {
			return plan, fmt.Errorf("prescription product IDs are required for rx scenarios")
		}
		plan.items = buildItems(snapshot.rxProducts, cfg.itemsPerOrder, cfg.quantity, index)
		plan.requiresPrescription = true
	case scenarioMixedDelivery:
		if len(snapshot.rxProducts) == 0 {
			return plan, fmt.Errorf("prescription product IDs are required for mixed scenarios")
		}
		if len(snapshot.deliveryZoneIDs) == 0 {
			return plan, fmt.Errorf("delivery zone IDs are required for mixed delivery scenarios")
		}
		mixedItemCount := max(2, cfg.itemsPerOrder)
		plan.items = append(plan.items, buildItems(snapshot.rxProducts, 1, cfg.quantity, index)...)
		plan.items = append(plan.items, buildItems(snapshot.otcProducts, mixedItemCount-1, cfg.quantity, index+7)...)
		plan.fulfillmentType = "DELIVERY"
		plan.tableNumber = "DELIVERY"
		plan.deliveryZoneID = snapshot.deliveryZoneIDs[index%len(snapshot.deliveryZoneIDs)]
		plan.deliveryAddress = fmt.Sprintf("Office Suite %03d, Nairobi mixed route", index%200)
		plan.deliveryContactName = fmt.Sprintf("Load Test %03d", index%1000)
		plan.deliveryNotes = "Automated mixed Rx/OTC delivery scenario"
		plan.requiresPrescription = true
	default:
		return plan, fmt.Errorf("unsupported scenario %s", scenario)
	}

	return plan, nil
}

func buildItems(productIDs []string, count int, quantity int, startIndex int) []map[string]any {
	items := make([]map[string]any, 0, count)
	for offset := 0; offset < count; offset++ {
		productID := productIDs[(startIndex+offset)%len(productIDs)]
		items = append(items, map[string]any{
			"product_id": productID,
			"quantity":   quantity,
		})
	}
	return items
}

func createOrder(client *http.Client, cfg loadConfig, index int, phone string, plan scenarioPlan) (*customerOrderResponse, error) {
	payload := map[string]any{
		"phone":                 phone,
		"table_number":          plan.tableNumber,
		"fulfillment_type":      plan.fulfillmentType,
		"delivery_zone_id":      plan.deliveryZoneID,
		"delivery_address":      plan.deliveryAddress,
		"delivery_contact_name": plan.deliveryContactName,
		"delivery_notes":        plan.deliveryNotes,
		"items":                 plan.items,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.baseURL, "/")+"/api/customer/orders", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("load-order-%06d", index))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var parsed customerOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return nil, fmt.Errorf("missing order id in response")
	}
	return &parsed, nil
}

func uploadPrescription(client *http.Client, cfg loadConfig, orderID string) error {
	payload := map[string]any{
		"media_id":   cfg.prescriptionMediaID,
		"media_type": cfg.prescriptionMediaType,
		"file_name":  cfg.prescriptionFileName,
		"caption":    cfg.prescriptionCaption,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.baseURL, "/")+"/api/customer/orders/"+orderID+"/prescriptions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func queuePayment(client *http.Client, cfg loadConfig, index int, orderID string, phone string) error {
	payload := map[string]any{
		"phone": phone,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.baseURL, "/")+"/api/customer/orders/"+orderID+"/pay/mpesa", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("load-pay-%06d", index))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func pollOrderStatus(client *http.Client, cfg loadConfig, orderID string) error {
	deadline := time.Now().Add(cfg.statusPollDuration)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.baseURL, "/")+"/api/customer/orders/"+orderID+"/status", nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d", resp.StatusCode)
		}

		time.Sleep(cfg.statusPollInterval)
	}
	return nil
}

func runDashboardClient(client *http.Client, cfg loadConfig, index int, stats *loadStats) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.dashboardDuration)
	defer cancel()

	eventsURL, err := url.Parse(strings.TrimRight(cfg.baseURL, "/") + "/api/admin/events")
	if err != nil {
		return err
	}
	eventsURL.RawQuery = url.Values{"token": []string{cfg.dashboardToken}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL.String(), nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	reader := bufio.NewScanner(resp.Body)
	for reader.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		if strings.HasPrefix(reader.Text(), "event:") {
			atomic.AddInt64(&stats.dashboardEvents, 1)
		}
	}

	if err := reader.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("scan events client %d: %w", index, err)
	}
	return nil
}

func incrementPhone(start string, offset int) string {
	value, _ := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	return fmt.Sprintf("%012d", value+int64(offset))
}

func percentile(latencies []time.Duration, ratio float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sortedDurations := append([]time.Duration(nil), latencies...)
	sort.Slice(sortedDurations, func(i, j int) bool { return sortedDurations[i] < sortedDurations[j] })
	index := int(float64(len(sortedDurations)-1) * ratio)
	return sortedDurations[index]
}

func isPayableStatus(status string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	return normalized == "APPROVED_AWAITING_PAYMENT" || normalized == "PENDING"
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
