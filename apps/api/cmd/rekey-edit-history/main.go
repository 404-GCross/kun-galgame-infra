// rekey-edit-history re-anchors the editing engine's history from the retiring
// wiki family onto the catalog registry (N5, refs/proj/161 §1 P1).
//
// It moves every edit_proposal / edit_revision / edit_proposal_amendment row
// filed against `galgame.game` onto `catalog.work`:
//
//	entity_family galgame → catalog
//	entity_type   galgame.game → catalog.work
//	entity_id     wiki gid → catalog work id (catalog_external_ref, wiki:gid)
//	field keys    the 26 wiki keys → the wave-154 catalog.work key table
//	              (keymap.go: 17 mapped onto 9, 9 retired in place)
//	snapshots     transformed into the catalog vocabulary and VALIDATED with
//	              the catalog fields' own closures before being written
//
// Preserved untouched: seq, the proposer/amender double signature, proposal
// status and decision bookkeeping, the proposal↔revision chain, and the legacy
// columns the E2a transform wrote. This tool renames a coordinate system; it
// does not rewrite what happened.
//
// A gid with no catalog work is RESIDUE: it is not migrated, it is listed
// (--residue-out), and the T phase dumps then deletes it. Rows are held
// together by entity: if a gid is residue, every proposal and revision of that
// gid is residue, so no chain can be half-migrated.
//
// Idempotent: the work set is `entity_type = 'galgame.game'`, which the apply
// empties. A second run migrates zero rows.
//
//	go run ./cmd/rekey-edit-history --dsn '...'                     # dry run
//	go run ./cmd/rekey-edit-history --dsn '...' --apply             # migrate
//	go run ./cmd/rekey-edit-history --dsn '...' --verify-only       # invariants
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/editing"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// entityChunk bounds one transaction: how many wiki entities are transformed
// and written together.
const entityChunk = 200

const (
	// wikiType is the work set: every engine row still filed under the
	// retiring family.
	wikiType = "galgame.game"

	// catalogFamily is the family the catalog.work spec registers under
	// (editspec.RegisterWork, Family: "catalog"). Spelled here because the
	// UPDATE writes the column directly.
	catalogFamily = "catalog"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED — this tool never guesses a database)")
	apply := flag.Bool("apply", false, "write changes (default: dry run, ledger only)")
	residueOut := flag.String("residue-out", "", "write the residue list to this JSON file")
	verifyOnly := flag.Bool("verify-only", false, "run the invariant checks against the database and exit")
	sampleDiff := flag.Int("sample-diff", 50, "render this many migrated entities through the engine's Diff read path after an apply (0 = skip)")
	flag.Parse()

	logger.Init("development")
	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(2)
	}
	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		slog.Error("db connect", "error", err)
		os.Exit(1)
	}
	if s, err := db.DB(); err == nil {
		defer s.Close()
	}

	ctx := context.Background()
	if *verifyOnly {
		if err := runVerify(ctx, db); err != nil {
			slog.Error("verify failed", "error", err)
			os.Exit(1)
		}
		if *sampleDiff > 0 {
			if err := sampleDiffs(ctx, db, *sampleDiff); err != nil {
				slog.Error("diff sampling failed", "error", err)
				os.Exit(1)
			}
		}
		return
	}

	r := &rekeyer{db: db, apply: *apply, sampleDiff: *sampleDiff}
	if err := r.run(ctx); err != nil {
		slog.Error("rekey failed", "error", err)
		os.Exit(1)
	}
	if *residueOut != "" {
		if err := writeJSON(*residueOut, r.residue); err != nil {
			slog.Error("write residue", "error", err)
			os.Exit(1)
		}
		slog.Info("residue written", "file", *residueOut, "entries", len(r.residue))
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}

// residueEntry is one row (or one entity's rows) the migration refuses to move.
type residueEntry struct {
	Kind     string `json:"kind"` // "entity" | "open_proposal"
	GID      int64  `json:"gid"`
	Reason   string `json:"reason"`
	Revision int    `json:"revisions"`
	Proposal int    `json:"proposals"`
}

