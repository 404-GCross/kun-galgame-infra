package cache

import (
	"context"
	"errors"
	"time"

	"api/pkg/config"
	"api/pkg/logger"

	"github.com/gofiber/storage/redis/v3"
)

// RedisCache wraps redis.Storage with additional methods
type RedisCache struct {
	store *redis.Storage
}

// NewRedisCache creates a new Redis connection
func NewRedisCache(cfg config.RedisConfig) (*RedisCache, error) {
	if !cfg.Enabled {
		logger.Info("Redis is disabled")
		return &RedisCache{store: nil}, nil
	}

	store := redis.New(redis.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: cfg.Password,
		Database: cfg.DB,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := store.Conn().Ping(ctx).Err(); err != nil {
		return nil, err
	}

	logger.Info("Redis connected successfully")

	return &RedisCache{store: store}, nil
}

// Storage returns the underlying redis.Storage for middleware use
func (r *RedisCache) Storage() *redis.Storage {
	return r.store
}

// Close closes the Redis connection
func (r *RedisCache) Close() error {
	if r.store == nil {
		return nil
	}
	return r.store.Close()
}

// Enabled reports whether the underlying Redis connection is live.
// Use this in callers that have meaningful behavior to skip when the
// cache is off (e.g. optional caching of computed values). Code that
// REQUIRES the cache (e.g. verification-code flows) should NOT branch
// on Enabled — it should just call Get/Set/Delete and let them fail
// loud so the misconfiguration is visible.
func (r *RedisCache) Enabled() bool {
	return r.store != nil
}

// ErrCacheDisabled is returned by Set/Get/Delete when REDIS_ENABLED=false.
// Returning it loudly (rather than silently no-op-ing) prevents the
// failure mode where a verification-code flow appears to send a code,
// the consumer reports success, but the code was never stored — so any
// subsequent verify fails with "code expired" and the root cause stays
// invisible until debugging.
var ErrCacheDisabled = errors.New("cache: Redis is disabled (set REDIS_ENABLED=true)")

// Set stores a value in Redis.
//
// Returns ErrCacheDisabled when REDIS_ENABLED=false — callers must
// handle that explicitly. Pre-2026-05-23 this silently returned nil,
// which broke verification flows in dev (cache.Set "succeeded", but
// the subsequent cache.Get returned (nil, nil), and the caller
// interpreted that as "code expired").
func (r *RedisCache) Set(key string, value []byte, expiration time.Duration) error {
	if r.store == nil {
		return ErrCacheDisabled
	}
	return r.store.Set(key, value, expiration)
}

// Get retrieves a value from Redis. See Set for the disabled-mode contract.
func (r *RedisCache) Get(key string) ([]byte, error) {
	if r.store == nil {
		return nil, ErrCacheDisabled
	}
	return r.store.Get(key)
}

// Delete removes a value from Redis. See Set for the disabled-mode contract.
func (r *RedisCache) Delete(key string) error {
	if r.store == nil {
		return ErrCacheDisabled
	}
	return r.store.Delete(key)
}

// Publish sends a fire-and-forget message on a Redis pub/sub channel.
//
// Pub/sub is not caching, and it is deliberately thin here: the only current
// user (the authz overlay invalidation) treats delivery as a best-effort nudge
// on top of a source of truth it can always re-read, so there is no delivery
// guarantee to design for.
func (r *RedisCache) Publish(ctx context.Context, channel, payload string) error {
	if r.store == nil {
		return ErrCacheDisabled
	}
	return r.store.Conn().Publish(ctx, channel, payload).Err()
}

// Subscribe returns a channel of payloads published to the named channel. The
// subscription is closed when ctx is done.
//
// Payload strings only: a subscriber that needed structured data would be
// trusting the message, and messages get lost, duplicated and reordered. The
// intended shape is "something changed, go look".
func (r *RedisCache) Subscribe(ctx context.Context, channel string) (<-chan string, error) {
	if r.store == nil {
		return nil, ErrCacheDisabled
	}
	sub := r.store.Conn().Subscribe(ctx, channel)
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, err
	}

	out := make(chan string, 1)
	go func() {
		defer close(out)
		defer sub.Close()
		messages := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-messages:
				if !ok {
					return
				}
				select {
				case out <- msg.Payload:
				default:
					// A refresh is already queued; another "go look" adds
					// nothing, so drop it rather than block the reader.
				}
			}
		}
	}()
	return out, nil
}
