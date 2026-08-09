package importer

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The VNDB releases wave (step 76) projects VNDB's release layer into
// catalog_release as ADDITIVE rows — the SKU layer's first real data. For every
// work holding an EXACT VNDB work anchor (source 2) it writes ONE row per
// (work, vndb release) pair: a release covering several VNs becomes one row per
// covered work, each owned by that work (design nail 1). It NEVER touches an
// existing row — the pre-existing 1:1 stubs host the DLsite workno anchors — and
// creates no columns/tables (governed Extra jsonb only).
//
// Field mapping (design nail 2, surveyed shapes):
//   - Kind: releases.patch → ReleaseKindPatch; else releases_vn.rtype 'trial' →
//     ReleaseKindTrial; else ('complete'/'partial') → ReleaseKindDefault. VNDB's
//     rtype is a VN-completeness axis, NOT a digital/physical medium, so Digital/
//     Physical are deliberately never inferred (the medium is not staged). The
//     Extra list omits `patch`, so ReleaseKindPatch is the patch flag's home.
//   - Title/Lang: Lang = releases.olang (the release's declared original
//     language, always present); Title = the release's non-MTL title in that
//     language (a machine translation is not the original — "mtl 行不算原语言").
//   - Platform: releases_platforms codes verbatim; Platform column = the first
//     (sorted) code, Extra.platforms carries the full set (design nail 2).
//   - Date: releases.released (yyyymmdd int; 99999999 TBA / month|day 0 partial)
//     parsed into the nullable released_y/m/d trio, gated to [1950, now+3] — the
//     doc-66 year gate, which also drops the TBA placeholder (year 9999).
//   - Extra jsonb: vndb_id ("r123"), minage (only when known — 0 = all-ages is
//     meaningful, NULL = unknown → omitted), freeware, official, the full
//     languages set and platforms set. No column promotion (a buffer, not a home).
//
// # Identity discipline (wave 202)
//
// The wave answers TWO independent questions, and conflating them is what took a
// prod --apply down on a 23505:
//
//	does this (work, release) association exist? → key (work_id, rid); no unique
//	    constraint governs it, several works may legitimately host the same rid.
//	is this rid's identity slot free?            → key (source_id, rid, entity_type),
//	    governed by uq_catalog_external_ref_exact ... WHERE link_kind = 0, which has
//	    NO work column.
//
// Answering the second with the first is a resume index FINER than the unique it
// protects: it honestly reports "this work has not done rid yet", the wave plans
// a mint, and the mint collides with the anchor a DIFFERENT work already holds.
//
// So catalog_external_ref is now the single identity index for releases and
// Extra.vndb_id is pure payload (still written — cheap provenance, and the human
// back-reference — but never the authority):
//   - ROW decision: keyed (work_id, rid), read from the refs (with a transitional
//     Extra.vndb_id arm, see loadExistingVNDBReleaseKeys). Liveness-BLIND on
//     purpose — a retired row is not a vacancy, it still answers to its id, so
//     re-minting would resurrect it on every pass. The two skip classes are
//     counted apart (SkippedExisting / SkippedRetired).
//   - ANCHOR decision: keyed by rid ALONE, read from catalog_external_ref. Free
//     slot + exactly one upstream vid → mint the exact anchor. Slot held by
//     anyone (alive or soft-deleted) → do NOT mint and do NOT re-grade the
//     existing ref (re-grading an anchor is an adjudication, not something an
//     import may do silently) — the row is still created and carries a PROBABLE
//     ref instead. More than one upstream vid → an exact anchor would be
//     ambiguous, so PROBABLE as well. Each class has its own counter; probable is
//     outside the partial unique, so it can never contend for the exact slot.
//     This is the rostervndb / vndbcredits family pattern, applied to releases.
//
// Every row this wave creates therefore leaves holding a ref, and RunReleases
// opens with the phase-1 backfill that gives the legacy anchorless rows one too.
// Exact minting goes through ONE chokepoint (mintReleaseExactAnchors) that
// reports "claimed by someone else" instead of raising 23505, and the writes are
// batched behind savepoints so a single bad row can never roll back the wave.

