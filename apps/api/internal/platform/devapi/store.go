package devapi

import (
	"context"
	"errors"
	"time"

	"api/internal/infrastructure/cache"
)

var ErrStoreUnavailable = errors.New("devapi: counter store unavailable")

type Store interface {
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Available(ctx context.Context) bool
}

type redisStore struct {
	cache *cache.RedisCache
}

func NewRedisStore(c *cache.RedisCache) Store {
	return &redisStore{cache: c}
}

func (s *redisStore) live() bool {
	return s.cache != nil && s.cache.Enabled()
}

func (s *redisStore) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if !s.live() {
		return 0, ErrStoreUnavailable
	}
	conn := s.cache.Storage().Conn()
	n, err := conn.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if n == 1 {
		_ = conn.Expire(ctx, key, ttl).Err()
	}
	return n, nil
}

func (s *redisStore) Get(_ context.Context, key string) ([]byte, error) {
	if !s.live() {
		return nil, nil
	}
	b, err := s.cache.Get(key)
	if err != nil {
		return nil, nil
	}
	return b, nil
}

func (s *redisStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if !s.live() {
		return nil
	}
	return s.cache.Set(key, value, ttl)
}

func (s *redisStore) Del(_ context.Context, key string) error {
	if !s.live() {
		return nil
	}
	return s.cache.Delete(key)
}

func (s *redisStore) Available(_ context.Context) bool {
	return s.live()
}
