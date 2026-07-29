package importer

import (
	"encoding/json"
	"log/slog"
	"sort"
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
// Idempotence (design nail 3): the resume index is the set of existing
// (work_id, extra->>'vndb_id') — a hit skips the whole plan, so a second pass
// writes nothing (row + anchor + revision are created atomically per apply).
// external_ref: a release covering exactly ONE vn (globally) gets an exact
// release anchor (source 2, entity_type 6, external_id = r-id, the vndb-character/
// staff waves' tier-0 auto-exact precedent); a multi-vn release gets NO anchor
// (it would collide on the exact (source, external_id, entity_type) unique) —
// Extra.vndb_id keeps it back-referenceable. Counts go to the report.

const ruleVNDBRelease = "rule:vndb-release-import" // exact release anchor (external_id = r-id)

// releaseMinYear is the sanity floor for a fillable release year (matches the
// doc-66 backfill gate; nothing catalogued predates the home-computer era).
const releaseMinYear = 1950

// ReleaseStats is the per-wave tally.
type ReleaseStats struct {
	InGatePairs       int // (work, release) pairs the exact vndb work anchor admits
	Planned           int // new rows to insert (after the idempotent skip)
	ReleasesWritten   int
	AnchorsWritten    int // single-vn exact release anchors created
	MultiVNUnanchored int // plan rows whose release spans >1 vn → intentionally no anchor
	SkippedExisting   int // idempotent resume: (work, vndb_id) already present
	KindDefault       int
	KindTrial         int
	KindPatch         int
	NoDate            int // TBA / out-of-gate year → date trio left NULL
	NoTitle           int // no non-MTL title in the original language
	Errors            int
}

// relMeta is one src_vndb.releases row's release-level fields.
type relMeta struct {
	olang    string
	released int
	minage   *int16
	freeware bool
	official bool
	patch    bool
}

// relTitle is one src_vndb.releases_titles row (a release's title in ONE lang).
type relTitle struct {
	lang  string
	mtl   bool
	title string
	latin string
}

// releaseGateRow is one (work, release) gate pair with its deterministic rtype.
type releaseGateRow struct {
	WorkID int64  `gorm:"column:work_id"`
	RID    string `gorm:"column:rid"`
	RType  string `gorm:"column:rtype"`
}

// releasePlan is one planned catalog_release row plus the bookkeeping the apply
// transaction needs (the r-id for the anchor, and whether it earns one).
type releasePlan struct {
	rel      model.CatalogRelease
	rid      string
	singleVN bool
}

// RunReleases lands the VNDB releases wave (step 76). Single DSN — src_vndb and
// catalog share the database; eg is unused.
func (im *Importer) RunReleases() (ReleaseStats, error) {
	var st ReleaseStats

	// 1. Gate: one (work, release) per row, deduped with a deterministic rtype.
	// VNDB external_ids carry the "v" prefix ("v38"), so the anchor JOIN is on
	// text equality (the doc-73 recipe; a ::bigint cast would fail).
	var gates []releaseGateRow
	if err := im.catalog.Raw(`
		SELECT r.entity_id AS work_id, rv.id AS rid, min(rv.rtype) AS rtype
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

	// 3. Build the plans, counting the skip / kind / missing-facet classes.
	maxYear := time.Now().Year() + 3 // sanity ceiling; rejects 2050/2099 placeholders
	var plans []releasePlan
	for _, g := range gates {
		m, ok := meta[g.RID]
		if !ok { // releases_vn.id absent from releases — a staging inconsistency (FK guarantees none)
			st.Errors++
			continue
		}
		if existing[releaseKey(g.WorkID, g.RID)] {
			st.SkippedExisting++
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

		single := vnCount[g.RID] == 1
		if !single {
			st.MultiVNUnanchored++
		}
		plans = append(plans, releasePlan{rel: rel, rid: g.RID, singleVN: single})
	}
	st.Planned = len(plans)

	anchorable := 0
	for _, p := range plans {
		if p.singleVN {
			anchorable++
		}
	}

	slog.Info("vndb releases plan",
		"in_gate_pairs", st.InGatePairs, "planned", st.Planned, "skipped_existing", st.SkippedExisting,
		"kind_default", st.KindDefault, "kind_trial", st.KindTrial, "kind_patch", st.KindPatch,
		"single_vn_anchors", anchorable, "multi_vn_unanchored", st.MultiVNUnanchored,
		"no_date", st.NoDate, "no_title", st.NoTitle, "errors", st.Errors)

	if im.dryRun {
		st.ReleasesWritten = len(plans) // would-be (clean state == apply)
		st.AnchorsWritten = anchorable
		return st, nil
	}
	if len(plans) == 0 {
		return st, nil
	}

	// 4. Apply: INSERT the rows (populating their IDENTITY ids), then the exact
	// anchors (single-vn only) + one imported revision per new release.
	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		releases := make([]model.CatalogRelease, len(plans))
		for i, p := range plans {
			releases[i] = p.rel
		}
		if err := tx.CreateInBatches(releases, 1000).Error; err != nil {
			return err
		}
		var refs []model.CatalogExternalRef
		revs := make([]model.CatalogRevision, len(releases))
		for i := range releases {
			revs[i] = importedRev(model.EntityTypeRelease, releases[i].ID, releaseSnapshotJSON(releases[i]))
			if plans[i].singleVN {
				refs = append(refs, selfRef(model.EntityTypeRelease, releases[i].ID, vndbSource, plans[i].rid, ruleVNDBRelease))
			}
		}
		if len(refs) > 0 {
			if err := tx.CreateInBatches(refs, 1000).Error; err != nil {
				return err
			}
		}
		if err := tx.CreateInBatches(revs, 1000).Error; err != nil {
			return err
		}
		st.ReleasesWritten = len(releases)
		st.AnchorsWritten = len(refs)
		// The releases hang off works that already existed, so their hosts have
		// to surface on the public changes feed. plans is the not-yet-stored set
		// (releaseKey dedup above), so a re-run plans nothing and touches nothing.
		hosts := make([]int64, 0, len(releases))
		for _, r := range releases {
			hosts = append(hosts, r.WorkID)
		}
		return touchWorks(tx, hosts)
	})
	return st, err
}

// releaseKey is the idempotence identity of a planned row: (work, vndb r-id).
func releaseKey(workID int64, vndbID string) string {
	return strconv.FormatInt(workID, 10) + "|" + vndbID
}

// originalTitle returns the release's title in its original language — the
// non-MTL row for olang (title, or its latin form when the native title is
// empty). ok=false when no such row exists (an MTL-only original language).
func originalTitle(rows []relTitle, olang string) (string, bool) {
	for _, t := range rows {
		if t.lang != olang || t.mtl {
			continue
		}
		if v := strings.TrimSpace(t.title); v != "" {
			return t.title, true
		}
		if v := strings.TrimSpace(t.latin); v != "" {
			return t.latin, true
		}
		return "", false // the olang row exists but carries no usable text
	}
	return "", false
}

// langSet is the release's full language list (every releases_titles row's lang,
// distinct, ascending — the rows arrive pre-sorted by the loader).
func langSet(rows []relTitle) []string {
	out := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, t := range rows {
		if seen[t.lang] {
			continue
		}
		seen[t.lang] = true
		out = append(out, t.lang)
	}
	return out
}

// buildReleaseExtra assembles the governed Extra jsonb. minage is included only
// when known (0 = all-ages is meaningful, NULL = unknown → omitted); languages/
// platforms only when non-empty. Map marshal sorts keys → deterministic bytes.
func buildReleaseExtra(rid string, m relMeta, langs, plats []string) datatypes.JSON {
	extra := map[string]any{
		"vndb_id":  rid,
		"freeware": m.freeware,
		"official": m.official,
	}
	if m.minage != nil {
		extra["minage"] = *m.minage
	}
	if len(langs) > 0 {
		extra["languages"] = langs
	}
	if len(plats) > 0 {
		extra["platforms"] = plats
	}
	b, _ := json.Marshal(extra)
	return b
}

// parseVNDBReleased splits a VNDB `released` integer (yyyymmdd; 99999999 = TBA;
// month|day 0 = partial) into the nullable released_y/m/d trio, gated to a sane
// [releaseMinYear, maxYear] window (the doc-66 gate — drops TBA, whose year is
// 9999, and any placeholder). ok=false → no usable date (leave the trio NULL).
func parseVNDBReleased(released, maxYear int) (y int16, m, d *int16, ok bool) {
	yy := released / 10000
	if yy < releaseMinYear || yy > maxYear {
		return 0, nil, nil, false
	}
	y = int16(yy)
	if mm := (released / 100) % 100; mm >= 1 && mm <= 12 {
		mv := int16(mm)
		m = &mv
		if dd := released % 100; dd >= 1 && dd <= 31 {
			dv := int16(dd)
			d = &dv
		}
	}
	return y, m, d, true
}

// --- loaders ---------------------------------------------------------------

func (im *Importer) loadReleaseMeta() (map[string]relMeta, error) {
	var rows []struct {
		ID       string `gorm:"column:id"`
		OLang    string `gorm:"column:olang"`
		Released int    `gorm:"column:released"`
		Minage   *int16 `gorm:"column:minage"`
		Freeware bool   `gorm:"column:freeware"`
		Official bool   `gorm:"column:official"`
		Patch    bool   `gorm:"column:patch"`
	}
	if err := im.catalog.Raw(`SELECT id, olang, released, minage, freeware, official, patch FROM src_vndb.releases`).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]relMeta, len(rows))
	for _, r := range rows {
		m[r.ID] = relMeta{olang: r.OLang, released: r.Released, minage: r.Minage, freeware: r.Freeware, official: r.Official, patch: r.Patch}
	}
	return m, nil
}

func (im *Importer) loadReleasePlatforms() (map[string][]string, error) {
	var rows []struct {
		ID       string `gorm:"column:id"`
		Platform string `gorm:"column:platform"`
	}
	if err := im.catalog.Raw(`SELECT id, platform FROM src_vndb.releases_platforms ORDER BY id, platform`).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := map[string][]string{}
	for _, r := range rows {
		m[r.ID] = append(m[r.ID], r.Platform)
	}
	return m, nil
}

func (im *Importer) loadReleaseTitles() (map[string][]relTitle, error) {
	var rows []struct {
		ID    string `gorm:"column:id"`
		Lang  string `gorm:"column:lang"`
		MTL   bool   `gorm:"column:mtl"`
		Title string `gorm:"column:title"`
		Latin string `gorm:"column:latin"`
	}
	if err := im.catalog.Raw(`SELECT id, lang, mtl, title, latin FROM src_vndb.releases_titles ORDER BY id, lang`).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := map[string][]relTitle{}
	for _, r := range rows {
		m[r.ID] = append(m[r.ID], relTitle{lang: r.Lang, mtl: r.MTL, title: r.Title, latin: r.Latin})
	}
	return m, nil
}

func (im *Importer) loadReleaseVNCounts() (map[string]int, error) {
	var rows []struct {
		ID string `gorm:"column:id"`
		C  int    `gorm:"column:c"`
	}
	if err := im.catalog.Raw(`SELECT id, count(*) AS c FROM src_vndb.releases_vn GROUP BY id`).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int, len(rows))
	for _, r := range rows {
		m[r.ID] = r.C
	}
	return m, nil
}

// loadExistingVNDBReleaseKeys is the resume index: the (work_id, vndb_id) set
// already present. The `extra->>'vndb_id' IS NOT NULL` filter avoids the jsonb
// `?` operator (which collides with GORM's positional placeholder).
func (im *Importer) loadExistingVNDBReleaseKeys() (map[string]bool, error) {
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		VndbID string `gorm:"column:vndb_id"`
	}
	if err := im.catalog.Raw(`
		SELECT work_id, extra->>'vndb_id' AS vndb_id FROM catalog_release
		WHERE extra->>'vndb_id' IS NOT NULL AND deleted_at IS NULL`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[releaseKey(r.WorkID, r.VndbID)] = true
	}
	return m, nil
}

// capReleaseGatesByWork keeps only the gates for the first n distinct work ids
// (ascending) — the --limit debugging aid, deterministic.
func capReleaseGatesByWork(gates []releaseGateRow, n int) []releaseGateRow {
	works := map[int64]bool{}
	for _, g := range gates {
		works[g.WorkID] = true
	}
	keys := make([]int64, 0, len(works))
	for k := range works {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if n < len(keys) {
		keys = keys[:n]
	}
	keep := make(map[int64]bool, len(keys))
	for _, k := range keys {
		keep[k] = true
	}
	out := gates[:0:0]
	for _, g := range gates {
		if keep[g.WorkID] {
			out = append(out, g)
		}
	}
	return out
}