const (
	// ruleVNDBRelease marks the EXACT release anchor (external_id = r-id).
	ruleVNDBRelease = "rule:vndb-release-import"
	// ruleVNDBReleaseProbable marks the PROBABLE release ref this wave attaches
	// when the exact slot is not available (already held) or not appropriate (the
	// release spans several VNs). It records the id without claiming the slot.
	ruleVNDBReleaseProbable = "rule:vndb-release-import-probable"
	// ruleVNDBReleaseBackfill marks the phase-1 probable ref given to a release
	// row that predates this doctrine: it carries Extra.vndb_id but never got a
	// ref of its own, so it was invisible to the identity index.
	ruleVNDBReleaseBackfill = "rule:vndb-release-backfill"
)

// releaseMinYear is the sanity floor for a fillable release year (matches the
// doc-66 backfill gate; nothing catalogued predates the home-computer era).
const releaseMinYear = 1950

// releaseApplyBatch is the savepoint granularity of the apply pass. A batch that
// fails is rolled back alone and reported; the wave keeps going.
const releaseApplyBatch = 500

// ReleaseStats is the per-wave tally.
type ReleaseStats struct {
	InGatePairs int // (work, release) pairs the exact vndb work anchor admits
	Planned     int // new rows to insert (after the idempotent skip)
	// ProbableBackfilled is phase 1: pre-existing catalog_release rows that carry
	// Extra.vndb_id but held no release ref at all, given a PROBABLE one so they
	// become visible to the identity index. 0 on every run after the first.
	ProbableBackfilled int
	ReleasesWritten    int
	AnchorsWritten     int // exact release anchors minted (free slot + single upstream vid)
	// ProbableRefsWritten counts probable refs attached to rows created BY THIS
	// wave (the anchor-held and multi-vn classes below).
	ProbableRefsWritten int
	MultiVNUnanchored   int // plan rows whose release spans >1 vn → probable, never exact
	// AnchorHeldByOther counts SINGLE-VN plan rows — rows that would otherwise have
	// been minted — whose rid already has an exact ref held by another release
	// (alive or soft-deleted). The row is created and gets a probable ref; the
	// sitting anchor is left exactly as it is. Multi-VN rows are classified as
	// MultiVNUnanchored even when the slot is held: for them the held slot is the
	// normal state, not a collision.
	AnchorHeldByOther int
	// StaleAnchorHolders is the subset of AnchorHeldByOther where the holder sits
	// under a DIFFERENT work than the one upstream now gates the rid through —
	// the human-review worklist (see exportStaleAnchors).
	StaleAnchorHolders int
	// AnchorRaceLost counts planned exact mints the chokepoint found already
	// claimed at write time. Expected 0: a non-zero value means the identity index
	// moved between planning and writing (a concurrent writer).
	AnchorRaceLost  int
	SkippedExisting int // idempotent resume: (work, rid) already present on a live row
	SkippedRetired  int // (work, rid) held only by a soft-deleted row — claimed, not vacant
	KindDefault     int
	KindTrial       int
	KindPatch       int
	NoDate          int // TBA / out-of-gate year → date trio left NULL
	NoTitle         int // no non-MTL title in the original language
	// BatchFailures counts apply batches rolled back to their savepoint. Their
	// rows are absent from ReleasesWritten; the wave itself still commits.
	BatchFailures int
	Errors        int
}

// releaseGateRow is one (work, release) gate pair with its deterministic rtype.
// VID is the work's own vndb id — carried for the stale-holder worklist.
type releaseGateRow struct {
	WorkID int64  `gorm:"column:work_id"`
	VID    string `gorm:"column:vid"`
	RID    string `gorm:"column:rid"`
	RType  string `gorm:"column:rtype"`
}

