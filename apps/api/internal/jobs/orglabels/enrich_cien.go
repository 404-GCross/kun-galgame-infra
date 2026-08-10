package orglabels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

const (
	ruleCienIntroTwitter = "rule:cien-twitter"
	ruleCienSelf         = "rule:cien-self"
	ruleCienExtLink      = "rule:cien-ext-link"
)

type CienStats struct {
	Creators200      int
	CreatorsWithDesc int
	MakerConflicts   int
	NoLabelMatch     int
	MappedCreators   int
	MappedLabels     int
	ShortSkipped     int
	IntroPlanned     int
	IntroWritten     int
	IntroSkipDup     int
	TwitterPlanned   int
	TwitterWritten   int
	CienLinkPlanned  int
	CienLinkWritten  int
	ExtLinkPlanned   int
	ExtLinkWritten   int
	Errors           int
}

type cienProfile struct {
	CreatorID    int64  `gorm:"column:creator_id"`
	Desc         string `gorm:"column:descr"`
	MakersCSV    string `gorm:"column:makers_csv"`
	TwitterURL   string `gorm:"column:twitter_url"`
	ExtLinksJSON string `gorm:"column:ext_links_json"`
}

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

func enrichCien(ctx context.Context, catalog, dlsite *gorm.DB, apply bool) (CienStats, error) {
	var st CienStats

	makerLabels, err := loadDlsiteLabelMultimap(catalog)
	if err != nil {
		return st, fmt.Errorf("load dlsite label anchors: %w", err)
	}
	haveCien, err := preloadCienIntroKeys(catalog)
	if err != nil {
		return st, fmt.Errorf("preload cien intros: %w", err)
	}

	var profiles []cienProfile
	if err := dlsite.WithContext(ctx).Raw(`
		SELECT creator_id, coalesce(description, '') AS descr,
			coalesce(array_to_string(dlsite_maker_ids, ','), '') AS makers_csv,
			coalesce(twitter_url, '') AS twitter_url,
			coalesce(external_links::text, '') AS ext_links_json
		FROM cien_profiles
		WHERE http_status = 200
		ORDER BY creator_id`).Scan(&profiles).Error; err != nil {
		return st, fmt.Errorf("load cien_profiles: %w", err)
	}
	st.Creators200 = len(profiles)

	claimedBy := map[string]int64{}
	labelSet := map[int64]bool{}
	var intros []model.CatalogLabelIntro
	var refs []model.CatalogExternalRef
	var extRefs []model.CatalogExternalRef
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
				haveCien[key] = true
				st.IntroPlanned++
				intros = append(intros, model.CatalogLabelIntro{
					LabelID: lab, Lang: lang, Intro: desc, SourceID: sourceCien,
				})
			}
		}

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

		for _, el := range parseCienExtLinks(p.ExtLinksJSON) {
			src, ext, ok := cienExtLinkSource(el.URL)
			if !ok {
				continue
			}
			for _, lab := range labelIDs {
				lp := linkPlan{lab, src, ext, ruleCienExtLink}
				if k := planKey(lp); !seenRef[k] {
					seenRef[k] = true
					st.ExtLinkPlanned++
					extRefs = append(extRefs, model.CatalogExternalRef{
						EntityType: model.EntityTypeLabel, EntityID: lab,
						SourceID: src, ExternalID: ext,
						LinkKind: model.LinkKindRelated, MatchedBy: ruleCienExtLink,
					})
				}
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
		if st.ExtLinkWritten, err = batchInsert(ctx, catalog, extRefs); err != nil {
			return st, fmt.Errorf("insert ext-link refs: %w", err)
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
		"ext_link_planned", st.ExtLinkPlanned, "ext_link_written", st.ExtLinkWritten,
		"errors", st.Errors)
	return st, nil
}

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

func preloadCienIntroKeys(db *gorm.DB) (map[string]bool, error) {
	var rows []struct {
		LabelID int64  `gorm:"column:label_id"`
		Lang    string `gorm:"column:lang"`
	}
	if err := db.Raw(`SELECT label_id, lang FROM catalog_label_intro WHERE provenance = 0 AND source_id = ?`, sourceCien).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[strconv.FormatInt(r.LabelID, 10)+"|"+r.Lang] = true
	}
	return m, nil
}

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

type cienExtLinkRow struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

func parseCienExtLinks(raw string) []cienExtLinkRow {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var rows []cienExtLinkRow
	if json.Unmarshal([]byte(raw), &rows) != nil {
		return nil
	}
	return rows
}

func cienExtLinkSource(url string) (int16, string, bool) {
	host := extHost(url)
	switch {
	case hostUnder(host, "pixiv.net") || hostUnder(host, "pixiv.me"):
		return sourcePixiv, strings.TrimSpace(url), true
	case hostUnder(host, "dmm.co.jp"):
		return sourceDmm, strings.TrimSpace(url), true
	case hostUnder(host, "steampowered.com"):
		return sourceSteam, strings.TrimSpace(url), true
	}
	return 0, "", false
}

func hostUnder(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func extHost(url string) string {
	s := strings.TrimSpace(url)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexAny(s, "/:?#"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}
