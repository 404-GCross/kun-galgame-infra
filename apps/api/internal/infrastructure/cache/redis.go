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