// anchorDecision is the ANCHOR half of a plan, decided from the rid alone and
// independent of whether the row itself is new.
type anchorDecision int8

const (
	anchorMint        anchorDecision = iota // slot free + exactly one upstream vid → exact
	anchorHeldByOther                       // slot already held by another release → probable
	anchorMultiVN                           // >1 upstream vid → an exact anchor would be ambiguous
)

// releasePlan is one planned catalog_release row plus the bookkeeping the apply
// transaction needs (the r-id for the ref, and which grade that ref gets).
type releasePlan struct {
	rel    model.CatalogRelease
	rid    string
	anchor anchorDecision
}

// RunReleases lands the VNDB releases wave (step 76). Single DSN — src_vndb and
// catalog share the database; eg is unused.
func (im *Importer) RunReleases() (ReleaseStats, error) {
	var st ReleaseStats

	// 0. Phase 1 — give every legacy anchorless release row a probable ref, so
	// catalog_external_ref is a COMPLETE identity index before anything below
	// reads it. Idempotent; honours dry-run (counts only).
	backfilled, err := im.backfillReleaseProbableRefs()
	if err != nil {
		return st, err
	}
	st.ProbableBackfilled = backfilled

	// 1. Gate: one (work, release) per row, deduped with a deterministic rtype.
	// VNDB external_ids carry the "v" prefix ("v38"), so the anchor JOIN is on
	// text equality (the doc-73 recipe; a ::bigint cast would fail). A work may
	// hold several exact vndb ids, so the reported vid is min() for determinism.
	var gates []releaseGateRow
	if err := im.catalog.Raw(`
		SELECT r.entity_id AS work_id, min(r.external_id) AS vid, rv.id AS rid, min(rv.rtype) AS rtype
		FROM src_vndb.releases_vn rv
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = rv.vid
		GROUP BY r.entity_id, rv.id`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact).Scan(&gates).Error; err != nil {
		return st, err
	}
	if im.limit > 0 {
		gates = capReleaseGatesByWork(gates, im.limit)
	}
	st.InGatePairs = len(gates)

	// 2. Load the release-level facets once (bounded staging tables, the
	// loadVNDBAliasNames precedent — cheaper than a 150k-element IN clause).
	meta, err := im.loadReleaseMeta()
	if err != nil {
		return st, err
	}
	platforms, err := im.loadReleasePlatforms()
	if err != nil {
		return st, err
	}
	titles, err := im.loadReleaseTitles()
	if err != nil {
		return st, err
	}
	vnCount, err := im.loadReleaseVNCounts()
	if err != nil {
		return st, err
	}
	existing, err := im.loadExistingVNDBReleaseKeys()
	if err != nil {
		return st, err
	}
	// The ANCHOR index: rid → its exact holder, keyed by rid ALONE (no work), the
	// same shape as uq_catalog_external_ref_exact. This is what makes the mint
	// decision correct; the (work, rid) index above only decides the ROW.
	exactHolders, err := im.loadReleaseExactHolders()
	if err != nil {
		return st, err
	}

	// 3. Build the plans, counting the skip / kind / missing-facet classes.
	maxYear := time.Now().Year() + 3 // sanity ceiling; rejects 2050/2099 placeholders
	var plans []releasePlan
	var stale []staleAnchorRow
	for _, g := range gates {
		m, ok := meta[g.RID]
		if !ok { // releases_vn.id absent from releases — a staging inconsistency (FK guarantees none)
			st.Errors++
			continue
		}
		if retired, held := existing[releaseKey(g.WorkID, g.RID)]; held {
			if retired {
				st.SkippedRetired++
			} else {
				st.SkippedExisting++
			}
			continue
		}

		rel := model.CatalogRelease{WorkID: g.WorkID, Extra: datatypes.JSON(`{}`)}

		switch {
		case m.patch:
			rel.Kind = model.ReleaseKindPatch
			st.KindPatch++
		case g.RType == "trial":
			rel.Kind = model.ReleaseKindTrial
			st.KindTrial++
		default:
			rel.Kind = model.ReleaseKindDefault
			st.KindDefault++
		}

		if strings.TrimSpace(m.olang) != "" {
			lang := m.olang
			rel.Lang = &lang
		}
		if title, ok := originalTitle(titles[g.RID], m.olang); ok {
			rel.Title = &title
		} else {
			st.NoTitle++
		}

		plats := platforms[g.RID]
		if len(plats) > 0 {
			first := plats[0]
			rel.Platform = &first
		}

		if y, mo, d, ok := parseVNDBReleased(m.released, maxYear); ok {
			rel.ReleasedY = &y
			rel.ReleasedM = mo
			rel.ReleasedD = d
		} else {
			st.NoDate++
		}

		rel.Extra = buildReleaseExtra(g.RID, m, langSet(titles[g.RID]), plats)

		// The anchor half. Decided here (not at write time) so a dry run and an
		// apply report exactly the same numbers.
		// Multi-VN is tested FIRST because it is intrinsic to the release: such a
		// row would never be minted whatever the slot's state, and one of the works
		// it covers legitimately holds the anchor. Classifying it as "held by
		// another" would be true but useless, and would put a normal multi-VN
		// release into the stale worklist as a disagreement it is not.
		anchor := anchorMint
		switch holder, held := exactHolders[g.RID]; {
		case vnCount[g.RID] != 1:
			anchor = anchorMultiVN
			st.MultiVNUnanchored++
		case held:
			anchor = anchorHeldByOther
			st.AnchorHeldByOther++
			if holder.workID != g.WorkID {
				st.StaleAnchorHolders++
				stale = append(stale, staleAnchorRow{
					rid: g.RID, gateWorkID: g.WorkID, gateWorkVID: g.VID,
					holderReleaseID: holder.releaseID, holderWorkID: holder.workID,
					holderRetired: holder.retired,
				})
			}
		}
		plans = append(plans, releasePlan{rel: rel, rid: g.RID, anchor: anchor})
	}
	st.Planned = len(plans)

	anchorable := 0
	for _, p := range plans {
		if p.anchor == anchorMint {
			anchorable++
		}
	}

	slog.Info("vndb releases plan",
		"in_gate_pairs", st.InGatePairs, "planned", st.Planned, "skipped_existing", st.SkippedExisting,
		"skipped_retired", st.SkippedRetired, "probable_backfilled", st.ProbableBackfilled,
		"kind_default", st.KindDefault, "kind_trial", st.KindTrial, "kind_patch", st.KindPatch,
		"anchors_planned", anchorable, "multi_vn_unanchored", st.MultiVNUnanchored,
		"anchor_held_by_other", st.AnchorHeldByOther, "stale_anchor_holders", st.StaleAnchorHolders,
		"no_date", st.NoDate, "no_title", st.NoTitle, "errors", st.Errors)

	// The stale-holder worklist is a review artifact, so it is written in dry-run
	// too — seeing it is the whole point of planning before applying.
	if im.staleAnchorsOut != "" {
		if err := im.exportStaleAnchors(stale, im.staleAnchorsOut); err != nil {
			return st, err
		}
	}

	if im.dryRun {
		st.ReleasesWritten = len(plans) // would-be (clean state == apply)
		st.AnchorsWritten = anchorable
		st.ProbableRefsWritten = len(plans) - anchorable
		return st, nil
	}
	if len(plans) == 0 {
		return st, nil
	}

	// 4. Apply: rows + refs + revisions, in savepoint-guarded batches.
	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(plans); start += releaseApplyBatch {
			end := min(start+releaseApplyBatch, len(plans))
			if err := applyReleaseBatch(tx, plans[start:end], &st); err != nil {
				return err
			}
		}
		return nil
	})
	return st, err
}