type rekeyer struct {
	db         *gorm.DB
	apply      bool
	sampleDiff int

	tr      *transformer
	ids     *idMaps
	residue []residueEntry

	// ledger
	entities, entitiesMapped                 int
	revisions, revisionsMapped               int
	proposals, proposalsMapped               int
	amendments, amendmentsMapped             int
	seqOffsetEntities, seqOffsetMaxDisplaced int
}

func (r *rekeyer) run(ctx context.Context) error {
	srcID, err := wikiSourceID(ctx, r.db)
	if err != nil {
		return fmt.Errorf("resolve galgame_wiki source: %w", err)
	}
	r.ids, err = loadIDMaps(ctx, r.db, srcID)
	if err != nil {
		return fmt.Errorf("load id maps: %w", err)
	}
	slog.Info("id maps loaded",
		"source_id", srcID, "work", len(r.ids.Work), "label", len(r.ids.Label),
		"tag", len(r.ids.Tag), "engine", len(r.ids.Engine), "via_redirect", r.ids.Redirected)

	reg := editing.NewRegistry()
	if err := editspec.RegisterWork(reg, r.db); err != nil {
		return fmt.Errorf("register catalog.work: %w", err)
	}
	if r.tr, err = newTransformer(r.ids, reg); err != nil {
		return err
	}

	gids, err := r.wikiEntities(ctx)
	if err != nil {
		return err
	}
	r.entities = len(gids)
	for start := 0; start < len(gids); start += entityChunk {
		end := min(start+entityChunk, len(gids))
		if err := r.chunk(ctx, gids[start:end]); err != nil {
			return fmt.Errorf("chunk [%d,%d): %w", start, end, err)
		}
	}
	r.printLedger()
	if !r.apply {
		return nil
	}
	if err := runVerify(ctx, r.db); err != nil {
		return err
	}
	if r.sampleDiff > 0 {
		return sampleDiffs(ctx, r.db, r.sampleDiff)
	}
	return nil
}

