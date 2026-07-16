package service

import (
	"context"
	"sync"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
)

// ImageMeta is the intrinsic display metadata of an image hash: dimensions +
// the ThumbHash placeholder. Immutable per hash (content-addressed), so it is
// safe to cache forever. The galgame service attaches it to covers /
// screenshots / banners at read time so the frontend can reserve the correct
// aspect ratio and render a blur-up placeholder with no layout shift.
type ImageMeta struct {
	Width     int
	Height    int
	Thumbhash string
}

// ImageMetaFunc fetches intrinsic image metadata for a batch of hashes from
// image_service, keyed by hash. Hashes the service doesn't know are simply
// absent from the result. Wired in galgameapp.Mount to imageclient.MetaBatch; nil
// on the service disables enrichment (images still render — just without
// dimensions / placeholder, so the frontend falls back to a skeleton).
type ImageMetaFunc func(ctx context.Context, hashes []string) (map[string]ImageMeta, error)

// WithImageMeta wires the image_service metadata lookup used to enrich
// covers / screenshots / banners at read time. Returns the service for fluent
// chaining: `NewGalgameService(...).WithImageMeta(fn)`. Passing nil disables
// enrichment (= behaviour without this call).
func (s *GalgameService) WithImageMeta(fn ImageMetaFunc) *GalgameService {
	s.imageMeta = fn
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
func (s *GalgameService) resolveImageMeta(ctx context.Context, hashes []string) map[string]ImageMeta {
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

	for _, chunk := range chunkStrings(misses, 1000) {
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
			// placeholder forever (the bug that left backfilled images without
			// blur-up until a service restart). Return it for its dimensions
			// but don't cache, so a later read re-resolves and lights it up.
			if s.metaCache != nil && m.Thumbhash != "" {
				s.metaCache.put(h, m)
			}
		}
	}
	return out
}

// enrichGalgameImages fills the transient Width/Height/Thumbhash on a
// galgame's covers + screenshots from image_service (the detail view). No-op
// when enrichment is unwired or the galgame carries no image relations.
func (s *GalgameService) enrichGalgameImages(ctx context.Context, g *model.Galgame) {
	if g == nil || s.imageMeta == nil {
		return
	}
	hashes := make([]string, 0, len(g.Cover)+len(g.Screenshot))
	for i := range g.Cover {
		hashes = append(hashes, g.Cover[i].ImageHash)
	}
	for i := range g.Screenshot {
		hashes = append(hashes, g.Screenshot[i].ImageHash)
	}
	metas := s.resolveImageMeta(ctx, hashes)
	if len(metas) == 0 {
		return
	}
	for i := range g.Cover {
		if m, ok := metas[g.Cover[i].ImageHash]; ok {
			g.Cover[i].Width, g.Cover[i].Height, g.Cover[i].Thumbhash = m.Width, m.Height, m.Thumbhash
		}
	}
	for i := range g.Screenshot {
		if m, ok := metas[g.Screenshot[i].ImageHash]; ok {
			g.Screenshot[i].Width, g.Screenshot[i].Height, g.Screenshot[i].Thumbhash = m.Width, m.Height, m.Thumbhash
		}
	}
}

// enrichBriefBanners fills EffectiveBanner{Width,Height,Thumbhash} on a set of
// briefs from image_service, in one batched lookup. Takes pointers so it works
// uniformly for plain briefs and the GalgameBrief embedded in a detail brief.
// No-op when enrichment is unwired.
func (s *GalgameService) enrichBriefBanners(ctx context.Context, briefs []*dto.GalgameBrief) {
	if s.imageMeta == nil || len(briefs) == 0 {
		return
	}
	hashes := make([]string, 0, len(briefs))
	for _, b := range briefs {
		if b.EffectiveBannerHash != nil {
			hashes = append(hashes, *b.EffectiveBannerHash)
		}
	}
	metas := s.resolveImageMeta(ctx, hashes)
	if len(metas) == 0 {
		return
	}
	for _, b := range briefs {
		if b.EffectiveBannerHash == nil {
			continue
		}
		if m, ok := metas[*b.EffectiveBannerHash]; ok {
			b.EffectiveBannerWidth = m.Width
			b.EffectiveBannerHeight = m.Height
			b.EffectiveBannerThumbhash = m.Thumbhash
		}
	}
}

// enrichBriefValues is the value-slice convenience over enrichBriefBanners:
// it fills each brief's banner metadata in place. Used by the list / batch
// endpoints that hold a []dto.GalgameBrief.
func (s *GalgameService) enrichBriefValues(ctx context.Context, items []dto.GalgameBrief) {
	if s.imageMeta == nil || len(items) == 0 {
		return
	}
	ptrs := make([]*dto.GalgameBrief, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	s.enrichBriefBanners(ctx, ptrs)
}

// chunkStrings splits s into consecutive sub-slices of at most size elements.
func chunkStrings(s []string, size int) [][]string {
	if size <= 0 || len(s) <= size {
		return [][]string{s}
	}
	out := make([][]string, 0, (len(s)+size-1)/size)
	for i := 0; i < len(s); i += size {
		out = append(out, s[i:min(i+size, len(s))])
	}
	return out
}
