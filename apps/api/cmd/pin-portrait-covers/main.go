// pin-portrait-covers selects, super-resolves and pins the best PORTRAIT cover
// per wiki galgame — the vertical companion to the landscape sort_order=0 pin
// (U-02b). It never touches existing landscape pins or existing cover rows: a
// super-resolved image is a NEW cover row (source='upscale') that gets the
// portrait pin, so the original is never dereferenced.
//
// Three states per game (best portrait = largest long-edge, kind main>pkgfront):
//   - long-edge >= 1080            → pin it directly
//   - long-edge <  1080            → export for upscale-bench, reinject + pin
//   - no portrait cover            → no action (frontend fallback, step 37)
//
// A best portrait with sexual >= 2 is NOT auto-pinned (recorded), a conservative
// gate the existing landscape display lacks (41-doc env fact 5).
//
// Modes (default = dry):
//
//	pin-portrait-covers                                  # dry: full-library three-state report
//	pin-portrait-covers --apply                          # pin the >=1080 best portraits
//	pin-portrait-covers --export-dir DIR [--limit N]     # download <1080 best portraits for upscale-bench
//	pin-portrait-covers --reinject-dir DIR --apply \     # upload upscaled webp + insert source='upscale' row + pin
//	    --image-base-url http://127.0.0.1:15006
//
// IMPORTANT: --image-base-url MUST point at the LOCAL compose image service
// (MinIO-backed); the native dev service's S3 targets production R2 (P3 mine).
// Original bytes are fetched read-only from the production CDN (safe).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"sort"
	"strings"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run — report only)")
	limit := flag.Int("limit", 0, "max games to act on (0 = all)")
	exportDir := flag.String("export-dir", "", "EXPORT mode: download <1080 best portraits here for upscale-bench")
	reinjectDir := flag.String("reinject-dir", "", "REINJECT mode: read upscale-bench webp outputs here, upload + insert + pin")
	imageBaseURL := flag.String("image-base-url", "", "image_service base for uploads — the LOCAL compose service (http://127.0.0.1:15006), never the native dev service")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)
	ctx := context.Background()

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("images db connect", "error", err)
		os.Exit(1)
	}
	defer imagesDB.Close()

	// Reinject reads upscaled files back; it does not need the selection scan.
	if *reinjectDir != "" {
		if err := runReinject(ctx, cfg, wikiDB.DB(), *reinjectDir, *imageBaseURL, *apply); err != nil {
			slog.Error("reinject", "error", err)
			os.Exit(1)
		}
		return
	}

	dims, err := loadDims(ctx, imagesDB.DB())
	if err != nil {
		slog.Error("load dims", "error", err)
		os.Exit(1)
	}
	slog.Info("dims loaded", "images_with_dims", len(dims))

	sels, err := buildSelections(ctx, wikiDB.DB(), dims)
	if err != nil {
		slog.Error("build selections", "error", err)
		os.Exit(1)
	}
	reportStates(sels)

	switch {
	case *exportDir != "":
		if err := runExport(ctx, cfg, sels, *exportDir, *limit); err != nil {
			slog.Error("export", "error", err)
			os.Exit(1)
		}
	case *apply:
		if err := runPinApply(ctx, wikiDB.DB(), sels, *limit); err != nil {
			slog.Error("pin apply", "error", err)
			os.Exit(1)
		}
	default:
		slog.Info("DRY RUN — nothing written; --apply to pin >=1080, --export-dir to stage upscales")
	}
}

// loadDims reads {hash → [w,h]} for every non-deleted image (mirrors
// portraitfill.loadDims; the two tools are independent one-shots).
func loadDims(ctx context.Context, db *gorm.DB) (map[string][2]int, error) {
	type row struct {
		Hash   string
		Width  int
		Height int
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(
		"SELECT hash, width, height FROM images WHERE deleted_at IS NULL").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string][2]int, len(rows))
	for _, r := range rows {
		out[strings.TrimSpace(r.Hash)] = [2]int{r.Width, r.Height}
	}
	return out, nil
}

// buildSelections loads every cover row, joins dims in memory, groups by game
// and classifies each. Games are returned id-ascending for deterministic slices.
func buildSelections(ctx context.Context, db *gorm.DB, dims map[string][2]int) ([]selection, error) {
	type row struct {
		GalgameID      int    `gorm:"column:galgame_id"`
		ImageHash      string `gorm:"column:image_hash"`
		Kind           string `gorm:"column:kind"`
		Sexual         int16  `gorm:"column:sexual"`
		Violence       int16  `gorm:"column:violence"`
		Source         string `gorm:"column:source"`
		PortraitPinned bool   `gorm:"column:portrait_pinned"`
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(
		`SELECT galgame_id, image_hash, kind, sexual, violence, source, portrait_pinned
		   FROM galgame_cover ORDER BY galgame_id`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	byGame := map[int][]coverRow{}
	order := []int{}
	for _, r := range rows {
		if _, seen := byGame[r.GalgameID]; !seen {
			order = append(order, r.GalgameID)
		}
		c := coverRow{
			GameID: r.GalgameID, Hash: strings.TrimSpace(r.ImageHash), Kind: r.Kind,
			Sexual: r.Sexual, Violence: r.Violence, Source: r.Source, PortraitPinned: r.PortraitPinned,
		}
		if d, ok := dims[c.Hash]; ok {
			c.Width, c.Height, c.DimsKnown = d[0], d[1], true
		}
		byGame[r.GalgameID] = append(byGame[r.GalgameID], c)
	}
	sort.Ints(order)
	out := make([]selection, 0, len(order))
	for _, gid := range order {
		out = append(out, classify(gid, byGame[gid]))
	}
	return out, nil
}

// reportStates logs the full-library three-state distribution (the dry report).
func reportStates(sels []selection) {
	var noPortrait, alreadyPinned, nsfw, directPin, needUpscale, hasUpscale, needUpscalePending int
	longEdge := map[string]int{"<512": 0, "512-767": 0, "768-1079": 0}
	for _, s := range sels {
		switch s.State {
		case stateNoPortrait:
			noPortrait++
		case stateAlreadyPinned:
			alreadyPinned++
		case stateNSFWDeferred:
			nsfw++
		case stateDirectPin:
			directPin++
		case stateNeedUpscale:
			needUpscale++
			if s.HasUpscale {
				needUpscalePending++ // already upscaled but best still <1080 (rare)
			}
			switch {
			case s.Best.Height < 512:
				longEdge["<512"]++
			case s.Best.Height < 768:
				longEdge["512-767"]++
			default:
				longEdge["768-1079"]++
			}
		}
		if s.HasUpscale {
			hasUpscale++
		}
	}
	slog.Info("portrait selection — full-library three-state",
		"games", len(sels),
		"direct_pin_>=1080", directPin,
		"need_upscale_<1080", needUpscale,
		"no_portrait", noPortrait,
		"nsfw_deferred", nsfw,
		"already_pinned", alreadyPinned,
		"has_upscale_row", hasUpscale,
		"need_upscale_but_upscale_exists", needUpscalePending,
		"need_upscale_long_edge_dist", longEdge)
}
