// Package chartraits imports the VNDB trait vocabulary + hierarchy + the
// character×trait links into the catalog (step 93, refs/proj/93):
//
//   - phase 1 vocab: src_vndb.traits (3,327) → catalog_character_trait,
//     change-detected upsert keyed by vndb_tid (a dump refresh + re-run updates
//     renamed/reflagged rows in place; an unchanged re-run writes zero).
//   - phase 2 hierarchy: src_vndb.traits_parents (~3.7k) →
//     catalog_character_trait_parent, vndb tids resolved to OUR ids;
//     insert-if-absent + stale-edge delete (the dump is the truth).
//   - phase 3 links: src_vndb.chars_traits × EXACT vndb character anchors
//     (entity_type=4, source_id=2, link_kind=0; matched_by unrestricted — the
//     66/69 ruling) → catalog_character_trait_link, streamed in 1k-row
//     multi-row upserts with change detection (spoiler re-grades upstream →
//     refresh-runnable, the step-62 discipline). ~2.9M rows.
//
// Single --dsn (src_vndb is a schema inside the catalog DB). Dry-run default.
package chartraits

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// linkBatchSize keeps a multi-row upsert at 4 bind params per row — far under
// the 65,535 cap (the PG bind-parameter ceiling, multiple prior stings).
const linkBatchSize = 1000

// Opts configures a run.
type Opts struct {
	Apply bool
	DSN   string // catalog DB (hosts src_vndb) — REQUIRED
}

// Stats reports a run. Planned counters are identical in dry and apply.
type Stats struct {
	VocabTotal     int // src vocabulary rows seen
	VocabWritten   int // created or value-changed (apply); planned writes (dry)
	VocabUnchanged int

	EdgesTotal   int // resolved DAG edges in the dump
	EdgesAdded   int
	EdgesDeleted int // stale edges removed (dump = truth)

	LinksSeen      int // anchored link rows streamed
	LinksWritten   int // created or value-changed
	LinksUnchanged int

	Errors int
}

// Run executes the import.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	db, err := gorm.Open(postgres.Open(opts.DSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	st := &Stats{}
	if err := runVocab(ctx, db, opts, st); err != nil {
		return nil, err
	}
	idByTID, err := loadIDMap(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := runEdges(ctx, db, opts, idByTID, st); err != nil {
		return nil, err
	}
	if err := runLinks(ctx, db, opts, st); err != nil {
		return nil, err
	}
	slog.Info("chartraits done", "apply", opts.Apply,
		"vocab_total", st.VocabTotal, "vocab_written", st.VocabWritten, "vocab_unchanged", st.VocabUnchanged,
		"edges_total", st.EdgesTotal, "edges_added", st.EdgesAdded, "edges_deleted", st.EdgesDeleted,
		"links_seen", st.LinksSeen, "links_written", st.LinksWritten, "links_unchanged", st.LinksUnchanged,
		"errors", st.Errors)
	return st, nil
}

