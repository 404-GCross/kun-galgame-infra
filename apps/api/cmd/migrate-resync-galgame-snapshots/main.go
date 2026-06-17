// migrate-resync-galgame-snapshots restores the invariant "the latest galgame
// revision snapshot == the live galgame state" for every galgame whose head
// revision drifted from live.
//
// WHY: VNDB enrichment + one-off backfills (covers, screenshots, bid,
// release_date, aliases) historically wrote the LIVE galgame tables without
// minting a revision or patching the head snapshot. That left ~9.6k head
// snapshots stale, so the NEXT user edit's /diff compared against a stale
// baseline and over-reported almost every field as "newly added". This catches
// the head snapshots up to live (the same in-place head-patch ReconcileVndbLinks
// already performs for the ongoing sync) so future diffs show only the real
// change — and it also fixes the latent PR-rebase / revert-to-head correctness
// bugs that read the head as "current".
//
// SAFE: only the HEAD (max revision) of each galgame is touched — historical
// revisions are left untouched. Verified one-directional on prod (live ≥
// snapshot in 100% of cases), so this only ever fills in what live already has;
// it never removes snapshot-only data. No new revisions are created. Legacy
// changed_fields stays NULL (→ /diff fallback). Idempotent: a head already
// equal to live is skipped, so a re-run after a full apply plans 0 writes.
//
//	go run ./cmd/migrate-resync-galgame-snapshots                  # dry run (report only)
//	go run ./cmd/migrate-resync-galgame-snapshots --apply --limit 5    # apply a tiny test batch
//	go run ./cmd/migrate-resync-galgame-snapshots --apply          # perform all
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	apply := flag.Bool("apply", false, "perform the changes (default: dry run, nothing written)")
	samples := flag.Int("samples", 30, "number of drifted galgames to print")
	limit := flag.Int("limit", 0, "cap the number of head snapshots rewritten (0 = all); for a safe test batch")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame db", "error", err, "dbname", cfg.GalgameDatabase.DBName)
		os.Exit(1)
	}
	defer db.Close()
	gdb := db.DB()

	// All galgames that have at least one revision.
	var gids []int
	if err := gdb.Model(&model.GalgameRevision{}).
		Distinct("galgame_id").Order("galgame_id").Pluck("galgame_id", &gids).Error; err != nil {
		slog.Error("load galgame ids with revisions", "error", err)
		os.Exit(1)
	}
	fmt.Printf("galgames with revision history: %d\n", len(gids))

	type drift struct {
		GID        int
		HeadRev    int
		HeadRevID  int
		LiveJSON   []byte
		ChangedKey []string
	}

	var drifted []drift
	keyHisto := map[string]int{}
	scanned := 0
	for _, gid := range gids {
		live, err := loadLive(gdb, gid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				continue // revision rows whose galgame was hard-deleted — skip
			}
			slog.Error("load live galgame", "gid", gid, "error", err)
			os.Exit(1)
		}
		scanned++
		liveSnap := model.TakeSnapshot(live)

		var head model.GalgameRevision
		if err := gdb.Where("galgame_id = ?", gid).
			Order("revision DESC").Limit(1).First(&head).Error; err != nil {
			slog.Error("load head revision", "gid", gid, "error", err)
			os.Exit(1)
		}
		storedSnap, err := model.SnapshotFromJSON(head.Snapshot)
		if err != nil {
			slog.Error("parse head snapshot", "gid", gid, "rev", head.Revision, "error", err)
			os.Exit(1)
		}

		changed := model.ChangedKeys(storedSnap, liveSnap)
		if len(changed) == 0 {
			continue // head already == live (idempotent skip)
		}
		liveJSON, err := liveSnap.ToJSON()
		if err != nil {
			slog.Error("marshal live snapshot", "gid", gid, "error", err)
			os.Exit(1)
		}
		keys := model.KeysOf(changed)
		for _, k := range keys {
			keyHisto[k]++
		}
		drifted = append(drifted, drift{GID: gid, HeadRev: head.Revision, HeadRevID: head.ID, LiveJSON: liveJSON, ChangedKey: keys})
	}

	fmt.Printf("\n==================== PLAN ====================\n")
	fmt.Printf("scanned (live galgame found):        %d\n", scanned)
	fmt.Printf("head snapshots drifted from live:    %d\n", len(drifted))
	fmt.Printf("drift by field (head ≠ live):\n")
	hk := make([]string, 0, len(keyHisto))
	for k := range keyHisto {
		hk = append(hk, k)
	}
	sort.Slice(hk, func(i, j int) bool { return keyHisto[hk[i]] > keyHisto[hk[j]] })
	for _, k := range hk {
		fmt.Printf("  · %-18s %d\n", k, keyHisto[k])
	}
	for i, d := range drifted {
		if i >= *samples {
			fmt.Printf("  … (+%d more)\n", len(drifted)-*samples)
			break
		}
		fmt.Printf("  galgame #%d head rev %d → resync (%v)\n", d.GID, d.HeadRev, d.ChangedKey)
	}

	if !*apply {
		fmt.Printf("\nDRY RUN — nothing written. Re-run with --apply to perform.\n")
		return
	}

	toApply := drifted
	if *limit > 0 && *limit < len(toApply) {
		toApply = toApply[:*limit]
		fmt.Printf("\n--limit %d: rewriting only the first %d head snapshots\n", *limit, len(toApply))
	}
	fmt.Printf("\nAPPLYING %d head-snapshot resyncs (in-place, no new revisions)…\n", len(toApply))

	applied := 0
	for _, d := range toApply {
		// Update by the head row's PK; condition on it still being the max
		// revision guards against a concurrent edit minting a newer head
		// between scan and apply (in which case the newer head is already live).
		res := gdb.Exec(`
			UPDATE galgame_revision
			SET snapshot = ?::jsonb
			WHERE id = ?
			  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
		`, string(d.LiveJSON), d.HeadRevID, d.GID)
		if res.Error != nil {
			slog.Error("resync head snapshot failed", "gid", d.GID, "rev", d.HeadRev, "error", res.Error)
			os.Exit(1)
		}
		if res.RowsAffected == 1 {
			applied++
		}
	}

	fmt.Printf("\nDONE. head snapshots resynced=%d (of %d planned).\n", applied, len(toApply))
}

// loadLive mirrors service.loadGalgameWithRelations: it preloads every relation
// TakeSnapshot reads, in the same deterministic order, so the rebuilt snapshot
// is byte-identical to what the service layer would record for the same state.
func loadLive(db *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := db.
		Preload("Alias").
		Preload("Tag").
		Preload("Official").
		Preload("Engine").
		Preload("Link", func(db *gorm.DB) *gorm.DB { return db.Order("id ASC") }).
		Preload("Cover", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, created ASC") }).
		Preload("Screenshot", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, created ASC") }).
		First(&g, id).Error
	return &g, err
}