// applyReleaseBatch writes one batch inside its own SAVEPOINT. A batch that
// fails is rolled back alone and reported through BatchFailures — one bad row
// must never cost the other 4,000 (the wave-wide rollback that motivated this
// refactor). Only a failure to roll back is fatal: the subtransaction is then
// unusable and the wave cannot honestly continue.
func applyReleaseBatch(tx *gorm.DB, plans []releasePlan, st *ReleaseStats) error {
	const sp = "vndb_releases_batch"
	if err := tx.SavePoint(sp).Error; err != nil {
		return err
	}
	var delta ReleaseStats
	if err := writeReleaseBatch(tx, plans, &delta); err != nil {
		if rbErr := tx.RollbackTo(sp).Error; rbErr != nil {
			return rbErr
		}
		st.BatchFailures++
		st.Errors++
		slog.Error("vndb releases batch rolled back",
			"rows", len(plans), "first_rid", plans[0].rid, "last_rid", plans[len(plans)-1].rid, "error", err)
		return nil
	}
	if err := tx.Exec("RELEASE SAVEPOINT " + sp).Error; err != nil {
		return err
	}
	st.ReleasesWritten += delta.ReleasesWritten
	st.AnchorsWritten += delta.AnchorsWritten
	st.ProbableRefsWritten += delta.ProbableRefsWritten
	st.AnchorRaceLost += delta.AnchorRaceLost
	return nil
}

