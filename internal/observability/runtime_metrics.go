package observability

import (
	"database/sql"
	"sync/atomic"
	"time"
)

type RuntimeMetrics struct {
	orderCreates            atomic.Int64
	orderCreateErrors       atomic.Int64
	orderCreateReplays      atomic.Int64
	lastOrderCreateLatency  atomic.Int64
	stockRetryCount         atomic.Int64
	paymentDispatchAttempts atomic.Int64
	paymentDispatchSuccess  atomic.Int64
	paymentDispatchErrors   atomic.Int64
	lastDispatchLatencyMS   atomic.Int64
	paymentQueueDepth       atomic.Int64
	oldestQueuedAgeSeconds  atomic.Int64
	unmatchedCreated        atomic.Int64
	whatsappWebhookAccepted atomic.Int64
	sseSubscribers          atomic.Int64
	sseDroppedEvents        atomic.Int64
}

type Snapshot struct {
	Orders struct {
		Created             int64 `json:"created"`
		Errors              int64 `json:"errors"`
		IdempotentReplays   int64 `json:"idempotent_replays"`
		LastCreateLatencyMS int64 `json:"last_create_latency_ms"`
	} `json:"orders"`
	Stock struct {
		RetryCount int64 `json:"retry_count"`
	} `json:"stock"`
	Payments struct {
		DispatchAttempts       int64 `json:"dispatch_attempts"`
		DispatchSuccess        int64 `json:"dispatch_success"`
		DispatchErrors         int64 `json:"dispatch_errors"`
		LastDispatchLatencyMS  int64 `json:"last_dispatch_latency_ms"`
		QueueDepth             int64 `json:"queue_depth"`
		OldestQueuedAgeSeconds int64 `json:"oldest_queued_age_seconds"`
		UnmatchedCreated       int64 `json:"unmatched_created"`
		UnmatchedPending       int64 `json:"unmatched_pending"`
	} `json:"payments"`
	Webhooks struct {
		WhatsAppAccepted int64 `json:"whatsapp_accepted"`
	} `json:"webhooks"`
	SSE struct {
		Subscribers   int64 `json:"subscribers"`
		DroppedEvents int64 `json:"dropped_events"`
	} `json:"sse"`
	Database struct {
		MaxOpenConnections int   `json:"max_open_connections"`
		OpenConnections    int   `json:"open_connections"`
		InUse              int   `json:"in_use"`
		Idle               int   `json:"idle"`
		WaitCount          int64 `json:"wait_count"`
		WaitDurationMS     int64 `json:"wait_duration_ms"`
	} `json:"database"`
	GeneratedAt time.Time `json:"generated_at"`
}

func NewRuntimeMetrics() *RuntimeMetrics {
	return &RuntimeMetrics{}
}

func (m *RuntimeMetrics) ObserveOrderCreate(created bool, err error, duration time.Duration) {
	if m == nil {
		return
	}
	m.lastOrderCreateLatency.Store(duration.Milliseconds())
	if err != nil {
		m.orderCreateErrors.Add(1)
		return
	}
	if created {
		m.orderCreates.Add(1)
		return
	}
	m.orderCreateReplays.Add(1)
}

func (m *RuntimeMetrics) IncStockRetry() {
	if m != nil {
		m.stockRetryCount.Add(1)
	}
}

func (m *RuntimeMetrics) ObservePaymentDispatch(success bool, duration time.Duration) {
	if m == nil {
		return
	}
	m.paymentDispatchAttempts.Add(1)
	m.lastDispatchLatencyMS.Store(duration.Milliseconds())
	if success {
		m.paymentDispatchSuccess.Add(1)
		return
	}
	m.paymentDispatchErrors.Add(1)
}

func (m *RuntimeMetrics) SetQueueStats(depth int, oldestAge time.Duration) {
	if m == nil {
		return
	}
	m.paymentQueueDepth.Store(int64(depth))
	if oldestAge < 0 {
		oldestAge = 0
	}
	m.oldestQueuedAgeSeconds.Store(int64(oldestAge / time.Second))
}

func (m *RuntimeMetrics) IncUnmatchedCreated() {
	if m != nil {
		m.unmatchedCreated.Add(1)
	}
}

func (m *RuntimeMetrics) IncWhatsAppAccepted() {
	if m != nil {
		m.whatsappWebhookAccepted.Add(1)
	}
}

func (m *RuntimeMetrics) SetSSESubscribers(count int) {
	if m != nil {
		m.sseSubscribers.Store(int64(count))
	}
}

func (m *RuntimeMetrics) AddSSEDropped(count int64) {
	if m != nil && count > 0 {
		m.sseDroppedEvents.Add(count)
	}
}

func (m *RuntimeMetrics) Snapshot(dbStats sql.DBStats, unmatchedPending int) Snapshot {
	var snapshot Snapshot
	if m != nil {
		snapshot.Orders.Created = m.orderCreates.Load()
		snapshot.Orders.Errors = m.orderCreateErrors.Load()
		snapshot.Orders.IdempotentReplays = m.orderCreateReplays.Load()
		snapshot.Orders.LastCreateLatencyMS = m.lastOrderCreateLatency.Load()
		snapshot.Stock.RetryCount = m.stockRetryCount.Load()
		snapshot.Payments.DispatchAttempts = m.paymentDispatchAttempts.Load()
		snapshot.Payments.DispatchSuccess = m.paymentDispatchSuccess.Load()
		snapshot.Payments.DispatchErrors = m.paymentDispatchErrors.Load()
		snapshot.Payments.LastDispatchLatencyMS = m.lastDispatchLatencyMS.Load()
		snapshot.Payments.QueueDepth = m.paymentQueueDepth.Load()
		snapshot.Payments.OldestQueuedAgeSeconds = m.oldestQueuedAgeSeconds.Load()
		snapshot.Payments.UnmatchedCreated = m.unmatchedCreated.Load()
		snapshot.Webhooks.WhatsAppAccepted = m.whatsappWebhookAccepted.Load()
		snapshot.SSE.Subscribers = m.sseSubscribers.Load()
		snapshot.SSE.DroppedEvents = m.sseDroppedEvents.Load()
	}
	snapshot.Payments.UnmatchedPending = int64(unmatchedPending)
	snapshot.Database.MaxOpenConnections = dbStats.MaxOpenConnections
	snapshot.Database.OpenConnections = dbStats.OpenConnections
	snapshot.Database.InUse = dbStats.InUse
	snapshot.Database.Idle = dbStats.Idle
	snapshot.Database.WaitCount = dbStats.WaitCount
	snapshot.Database.WaitDurationMS = dbStats.WaitDuration.Milliseconds()
	snapshot.GeneratedAt = time.Now().UTC()
	return snapshot
}
