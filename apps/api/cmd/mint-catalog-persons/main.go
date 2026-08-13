package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"api/internal/jobs/personmint"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, accounting only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	clusters := flag.String("clusters", "", "path to the wave-152 clusters.jsonl — REQUIRED")
	worklist := flag.String("split-worklist", "", "path to the wave-152 e4_p2_split_worklist.jsonl — REQUIRED")
	outDir := flag.String("out-dir", "", "directory for the defer / conflict artefacts (optional)")
	limit := flag.Int("limit", 0, "max auto clusters to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many auto clusters (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := personmint.Run(context.Background(), personmint.Opts{
		Apply:             *apply,
		DSN:               *dsn,
		ClustersPath:      *clusters,
		SplitWorklistPath: *worklist,
		Limit:             *limit,
		Offset:            *offset,
	})
	if err != nil {
		slog.Error("mint-catalog-persons", "error", err)
		os.Exit(1)
	}
	for _, s := range st.Samples {
		slog.Info("person-mint sample", "cluster", s.ClusterID, "reused_person", s.PersonID,
			"display_name", s.DisplayName, "names", s.Names, "anchors", s.Anchors)
	}
	if *outDir != "" {
		if err := dump(*outDir, st); err != nil {
			slog.Error("mint-catalog-persons artefacts", "error", err)
			os.Exit(1)
		}
	}
	slog.Info("mint-catalog-persons done", "apply", *apply,
		"clusters_total", st.ClustersTotal, "clusters_auto", st.ClustersAuto, "members", st.Members,
		"minted", st.Minted, "minted_new", st.MintedNew, "minted_reuse", st.MintedReuse,
		"deferred", st.Deferred, "defers", st.Defers, "defer_overlap", st.Overlap,
		"would_create_person", st.WouldCreatePerson, "would_link", st.WouldLink, "links_already", st.LinksAlready,
		"would_anchor", st.WouldAnchor, "anchors_already", st.AnchorsAlready,
		"would_set_gender", st.WouldSetGender, "gender_kept", st.GenderKept, "gender_conflicts", st.GenderConflicts,
		"would_set_birth", st.WouldSetBirth, "birth_kept", st.BirthKept, "birth_conflicts", st.BirthConflicts,
		"persons_created", st.PersonsCreated, "links_written", st.LinksWritten,
		"anchors_written", st.AnchorsWritten, "persons_updated", st.PersonsUpdated, "errors", st.Errors)
	if st.Errors > 0 {
		os.Exit(1)
	}
}

func dump(dir string, st *personmint.Stats) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(dir, "defers.jsonl"), len(st.DeferList), func(i int) any { return st.DeferList[i] }); err != nil {
		return err
	}
	return writeJSONL(filepath.Join(dir, "gender_conflicts.jsonl"), len(st.Conflicts), func(i int) any { return st.Conflicts[i] })
}

func writeJSONL(path string, n int, at func(int) any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := 0; i < n; i++ {
		if err := enc.Encode(at(i)); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
