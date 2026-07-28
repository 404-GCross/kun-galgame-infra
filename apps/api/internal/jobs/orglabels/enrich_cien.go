package orglabels

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Ci-en creator-profile projection (refs/proj/86): cien_profiles (crawled by
// kun-dlsite-api, doc 85) → the labels anchored to dlsite maker ids. Pure
// STRUCTURAL mapping — unnest(dlsite_maker_ids) ∩ catalog_external_ref
// (entity_type=3, source=dlsite, exact|probable) — zero name matching.
//
//   - intro: description → catalog_label_intro, source_id=cien(14),
//     fill-missing on (label, lang, source=14) so a cien row COEXISTS with a
//     vndb/bgm row of the same language (doc 86 裁定1; read-face survivorship
//     orders them). Verbatim except CRLF→LF; blank/short (<10 runes) counted,
//     never written.
//   - twitter_url → external_ref related, source=twitter(10), normalized
//     handle (E2 normalizer verbatim).
//   - cien self-link → external_ref related, source=cien(14),
//     external_id=creator_id — the exact shape E2 wrote from EG raw, so ON
//     CONFLICT dedups the overlap naturally.
//
// One creator, many makers → EVERY anchored label gets the projection. One
// maker claimed by MANY creators → first creator (creator_id ASC) wins, later
// claims counted as conflicts (doc 86: 疑似合作社/占坑).
const (
	ruleCienIntroTwitter = "rule:cien-twitter"
	ruleCienSelf         = "rule:cien-self"
)

// CienStats reports one projection run (doc 86 task item 2 counter set).
type CienStats struct {
	Creators200      int // cien_profiles rows with http_status=200
	CreatorsWithDesc int // …of those, trimmed description >= 10 runes
	MakerConflicts   int // extra claims on an already-claimed maker (first-seen wins)
	NoLabelMatch     int // creators whose makers hit no dlsite label anchor
	MappedCreators   int // creators with >= 1 anchored label
	MappedLabels     int // distinct labels hit
	ShortSkipped     int // mapped creators whose desc is blank/short — no intro row
	IntroPlanned     int
	IntroWritten     int
	IntroSkipDup     int // (label, lang, source=cien) already present — fill-missing
	TwitterPlanned   int
	TwitterWritten   int
	CienLinkPlanned  int
	CienLinkWritten  int
	Errors           int
}

// cienProfile is one crawled creator row (makers pre-joined to CSV — keeps the
// scan pgx-simple; splitMakersCSV undoes it).
type cienProfile struct {
	CreatorID  int64  `gorm:"column:creator_id"`
	Desc       string `gorm:"column:descr"`
	MakersCSV  string `gorm:"column:makers_csv"`
	TwitterURL string `gorm:"column:twitter_url"`
}

// RunEnrichCien opens the catalog + dlsite pools and runs the projection.
// The dlsite pool (--dlsite-dsn) hosts cien_profiles; it is only ever read.
func RunEnrichCien(ctx context.Context, opts Opts) (CienStats, error) {
	if opts.DSN == "" {
		return CienStats{}, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess the target database")
	}
	if opts.DlsiteDSN == "" {
		return CienStats{}, fmt.Errorf("dlsite DSN is required (--dlsite-dsn); cien_profiles lives in the dlsite database")
	}
	catalog, err := openGorm(opts.DSN)
	if err != nil {
		return CienStats{}, fmt.Errorf("open catalog pool: %w", err)
	}
	dlsite, err := openGorm(opts.DlsiteDSN)
	if err != nil {
		return CienStats{}, fmt.Errorf("open dlsite pool: %w", err)
	}
	return enrichCien(ctx, catalog, dlsite, opts.Apply)
}

// enrichCien is the pool-agnostic core (tests inject one handle for both).
func enrichCien(ctx context.Context, catalog, dlsite *gorm.DB, apply bool) (CienStats, error) {
	var st CienStats

	// maker external_id → []label_id (one RG id may anchor several labels).
	makerLabels, err := loadDlsiteLabelMultimap(catalog)
	if err != nil {
		return st, fmt.Errorf("load dlsite label anchors: %w", err)
	}
	// (label_id, lang) pairs already carrying a cien intro — the fill-missing
	// key is (label, lang, SOURCE), so only source=cien rows skip (doc 86 裁定1).
	haveCien, err := preloadCienIntroKeys(catalog)
	if err != nil {
		return st, fmt.Errorf("preload cien intros: %w", err)
	}

	var profiles []cienProfile
	if err := dlsite.WithContext(ctx).Raw(`
		SELECT creator_id, coalesce(description, '') AS descr,
			coalesce(array_to_string(dlsite_maker_ids, ','), '') AS makers_csv,
			coalesce(twitter_url, '') AS twitter_url
		FROM cien_profiles
		WHERE http_status = 200
		ORDER BY creator_id`).Scan(&profiles).Error; err != nil {
		return st, fmt.Errorf("load cien_profiles: %w", err)
	}
	st.Creators200 = len(profiles)

	claimedBy := map[string]int64{} // maker → winning creator (first-seen)
	labelSet := map[int64]bool{}
	var intros []model.CatalogLabelIntro
	var refs []model.CatalogExternalRef
	seenRef := map[string]bool{}

	for i := range profiles {
		p := &profiles[i]
		desc := strings.TrimSpace(normalizeText(p.Desc))
		if len([]rune(desc)) >= 10 {
			st.CreatorsWithDesc++
		}
		makers := splitMakersCSV(p.MakersCSV)
		if len(makers) == 0 {
			continue
		}
		// Resolve this creator's labels, first-seen-wins per maker.
		labels := map[int64]bool{}
		hadMakers := false
		for _, m := range makers {
			hadMakers = true
			if owner, taken := claimedBy[m]; taken && owner != p.CreatorID {
				st.MakerConflicts++
				continue
			}
			claimedBy[m] = p.CreatorID
			for _, lab := range makerLabels[m] {
				labels[lab] = true
			}
		}
		if len(labels) == 0 {
			if hadMakers {
				st.NoLabelMatch++
			}
			continue
		}
		st.MappedCreators++
		labelIDs := make([]int64, 0, len(labels))
		for lab := range labels {
			labelIDs = append(labelIDs, lab)
		}
		sort.Slice(labelIDs, func(a, b int) bool { return labelIDs[a] < labelIDs[b] })
		for _, lab := range labelIDs {
			labelSet[lab] = true
		}

		// Intro (fill-missing per (label, lang, source=cien)).
		if len([]rune(desc)) < 10 {
			st.ShortSkipped++
		} else {
			lang := detectLangVNDB(desc)
			for _, lab := range labelIDs {
				key := strconv.FormatInt(lab, 10) + "|" + lang
				if haveCien[key] {
					st.IntroSkipDup++
					continue
				}
				haveCien[key] = true // same-run dedup (two creators → one label)
				st.IntroPlanned++
				intros = append(intros, model.CatalogLabelIntro{
					LabelID: lab, Lang: lang, Intro: desc, SourceID: sourceCien,
				})
			}
		}

		// Links (independent of the intro decision).
		if h, ok := normalizeTwitter(p.TwitterURL); ok {
			for _, lab := range labelIDs {
				lp := linkPlan{lab, sourceTwitter, h, ruleCienIntroTwitter}
				if k := planKey(lp); !seenRef[k] {
					seenRef[k] = true
					st.TwitterPlanned++
					refs = append(refs, model.CatalogExternalRef{
						EntityType: model.EntityTypeLabel, EntityID: lab,
						SourceID: sourceTwitter, ExternalID: h,
						LinkKind: model.LinkKindRelated, MatchedBy: ruleCienIntroTwitter,
					})
				}
			}
		}
		self := strconv.FormatInt(p.CreatorID, 10)
		for _, lab := range labelIDs {
			lp := linkPlan{lab, sourceCien, self, ruleCienSelf}
			if k := planKey(lp); !seenRef[k] {
				seenRef[k] = true
				st.CienLinkPlanned++
				refs = append(refs, model.CatalogExternalRef{
					EntityType: model.EntityTypeLabel, EntityID: lab,
					SourceID: sourceCien, ExternalID: self,
					LinkKind: model.LinkKindRelated, MatchedBy: ruleCienSelf,
				})
			}
		}
	}
	st.MappedLabels = len(labelSet)

	if apply {
		n, err := batchInsert(ctx, catalog, intros)
		if err != nil {
			return st, fmt.Errorf("insert intros: %w", err)
		}
		st.IntroWritten = n
		var tw, cl []model.CatalogExternalRef
		for _, r := range refs {
			if r.SourceID == sourceTwitter {
				tw = append(tw, r)
			} else {
				cl = append(cl, r)
			}
		}
		if st.TwitterWritten, err = batchInsert(ctx, catalog, tw); err != nil {
			return st, fmt.Errorf("insert twitter refs: %w", err)
		}
		if st.CienLinkWritten, err = batchInsert(ctx, catalog, cl); err != nil {
			return st, fmt.Errorf("insert cien refs: %w", err)
		}
	}

	slog.Info("cien-label projection done", "apply", apply,
		"creators_200", st.Creators200, "creators_with_desc", st.CreatorsWithDesc,
		"maker_conflicts", st.MakerConflicts, "no_label_match", st.NoLabelMatch,
		"mapped_creators", st.MappedCreators, "mapped_labels", st.MappedLabels,
		"short_skipped", st.ShortSkipped,
		"intro_planned", st.IntroPlanned, "intro_written", st.IntroWritten,
		"intro_skip_dup", st.IntroSkipDup,
		"twitter_planned", st.TwitterPlanned, "twitter_written", st.TwitterWritten,
		"cien_link_planned", st.CienLinkPlanned, "cien_link_written", st.CienLinkWritten,
		"errors", st.Errors)
	return st, nil
}

// loadDlsiteLabelMultimap returns external_id → all label_ids anchored to it
// (entity_type=3, source=dlsite, exact+probable). Unlike anchoredLabels this
// keeps EVERY label — a maker id may legitimately anchor more than one label.
func loadDlsiteLabelMultimap(db *gorm.DB) (map[string][]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := db.Raw(`
		SELECT external_id, entity_id FROM catalog_external_ref
		WHERE entity_type = 3 AND source_id = ? AND link_kind IN (0, 1)`, sourceDlsite,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string][]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = append(m[r.ExternalID], r.EntityID)
	}
	return m, nil
}

// preloadCienIntroKeys returns the "labelID|lang" set already carrying a
// source=cien intro row (the doc-86 fill-missing key).
func preloadCienIntroKeys(db *gorm.DB) (map[string]bool, error) {
	var rows []struct {
		LabelID int64  `gorm:"column:label_id"`
		Lang    string `gorm:"column:lang"`
	}
	if err := db.Raw(`SELECT label_id, lang FROM catalog_label_intro WHERE source_id = ?`, sourceCien).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[strconv.FormatInt(r.LabelID, 10)+"|"+r.Lang] = true
	}
	return m, nil
}

// splitMakersCSV undoes the array_to_string join: trims, drops blanks, dedups
// preserving order.
func splitMakersCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
