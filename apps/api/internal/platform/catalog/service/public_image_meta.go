// public_image_meta.go — intrinsic image metadata (dimensions + ThumbHash) for
// the catalog public face, A2-1a. A straight port of the galgame service's
// pattern (internal/platform/galgame/service/image_meta.go): the same
// inject-a-func + cache-forever shape, because the invariant it rests on is the
// same — hashes are content-addressed, so their metadata can never change.
package service

import (
	"context"
	"sync"
)

// ImageMeta is the intrinsic display metadata of an image hash: dimensions +
// the ThumbHash placeholder. Immutable per hash (content-addressed), so it is
// safe to cache forever. The public face attaches it to covers so a consumer
// can reserve the correct aspect ratio and render a blur-up placeholder with
// no layout shift.
type ImageMeta struct {
	Width     int
	Height    int
	Thumbhash string
}

// ImageMetaFunc fetches intrinsic image metadata for a batch of hashes from
// image_service, keyed by hash. Hashes the service doesn't know are simply
// absent from the result. Wired in cmd/catalog to imageclient.MetaBatch; nil on
// the service disables enrichment (covers still render — just without
// dimensions / placeholder, and the cover SLOT picker loses its orientation
// evidence, see pickCoverSlots).
type ImageMetaFunc func(ctx context.Context, hashes []string) (map[string]ImageMeta, error)

// imageMetaCacheCap bounds the per-process cache. Covers are a small, hot set
// (one or two per work), so a soft cap in the low six figures holds the whole
// working set of a busy page without an eviction policy.
const imageMetaCacheCap = 200_000

// WithImageMeta wires the image_service metadata lookup used to enrich covers
// at read time. Returns the service for fluent chaining:
// `NewPublicService(...).WithImageMeta(fn)`. Passing nil disables enrichment
// (= behaviour without this call).
func (s *PublicService) WithImageMeta(fn ImageMetaFunc) *PublicService {
	s.imageMeta = fn
	if fn != nil && s.metaCache == nil {
		s.metaCache = newImageMetaCache(imageMetaCacheCap)
	}
	return s
}

// imageMetaCache is a tiny concurrency-safe cache of hash → ImageMeta. Caching
// forever is correct because metadata is immutable per content-addressed hash;
// a soft cap bounds memory — beyond it new hashes are simply re-fetched each
// time rather than evicting (no LRU machinery needed for this bounded domain).
type imageMetaCache struct {
	mu  sync.RWMutex
	m   map[string]ImageMeta
	cap int
}

func newImageMetaCache(capacity int) *imageMetaCache {
	return &imageMetaCache{m: make(map[string]ImageMeta), cap: capacity}
}

func (c *imageMetaCache) get(hash string) (ImageMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.m[hash]
	return v, ok
}

func (c *imageMetaCache) put(hash string, meta ImageMeta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Soft cap: stop growing once full (existing immutable entries are kept).
	if len(c.m) >= c.cap {
		return
	}
	c.m[hash] = meta
}

// resolveImageMeta returns metadata for the given hashes, cache-first: cache
// hits are served directly, misses are fetched in one batched call (chunked to
// the service's 1000-hash limit) and cached. It NEVER fails the caller — on a
// fetch error it returns whatever it already has, so reads degrade to skeleton
// placeholders instead of erroring. Safe with a nil imageMeta func (returns
// only cache hits, i.e. usually empty).
func (s *PublicService) resolveImageMeta(ctx context.Context, hashes []string) map[string]ImageMeta {
	out := make(map[string]ImageMeta, len(hashes))
	if len(hashes) == 0 {
		return out
	}

	misses := make([]string, 0, len(hashes))
	seen := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		if h == "" {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		if s.metaCache != nil {
			if m, ok := s.metaCache.get(h); ok {
				out[h] = m
				continue
			}
		}
		misses = append(misses, h)
	}

	if len(misses) == 0 || s.imageMeta == nil {
		return out
	}

	for _, chunk := range chunkHashes(misses, 1000) {
		fetched, err := s.imageMeta(ctx, chunk)
		if err != nil {
			// Non-fatal: serve what we have; the rest render as skeletons.
			break
		}
		for h, m := range fetched {
			out[h] = m
			// Cache COMPLETE entries only. A result with an empty Thumbhash is
			// a partial hit: the hash is known (width/height set) but the
			// thumbhash hasn't been computed/backfilled yet. The cache never
			// expires, so caching the empty value would pin the missing
			// placeholder forever. Return it for its dimensions but don't
			// cache, so a later read re-resolves and lights it up.
			if s.metaCache != nil && m.Thumbhash != "" {
				s.metaCache.put(h, m)
			}
		}
	}
	return out
}

// coverMetaFor is the cover-shaped convenience over resolveImageMeta: it
// collects the hashes of a cover set and resolves them in one batch.
func (s *PublicService) coverMetaFor(ctx context.Context, rows []WorkCoverRow) map[string]ImageMeta {
	return s.workMediaMetaFor(ctx, rows, nil)
}

// workMediaMetaFor resolves a work's WHOLE image set — covers and screenshots
// together — in ONE batch (A2-1b, which extended the enrichment to
// screenshots). Sharing the batch matters: a screenshot set runs to dozens of
// rows, and two separate calls would double the detail face's image_service
// round-trips for no benefit. nil (= no enrichment) when the lookup is unwired
// or there is nothing to resolve; every consumer treats a missing entry as
// "unknown" and omits the three keys.
func (s *PublicService) workMediaMetaFor(ctx context.Context, covers []WorkCoverRow, shots []WorkScreenshotRow) map[string]ImageMeta {
	if s.imageMeta == nil || (len(covers) == 0 && len(shots) == 0) {
		return nil
	}
	hashes := make([]string, 0, len(covers)+len(shots))
	for _, c := range covers {
		hashes = append(hashes, c.ImageHash)
	}
	for _, sc := range shots {
		hashes = append(hashes, sc.ImageHash)
	}
	return s.resolveImageMeta(ctx, hashes)
}

// chunkHashes splits s into consecutive sub-slices of at most size elements.
func chunkHashes(s []string, size int) [][]string {
	if size <= 0 || len(s) <= size {
		return [][]string{s}
	}
	out := make([][]string, 0, (len(s)+size-1)/size)
	for i := 0; i < len(s); i += size {
		out = append(out, s[i:min(i+size, len(s))])
	}
	return out
}
