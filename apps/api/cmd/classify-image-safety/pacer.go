package main

import (
	"context"
	"sync"
	"time"
)

// The moderations image path publishes no x-ratelimit headers and answers a 429
// with a generic body, so the sustainable rate cannot be read off a response —
// only discovered. A fixed rate picked by hand was wrong twice: 32 in flight
// burst to 34 img/s and then bled 10% of the corpus into exhausted retries.
type pacer struct {
	mu         sync.Mutex
	interval   time.Duration
	min        time.Duration
	max        time.Duration
	streak     int
	last       time.Time
	lastAdjust time.Time
}

func newPacer(min, start, max time.Duration) *pacer {
	return &pacer{interval: start, min: min, max: max}
}

func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	next := p.last.Add(p.interval)
	if next.Before(now) {
		next = now
	}
	p.last = next
	delay := next.Sub(now)
	p.mu.Unlock()

	if delay <= 0 {
		return nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// A 429 arrives at every in-flight worker at once. Backing off once per worker
// would ratchet the interval to the ceiling on a single throttling event, so
// only the first report in a cooldown window counts.
func (p *pacer) throttled() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streak = 0
	if time.Since(p.lastAdjust) < 2*time.Second {
		return
	}
	p.lastAdjust = time.Now()
	p.interval = min(p.interval*3/2, p.max)
}

func (p *pacer) ok() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streak++
	if p.streak >= 10 {
		p.streak = 0
		p.lastAdjust = time.Now()
		p.interval = max(p.interval*85/100, p.min)
	}
}

func (p *pacer) current() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.interval
}
