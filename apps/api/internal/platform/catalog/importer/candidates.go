package importer

import (
	"regexp"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

type CandidateStats struct {
	Twitter          int
	Pixiv            int
	Written          int
	Already          int
	AmbiguousSkipped int
}

func (im *Importer) RunCandidates() (CandidateStats, error) {
	var st CandidateStats
	cnAnchor, err := im.loadAnchors(model.EntityTypeCreditName)
	if err != nil {
		return st, err
	}

	bangumi := map[string]map[int64]bool{}
	eg := map[string]map[int64]bool{}
	add := func(m map[string]map[int64]bool, handle string, cnID int64) {
		if handle == "" || cnID == 0 {
			return
		}
		if m[handle] == nil {
			m[handle] = map[int64]bool{}
		}
		m[handle][cnID] = true
	}

	var brows []struct {
		PID int64  `gorm:"column:pid"`
		K   string `gorm:"column:k"`
		V   string `gorm:"column:v"`
	}
	if err := im.catalog.Raw(`
		SELECT p.id AS pid, f->>'Key' AS k, f->>'Value' AS v
		FROM src_bangumi.person p, jsonb_array_elements(p.infobox_parsed->'Fields') f
		WHERE p.parse_error='' AND jsonb_typeof(p.infobox_parsed->'Fields')='array'
		  AND ((f->>'Key') ~* 'twitter|pixiv')
		  AND p.id IN (SELECT external_id::bigint FROM catalog_external_ref WHERE entity_type = ? AND source_id = ?)`,
		model.EntityTypeCreditName, bangumiSource).Scan(&brows).Error; err != nil {
		return st, err
	}
	for _, r := range brows {
		cnID := cnAnchor[anchorKey(bangumiSource, strconv.FormatInt(r.PID, 10))]
		add(bangumi, handleOf(r.K, r.V), cnID)
	}

	var erows []struct {
		ID string `gorm:"column:id"`
		TW string `gorm:"column:tw"`
		PX string `gorm:"column:px"`
	}
	if err := im.eg.Raw(`SELECT id::text AS id, coalesce(raw->>'twitter_username','') AS tw, coalesce(raw->>'pixiv','') AS px FROM creaters`).Scan(&erows).Error; err != nil {
		return st, err
	}
	for _, r := range erows {
		cnID := cnAnchor[anchorKey(egSource, r.ID)]
		if cnID == 0 {
			continue
		}
		add(eg, handleOf("twitter", r.TW), cnID)
		add(eg, handleOf("pixiv", r.PX), cnID)
	}

	for h := range union(bangumi, eg) {
		if strings.HasPrefix(h, "tw:") {
			st.Twitter++
		} else if strings.HasPrefix(h, "px:") {
			st.Pixiv++
		}
	}

	var cands []model.CatalogMatchCandidate
	seen := map[[2]int64]bool{}
	for h, bset := range bangumi {
		eset, ok := eg[h]
		if !ok {
			continue
		}
		if len(bset) > 5 || len(eset) > 5 {
			st.AmbiguousSkipped++
			continue
		}
		for b := range bset {
			for e := range eset {
				a, z := b, e
				if a == z {
					continue
				}
				if a > z {
					a, z = z, a
				}
				if seen[[2]int64{a, z}] {
					continue
				}
				seen[[2]int64{a, z}] = true
				cands = append(cands, model.CatalogMatchCandidate{
					EntityType: model.EntityTypeCreditName, AID: a, BID: z,
					Reason: model.CandidateReasonSharedExternalID, Status: model.CandidateStatusPending,
				})
			}
		}
	}

	if im.dryRun {
		st.Written = len(cands)
		return st, nil
	}
	written := 0
	const batch = 1000
	for start := 0; start < len(cands); start += batch {
		end := min(start+batch, len(cands))
		chunk := cands[start:end]
		res := im.catalog.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk)
		if res.Error != nil {
			return st, res.Error
		}
		written += int(res.RowsAffected)
	}
	st.Written = written
	st.Already = len(cands) - written
	return st, nil
}

var reNonDigit = regexp.MustCompile(`\D+`)

func handleOf(key, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "pixiv"):
		id := reNonDigit.ReplaceAllString(value, "")
		if id == "" {
			return ""
		}
		return "px:" + id
	case strings.Contains(k, "twitter") || strings.Contains(k, "x(twitter)"):
		return twitterKey(value)
	}
	return ""
}

func twitterKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, host := range []string{"twitter.com/", "x.com/"} {
		if i := strings.Index(v, host); i >= 0 {
			v = v[i+len(host):]
		}
	}
	v = strings.TrimPrefix(v, "@")
	if i := strings.IndexAny(v, "/?#& "); i >= 0 {
		v = v[:i]
	}
	if v == "" || len(v) > 15 || !isHandle(v) {
		return ""
	}
	return "tw:" + v
}

func isHandle(s string) bool {
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func union(a, b map[string]map[int64]bool) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}
