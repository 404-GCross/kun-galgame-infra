package orglabels

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnrichStats tallies the E2b facets. Candidate = planned rows (dry + apply);
// Written = rows actually inserted (apply only; ON CONFLICT / dup-lang skips
// are excluded, so a second --apply run reports Written 0).
type EnrichStats struct {
	IntroCandidates int
	IntroWritten    int
	IntroSkipDup    int // label already has this language (any source)
	AliasCandidates int
	AliasWritten    int
	LinkCandidates  int
	LinkWritten     int
	Errors          int
}

func (s *EnrichStats) add(o EnrichStats) {
	s.IntroCandidates += o.IntroCandidates
	s.IntroWritten += o.IntroWritten
	s.IntroSkipDup += o.IntroSkipDup
	s.AliasCandidates += o.AliasCandidates
	s.AliasWritten += o.AliasWritten
	s.LinkCandidates += o.LinkCandidates
	s.LinkWritten += o.LinkWritten
	s.Errors += o.Errors
}

// RunEnrich opens the pools and executes the requested E2b facet(s).
func RunEnrich(ctx context.Context, opts Opts) (EnrichStats, error) {
	facet := opts.Facet
	if facet == "" {
		facet = "all"
	}
	needEG := facet == "alias" || facet == "link" || facet == "all"
	catalog, eg, err := openPools(opts, needEG)
	if err != nil {
		return EnrichStats{}, err
	}
	return enrichAll(ctx, catalog, eg, facet, opts.Apply)
}

// enrichAll is the pool-agnostic core (tests inject the catalog handle as eg
// too). intro/alias/link are all fill-missing and idempotent.
func enrichAll(ctx context.Context, catalog, eg *gorm.DB, facet string, apply bool) (EnrichStats, error) {
	var total EnrichStats
	if facet == "intro" || facet == "all" {
		st, err := enrichIntro(ctx, catalog, apply)
		if err != nil {
			return total, fmt.Errorf("intro facet: %w", err)
		}
		total.add(st)
	}
	if facet == "alias" || facet == "all" {
		st, err := enrichAlias(ctx, catalog, eg, apply)
		if err != nil {
			return total, fmt.Errorf("alias facet: %w", err)
		}
		total.add(st)
	}
	if facet == "link" || facet == "all" {
		st, err := enrichLink(ctx, catalog, eg, apply)
		if err != nil {
			return total, fmt.Errorf("link facet: %w", err)
		}
		total.add(st)
	}
	slog.Info("org-label enrich done", "facet", facet, "apply", apply,
		"intro_candidates", total.IntroCandidates, "intro_written", total.IntroWritten,
		"intro_skip_dup", total.IntroSkipDup, "alias_candidates", total.AliasCandidates,
		"alias_written", total.AliasWritten, "link_candidates", total.LinkCandidates,
		"link_written", total.LinkWritten, "errors", total.Errors)
	return total, nil
}

// anchoredLabels returns external_id → label_id for a source's entity_type=3
// anchors (exact + probable). This is the org→label bridge every facet enriches
// along.
func anchoredLabels(db *gorm.DB, source int16) (map[string]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := db.Raw(`
		SELECT external_id, entity_id FROM catalog_external_ref
		WHERE entity_type = 3 AND source_id = ? AND link_kind IN (0, 1)`, source,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.EntityID
	}
	return m, nil
}

// labelDisplayNames returns label_id → display_name for the given label ids, so
// the alias facet can drop a variant identical to the display name.
func labelDisplayNames(db *gorm.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const chunk = 10000
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		var rows []struct {
			ID          int64  `gorm:"column:id"`
			DisplayName string `gorm:"column:display_name"`
		}
		if err := db.Raw(`SELECT id, display_name FROM catalog_label WHERE id IN ?`, ids[start:end]).
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r.DisplayName
		}
	}
	return out, nil
}