// runVocab upserts the vocabulary rows (phase 1). Change detection compares
// every loaded column; dry mode counts rows whose stored values differ (or are
// absent) without writing.
func runVocab(ctx context.Context, db *gorm.DB, opts Opts, st *Stats) error {
	var rows []struct {
		ID           string `gorm:"column:id"`
		GID          string `gorm:"column:gid"`
		GOrder       int16  `gorm:"column:gorder"`
		DefaultSpoil int16  `gorm:"column:defaultspoil"`
		Sexual       bool   `gorm:"column:sexual"`
		Searchable   bool   `gorm:"column:searchable"`
		Applicable   bool   `gorm:"column:applicable"`
		Name         string `gorm:"column:name"`
		Alias        string `gorm:"column:alias"`
		Description  string `gorm:"column:description"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT id, gid, gorder, defaultspoil, sexual, searchable, applicable, name, alias, description
		FROM src_vndb.traits ORDER BY id`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load src traits: %w", err)
	}
	st.VocabTotal = len(rows)

	if !opts.Apply {
		// Dry plan: count rows that would be created or changed, in one query
		// per run (anti-join on all columns).
		var unchanged int64
		if err := db.WithContext(ctx).Raw(`SELECT count(*) FROM src_vndb.traits s
			JOIN catalog_character_trait t ON t.vndb_tid = s.id
			WHERE (t.name, t.group_tid, t.gorder, t.default_spoil, t.sexual, t.searchable, t.applicable, t.alias, t.description)
			    = (s.name, s.gid, s.gorder, s.defaultspoil, s.sexual, s.searchable, s.applicable, s.alias, s.description)`).
			Scan(&unchanged).Error; err != nil {
			return fmt.Errorf("dry vocab plan: %w", err)
		}
		st.VocabUnchanged = int(unchanged)
		st.VocabWritten = st.VocabTotal - int(unchanged)
		return nil
	}

	for _, r := range rows {
		res := db.WithContext(ctx).Exec(`
			INSERT INTO catalog_character_trait
				(vndb_tid, name, group_tid, gorder, default_spoil, sexual, searchable, applicable, alias, description)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (vndb_tid) DO UPDATE SET
				name = EXCLUDED.name, group_tid = EXCLUDED.group_tid, gorder = EXCLUDED.gorder,
				default_spoil = EXCLUDED.default_spoil, sexual = EXCLUDED.sexual,
				searchable = EXCLUDED.searchable, applicable = EXCLUDED.applicable,
				alias = EXCLUDED.alias, description = EXCLUDED.description, updated_at = now()
			WHERE (catalog_character_trait.name, catalog_character_trait.group_tid, catalog_character_trait.gorder,
			       catalog_character_trait.default_spoil, catalog_character_trait.sexual, catalog_character_trait.searchable,
			       catalog_character_trait.applicable, catalog_character_trait.alias, catalog_character_trait.description)
			    IS DISTINCT FROM
			      (EXCLUDED.name, EXCLUDED.group_tid, EXCLUDED.gorder, EXCLUDED.default_spoil, EXCLUDED.sexual,
			       EXCLUDED.searchable, EXCLUDED.applicable, EXCLUDED.alias, EXCLUDED.description)`,
			r.ID, r.Name, r.GID, r.GOrder, r.DefaultSpoil, r.Sexual, r.Searchable, r.Applicable, r.Alias, r.Description)
		if res.Error != nil {
			st.Errors++
			slog.Warn("vocab upsert", "tid", r.ID, "err", res.Error)
			continue
		}
		if res.RowsAffected == 1 {
			st.VocabWritten++
		} else {
			st.VocabUnchanged++
		}
	}
	return nil
}

// loadIDMap returns vndb_tid → our id (after phase 1 the map is complete for
// apply; in dry mode it may be partial or empty — edge planning handles that).
func loadIDMap(ctx context.Context, db *gorm.DB) (map[string]int64, error) {
	var rows []struct {
		ID      int64  `gorm:"column:id"`
		VndbTID string `gorm:"column:vndb_tid"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT id, vndb_tid FROM catalog_character_trait`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load trait id map: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.VndbTID] = r.ID
	}
	return out, nil
}

// runEdges rebuilds the DAG edge set (phase 2): insert-if-absent + delete
// stale. The dump is the truth for hierarchy topology.
func runEdges(ctx context.Context, db *gorm.DB, opts Opts, idByTID map[string]int64, st *Stats) error {
	var rows []struct {
		ID     string `gorm:"column:id"`
		Parent string `gorm:"column:parent"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT id, parent FROM src_vndb.traits_parents`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load src trait parents: %w", err)
	}

	type edge struct{ trait, parent int64 }
	want := make(map[edge]struct{}, len(rows))
	for _, r := range rows {
		t, okT := idByTID[r.ID]
		p, okP := idByTID[r.Parent]
		if !okT || !okP {
			continue // dry mode before first apply: vocabulary not landed yet
		}
		want[edge{t, p}] = struct{}{}
	}
	st.EdgesTotal = len(want)

	var existing []struct {
		TraitID  int64 `gorm:"column:trait_id"`
		ParentID int64 `gorm:"column:parent_id"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT trait_id, parent_id FROM catalog_character_trait_parent`).Scan(&existing).Error; err != nil {
		return fmt.Errorf("load existing edges: %w", err)
	}
	have := make(map[edge]struct{}, len(existing))
	for _, e := range existing {
		have[edge{e.TraitID, e.ParentID}] = struct{}{}
	}

	var toAdd []edge
	for e := range want {
		if _, ok := have[e]; !ok {
			toAdd = append(toAdd, e)
		}
	}
	var toDel []edge
	for e := range have {
		if _, ok := want[e]; !ok {
			toDel = append(toDel, e)
		}
	}
	st.EdgesAdded = len(toAdd)
	st.EdgesDeleted = len(toDel)
	if !opts.Apply {
		return nil
	}
	for _, e := range toAdd {
		if err := db.WithContext(ctx).Exec(`INSERT INTO catalog_character_trait_parent (trait_id, parent_id)
			VALUES (?, ?) ON CONFLICT DO NOTHING`, e.trait, e.parent).Error; err != nil {
			st.Errors++
			slog.Warn("edge insert", "trait", e.trait, "parent", e.parent, "err", err)
		}
	}
	for _, e := range toDel {
		if err := db.WithContext(ctx).Exec(`DELETE FROM catalog_character_trait_parent
			WHERE trait_id = ? AND parent_id = ?`, e.trait, e.parent).Error; err != nil {
			st.Errors++
			slog.Warn("edge delete", "trait", e.trait, "parent", e.parent, "err", err)
		}
	}
	return nil
}

