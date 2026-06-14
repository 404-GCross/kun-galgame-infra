// Package quota enforces per-site daily upload quotas for the artifact service
// via Redis counters. Mirrors internal/platform/image/quota but with an
// artifact: key namespace and reservation at INIT time (the service never sees
// the bytes; it reserves on the caller-declared size). Keys:
//
//	artifact:quota:count:<site>:<YYYYMMDD>
//	artifact:quota:bytes:<site>:<YYYYMMDD>
//
// Both expire in 26h so they self-clean across day rollover.
package quota

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"api/internal/infrastructure/cache"
)

const keyTTL = 26 * time.Hour

// Errors.
var (
	ErrCountExceeded = errors.New("quota: daily upload count exceeded")
	ErrBytesExceeded = errors.New("quota: daily upload bytes exceeded")
	ErrNotConfigured = errors.New("quota: redis cache not configured")
)

// Checker guards uploads with per-site daily quotas.
type Checker struct {
	cache *cache.RedisCache
}

// New creates a quota checker backed by the given Redis cache.
func New(c *cache.RedisCache) *Checker { return &Checker{cache: c} }

// DailyUsage is the current-day usage for a site.
type DailyUsage struct {
	Count      int64
	Bytes      int64
	ResetAt    time.Time
	LimitCount int
	LimitBytes int64
}

func dayKey(now time.Time) string { return now.UTC().Format("20060102") }

func nextDay(now time.Time) time.Time {
	t := now.UTC()
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
}

func countKey(site, day string) string { return fmt.Sprintf("artifact:quota:count:%s:%s", site, day) }
func bytesKey(site, day string) string { return fmt.Sprintf("artifact:quota:bytes:%s:%s", site, day) }

// Reserve checks quota and increments both counters. If either dimension would
// be exceeded it returns the specific error without consuming quota. Uses
// GET-then-SET (cache.RedisCache exposes only Get/Set); acceptable per-V1
// concurrency tradeoff, same as the image service.
func (c *Checker) Reserve(ctx context.Context, site string, bytesToAdd int64, limitCount int, limitBytes int64) (*DailyUsage, error) {
	if c == nil || c.cache == nil {
		return nil, ErrNotConfigured
	}
	now := time.Now()
	day := dayKey(now)
	cKey := countKey(site, day)
	bKey := bytesKey(site, day)

	cntRaw, _ := c.cache.Get(cKey)
	bytesRaw, _ := c.cache.Get(bKey)

	var cnt, bytes int64
	if len(cntRaw) > 0 {
		cnt, _ = strconv.ParseInt(string(cntRaw), 10, 64)
	}
	if len(bytesRaw) > 0 {
		bytes, _ = strconv.ParseInt(string(bytesRaw), 10, 64)
	}

	newCnt := cnt + 1
	newBytes := bytes + bytesToAdd

	if limitCount > 0 && newCnt > int64(limitCount) {
		return &DailyUsage{Count: cnt, Bytes: bytes, ResetAt: nextDay(now), LimitCount: limitCount, LimitBytes: limitBytes}, ErrCountExceeded
	}
	if limitBytes > 0 && newBytes > limitBytes {
		return &DailyUsage{Count: cnt, Bytes: bytes, ResetAt: nextDay(now), LimitCount: limitCount, LimitBytes: limitBytes}, ErrBytesExceeded
	}

	_ = c.cache.Set(cKey, []byte(strconv.FormatInt(newCnt, 10)), keyTTL)
	_ = c.cache.Set(bKey, []byte(strconv.FormatInt(newBytes, 10)), keyTTL)

	return &DailyUsage{Count: newCnt, Bytes: newBytes, ResetAt: nextDay(now), LimitCount: limitCount, LimitBytes: limitBytes}, nil
}