// preloadIntroLangs returns label_id → set of languages already present in
// catalog_label_intro (across ALL sources) — the fill-missing skip index.
// SOURCE rows only (provenance=0): a machine translation must never block a
// genuine upstream text in the same language from landing.
func preloadIntroLangs(db *gorm.DB) (map[int64]map[string]bool, error) {
	var rows []struct {
		LabelID int64  `gorm:"column:label_id"`
		Lang    string `gorm:"column:lang"`
	}
	if err := db.Raw(`SELECT label_id, lang FROM catalog_label_intro WHERE provenance = 0`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]map[string]bool, len(rows))
	for _, r := range rows {
		if m[r.LabelID] == nil {
			m[r.LabelID] = map[string]bool{}
		}
		m[r.LabelID][r.Lang] = true
	}
	return m, nil
}

// insertIntro writes one label intro, fill-missing. Returns true when a row was
// actually inserted (RowsAffected>0), false when the ON CONFLICT backstop fired.
// batchInsert writes rows with ON CONFLICT DO NOTHING (no target — any unique
// or PK violation is skipped), returning the number actually inserted. This is
// the fill-missing backstop that makes a second --apply write zero.
func batchInsert[T any](ctx context.Context, db *gorm.DB, rows []T) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 1000)
	return int(res.RowsAffected), res.Error
}

// enrichIntro writes VNDB producer descriptions + Bangumi company/group
// summaries onto their anchored labels (fill-missing, one per language). The
// preloaded existing-lang index skips a language any source already supplied.
func enrichIntro(ctx context.Context, db *gorm.DB, apply bool) (EnrichStats, error) {
	existing, err := preloadIntroLangs(db)
	if err != nil {
		return EnrichStats{}, err
	}
	var st EnrichStats
	var rows []model.CatalogLabelIntro
	plan := func(labelID int64, lang, text string, source int16) {
		if existing[labelID][lang] {
			st.IntroSkipDup++
			return
		}
		if existing[labelID] == nil {
			existing[labelID] = map[string]bool{}
		}
		existing[labelID][lang] = true // same-run dedup across sources
		st.IntroCandidates++
		rows = append(rows, model.CatalogLabelIntro{LabelID: labelID, Lang: lang, Intro: text, SourceID: source})
	}

	// VNDB (description → strip markup → 3-way lang).
	vndbLabels, err := anchoredLabels(db, sourceVNDB)
	if err != nil {
		return st, err
	}
	var vndbRows []struct {
		ExtID string `gorm:"column:ext_id"`
		Descr string `gorm:"column:descr"`
	}
	if err := db.Raw(`SELECT id AS ext_id, description AS descr FROM src_vndb.producers WHERE coalesce(description,'') <> ''`).
		Scan(&vndbRows).Error; err != nil {
		return st, err
	}
	for _, r := range vndbRows {
		labelID, ok := vndbLabels[r.ExtID]
		if !ok {
			continue
		}
		text := stripVNDBMarkup(normalizeText(r.Descr))
		if strings.TrimSpace(text) == "" {
			continue
		}
		plan(labelID, detectLangVNDB(text), text, sourceVNDB)
	}

	// Bangumi (summary → 2-way lang).
	bgmLabels, err := anchoredLabels(db, sourceBangumi)
	if err != nil {
		return st, err
	}
	var bgmRows []struct {
		ExtID   string `gorm:"column:ext_id"`
		Summary string `gorm:"column:summary"`
	}
	if err := db.Raw(`SELECT id::text AS ext_id, summary FROM src_bangumi.person WHERE type IN (2,3) AND coalesce(summary,'') <> ''`).
		Scan(&bgmRows).Error; err != nil {
		return st, err
	}
	for _, r := range bgmRows {
		labelID, ok := bgmLabels[r.ExtID]
		if !ok {
			continue
		}
		text := strings.TrimSpace(normalizeText(r.Summary))
		if text == "" {
			continue
		}
		plan(labelID, detectLang(text), text, sourceBangumi)
	}

	if apply {
		n, err := batchInsert(ctx, db, rows)
		if err != nil {
			return st, err
		}
		st.IntroWritten = n
	}
	return st, nil
}

