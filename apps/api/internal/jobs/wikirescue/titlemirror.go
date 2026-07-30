package wikirescue

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"
)

// galgameNamePivot is the FIXED column→language mapping of the wiki title
// bridge, copied verbatim from service.galgameNamePivot (read_titles.go) —
// including the table-position order, which is the order the bridge appends
// official rows in.
var galgameNamePivot = []struct{ Column, Lang string }{
	{"name_ja_jp", "ja"},
	{"name_en_us", "en"},
	{"name_zh_cn", "zh-Hans"},
	{"name_zh_tw", "zh-Hant"},
}

// titleKey identifies one catalog_work_title row: the table's whole unique key
// uq_catalog_work_title(work_id, lang, title, kind).
type titleKey struct {
	workID int64
	lang   string
	title  string
	kind   int16
}

// titleVal is the only value column step p owns. The bridge has no romaji
// column, so a mirrored row's latin is NULL — and an existing row carrying one
// is corrected, since the mirror is the whole truth of its scope.
type titleVal struct{ latin *string }

// stepTitleMirror mirrors the CLAIMED works' wiki title layer into
// catalog_work_title (step p, refs/proj/140 §2.1).
//
// The read face used to bridge titles for a claimed work (service.
// bridgeGalgameTitles): the four fixed galgame name columns pivot to OFFICIAL
// rows in BCP-47, and each galgame_alias row becomes an ALIAS row with no
// language. This step materializes exactly that projection, so the read face can
// stop bridging and read the table for every work alike.
//
// Semantics copied verbatim from the bridge, because the flipped face has to be
// byte-identical:
//
//   - an empty (whitespace-only) name column is not a name and yields no row;
//   - an alias whose TRIMMED string equals ANY of that body's four official
//     names is DROPPED (27% of the corpus). The bridge drops it to keep a reader
//     from seeing one name twice, and the official row wins the key because it
//     carries a language. Without this the unique key would not save us: the
//     alias's lang is '' so the two rows differ on the key and BOTH would surface.
//   - an alias row's name is trimmed (the bridge trims), an official name's is
//     too, and the trimmed form is what lands.
//
// OWNERSHIP SCOPE — the claimed works' OFFICIAL and ALIAS rows, and no others.
// Not "every row of a claimed work", which is what the design sketch assumed:
// SEARCH_HINT rows (kind=3) on claimed works are a live foreign lane. The dlsite
// release-attach importer (importer/egdlsite_create.createAttachChunk) files the
// DLsite kana of a work it attaches a release to as a search hint, claimed or
// not, and cmd/reindex-catalog already reads exactly those rows for claimed works
// ("search hints only for a claimed one — the hint lane has no wiki counterpart").
// They have no wiki counterpart, so a mirror that deleted them would both destroy
// findability data and flap against their importer forever. They stay.
func (r *Runner) stepTitleMirror(ctx context.Context) (Stats, error) {
	st := Stats{Step: "p"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}

	// ── desired: the bridge's own projection, from the wiki body.
	var names []struct {
		ID       int64  `gorm:"column:id"`
		NameJaJP string `gorm:"column:name_ja_jp"`
		NameEnUS string `gorm:"column:name_en_us"`
		NameZhCN string `gorm:"column:name_zh_cn"`
		NameZhTW string `gorm:"column:name_zh_tw"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name_ja_jp, name_en_us, name_zh_cn, name_zh_tw
		 FROM galgame ORDER BY id`).Scan(&names).Error; err != nil {
		return st, fmt.Errorf("read galgame names: %w", err)
	}
	desired := newMirrorDesired[titleKey, titleVal](len(names) * 3)
	// official[galgame id] is that body's name set — the alias dedup input.
	official := make(map[int64]map[string]struct{}, len(names))
	for _, n := range names {
		workID, ok := span.byGalgame[n.ID]
		if !ok {
			continue
		}
		byCol := map[string]string{
			"name_ja_jp": n.NameJaJP, "name_en_us": n.NameEnUS,
			"name_zh_cn": n.NameZhCN, "name_zh_tw": n.NameZhTW,
		}
		seen := make(map[string]struct{}, len(galgameNamePivot))
		for _, p := range galgameNamePivot {
			title := strings.TrimSpace(byCol[p.Column])
			if title == "" {
				continue // an empty column is not a name
			}
			seen[title] = struct{}{}
			st.Source++
			desired.put(titleKey{workID: workID, lang: p.Lang, title: title, kind: model.WorkTitleKindOfficial}, titleVal{})
		}
		official[n.ID] = seen
	}

	var aliases []struct {
		GalgameID int64  `gorm:"column:galgame_id"`
		Name      string `gorm:"column:name"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, name FROM galgame_alias
		 ORDER BY galgame_id, id`).Scan(&aliases).Error; err != nil {
		return st, fmt.Errorf("read galgame_alias: %w", err)
	}
	for _, a := range aliases {
		workID, ok := span.byGalgame[a.GalgameID]
		if !ok {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		st.Source++
		if _, dup := official[a.GalgameID][name]; dup {
			st.Skipped++ // the same name already surfaced as an official title
			continue
		}
		if !desired.put(titleKey{workID: workID, title: name, kind: model.WorkTitleKindAlias}, titleVal{}) {
			// The body carries this alias string twice; one row is all the unique
			// key can hold, and all a reader should see.
			st.Skipped++
		}
	}
	st.Anchored = desired.len()

	// ── existing: the ownership scope. kind IN (official, alias) only.
	var scope []struct {
		ID     int64   `gorm:"column:id"`
		WorkID int64   `gorm:"column:work_id"`
		Lang   string  `gorm:"column:lang"`
		Title  string  `gorm:"column:title"`
		Kind   int16   `gorm:"column:kind"`
		Latin  *string `gorm:"column:latin"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT t.id, t.work_id, t.lang, t.title, t.kind, t.latin FROM catalog_work_title t
		 `+claimedScopeJoin+` WHERE t.kind IN ?`,
		siteGalgameWiki, []int16{model.WorkTitleKindOfficial, model.WorkTitleKindAlias},
	).Scan(&scope).Error; err != nil {
		return st, fmt.Errorf("read catalog_work_title scope: %w", err)
	}
	existing := make(map[titleKey]mirrorExisting[titleVal], len(scope))
	for _, x := range scope {
		existing[titleKey{workID: x.WorkID, lang: x.Lang, title: x.Title, kind: x.Kind}] =
			mirrorExisting[titleVal]{id: x.ID, val: titleVal{latin: x.Latin}}
	}

	spec := mirrorSpec[titleKey, titleVal]{
		table:      "catalog_work_title",
		insertCols: []string{"work_id", "lang", "title", "kind", "latin"},
		rowOf: func(k titleKey, v titleVal) []any {
			return []any{k.workID, k.lang, k.title, k.kind, v.latin}
		},
		updateCols: []string{"latin"},
		updateArgs: func(v titleVal) []any { return []any{v.latin} },
		workOf:     func(k titleKey) int64 { return k.workID },
		same: func(a, b titleVal) bool {
			if (a.latin == nil) != (b.latin == nil) {
				return false
			}
			return a.latin == nil || *a.latin == *b.latin
		},
	}
	st.Note = fmt.Sprintf("scope=claimed×kind{official,alias}; aliases dropped as duplicates (of an official name or of another alias)=%d; search_hint rows left to their importer", st.Skipped)
	ledger := newMirrorLedger()
	if err := runMirror(ctx, r, ledger, spec, desired, existing); err != nil {
		return st, err
	}
	ledger.into(&st)
	return st, nil
}
