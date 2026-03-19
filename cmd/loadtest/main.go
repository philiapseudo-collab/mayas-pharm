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

type loadConfig struct {
	baseURL            string
	productIDs         []string
	total              int
	concurrency        int
	quantity           int
	itemsPerOrder      int
	phoneStart         string
	queueMPESA         bool
	pollStatus         bool
	statusPollDuration time.Duration
	statusPollInterval time.Duration
	dashboardClients   int
	dashboardToken     string
	dashboardDuration  time.Duration
	timeout            time.Duration
}

type orderResponse struct {
	ID string `json:"id"`
}

type loadStats struct {
	createdOrders    int64
	queuedPayments   int64
	polledStatuses   int64
	dashboardEvents  int64
	createErrors     int64
	paymentErrors    int64
	statusPollErrors int64
	dashboardErrors  int64
}

func main() {
	cfg := parseFlags()

	client := &http.Client{Timeout: cfg.timeout}
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
				runScenario(client, cfg, index, &stats, &latencies)
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

	log.Printf("load test finished in %s", time.Since(start))
	log.Printf("orders created: %d / %d", stats.createdOrders, cfg.total)
	log.Printf("payments queued: %d", stats.queuedPayments)
	log.Printf("status polls completed: %d", stats.polledStatuses)
	log.Printf("dashboard SSE clients: %d", cfg.dashboardClients)
	log.Printf("dashboard SSE events observed: %d", stats.dashboardEvents)
	log.Printf("create errors: %d", stats.createErrors)
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
	var productIDsCSV string

	cfg := loadConfig{}
	flag.StringVar(&cfg.baseURL, "base-url", "http://localhost:8080", "Base URL for the API server")
	flag.StringVar(&productIDsCSV, "product-ids", "", "Comma-separated product IDs to rotate across orders")
	flag.IntVar(&cfg.total, "orders", 300, "Total number of order scenarios to execute")
	flag.IntVar(&cfg.concurrency, "concurrency", 50, "Maximum number of concurrent workers")
	flag.IntVar(&cfg.quantity, "quantity", 1, "Quantity per item in each order")
	flag.IntVar(&cfg.itemsPerOrder, "items-per-order", 1, "How many products to include in each order")
	flag.StringVar(&cfg.phoneStart, "phone-start", "254700100000", "Starting customer phone in 254xxxxxxxxx format")
	flag.BoolVar(&cfg.queueMPESA, "queue-mpesa", false, "Queue M-Pesa payment attempts after order creation")
	flag.BoolVar(&cfg.pollStatus, "poll-status", false, "Poll order status after create/payment to simulate customer refresh load")
	flag.DurationVar(&cfg.statusPollDuration, "status-poll-duration", 30*time.Second, "Maximum duration to keep polling a customer order")
	flag.DurationVar(&cfg.statusPollInterval, "status-poll-interval", 3*time.Second, "Interval between customer status polls")
	flag.IntVar(&cfg.dashboardClients, "dashboard-clients", 0, "Number of admin SSE clients to attach during the run")
	flag.StringVar(&cfg.dashboardToken, "dashboard-token", "", "Bearer token used by dashboard SSE clients")
	flag.DurationVar(&cfg.dashboardDuration, "dashboard-duration", 45*time.Second, "How long each dashboard SSE client should stay connected")
	flag.DurationVar(&cfg.timeout, "timeout", 15*time.Second, "HTTP client timeout")
	flag.Parse()

	cfg.productIDs = parseProductIDs(productIDsCSV)
	if len(cfg.productIDs) == 0 {
		log.Fatal("-product-ids is required")
	}
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

	return cfg
}

func parseProductIDs(input string) []string {
	parts := strings.Split(input, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

func runScenario(client *http.Client, cfg loadConfig, index int, stats *loadStats, latencies *sync.Map) {
	start := time.Now()
	phone := incrementPhone(cfg.phoneStart, index)

	orderID, err := createOrder(client, cfg, index, phone)
	if err != nil {
		atomic.AddInt64(&stats.createErrors, 1)
		log.Printf("create order %d failed: %v", index, err)
		return
	}
	atomic.AddInt64(&stats.createdOrders, 1)

	if cfg.queueMPESA {
		if err := queuePayment(client, cfg, index, orderID, phone); err != nil {
			atomic.AddInt64(&stats.paymentErrors, 1)
			log.Printf("queue payment %d failed: %v", index, err)
		} else {
			atomic.AddInt64(&stats.queuedPayments, 1)
		}
	}

	if cfg.pollStatus {
		if err := pollOrderStatus(client, cfg, orderID); err != nil {
			atomic.AddInt64(&stats.statusPollErrors, 1)
			log.Printf("status polling %d failed: %v", index, err)
		} else {
			atomic.AddInt64(&stats.polledStatuses, 1)
		}
	}

	latencies.Store(index, time.Since(start))
}

func createOrder(client *http.Client, cfg loadConfig, index int, phone string) (string, error) {
	payload := map[string]any{
		"phone":        phone,
		"table_number": "PICKUP",
		"items":        buildItems(cfg, index),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.baseURL, "/")+"/api/customer/orders", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("load-order-%06d", index))

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var parsed orderResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", fmt.Errorf("missing order id in response")
	}

	return parsed.ID, nil
}

func buildItems(cfg loadConfig, index int) []map[string]any {
	items := make([]map[string]any, 0, cfg.itemsPerOrder)
	for offset := 0; offset < cfg.itemsPerOrder; offset++ {
		productID := cfg.productIDs[(index+offset)%len(cfg.productIDs)]
		items = append(items, map[string]any{
			"product_id": productID,
			"quantity":   cfg.quantity,
		})
	}
	return items
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
	sorted := append([]time.Duration(nil), latencies...)
	sortDurations(sorted)
	index := int(float64(len(sorted)-1) * ratio)
	return sorted[index]
}

func sortDurations(values []time.Duration) {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
}
