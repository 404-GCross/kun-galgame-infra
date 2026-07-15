package devapi

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// usageKey identifies one (client, key, face, day) usage bucket.
type usageKey struct {
	clientID string
	keyID    uint
	face     string
	day      string
}

type usageDelta struct {
	count, s4xx, s5xx int64
}

// UsageRecorder accumulates per-(client, key, face, day) request counts in
// memory and periodically flushes them to developer_api_usage via an
// ACCUMULATING upsert. Per-instance in-memory aggregation + accumulating upsert
// is correct across replicas (each flush contributes its own delta) and needs
// no Redis for the history table — the Redis quota counter already provides
// real-time enforcement. Faces call Record per request and drive Flush from a
// ticker (wired in 02/03); this step delivers + tests the functions only.
type UsageRecorder struct {
	mu     sync.Mutex
	deltas map[usageKey]*usageDelta
	repo   *Repository
	store  Store
}

// NewUsageRecorder builds a recorder over the repository (flush target) and the
// counter store (last-used throttle).
func NewUsageRecorder(repo *Repository, store Store) *UsageRecorder {
	return &UsageRecorder{deltas: make(map[usageKey]*usageDelta), repo: repo, store: store}
}

// Record adds one request to the in-memory rollup for the credential's key on
// the given face, classifying its HTTP status into the 4xx/5xx buckets.
func (u *UsageRecorder) Record(cred *Credential, face string, status int) {
	day := time.Now().UTC().Format("2006-01-02")
	k := usageKey{clientID: cred.ClientID, keyID: cred.KeyID, face: face, day: day}
	u.mu.Lock()
	defer u.mu.Unlock()
	d := u.deltas[k]
	if d == nil {
		d = &usageDelta{}
		u.deltas[k] = d
	}
	d.count++
	switch {
	case status >= 500:
		d.s5xx++
	case status >= 400:
		d.s4xx++
	}
}

// Flush upserts the accumulated deltas and clears the in-memory buffer. A flush
// with nothing pending is a no-op, so re-flushing never double-counts. On an
// upsert error the batch is merged back so counts are not lost.
func (u *UsageRecorder) Flush(ctx context.Context) error {
	u.mu.Lock()
	pending := u.deltas
	u.deltas = make(map[usageKey]*usageDelta)
	u.mu.Unlock()

	if len(pending) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]DeveloperAPIUsage, 0, len(pending))
	for k, d := range pending {
		rows = append(rows, DeveloperAPIUsage{
			ClientID:  k.clientID,
			KeyID:     k.keyID,
			Face:      k.face,
			Day:       k.day,
			Count:     d.count,
			Status4xx: d.s4xx,
			Status5xx: d.s5xx,
			UpdatedAt: now,
		})
	}
	if err := u.repo.UpsertUsage(ctx, rows); err != nil {
		u.remerge(pending)
		return err
	}
	return nil
}

// remerge folds a failed batch back into the live buffer (best-effort
// durability across a transient DB failure).
func (u *UsageRecorder) remerge(pending map[usageKey]*usageDelta) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for k, d := range pending {
		cur := u.deltas[k]
		if cur == nil {
			cur = &usageDelta{}
			u.deltas[k] = cur
		}
		cur.count += d.count
		cur.s4xx += d.s4xx
		cur.s5xx += d.s5xx
	}
}

// TouchLastUsed writes a key's last_used_at at most once per minute, throttled
// by a short-TTL counter (`devkey:lastused:{key_id}:{minute}`). Best-effort:
// any store outage or DB error is swallowed (a stale last_used_at is cosmetic).
func (u *UsageRecorder) TouchLastUsed(ctx context.Context, cred *Credential) {
	minute := time.Now().UTC().Unix() / 60
	key := fmt.Sprintf("devkey:lastused:%d:%d", cred.KeyID, minute)
	n, err := u.store.Incr(ctx, key, 90*time.Second)
	if err != nil || n != 1 {
		return // throttled this minute, or store down
	}
	_ = u.repo.TouchLastUsed(ctx, cred.KeyID, time.Now().UTC())
}
