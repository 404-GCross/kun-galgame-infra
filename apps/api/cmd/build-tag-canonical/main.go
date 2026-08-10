package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/tagcanon"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, digest only)")
	dsn := flag.String("dsn", "", "catalog DSN (holds all three tag vocabularies) — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max groups to write (0 = all; >0 = small-sample apply, Norm-sorted)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := tagcanon.Run(context.Background(), tagcanon.Opts{Apply: *apply, DSN: *dsn, Limit: *limit})
	if err != nil {
		slog.Error("build-tag-canonical", "error", err)
		os.Exit(1)
	}
	slog.Info("build-tag-canonical summary",
		"apply", *apply, "limit", *limit,
		"vndb_names", st.VndbNames, "bangumi_names", st.BangumiNames, "dlsite_names", st.DlsiteNames,
		"bangumi_junk", st.BangumiJunk, "groups", st.Groups, "meta_groups", st.MetaGroups,
		"tri_source", st.TriSource, "planned_maps", st.PlannedMaps,
		"tags_created", st.TagsCreated, "tags_conflict", st.TagsConflict,
		"maps_created", st.MapsCreated, "maps_conflict", st.MapsConflict, "errors", st.Errors)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
