package tagcanon

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	xnorm "golang.org/x/text/unicode/norm"
)

const (
	sourceKeyVNDB    = "vndb"
	sourceKeyBangumi = "bangumi"
	sourceKeyDlsite  = "dlsite"
	sourceKeyCurated = "curated"
)

type sourceIDs struct {
	vndb    int16
	bangumi int16
	dlsite  int16
	curated int16
}

func resolveSources(ctx context.Context, db *gorm.DB) (sourceIDs, error) {
	var s sourceIDs
	for key, dst := range map[string]*int16{
		sourceKeyVNDB: &s.vndb, sourceKeyBangumi: &s.bangumi, sourceKeyDlsite: &s.dlsite,
		sourceKeyCurated: &s.curated,
	} {
		if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(dst).Error; err != nil {
			return s, fmt.Errorf("resolve source %q: %w", key, err)
		}
	}
	if s.vndb == 0 || s.bangumi == 0 || s.dlsite == 0 || s.curated == 0 {
		return s, fmt.Errorf("registry not seeded (vndb=%d bangumi=%d dlsite=%d curated=%d)", s.vndb, s.bangumi, s.dlsite, s.curated)
	}
	return s, nil
}

type vocabEntry struct {
	SourceID   int16
	Name       string
	Norm       string
	Usage      int
	Junk       bool
	JunkReason string
}

func loadWorkTagVocab(ctx context.Context, db *gorm.DB, sourceID int16) ([]vocabEntry, error) {
	var rows []struct {
		Name  string `gorm:"column:name"`
		Usage int    `gorm:"column:usage"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT name, count(DISTINCT work_id) AS usage
		FROM catalog_work_tag WHERE source_id = ?
		GROUP BY name`, sourceID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load work_tag vocab (source %d): %w", sourceID, err)
	}
	out := make([]vocabEntry, 0, len(rows))
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		out = append(out, vocabEntry{SourceID: sourceID, Name: name, Norm: normalize(name), Usage: r.Usage})
	}
	return out, nil
}

// junkIndex is the entity-name blocklist for bangumi's free-text tag lane, plus
// the vocabulary that overrides it. Wave 208 hand-rejected 419 person names and
// 129 company nicknames out of one review batch — a name the catalog already
// knows as a company or a credit is the dominant noise class there.
type junkIndex struct {
	blocked map[string]string
	vocab   map[string]struct{}
}

func loadJunkIndex(ctx context.Context, db *gorm.DB) (*junkIndex, error) {
	idx := &junkIndex{blocked: map[string]string{}, vocab: map[string]struct{}{}}
	for _, q := range []struct{ reason, sql string }{
		// Order matters only for the reason label a name ends up reported under.
		{"person_alias", `SELECT name FROM catalog_name_alias`},
		{"person", `SELECT name FROM catalog_credit_name`},
		{"label_alias", `SELECT name FROM catalog_label_alias`},
		{"label", `SELECT display_name FROM catalog_label`},
	} {
		var names []string
		if err := db.WithContext(ctx).Raw(q.sql).Scan(&names).Error; err != nil {
			return nil, fmt.Errorf("load %s norms: %w", q.reason, err)
		}
		for _, n := range names {
			k := normalize(n)
			if k == "" {
				continue
			}
			idx.blocked[k] = q.reason
			// The catalog stores Japanese personal names with a space
			// ("鈴木 達央"); a bangumi tagger types them without one. That single
			// character hid 315 person and company names from this gate.
			if s := spaceless(k); s != "" && s != k {
				idx.blocked[s] = q.reason
			}
		}
	}
	for _, q := range []string{
		`SELECT name FROM catalog_tag`,
		`SELECT source_name FROM catalog_tag_source_map`,
	} {
		var names []string
		if err := db.WithContext(ctx).Raw(q).Scan(&names).Error; err != nil {
			return nil, fmt.Errorf("load vocabulary norms: %w", err)
		}
		for _, n := range names {
			if k := normalize(n); k != "" {
				idx.vocab[k] = struct{}{}
			}
		}
	}
	return idx, nil
}

// A name the vocabulary already carries is never junk: a company sharing its
// spelling ("SIM" is a studio AND the simulation genre) must not retire a tag
// the catalog is already answering with.
func (j *junkIndex) reason(norm string) string {
	if j == nil {
		return ""
	}
	if _, known := j.vocab[norm]; known {
		return ""
	}
	if r, hit := j.blocked[norm]; hit {
		return r
	}
	return j.blocked[spaceless(norm)]
}

func spaceless(s string) string { return strings.ReplaceAll(s, " ", "") }

func loadRejectedNames(ctx context.Context, db *gorm.DB) (map[string]struct{}, error) {
	var rows []struct {
		SourceID   int16  `gorm:"column:source_id"`
		SourceName string `gorm:"column:source_name"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT source_id, source_name FROM catalog_tag_rejection`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load tag rejections: %w", err)
	}
	out := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		out[mapKey(r.SourceID, r.SourceName)] = struct{}{}
	}
	return out, nil
}

func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(xnorm.NFKC.String(s)))
}

var (
	reNumber = regexp.MustCompile(`^\d+$`)
	reDate   = regexp.MustCompile(`^\d{4}([-/.]\d{1,2}([-/.]\d{1,2})?|年(\d{1,2}月(\d{1,2}日?)?)?)?$`)
	reDecade = regexp.MustCompile(`^(19|20)\d0s$`)
	reDisc   = regexp.MustCompile(`^(s|ss|season|disc|cd|dvd|vol\.?|ep\.?|盘|碟)\s?\d{1,3}$`)
)

func bgmJunk(norm string, idx *junkIndex) string {
	switch {
	case reNumber.MatchString(norm):
		return "number"
	case reDate.MatchString(norm):
		return "date"
	case reDecade.MatchString(norm):
		return "decade"
	case reDisc.MatchString(norm):
		return "disc"
	}
	if isMeta(norm) {
		return ""
	}
	return idx.reason(norm)
}

var metaNorms = map[string]struct{}{}

func init() {
	for _, n := range []string{
		"r18", "r-18", "18x", "18r", "nsfw", "工口", "ero", "h", "全年龄", "健全", "无修", "无修正",
		"pc", "windows", "steam", "android", "ios", "mac", "linux", "ns", "psv", "ps4", "ps5", "web", "vr", "xboxone", "xsx",
		"rpg", "hrpg", "jrpg", "arpg", "srpg", "adv", "avg", "冒险", "slg", "act", "stg", "trpg", "puz", "tps", "sim", "roguelike", "eroge", "vn",
		"同人", "汉化", "官中", "机翻", "生肉", "无语音", "扩展包", "dlc",
		"像素", "像素风", "像素h", "3d", "3dhentai", "live2d", "动画", "动态cg", "动态", "ai生成", "ai",
		"rpgmaker", "rpg制作大师", "rm",
		"galgame", "gal", "黄油", "游戏", "游戏性",
	} {
		metaNorms[n] = struct{}{}
	}
}

func isMeta(norm string) bool { _, ok := metaNorms[norm]; return ok }
