package devapi

import (
	"context"
	"errors"
	"time"

	"api/internal/infrastructure/cache"
)

// ErrStoreUnavailable signals the counter backend is unreachable or disabled.
// Rate-limit and quota checks treat it as fail-open (裁定 5): the public read
// face stays available; only the DB credential check is never failed open.
var ErrStoreUnavailable = errors.New("devapi: counter store unavailable")

// Store is the minimal Redis surface the devapi middleware needs. A Redis-backed
// implementation and an in-memory fake (tests) both satisfy it, so unit tests
// need no external Redis (裁定 11). Counters (Incr) drive rate-limit / quota /
// last-used throttling; the KV ops (Get/Set/Del) back the credential resolve
// cache.
type Store interface {
	// Incr atomically increments the counter at key, setting ttl when the key
	// is first created, and returns the post-increment value.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	// Get returns the cached bytes for key, or (nil, nil) on miss.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores value at key with ttl.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del removes key (used to actively bust a revoked key's resolve cache).
	Del(ctx context.Context, key string) error
}

// redisStore adapts *cache.RedisCache to Store. Atomic INCR/EXPIRE go through
// the underlying go-redis client (Storage().Conn()); the KV ops reuse the
// wrapper's Get/Set/Delete. When Redis is disabled or errors, counter ops
// report ErrStoreUnavailable (caller fails open) and cache ops degrade to a
// miss / no-op (caching is best-effort).
type redisStore struct {
	cache *cache.RedisCache
}

// NewRedisStore builds a Store backed by the shared Redis cache. A nil or
// disabled cache yields a store whose counters always fail open.
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
		// Best-effort expiry on first creation; a lost EXPIRE just means the
		// bucket lingers slightly longer, not a correctness bug.
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
		// Any backend hiccup is treated as a cache miss (best-effort caching).
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
