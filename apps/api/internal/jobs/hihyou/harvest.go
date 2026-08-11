package hihyou

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type HarvestOpts struct {
	Dir      string
	Passes   int
	Gap      time.Duration
	Cooldown time.Duration
	PageSize int
	Refresh  bool // re-fetch articles already complete on disk
}

func DefaultHarvestOpts() HarvestOpts {
	// The defaults are the measured recovery shape, not round numbers: one
	// attempt per article, 30s apart, 600s between passes. See codeRateLimited.
	return HarvestOpts{Passes: 6, Gap: 30 * time.Second, Cooldown: 10 * time.Minute, PageSize: 30}
}

type HarvestSummary struct {
	Weeklies    int     `json:"weeklies"`
	Fetched     int     `json:"fetched"`
	AlreadyHave int     `json:"already_have"`
	Missing     int     `json:"missing"`
	MissingCVs  []int64 `json:"missing_cvs,omitempty"`
	RateLimited int     `json:"rate_limited"`
	Passes      int     `json:"passes_used"`
}

// Harvest walks the space index, keeps the Gal周报 issues, and stores each
// article verbatim. It writes only the corpus — never the database.
func Harvest(ctx context.Context, opts HarvestOpts) (*HarvestSummary, error) {
	if opts.Dir == "" {
		return nil, errors.New("harvest: --dir is required")
	}
	if opts.Passes < 1 {
		opts.Passes = 1
	}
	if opts.PageSize < 1 || opts.PageSize > 30 {
		opts.PageSize = 30
	}
	corpus := Corpus{Dir: opts.Dir}
	if err := corpus.Mkdirs(); err != nil {
		return nil, err
	}
	client, err := NewClient()
	if err != nil {
		return nil, err
	}
	if err := client.Warm(ctx); err != nil {
		return nil, err
	}

	weeklies, err := harvestIndex(ctx, client, corpus, opts)
	if err != nil {
		return nil, err
	}
	sum := &HarvestSummary{Weeklies: len(weeklies)}

	for pass := 1; pass <= opts.Passes; pass++ {
		if pass > 1 {
			// A fresh cookie every pass: a stale buvid3 and the quota produce the
			// same -509, so there is no way to tell them apart from the response.
			if err := client.Warm(ctx); err != nil {
				return sum, err
			}
		}
		sum.Passes = pass
		attempted := 0
		for _, e := range weeklies {
			if !opts.Refresh && corpus.Has(e.ID) {
				continue
			}
			attempted++
			_, body, err := client.Article(ctx, e.ID)
			if err != nil {
				if !errors.Is(err, ErrRateLimited) {
					slog.Warn("hihyou: article fetch failed", "cv", e.ID, "err", err)
				}
			} else if err := corpus.Write(corpus.ArticlePath(e.ID), body); err != nil {
				return sum, err
			} else {
				sum.Fetched++
			}
			if err := sleep(ctx, opts.Gap); err != nil {
				return sum, err
			}
		}
		if attempted == 0 {
			break
		}
		if pass < opts.Passes {
			if err := sleep(ctx, opts.Cooldown); err != nil {
				return sum, err
			}
		}
	}

	for _, e := range weeklies {
		if corpus.Has(e.ID) {
			sum.AlreadyHave++
		} else {
			sum.Missing++
			sum.MissingCVs = append(sum.MissingCVs, e.ID)
		}
	}
	sum.RateLimited = client.RateLimitedCount()
	return sum, nil
}

func harvestIndex(ctx context.Context, client *Client, corpus Corpus, opts HarvestOpts) ([]IndexEntry, error) {
	var weeklies []IndexEntry
	seen := map[int64]bool{}
	for page := 1; ; page++ {
		entries, total, body, err := client.Index(ctx, page, opts.PageSize)
		if err != nil {
			return nil, err
		}
		if err := corpus.Write(corpus.IndexPath(page), body); err != nil {
			return nil, err
		}
		for _, e := range entries {
			if _, ok := IssueNumber(e.Title); !ok || seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			weeklies = append(weeklies, e)
		}
		if len(entries) == 0 || page*opts.PageSize >= total {
			break
		}
		if err := sleep(ctx, opts.Gap); err != nil {
			return nil, err
		}
	}
	return weeklies, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
