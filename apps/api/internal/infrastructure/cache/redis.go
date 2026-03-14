package cache

import (
	"context"
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

// Set stores a value in Redis
func (r *RedisCache) Set(key string, value []byte, expiration time.Duration) error {
	if r.store == nil {
		return nil
	}
	return r.store.Set(key, value, expiration)
}

// Get retrieves a value from Redis
func (r *RedisCache) Get(key string) ([]byte, error) {
	if r.store == nil {
		return nil, nil
	}
	return r.store.Get(key)
}

// Delete removes a value from Redis
func (r *RedisCache) Delete(key string) error {
	if r.store == nil {
		return nil
	}
	return r.store.Delete(key)
}
