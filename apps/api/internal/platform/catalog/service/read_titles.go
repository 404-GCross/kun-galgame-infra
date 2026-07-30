// read_titles.go — the work TITLE read face and its CLAIMED bridge (A2-R1,
// refs/proj/136 区 A).
//
// Titles were the last identity facet still read straight out of the catalog's
// own table, and that is exactly why a claimed work's Chinese name and aliases
// disappeared from every consumer: a CLAIMED work's names live in the WIKI body
// (galgame's four name columns + galgame_alias), never in catalog_work_title,
// so 87% of claimed works had no title row at all. This file gives titles the
// same bridge-not-copy treatment (refs/proj/51 §2) the intro / cover /
// screenshot facets already have:
//
//   - CLAIMED (site='galgame_wiki'): bridged from the galgame body — the four
//     fixed name columns pivot to OFFICIAL rows (galgameNamePivot, the intro
//     bridge's table-position shape), and each galgame_alias row becomes an
//     ALIAS row with NO language. The wiki records no language for an alias, and
//     inventing one would be a fabrication — an empty tag also keeps aliases out
//     of the D7 four-key names pivot (which they do not belong in) while still
//     reaching the detail titles[] and the search index, which is precisely the
//     wanted behavior: an alias is searchable and displayable, not a name slot.
//   - BODYLESS (site empty or NULL): the work's catalog_work_title rows,
//     verbatim.
//   - Strict XOR (§8.D, the intro bridge's own law): a claimed work reads ONLY
//     the bridge and never falls back to native rows, even when it still has
//     some (shadow-never-delete).
//
// Bridge-not-copy in the strong sense: nothing here writes catalog_work_title.
//
// TWO LANES, ONE BRIDGE. The bridge half is shared verbatim; the native half
// differs because the two consumers have different frozen contracts:
//
//   - the DISPLAY lane (loadWorkTitles) drops search_hint rows in the query —
//     they are findability-only and never leave the internal face — and orders
//     (kind, id) so the list's per-key pivot has a stable first row;
//   - the DETAIL lane (loadWorkDetailTitles) keeps EVERY kind and the (kind,
//     lang) order the S2S work record has always emitted, so a bodyless work's
//     detail response stays byte-identical to what it was before this wave.
package service

import (
	"context"
	"strings"

	"api/internal/platform/catalog/model"
)

// WorkTitleRow is one title on a work's read face — the unified shape the wiki
// bridge and the catalog-native table both project into. Latin is the romanized
// rendering ("" when unrecorded; the wiki body has no romaji column, so bridged
// rows never carry one).
type WorkTitleRow struct {
	Lang  string
	Title string
	Latin string
	Kind  int16
}

// galgameNamePivot is the FIXED column→language mapping of the claimed title
// bridge — the galgameIntroPivot of the name family, in the same table position
// order, with the same BCP-47 tags so bridged and native rows are one shape.
var galgameNamePivot = []struct{ Column, Lang string }{
	{"name_ja_jp", "ja"},
	{"name_en_us", "en"},
	{"name_zh_cn", "zh-Hans"},
	{"name_zh_tw", "zh-Hant"},
}

// loadWorkTitles reads the DISPLAY titles for a set of works: claimed works
// bridge the wiki body, bodyless works read their native rows with search_hint
// excluded at the query level. Batched (§9.1) — three queries for the whole
// set, never per work. A work with no title is absent from the map (the caller
// renders []).
func (s *ReadService) loadWorkTitles(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTitleRow, error) {
	return s.loadTitles(ctx, subjects, false)
}

// loadWorkDetailTitles is the DETAIL lane: same bridge, but the native half
// keeps every kind (search hints included — the internal work record has always
// carried them) in its frozen (kind, lang) order.
func (s *ReadService) loadWorkDetailTitles(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTitleRow, error) {
	return s.loadTitles(ctx, subjects, true)
}