// wikiEntities lists every gid that still holds engine rows, from BOTH tables:
// a proposal-only entity (declined without ever merging) is as much an entity
// as one with revisions.
func (r *rekeyer) wikiEntities(ctx context.Context) ([]int64, error) {
	var gids []int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT entity_id FROM edit_revision WHERE entity_type = ?
		 UNION
		 SELECT entity_id FROM edit_proposal WHERE entity_type = ?
		 ORDER BY 1`, wikiType, wikiType).Scan(&gids).Error
	return gids, err
}

type revisionRow struct {
	ID            int64  `gorm:"column:id"`
	EntityID      int64  `gorm:"column:entity_id"`
	Seq           int    `gorm:"column:seq"`
	ChangedFields []byte `gorm:"column:changed_fields"`
	Snapshot      []byte `gorm:"column:snapshot"`
}

type proposalRow struct {
	ID              int64  `gorm:"column:id"`
	EntityID        int64  `gorm:"column:entity_id"`
	BaseRevisionSeq int    `gorm:"column:base_revision_seq"`
	Patch           []byte `gorm:"column:patch"`
	Status          int16  `gorm:"column:status"`
}

func (r *rekeyer) chunk(ctx context.Context, gids []int64) error {
	var revs []revisionRow
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, entity_id, seq, changed_fields, snapshot FROM edit_revision
		 WHERE entity_type = ? AND entity_id IN ? ORDER BY entity_id, seq`, wikiType, gids).
		Scan(&revs).Error; err != nil {
		return err
	}
	var props []proposalRow
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, entity_id, base_revision_seq, patch, status FROM edit_proposal
		 WHERE entity_type = ? AND entity_id IN ? ORDER BY entity_id, id`, wikiType, gids).
		Scan(&props).Error; err != nil {
		return err
	}
	r.revisions += len(revs)
	r.proposals += len(props)

	// Seq offsets: a work that ALREADY has catalog.work revisions (N2/N3 shipped
	// before this window) would collide on uq_edit_revision_entity_seq. The
	// migrated chain is then appended above the existing max — order and
	// monotonicity preserved, and every displaced entity is reported, because
	// a downstream consumer holding a seq needs to know it moved.
	offsets, err := r.seqOffsets(ctx, gids)
	if err != nil {
		return err
	}

	byGID := map[int64][]revisionRow{}
	for _, rev := range revs {
		byGID[rev.EntityID] = append(byGID[rev.EntityID], rev)
	}
	propsByGID := map[int64][]proposalRow{}
	for _, p := range props {
		propsByGID[p.EntityID] = append(propsByGID[p.EntityID], p)
	}

	type revUpdate struct {
		id       int64
		workID   int64
		seq      int
		changed  []byte
		snapshot []byte
	}
	type propUpdate struct {
		id      int64
		workID  int64
		baseSeq int
		patch   []byte
	}
	var revUpdates []revUpdate
	var propUpdates []propUpdate
	var migratedProposalIDs []int64

	for _, gid := range gids {
		workID, mapped := r.ids.Work[gid]
		if !mapped {
			r.residue = append(r.residue, residueEntry{
				Kind: "entity", GID: gid,
				Reason:   "no catalog work carries this gid (catalog_external_ref wiki:gid): the entity has no registry counterpart to re-anchor onto",
				Revision: len(byGID[gid]), Proposal: len(propsByGID[gid]),
			})
			continue
		}
		r.entitiesMapped++
		offset := offsets[workID]
		if offset > 0 {
			r.seqOffsetEntities++
			r.seqOffsetMaxDisplaced = max(r.seqOffsetMaxDisplaced, offset)
		}
		for _, rev := range byGID[gid] {
			snapshot, err := decodeObject(rev.Snapshot)
			if err != nil {
				return fmt.Errorf("revision %d snapshot: %w", rev.ID, err)
			}
			newSnap, route := r.tr.document(snapshot, modeSnapshot)
			var changed []string
			if err := json.Unmarshal(rev.ChangedFields, &changed); err != nil {
				return fmt.Errorf("revision %d changed_fields: %w", rev.ID, err)
			}
			snapJSON, err := json.Marshal(newSnap)
			if err != nil {
				return err
			}
			changedJSON, err := json.Marshal(rekeyChangedFields(changed, route))
			if err != nil {
				return err
			}
			revUpdates = append(revUpdates, revUpdate{
				id: rev.ID, workID: workID, seq: rev.Seq + offset,
				changed: changedJSON, snapshot: snapJSON,
			})
		}
		for _, p := range propsByGID[gid] {
			if p.Status == editing.StatusOpen {
				// An open proposal's patch is still REPLAYABLE, and a patch is a
				// partial document whose fold keys cannot be translated (see
				// transform.go docMode). Migrating one would hand the merge path a
				// half-vocabulary patch. It stays behind as residue; the window
				// operator decides (decline it, or merge it before the cutover).
				r.residue = append(r.residue, residueEntry{
					Kind: "open_proposal", GID: gid,
					Reason:   fmt.Sprintf("proposal %d is still OPEN: its patch is replayable and cannot be safely rekeyed — decline or merge it before the window", p.ID),
					Proposal: 1,
				})
				continue
			}
			patch, err := decodeObject(p.Patch)
			if err != nil {
				return fmt.Errorf("proposal %d patch: %w", p.ID, err)
			}
			newPatch, _ := r.tr.document(patch, modePatch)
			patchJSON, err := json.Marshal(newPatch)
			if err != nil {
				return err
			}
			baseSeq := p.BaseRevisionSeq
			if baseSeq > 0 {
				baseSeq += offset
			}
			propUpdates = append(propUpdates, propUpdate{id: p.ID, workID: workID, baseSeq: baseSeq, patch: patchJSON})
			migratedProposalIDs = append(migratedProposalIDs, p.ID)
		}
	}

	amendments, err := r.amendmentUpdates(ctx, migratedProposalIDs)
	if err != nil {
		return err
	}
	r.revisionsMapped += len(revUpdates)
	r.proposalsMapped += len(propUpdates)
	r.amendmentsMapped += len(amendments)

	if !r.apply {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range revUpdates {
			if err := tx.Exec(
				`UPDATE edit_revision SET entity_family = ?, entity_type = ?, entity_id = ?, seq = ?,
				        changed_fields = ?, snapshot = ?
				 WHERE id = ? AND entity_type = ?`,
				catalogFamily, editspec.TypeWork, u.workID, u.seq, u.changed, u.snapshot, u.id, wikiType).
				Error; err != nil {
				return err
			}
		}
		for _, u := range propUpdates {
			if err := tx.Exec(
				`UPDATE edit_proposal SET entity_family = ?, entity_type = ?, entity_id = ?,
				        base_revision_seq = ?, patch = ?
				 WHERE id = ? AND entity_type = ?`,
				catalogFamily, editspec.TypeWork, u.workID, u.baseSeq, u.patch, u.id, wikiType).
				Error; err != nil {
				return err
			}
		}
		for id, delta := range amendments {
			if err := tx.Exec(`UPDATE edit_proposal_amendment SET patch_delta = ? WHERE id = ?`, delta, id).
				Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// seqOffsets returns, per work id, the max seq already held by NATIVE
// catalog.work revisions (0 = none, the expected case in the N5 window).
func (r *rekeyer) seqOffsets(ctx context.Context, gids []int64) (map[int64]int, error) {
	workIDs := make([]int64, 0, len(gids))
	for _, gid := range gids {
		if workID, ok := r.ids.Work[gid]; ok {
			workIDs = append(workIDs, workID)
		}
	}
	out := map[int64]int{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		EntityID int64 `gorm:"column:entity_id"`
		MaxSeq   int   `gorm:"column:max_seq"`
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT entity_id, max(seq) AS max_seq FROM edit_revision
		 WHERE entity_type = ? AND entity_id IN ? GROUP BY entity_id`,
		editspec.TypeWork, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.EntityID] = row.MaxSeq
	}
	return out, nil
}