// enrichAlias writes VNDB producer aliases (spelling variant) + EG furigana
// (search hint) onto their anchored labels. A variant equal to the label's
// display name is dropped; lang is unknown (”).
func enrichAlias(ctx context.Context, db, eg *gorm.DB, apply bool) (EnrichStats, error) {
	var st EnrichStats
	var rows []model.CatalogLabelAlias
	seen := map[string]bool{}
	// source is which upstream published the spelling — the two legs below draw
	// from different ones, so it is a per-plan argument rather than a constant of
	// the run. Recorded since wave 195; provenance is flat source (0) because
	// neither leg translates anything, it forwards what the upstream wrote.
	plan := func(labelID int64, name string, kind, source int16) {
		key := fmt.Sprintf("%d\x00%s", labelID, name)
		if seen[key] {
			return
		}
		seen[key] = true
		st.AliasCandidates++
		src := source
		rows = append(rows, model.CatalogLabelAlias{
			LabelID: labelID, Name: name, Lang: "", Kind: kind,
			SourceID: &src, Provenance: model.AliasProvenanceSource,
		})
	}

	// VNDB alias lines → spelling variant.
	vndbLabels, err := anchoredLabels(db, sourceVNDB)
	if err != nil {
		return st, err
	}
	names, err := labelDisplayNames(db, valueIDs(vndbLabels))
	if err != nil {
		return st, err
	}
	var vndbRows []struct {
		ExtID string `gorm:"column:ext_id"`
		Alias string `gorm:"column:alias"`
	}
	if err := db.Raw(`SELECT id AS ext_id, alias FROM src_vndb.producers WHERE coalesce(alias,'') <> ''`).
		Scan(&vndbRows).Error; err != nil {
		return st, err
	}
	for _, r := range vndbRows {
		labelID, ok := vndbLabels[r.ExtID]
		if !ok {
			continue
		}
		for line := range strings.SplitSeq(r.Alias, "\n") {
			name := strings.TrimSpace(line)
			if name == "" || name == names[labelID] {
				continue
			}
			plan(labelID, name, model.AliasKindSpellingVariant, sourceVNDB)
		}
	}

	// EG furigana → search hint. Requires the eg pool.
	if eg != nil {
		egLabels, err := anchoredLabels(db, sourceEG)
		if err != nil {
			return st, err
		}
		egNames, err := labelDisplayNames(db, valueIDs(egLabels))
		if err != nil {
			return st, err
		}
		var egRows []struct {
			ExtID string `gorm:"column:ext_id"`
			BF    string `gorm:"column:bf"`
			MF    string `gorm:"column:mf"`
		}
		if err := eg.Raw(`SELECT id::text AS ext_id, coalesce(raw->>'brandfurigana','') AS bf, coalesce(raw->>'makerfurigana','') AS mf FROM brands`).
			Scan(&egRows).Error; err != nil {
			return st, err
		}
		for _, r := range egRows {
			labelID, ok := egLabels[r.ExtID]
			if !ok {
				continue
			}
			for _, name := range dedupStrings([]string{strings.TrimSpace(r.BF), strings.TrimSpace(r.MF)}) {
				if name == "" || name == egNames[labelID] {
					continue
				}
				plan(labelID, name, model.AliasKindSearchHint, sourceEG)
			}
		}
	}

	if apply {
		n, err := batchInsert(ctx, db, rows)
		if err != nil {
			return st, err
		}
		st.AliasWritten = n
	}
	return st, nil
}

// valueIDs returns the distinct label ids of an ext→label map.
func valueIDs(m map[string]int64) []int64 {
	seen := make(map[int64]bool, len(m))
	out := make([]int64, 0, len(m))
	for _, v := range m {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
