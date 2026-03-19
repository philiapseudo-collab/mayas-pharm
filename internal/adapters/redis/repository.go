package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/redis/go-redis/v9"
)

const (
	// SessionKeyPrefix is the prefix for session keys in Redis
	SessionKeyPrefix = "session:"
	// DefaultSessionTTL is the default TTL for sessions (2 hours)
	DefaultSessionTTL = 2 * time.Hour
)

// Repository implements SessionRepository using Redis
type Repository struct {
	client *redis.Client
}

// NewRepository creates a new Redis repository
func NewRepository(client *redis.Client) *Repository {
	return &Repository{client: client}
}

// Get retrieves a session from Redis
func (r *Repository) Get(ctx context.Context, phone string) (*core.Session, error) {
	key := SessionKeyPrefix + phone
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("session not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	var session core.Session
	if err := json.Unmarshal([]byte(val), &session); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &session, nil
}

// Set stores a session in Redis with TTL
func (r *Repository) Set(ctx context.Context, phone string, session *core.Session, ttl int) error {
	key := SessionKeyPrefix + phone
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	ttlDuration := time.Duration(ttl) * time.Second
	if ttl <= 0 {
		ttlDuration = DefaultSessionTTL
	}

	if err := r.client.Set(ctx, key, data, ttlDuration).Err(); err != nil {
		return fmt.Errorf("failed to set session: %w", err)
	}

	return nil
}

// Delete removes a session from Redis
func (r *Repository) Delete(ctx context.Context, phone string) error {
	key := SessionKeyPrefix + phone
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// UpdateStep updates the state/step of a session
func (r *Repository) UpdateStep(ctx context.Context, phone string, step string) error {
	session, err := r.Get(ctx, phone)
	if err != nil {
		return err
	}

	session.State = step
	return r.Set(ctx, phone, session, 0) // Use default TTL
}

// UpdateCart updates the cart items in a session
func (r *Repository) UpdateCart(ctx context.Context, phone string, cartItems string) error {
	session, err := r.Get(ctx, phone)
	if err != nil {
		return err
	}

	var cart []core.CartItem
	if cartItems != "" {
		if err := json.Unmarshal([]byte(cartItems), &cart); err != nil {
			return fmt.Errorf("failed to unmarshal cart: %w", err)
		}
	}

	session.Cart = cart
	return r.Set(ctx, phone, session, 0) // Use default TTL
}