// amendmentUpdates rekeys the patch deltas of the proposals being migrated.
func (r *rekeyer) amendmentUpdates(ctx context.Context, proposalIDs []int64) (map[int64][]byte, error) {
	out := map[int64][]byte{}
	if len(proposalIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID         int64  `gorm:"column:id"`
		PatchDelta []byte `gorm:"column:patch_delta"`
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, patch_delta FROM edit_proposal_amendment WHERE proposal_id IN ?`, proposalIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	r.amendments += len(rows)
	for _, row := range rows {
		delta, err := decodeObject(row.PatchDelta)
		if err != nil {
			return nil, fmt.Errorf("amendment %d: %w", row.ID, err)
		}
		encoded, err := json.Marshal(r.tr.patchDelta(delta))
		if err != nil {
			return nil, err
		}
		out[row.ID] = encoded
	}
	return out, nil
}

func (r *rekeyer) printLedger() {
	slog.Info("entities", "wiki_total", r.entities, "mapped", r.entitiesMapped,
		"residue", r.entities-r.entitiesMapped)
	slog.Info("rows", "revisions", r.revisions, "revisions_mapped", r.revisionsMapped,
		"proposals", r.proposals, "proposals_mapped", r.proposalsMapped,
		"amendments", r.amendments, "amendments_mapped", r.amendmentsMapped)
	slog.Info("seq", "entities_offset", r.seqOffsetEntities, "max_offset", r.seqOffsetMaxDisplaced)

	fmt.Println("\n── field keys MAPPED (catalog key → documents) ──")
	for _, key := range sortedByCount(r.tr.stats.Mapped) {
		fmt.Printf("  %-38s %7d\n", key, r.tr.stats.Mapped[key])
	}
	fmt.Println("\n── field keys RETIRED IN PLACE (wiki key → documents) ──")
	for _, key := range sortedByCount(r.tr.stats.Retired) {
		fmt.Printf("  %-38s %7d\n", key, r.tr.stats.Retired[key])
	}
	fmt.Println("\n── retirement reasons ──")
	for _, reason := range sortedByCount(r.tr.stats.Reasons) {
		fmt.Printf("  %7d  %s\n", r.tr.stats.Reasons[reason], reason)
	}
	fmt.Printf("\n── residue (%d entries) ──\n", len(r.residue))
	for i, entry := range r.residue {
		if i == 30 {
			fmt.Printf("  … %d more (use --residue-out for the full list)\n", len(r.residue)-30)
			break
		}
		fmt.Printf("  %-14s gid=%-8d revisions=%d proposals=%d  %s\n",
			entry.Kind, entry.GID, entry.Revision, entry.Proposal, entry.Reason)
	}
	fmt.Println()
}

func sortedByCount(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

func decodeObject(raw []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