// loadTitles is the shared body of the two lanes; withHints selects the native
// half's caliber (see the file doc).
func (s *ReadService) loadTitles(ctx context.Context, subjects []claimSubject, withHints bool) (map[int64][]WorkTitleRow, error) {
	out := make(map[int64][]WorkTitleRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	galgameIDs, galgameToWork, bodylessIDs := partitionClaimSubjects(subjects)
	if len(galgameIDs) > 0 {
		if err := s.bridgeGalgameTitles(ctx, galgameIDs, galgameToWork, out); err != nil {
			return nil, err
		}
	}
	if len(bodylessIDs) > 0 {
		if err := s.nativeWorkTitles(ctx, bodylessIDs, out, withHints); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// bridgeGalgameTitles reads the claimed works' wiki bodies in TWO queries — the
// name columns and the alias rows — and projects both into title rows. Officials
// lead in the fixed pivot order, aliases follow in wiki id order, which is the
// (kind ASC, stable within kind) ordering both lanes' native halves produce.
//
// An alias whose exact string is already one of the four official names is
// DROPPED (27% of the corpus): the two are the same name, and a reader must not
// be shown it twice — the same "one picture, one row" rule the screenshot lanes
// dedup on (refs/proj/128), applied inside the bridge rather than across lanes.
// Officials win the key because they carry a language and the alias does not.
func (s *ReadService) bridgeGalgameTitles(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, out map[int64][]WorkTitleRow) error {
	db := s.db.WithContext(ctx)

	var names []struct {
		ID       int64  `gorm:"column:id"`
		NameJaJP string `gorm:"column:name_ja_jp"`
		NameEnUS string `gorm:"column:name_en_us"`
		NameZhCN string `gorm:"column:name_zh_cn"`
		NameZhTW string `gorm:"column:name_zh_tw"`
	}
	if err := db.Raw(`SELECT id, name_ja_jp, name_en_us, name_zh_cn, name_zh_tw
		FROM galgame WHERE id IN ?`, galgameIDs).Scan(&names).Error; err != nil {
		return err
	}
	// official[galgame id] is that body's name set, for the alias dedup below.
	official := make(map[int64]map[string]struct{}, len(names))
	for _, r := range names {
		workID, ok := galgameToWork[r.ID]
		if !ok {
			continue
		}
		byCol := map[string]string{
			"name_ja_jp": r.NameJaJP, "name_en_us": r.NameEnUS,
			"name_zh_cn": r.NameZhCN, "name_zh_tw": r.NameZhTW,
		}
		seen := make(map[string]struct{}, len(galgameNamePivot))
		for _, p := range galgameNamePivot {
			title := strings.TrimSpace(byCol[p.Column])
			if title == "" {
				continue // an empty column is not a name
			}
			seen[title] = struct{}{}
			out[workID] = append(out[workID], WorkTitleRow{
				Lang: p.Lang, Title: title, Kind: model.WorkTitleKindOfficial,
			})
		}
		official[r.ID] = seen
	}

	var aliases []struct {
		GalgameID int64  `gorm:"column:galgame_id"`
		Name      string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT galgame_id, name FROM galgame_alias
		WHERE galgame_id IN ? ORDER BY galgame_id, id`, galgameIDs).Scan(&aliases).Error; err != nil {
		return err
	}
	for _, a := range aliases {
		workID, ok := galgameToWork[a.GalgameID]
		if !ok {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		if _, dup := official[a.GalgameID][name]; dup {
			continue // the same name already surfaced as an official title
		}
		out[workID] = append(out[workID], WorkTitleRow{
			// No language: the wiki records none for an alias, and no lang is the
			// honest projection (it also keeps aliases out of the D7 names pivot).
			Title: name, Kind: model.WorkTitleKindAlias,
		})
	}
	return nil
}

// nativeWorkTitles reads the BODYLESS works' catalog_work_title rows in ONE
// query. withHints selects the lane's caliber: the display lane excludes
// search_hint at the query level and orders (kind, id); the detail lane keeps
// every kind in the frozen (kind, lang) order.
func (s *ReadService) nativeWorkTitles(ctx context.Context, workIDs []int64, out map[int64][]WorkTitleRow, withHints bool) error {
	q := `SELECT work_id, lang, title, coalesce(latin, '') AS latin, kind FROM catalog_work_title
		WHERE work_id IN ? AND kind <> ? ORDER BY work_id, kind, id`
	args := []any{workIDs, model.WorkTitleKindSearchHint}
	if withHints {
		q = `SELECT work_id, lang, title, coalesce(latin, '') AS latin, kind FROM catalog_work_title
			WHERE work_id IN ? ORDER BY work_id, kind, lang`
		args = []any{workIDs}
	}
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Title  string `gorm:"column:title"`
		Latin  string `gorm:"column:latin"`
		Kind   int16  `gorm:"column:kind"`
	}
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkTitleRow{
			Lang: r.Lang, Title: r.Title, Latin: r.Latin, Kind: r.Kind,
		})
	}
	return nil
}
