package main

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// runVerify is the invariant suite. It runs automatically after an --apply and
// can be run on its own (--verify-only) against any database, before or after
// the migration — every check is phrased over the CURRENT state, so a failure
// is a fact about the database and not about this process's bookkeeping.
//
// A failing check is an error, not a warning: these are the four things that,
// if broken, make the history unreadable or the engine unsafe.
func runVerify(ctx context.Context, db *gorm.DB) error {
	checks := []struct {
		name string
		run  func(context.Context, *gorm.DB) (string, error)
	}{
		{"leftover-wiki-rows", verifyLeftovers},
		{"chain-integrity", verifyChains},
		{"seq-monotonicity", verifySeq},
		{"entity-resolvable", verifyEntities},
	}
	failed := 0
	for _, check := range checks {
		detail, err := check.run(ctx, db)
		if err != nil {
			return fmt.Errorf("%s: %w", check.name, err)
		}
		if detail == "" {
			slog.Info("invariant OK", "check", check.name)
			continue
		}
		failed++
		slog.Error("invariant FAILED", "check", check.name, "detail", detail)
	}
	if failed > 0 {
		return fmt.Errorf("%d invariant(s) failed", failed)
	}
	return nil
}

// sampleDiffs renders real diffs through the ENGINE's read path — the same
// Engine.Diff the history page calls — over n migrated entities picked at
// random among those holding at least two revisions. It is the only check that
// exercises the migrated JSONB end to end rather than counting rows: a
// snapshot that decodes but renders as nothing, or a spec whose field lookup
// blows up on a retired key, shows up here and nowhere else.
func sampleDiffs(ctx context.Context, db *gorm.DB, n int) error {
	var picks []struct {
		EntityID int64 `gorm:"column:entity_id"`
		MinSeq   int   `gorm:"column:min_seq"`
		MaxSeq   int   `gorm:"column:max_seq"`
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT entity_id, min(seq) AS min_seq, max(seq) AS max_seq FROM edit_revision
		 WHERE entity_type = ? GROUP BY entity_id HAVING count(*) > 1
		 ORDER BY random() LIMIT ?`, editspec.TypeWork, n).Scan(&picks).Error; err != nil {
		return err
	}
	if len(picks) == 0 {
		return fmt.Errorf("no migrated entity has more than one revision — nothing to render")
	}
	reg := editing.NewRegistry()
	if err := editspec.RegisterWork(reg, db); err != nil {
		return err
	}
	engine := editing.NewEngine(db, reg)
	fields, empty := 0, 0
	for _, p := range picks {
		diffs, err := engine.Diff(ctx, editspec.TypeWork, p.EntityID, p.MinSeq, p.MaxSeq)
		if err != nil {
			return fmt.Errorf("diff work %d seq %d→%d: %w", p.EntityID, p.MinSeq, p.MaxSeq, err)
		}
		if len(diffs) == 0 {
			empty++
		}
		fields += len(diffs)
	}
	slog.Info("sampled diff render OK", "entities", len(picks), "field_diffs", fields, "empty_diffs", empty)
	return nil
}

// verifyLeftovers reports the wiki rows still present. This is NOT a failure by
// itself — residue is expected to stay until the T phase dumps and deletes it —
// so it only fails when a leftover row belongs to an entity that DID get a
// catalog counterpart, which would mean a chain was cut in half.
func verifyLeftovers(ctx context.Context, db *gorm.DB) (string, error) {
	var counts struct {
		Revisions int64 `gorm:"column:revisions"`
		Proposals int64 `gorm:"column:proposals"`
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT (SELECT count(*) FROM edit_revision WHERE entity_type = ?) AS revisions,
		        (SELECT count(*) FROM edit_proposal WHERE entity_type = ?) AS proposals`,
		wikiType, wikiType).Scan(&counts).Error; err != nil {
		return "", err
	}
	slog.Info("wiki rows remaining (residue, dumped then deleted in T)",
		"revisions", counts.Revisions, "proposals", counts.Proposals)

	var split int64
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM edit_revision r
		 JOIN catalog_external_ref x
		   ON x.entity_type = 5 AND x.link_kind = 0 AND x.external_id = r.entity_id::text
		   AND x.matched_by = ?
		 WHERE r.entity_type = ?`, matchedByGID, wikiType).Scan(&split).Error; err != nil {
		return "", err
	}
	if split > 0 {
		return fmt.Sprintf("%d wiki revisions belong to a gid that HAS a catalog work — half-migrated entity", split), nil
	}
	return "", nil
}

// verifyChains: a revision that names a proposal must sit on the same entity as
// that proposal. This is what "the proposal↔revision chain is preserved" means
// operationally, and it is the invariant a per-row (rather than per-entity)
// residue rule would break.
func verifyChains(ctx context.Context, db *gorm.DB) (string, error) {
	var broken int64
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM edit_revision r
		 LEFT JOIN edit_proposal p ON p.id = r.proposal_id
		 WHERE r.proposal_id IS NOT NULL
		   AND (p.id IS NULL
		        OR p.entity_family <> r.entity_family
		        OR p.entity_type <> r.entity_type
		        OR p.entity_id <> r.entity_id)`).Scan(&broken).Error; err != nil {
		return "", err
	}
	if broken > 0 {
		return fmt.Sprintf("%d revisions point at a proposal on a different entity (or at none)", broken), nil
	}
	return "", nil
}

// verifySeq: per entity, seq must order the revisions the same way their ids
// do. The unique index already forbids duplicates; what it cannot see is a
// migration that renumbered a chain out of order.
func verifySeq(ctx context.Context, db *gorm.DB) (string, error) {
	var bad int64
	if err := db.WithContext(ctx).Raw(
		`WITH ordered AS (
		     SELECT entity_family, entity_type, entity_id, seq,
		            row_number() OVER (PARTITION BY entity_family, entity_type, entity_id ORDER BY id) AS by_id,
		            row_number() OVER (PARTITION BY entity_family, entity_type, entity_id ORDER BY seq) AS by_seq
		     FROM edit_revision
		 )
		 SELECT count(*) FROM ordered WHERE by_id <> by_seq`).Scan(&bad).Error; err != nil {
		return "", err
	}
	if bad > 0 {
		return fmt.Sprintf("%d revisions whose seq order disagrees with their id order", bad), nil
	}
	return "", nil
}

// verifyEntities: every catalog.work revision must address a row that exists.
// Soft-deleted works are fine — history outlives the entity, deliberately (the
// claim-event table makes the same choice) — but a phantom id is not.
func verifyEntities(ctx context.Context, db *gorm.DB) (string, error) {
	var orphans int64
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM edit_revision r
		 WHERE r.entity_type = ?
		   AND NOT EXISTS (SELECT 1 FROM catalog_work w WHERE w.id = r.entity_id)`,
		editspec.TypeWork).Scan(&orphans).Error; err != nil {
		return "", err
	}
	if orphans > 0 {
		return fmt.Sprintf("%d catalog.work revisions address a work id that does not exist", orphans), nil
	}
	return "", nil
}