// writeReleaseBatch inserts the batch's rows, then their refs and revisions, and
// bumps the host works. Every exact ref goes through mintReleaseExactAnchors —
// the file's single exact-minting chokepoint — and anything it could not claim
// falls back to a probable ref rather than failing.
func writeReleaseBatch(tx *gorm.DB, plans []releasePlan, st *ReleaseStats) error {
	releases := make([]model.CatalogRelease, len(plans))
	for i, p := range plans {
		releases[i] = p.rel
	}
	if err := tx.CreateInBatches(releases, 1000).Error; err != nil {
		return err
	}

	var mints, probables []releaseAnchorItem
	revs := make([]model.CatalogRevision, len(releases))
	for i := range releases {
		revs[i] = importedRev(model.EntityTypeRelease, releases[i].ID, releaseSnapshotJSON(releases[i]))
		item := releaseAnchorItem{releaseID: releases[i].ID, rid: plans[i].rid}
		if plans[i].anchor == anchorMint {
			mints = append(mints, item)
		} else {
			probables = append(probables, item)
		}
	}

	landed, err := mintReleaseExactAnchors(tx, mints)
	if err != nil {
		return err
	}
	for _, m := range mints {
		if !landed[m.releaseID] {
			// The slot was claimed between planning and writing. The row still
			// deserves to be findable by its rid, so it drops to probable.
			st.AnchorRaceLost++
			probables = append(probables, m)
		}
	}
	st.AnchorsWritten = len(landed)

	written, err := insertReleaseProbableRefs(tx, probables, ruleVNDBReleaseProbable)
	if err != nil {
		return err
	}
	st.ProbableRefsWritten = written

	if err := tx.CreateInBatches(revs, 1000).Error; err != nil {
		return err
	}
	st.ReleasesWritten = len(releases)

	// The releases hang off works that already existed, so their hosts have to
	// surface on the public changes feed. plans is the not-yet-stored set
	// (releaseKey dedup above), so a re-run plans nothing and touches nothing.
	hosts := make([]int64, 0, len(releases))
	for _, r := range releases {
		hosts = append(hosts, r.WorkID)
	}
	return touchWorks(tx, hosts)
}

// releaseKey is the ROW identity of a planned row: (work, vndb r-id). It is
// deliberately NOT the anchor key — see the file header.
func releaseKey(workID int64, vndbID string) string {
	return strconv.FormatInt(workID, 10) + "|" + vndbID
}
