package devapi

import (
	"context"
	"strconv"
	"sync"
	"time"
)

type memStore struct {
	mu       sync.Mutex
	counters map[string]int64
	kv       map[string][]byte
	failing  bool
}

func newMemStore() *memStore {
	return &memStore{counters: map[string]int64{}, kv: map[string][]byte{}}
}

func (s *memStore) Incr(_ context.Context, key string, _ time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return 0, ErrStoreUnavailable
	}
	s.counters[key]++
	return s.counters[key], nil
}

func (s *memStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return nil, nil
	}
	return s.kv[key], nil
}

func (s *memStore) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return nil
	}
	s.kv[key] = value
	return nil
}

func (s *memStore) Del(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kv, key)
	return nil
}

func (s *memStore) Available(_ context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.failing
}

func (s *memStore) setCounter(key string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[key] = []byte(strconv.FormatInt(n, 10))
}
