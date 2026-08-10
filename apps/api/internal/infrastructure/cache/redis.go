package cache

import (
	"context"
	"errors"
	"time"

	"api/pkg/config"
	"api/pkg/logger"

	"github.com/gofiber/storage/redis/v3"
)

type RedisCache struct {
	store *redis.Storage
}

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := store.Conn().Ping(ctx).Err(); err != nil {
		return nil, err
	}

	logger.Info("Redis connected successfully")

	return &RedisCache{store: store}, nil
}

func (r *RedisCache) Storage() *redis.Storage {
	return r.store
}

func (r *RedisCache) Close() error {
	if r.store == nil {
		return nil
	}
	return r.store.Close()
}

func (r *RedisCache) Enabled() bool {
	return r.store != nil
}

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

func (r *RedisCache) Get(key string) ([]byte, error) {
	if r.store == nil {
		return nil, ErrCacheDisabled
	}
	return r.store.Get(key)
}

func (r *RedisCache) Delete(key string) error {
	if r.store == nil {
		return ErrCacheDisabled
	}
	return r.store.Delete(key)
}

func (r *RedisCache) Publish(ctx context.Context, channel, payload string) error {
	if r.store == nil {
		return ErrCacheDisabled
	}
	return r.store.Conn().Publish(ctx, channel, payload).Err()
}

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
				}
			}
		}
	}()
	return out, nil
}
