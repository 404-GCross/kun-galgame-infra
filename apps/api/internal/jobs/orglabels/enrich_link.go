package orglabels

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

const (
	ruleEGOfficial  = "rule:eg-official-site"
	ruleEGTwitter   = "rule:eg-twitter"
	ruleEGCien      = "rule:eg-cien"
	ruleBGMOfficial = "rule:bangumi-official-site"
	ruleBGMTwitter  = "rule:bangumi-twitter"
)

type linkPlan struct {
	labelID    int64
	source     int16
	externalID string
	rule       string
}

func enrichLink(ctx context.Context, db, eg *gorm.DB, apply bool) (EnrichStats, error) {
	var st EnrichStats
	var plans []linkPlan

	if eg != nil {
		egLabels, err := anchoredLabels(db, sourceEG)
		if err != nil {
			return st, err
		}
		var egRows []struct {
			ExtID string `gorm:"column:ext_id"`
			URL   string `gorm:"column:url"`
			TW    string `gorm:"column:tw"`
			Cien  string `gorm:"column:cien"`
		}
		if err := eg.Raw(`SELECT id::text AS ext_id, coalesce(raw->>'url','') AS url,
			coalesce(raw->>'twitter','') AS tw, coalesce(raw->>'cien','') AS cien FROM brands`).
			Scan(&egRows).Error; err != nil {
			return st, err
		}
		for _, r := range egRows {
			labelID, ok := egLabels[r.ExtID]
			if !ok {
				continue
			}
			if id, ok := normalizeURL(r.URL); ok {
				plans = append(plans, linkPlan{labelID, sourceOfficialSite, id, ruleEGOfficial})
			}
			if h, ok := normalizeTwitter(r.TW); ok {
				plans = append(plans, linkPlan{labelID, sourceTwitter, h, ruleEGTwitter})
			}
			if id, ok := normalizeCien(r.Cien); ok {
				plans = append(plans, linkPlan{labelID, sourceCien, id, ruleEGCien})
			}
		}
	}

	bgmLabels, err := anchoredLabels(db, sourceBangumi)
	if err != nil {
		return st, err
	}
	var bgmRows []struct {
		ExtID string `gorm:"column:ext_id"`
		Key   string `gorm:"column:k"`
		Value string `gorm:"column:v"`
	}
	if err := db.Raw(`
		SELECT p.id::text AS ext_id, f->>'Key' AS k, f->>'Value' AS v
		FROM src_bangumi.person p, jsonb_array_elements(p.infobox_parsed->'Fields') f
		WHERE p.type IN (2,3) AND coalesce(p.parse_error,'') = ''
		  AND jsonb_typeof(p.infobox_parsed->'Fields') = 'array'
		  AND coalesce(f->>'Value','') <> ''`).Scan(&bgmRows).Error; err != nil {
		return st, err
	}
	for _, r := range bgmRows {
		labelID, ok := bgmLabels[r.ExtID]
		if !ok {
			continue
		}
		switch classifyBGMKey(r.Key) {
		case bgmKeyWebsite:
			if id, ok := normalizeURL(r.Value); ok {
				plans = append(plans, linkPlan{labelID, sourceOfficialSite, id, ruleBGMOfficial})
			}
		case bgmKeyTwitter:
			if h, ok := normalizeTwitter(r.Value); ok {
				plans = append(plans, linkPlan{labelID, sourceTwitter, h, ruleBGMTwitter})
			}
		}
	}

	seen := make(map[string]bool, len(plans))
	var refs []model.CatalogExternalRef
	for _, p := range plans {
		k := planKey(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		st.LinkCandidates++
		refs = append(refs, model.CatalogExternalRef{
			EntityType: model.EntityTypeLabel,
			EntityID:   p.labelID,
			SourceID:   p.source,
			ExternalID: p.externalID,
			LinkKind:   model.LinkKindRelated,
			MatchedBy:  p.rule,
		})
	}
	if apply {
		n, err := batchInsert(ctx, db, refs)
		if err != nil {
			return st, err
		}
		st.LinkWritten = n
	}
	return st, nil
}

func planKey(p linkPlan) string {
	return strconv.FormatInt(p.labelID, 10) + "|" + strconv.Itoa(int(p.source)) + "|" + p.externalID
}

type bgmKeyClass int

const (
	bgmKeyNone bgmKeyClass = iota
	bgmKeyWebsite
	bgmKeyTwitter
)

var (
	reWebsiteKey = regexp.MustCompile(`官网|官方网站|官方網站|官方网址|官方網址|网站|網站|主页|主頁|website|homepage`)
	reWebExclude = regexp.MustCompile(`(?i)微博|博客|blog|weibo|steam|dlsite|facebook|youtube|niconico|bilibili|pixiv|instagram`)
	reTwitterKey = regexp.MustCompile(`(?i)twitter|^x\b|x\s*\(twitter\)|x\(twitter\)`)
)

func classifyBGMKey(key string) bgmKeyClass {
	k := strings.TrimSpace(key)
	if reWebExclude.MatchString(k) {
		if reTwitterKey.MatchString(k) {
			return bgmKeyTwitter
		}
		return bgmKeyNone
	}
	if reTwitterKey.MatchString(k) {
		return bgmKeyTwitter
	}
	if reWebsiteKey.MatchString(strings.ToLower(k)) || reWebsiteKey.MatchString(k) {
		return bgmKeyWebsite
	}
	return bgmKeyNone
}

var (
	reArchiveWrap = regexp.MustCompile(`(?i)web\.archive\.org/web/[^/]+/(https?://.+)$`)
	reScheme      = regexp.MustCompile(`(?i)^(https?:)?//`)
	reTwitterHost = regexp.MustCompile(`(?i)^(https?://)?(www\.|mobile\.)?(twitter\.com|x\.com)/`)
	reHandle      = regexp.MustCompile(`^[A-Za-z0-9_]{1,30}$`)
	reDigits      = regexp.MustCompile(`^[0-9]+$`)
)

func normalizeURL(u string) (string, bool) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", false
	}
	if m := reArchiveWrap.FindStringSubmatch(u); m != nil {
		u = m[1]
	}
	u = reScheme.ReplaceAllString(u, "")
	u = strings.TrimRight(u, "/")
	u = strings.TrimSpace(u)
	if u == "" || !strings.Contains(u, ".") || strings.ContainsAny(u, " \t") {
		return "", false
	}
	return u, true
}

func normalizeTwitter(v string) (string, bool) {
	v = strings.TrimSpace(v)
	v = reTwitterHost.ReplaceAllString(v, "")
	v = strings.TrimPrefix(v, "@")
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if !reHandle.MatchString(v) {
		return "", false
	}
	return v, true
}

func normalizeCien(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !reDigits.MatchString(v) {
		return "", false
	}
	return v, true
}
