package hihyou

import (
	"context"
	"log/slog"
	"sync"

	"api/internal/platform/news/model"
)

// warm resolves an issue's pictures ahead of the writes. Fetch-and-upload is
// the whole cost of this job (4.1s per picture serially, nine hours for the
// corpus) and it is pure network, so it runs concurrently here while the
// database writes stay on one goroutine — the item loop afterwards only reads
// the cache this fills.
//
// Anything already stored is seeded from the database rather than fetched, so a
// second run over an issue downloads nothing.
func (w *writer) warm(ctx context.Context, urls []string, st *stats) {
	if w.images == nil || len(urls) == 0 {
		return
	}
	want := make([]string, 0, len(urls))
	seen := map[string]bool{}
	for _, u := range urls {
		if u == "" || seen[u] || w.uploaded[u] != "" {
			continue
		}
		seen[u] = true
		want = append(want, u)
	}
	if len(want) == 0 {
		return
	}

	type mapping struct {
		OriginURL string
		ImageHash string
	}
	// Two queries into two slices: Scan replaces the destination rather than
	// appending to it, so sharing one would silently drop the gallery's mappings.
	var gallery, banners []mapping
	if err := w.db.WithContext(ctx).Model(&model.NewsItemImage{}).
		Select("origin_url", "image_hash").Where("origin_url IN ?", want).
		Scan(&gallery).Error; err != nil {
		slog.Warn("hihyou: could not read stored pictures — the pass will re-fetch", "err", err)
	}
	if err := w.db.WithContext(ctx).Model(&model.NewsItem{}).
		Select("banner_origin_url AS origin_url", "banner_hash AS image_hash").
		Where("banner_origin_url IN ? AND banner_hash <> ''", want).
		Scan(&banners).Error; err != nil {
		slog.Warn("hihyou: could not read stored banners — the pass will re-fetch", "err", err)
	}
	for _, k := range append(gallery, banners...) {
		if k.ImageHash != "" {
			w.uploaded[k.OriginURL] = k.ImageHash
		}
	}

	var pending []string
	for _, u := range want {
		if w.uploaded[u] == "" {
			pending = append(pending, u)
		}
	}
	if len(pending) == 0 {
		return
	}

	n := max(w.opts.Concurrency, 1)
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	work := make(chan string)
	for range n {
		wg.Go(func() {
			for u := range work {
				h, err := w.upload(ctx, u)
				mu.Lock()
				if err != nil {
					w.failed[u] = true
					st.imagesFail++
					slog.Warn("hihyou: picture fetch/upload failed — the row is kept and the next run retries it",
						"url", u, "err", err)
				} else {
					w.uploaded[u] = h
					st.imagesUp++
				}
				mu.Unlock()
			}
		})
	}
	for _, u := range pending {
		select {
		case work <- u:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
}
