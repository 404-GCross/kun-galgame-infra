package importer

import (
	"encoding/json"
	"sort"
	"strings"

	"gorm.io/datatypes"
)

// Staging-side loading and field mapping for the VNDB releases wave: the
// src_vndb facet loaders (each read whole — bounded tables, the
// loadVNDBAliasNames precedent, cheaper than a 150k-element IN clause) plus the
// pure functions that turn one upstream release into catalog_release columns.
// Identity lives in vndbreleases_identity.go; nothing here touches a ref.

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

// buildReleaseExtra assembles the governed Extra jsonb. vndb_id stays here as
// payload and human back-reference — catalog_external_ref, not this key, is the
// identity index. minage is included only when known (0 = all-ages is
// meaningful, NULL = unknown → omitted); languages/platforms only when
// non-empty. Map marshal sorts keys → deterministic bytes.
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
