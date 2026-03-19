package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/observability"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	eventChannelName = "mayas-pharm:events"
	eventIDKey       = "mayas-pharm:events:id"
)

// EventType represents the type of event.
type EventType string

const (
	EventNewOrder       EventType = "new_order"
	EventOrderUpdated   EventType = "order_updated"
	EventOrderReady     EventType = "order_ready"
	EventOrderCompleted EventType = "order_completed"
	EventStockUpdated   EventType = "stock_updated"
	EventPriceUpdated   EventType = "price_updated"
)

// Event represents a server-sent event.
type Event struct {
	ID        int64       `json:"id"`
	Type      EventType   `json:"type"`
	Data      interface{} `json:"data"`
	CreatedAt time.Time   `json:"created_at"`
}

type redisEnvelope struct {
	ID        int64           `json:"id"`
	Type      EventType       `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	Source    string          `json:"source"`
}

// EventBus manages SSE subscriptions and broadcasts events.
type EventBus struct {
	subscribers map[string]chan Event
	mu          sync.RWMutex
	redis       *goredis.Client
	instanceID  string
	localID     atomic.Int64
	metrics     *observability.RuntimeMetrics
}

// NewEventBus creates a new event bus. When Redis is provided, events are fanned out across instances.
func NewEventBus(redisClient *goredis.Client, metrics *observability.RuntimeMetrics) *EventBus {
	bus := &EventBus{
		subscribers: make(map[string]chan Event),
		redis:       redisClient,
		instanceID:  uuid.New().String(),
		metrics:     metrics,
	}

	if redisClient != nil {
		go bus.consumeRedis()
	}

	return bus
}

// Subscribe adds a new subscriber and returns a channel for receiving events.
func (eb *EventBus) Subscribe(ctx context.Context, id string) <-chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan Event, 64)
	eb.subscribers[id] = ch
	if eb.metrics != nil {
		eb.metrics.SetSSESubscribers(len(eb.subscribers))
	}

	go func() {
		<-ctx.Done()
		eb.Unsubscribe(id)
	}()

	return ch
}

// Unsubscribe removes a subscriber.
func (eb *EventBus) Unsubscribe(id string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if ch, exists := eb.subscribers[id]; exists {
		close(ch)
		delete(eb.subscribers, id)
		if eb.metrics != nil {
			eb.metrics.SetSSESubscribers(len(eb.subscribers))
		}
	}
}

// Publish sends an event to all subscribers and, when configured, across Redis.
func (eb *EventBus) Publish(eventType EventType, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		slog.Error("failed to marshal event payload", "type", eventType, "error", err)
		return
	}

	eventID := eb.nextEventID(context.Background())
	event := Event{
		ID:        eventID,
		Type:      eventType,
		Data:      json.RawMessage(payload),
		CreatedAt: time.Now().UTC(),
	}

	if eb.redis != nil {
		envelope := redisEnvelope{
			ID:        event.ID,
			Type:      event.Type,
			Data:      json.RawMessage(payload),
			CreatedAt: event.CreatedAt,
			Source:    eb.instanceID,
		}
		body, marshalErr := json.Marshal(envelope)
		if marshalErr != nil {
			slog.Error("failed to marshal redis event envelope", "type", eventType, "error", marshalErr)
		} else if err := eb.redis.Publish(context.Background(), eventChannelName, body).Err(); err != nil {
			slog.Error("failed to publish event to redis", "type", eventType, "error", err)
		}
	}

	eb.publishLocal(event)
}

func (eb *EventBus) publishLocal(event Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			if eb.metrics != nil {
				eb.metrics.AddSSEDropped(1)
			}
		}
	}
}

func (eb *EventBus) nextEventID(ctx context.Context) int64 {
	if eb.redis != nil {
		if id, err := eb.redis.Incr(ctx, eventIDKey).Result(); err == nil {
			return id
		}
	}
	return eb.localID.Add(1)
}

func (eb *EventBus) consumeRedis() {
	pubsub := eb.redis.Subscribe(context.Background(), eventChannelName)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		var envelope redisEnvelope
		if err := json.Unmarshal([]byte(msg.Payload), &envelope); err != nil {
			slog.Warn("failed to decode redis event", "error", err)
			continue
		}
		if envelope.Source == eb.instanceID {
			continue
		}
		eb.publishLocal(Event{
			ID:        envelope.ID,
			Type:      envelope.Type,
			Data:      envelope.Data,
			CreatedAt: envelope.CreatedAt,
		})
	}
}

// PublishNewOrder publishes a new order event.
func (eb *EventBus) PublishNewOrder(order interface{}) {
	eb.Publish(EventNewOrder, order)
}

// PublishOrderUpdated publishes a generic order-updated event.
func (eb *EventBus) PublishOrderUpdated(order interface{}) {
	eb.Publish(EventOrderUpdated, order)
}

// PublishOrderReady publishes an order ready event.
func (eb *EventBus) PublishOrderReady(order interface{}) {
	eb.Publish(EventOrderReady, order)
}

// PublishOrderCompleted publishes an order completed event.
func (eb *EventBus) PublishOrderCompleted(orderID string) {
	eb.Publish(EventOrderCompleted, map[string]string{"order_id": orderID})
}

// PublishStockUpdated publishes a stock updated event.
func (eb *EventBus) PublishStockUpdated(productID string, stock int) {
	eb.Publish(EventStockUpdated, map[string]interface{}{
		"product_id": productID,
		"stock":      stock,
	})
}

// PublishPriceUpdated publishes a price updated event.
func (eb *EventBus) PublishPriceUpdated(productID string, price float64) {
	eb.Publish(EventPriceUpdated, map[string]interface{}{
		"product_id": productID,
		"price":      price,
	})
}

// FormatSSE formats an event as a Server-Sent Event string.
func FormatSSE(event Event) (string, error) {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return "", err
	}

	sse := ""
	if event.ID > 0 {
		sse += fmt.Sprintf("id: %d\n", event.ID)
	}
	sse += "event: " + string(event.Type) + "\n"
	sse += "data: " + string(data) + "\n\n"
	return sse, nil
}
