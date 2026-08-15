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

func loadLabelNorms(ctx context.Context, db *gorm.DB) (map[string]struct{}, error) {
	var names []string
	if err := db.WithContext(ctx).Raw(`SELECT display_name FROM catalog_label`).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("load label norms: %w", err)
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		if k := normalize(n); k != "" {
			set[k] = struct{}{}
		}
	}
	return set, nil
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

func bgmJunk(norm string, labelNorms map[string]struct{}) string {
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
	if _, hit := labelNorms[norm]; hit {
		return "label"
	}
	return ""
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
