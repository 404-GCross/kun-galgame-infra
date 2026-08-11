// harvest-hihyou stores Galgame 批评's Gal周报 issues on disk and nothing else.
// It is deliberately a separate binary from import-hihyou-weekly: iterating the
// segmentation predicate must never mean asking bilibili for the corpus again.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/hihyou"
	"api/pkg/logger"
)

func main() {
	def := hihyou.DefaultHarvestOpts()
	dir := flag.String("dir", "", "corpus directory (article/ and index/ are created under it)")
	passes := flag.Int("passes", def.Passes, "second-chance passes over articles still missing")
	gap := flag.Duration("gap", def.Gap, "delay between requests; -509 is a rolling per-IP quota, so a short gap recovers less, not more")
	cooldown := flag.Duration("cooldown", def.Cooldown, "delay between passes")
	pageSize := flag.Int("page-size", def.PageSize, "index page size (upstream caps at 30)")
	refresh := flag.Bool("refresh", false, "re-fetch articles already complete on disk")
	flag.Parse()

	logger.Init(os.Getenv("KUN_ENV"))

	sum, err := hihyou.Harvest(context.Background(), hihyou.HarvestOpts{
		Dir:      *dir,
		Passes:   *passes,
		Gap:      *gap,
		Cooldown: *cooldown,
		PageSize: *pageSize,
		Refresh:  *refresh,
	})
	if sum != nil {
		b, _ := json.MarshalIndent(sum, "", "  ")
		os.Stdout.Write(append(b, '\n'))
	}
	if err != nil {
		slog.Error("harvest-hihyou", "error", err)
		os.Exit(1)
	}
}