// runLinks streams the anchored link projection (phase 3) and flushes 1k-row
// change-detected upserts. DISTINCT ON keeps one row per (character, trait)
// even when a character carries multiple vndb anchors (lowest external_id
// wins, deterministic). The stream joins the SRC vocabulary (always present)
// so a dry run before the first apply still plans honestly; OUR trait id
// resolves via LEFT JOIN (phase 1 has landed it by the time apply reaches
// phase 3 — a NULL id in apply mode is counted as an error, never written).
func runLinks(ctx context.Context, db *gorm.DB, opts Opts, st *Stats) error {
	rows, err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (r.entity_id, ct.tid) r.entity_id AS character_id,
		       COALESCE(t.id, 0) AS trait_id, ct.spoil AS spoiler_level, ct.lie AS lie
		FROM src_vndb.chars_traits ct
		JOIN src_vndb.traits s ON s.id = ct.tid
		JOIN catalog_external_ref r ON r.entity_type = 4 AND r.source_id = 2
			AND r.link_kind = 0 AND r.external_id = ct.id
		LEFT JOIN catalog_character_trait t ON t.vndb_tid = ct.tid
		ORDER BY r.entity_id, ct.tid, r.external_id`).Rows()
	if err != nil {
		return fmt.Errorf("stream links: %w", err)
	}
	defer rows.Close()

	type link struct {
		characterID  int64
		traitID      int64
		spoilerLevel int16
		lie          bool
	}
	batch := make([]link, 0, linkBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		st.LinksSeen += len(batch)
		if !opts.Apply {
			batch = batch[:0]
			return nil
		}
		// Guard: an unresolved trait id can only mean phase 1 failed for that
		// row — never write a 0 FK.
		kept := batch[:0]
		for _, l := range batch {
			if l.traitID == 0 {
				st.Errors++
				continue
			}
			kept = append(kept, l)
		}
		batch = kept
		if len(batch) == 0 {
			return nil
		}
		var sb strings.Builder
		args := make([]any, 0, len(batch)*4)
		sb.WriteString(`INSERT INTO catalog_character_trait_link (character_id, trait_id, spoiler_level, lie) VALUES `)
		for i, l := range batch {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?, ?, ?, ?)")
			args = append(args, l.characterID, l.traitID, l.spoilerLevel, l.lie)
		}
		sb.WriteString(` ON CONFLICT (character_id, trait_id) DO UPDATE SET
			spoiler_level = EXCLUDED.spoiler_level, lie = EXCLUDED.lie, updated_at = now()
			WHERE (catalog_character_trait_link.spoiler_level, catalog_character_trait_link.lie)
			    IS DISTINCT FROM (EXCLUDED.spoiler_level, EXCLUDED.lie)`)
		res := db.WithContext(ctx).Exec(sb.String(), args...)
		if res.Error != nil {
			st.Errors++
			slog.Warn("link batch upsert", "size", len(batch), "err", res.Error)
			batch = batch[:0]
			return nil // keep going; errors are counted
		}
		st.LinksWritten += int(res.RowsAffected)
		st.LinksUnchanged += len(batch) - int(res.RowsAffected)
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		var l link
		if err := rows.Scan(&l.characterID, &l.traitID, &l.spoilerLevel, &l.lie); err != nil {
			return fmt.Errorf("scan link row: %w", err)
		}
		batch = append(batch, l)
		if len(batch) >= linkBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("link stream: %w", err)
	}
	return flush()
}
