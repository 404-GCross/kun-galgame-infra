package tagcanon

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	xnorm "golang.org/x/text/unicode/norm"
)

// Source keys resolved at runtime (never hardcoded ids — the bgmsummaries /
// worktags discipline: a rehearsal / prod DB with different seeds still works).
const (
	sourceKeyVNDB    = "vndb"
	sourceKeyBangumi = "bangumi"
	sourceKeyDlsite  = "dlsite"
)

// sourceIDs holds the three vocabulary sources' registry ids.
type sourceIDs struct {
	vndb    int16
	bangumi int16
	dlsite  int16
}

func resolveSources(ctx context.Context, db *gorm.DB) (sourceIDs, error) {
	var s sourceIDs
	for key, dst := range map[string]*int16{
		sourceKeyVNDB: &s.vndb, sourceKeyBangumi: &s.bangumi, sourceKeyDlsite: &s.dlsite,
	} {
		if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(dst).Error; err != nil {
			return s, fmt.Errorf("resolve source %q: %w", key, err)
		}
	}
	if s.vndb == 0 || s.bangumi == 0 || s.dlsite == 0 {
		return s, fmt.Errorf("registry not seeded (vndb=%d bangumi=%d dlsite=%d)", s.vndb, s.bangumi, s.dlsite)
	}
	return s, nil
}

// vocabEntry is one distinct (source, name) with its usage count and — for
// bangumi only — its junk verdict. Norm is the NFKC+casefold+trim key the
// grouping folds on; Name is the ORIGINAL string the map row is written with.
type vocabEntry struct {
	SourceID   int16
	Name       string
	Norm       string
	Usage      int
	Junk       bool   // bangumi junk: excluded from grouping AND from stats
	JunkReason string // date / disc / number / label
}

// loadWorkTagVocab reads a catalog_work_tag source's vocabulary: distinct name
// with usage = number of distinct works carrying it.
//
// Since wave 149 this backs the vndb lane too. It used to read the wiki tag
// layer directly (galgame_tag.name ⋈ galgame_tag_relation), which does not
// survive the galgame family's retirement; W1-pre step r materialized that whole
// layer into catalog_work_tag under source_id=vndb *carrying the same curated
// zh display names*, so the native table is a name-for-name replacement — not
// the English src_vndb.tags upstream, which would have swapped the vocabulary's
// language out from under the cross-source folding. Measured on the 2026-07-30
// kun_catalog snapshot: 2,793 native entries vs 3,037 wiki entries, ZERO names
// added and 244 dropped — every dropped name was carried only by unclaimed wiki
// drafts (57 of them by nothing at all), so none of them has a catalog-side
// footprint once the wiki tables are gone.
//
// 66 of those 244 do currently fold with a bangumi/dlsite name, so the group
// count falls with them. That is the correction, not the cost: a cross-source
// group exists to canonicalize a name that several sources actually carry, and
// a vndb map row for a name with no vndb work edge canonicalizes nothing. The
// wiki-draft-only vocabulary was inflating the vndb lane with supply the
// catalog read face never saw.
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

// loadLabelNorms preloads the normalized catalog_label display names — the
// maker/circle set a bangumi tag colliding with is a misfiled attribution
// entity (ディーゼルマイン / AVANTGARDE …), not content: junk. Normalized in Go
// with the SAME function as the tags so the collision test is exact.
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

// normalize is the grouping key: NFKC compatibility composition (fullwidth
// （）→ () and Ａ→A, etc.), simple casefold (lower — CJK has no case, latin
// folds Galgame→galgame), and trim. Idempotent.
func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(xnorm.NFKC.String(s)))
}

// Junk regexes (bangumi only, doc 74 §确定性预配-2). A name is junk when it is a
// pure number, a date, a decade, or a season/disc/volume number — personal
// cataloguing metadata that is redundant with structured fields and never
// belongs in a content vocabulary (pilot §分类: bgm junk ~2/3 mechanically
// filterable).
var (
	reNumber = regexp.MustCompile(`^\d+$`)
	reDate   = regexp.MustCompile(`^\d{4}([-/.]\d{1,2}([-/.]\d{1,2})?|年(\d{1,2}月(\d{1,2}日?)?)?)?$`)
	reDecade = regexp.MustCompile(`^(19|20)\d0s$`)
	reDisc   = regexp.MustCompile(`^(s|ss|season|disc|cd|dvd|vol\.?|ep\.?|盘|碟)\s?\d{1,3}$`)
)

// bgmJunk classifies a bangumi name (norm = its normalized form). Returns the
// reason tag when junk, "" otherwise.
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

// metaNorms is the hand-pinned platform-meta set (doc 74 §确定性预配-4, from the
// pilot's ~10 meta families). A canonical row whose norm is in this set is
// stamped kind=meta (stays out of the content-tag cloud, feeds an attribute
// filter). Everything else is kind=content. Keys are normalized forms. The set
// is generous on purpose (over-inclusion is harmless — a norm that never forms
// a cross-source group never stamps anything), grounded in the pilot families:
// rating / platform / genre-of-play / doujin+localization / presentation /
// engine / medium.
var metaNorms = map[string]struct{}{}

func init() {
	for _, n := range []string{
		// rating (adult / all-ages / censorship status)
		"r18", "r-18", "18x", "18r", "nsfw", "工口", "ero", "h", "全年龄", "健全", "无修", "无修正",
		// platform / OS
		"pc", "windows", "steam", "android", "ios", "mac", "linux", "ns", "psv", "ps4", "ps5", "web", "vr", "xboxone", "xsx",
		// genre-of-play (form) — the RPG/ADV/SLG families
		"rpg", "hrpg", "jrpg", "arpg", "srpg", "adv", "avg", "冒险", "slg", "act", "stg", "trpg", "puz", "tps", "sim", "roguelike", "eroge", "vn",
		// doujin / distribution / localization status
		"同人", "汉化", "官中", "机翻", "生肉", "无语音", "扩展包", "dlc",
		// presentation (pixel / 3d / animation / AI)
		"像素", "像素风", "像素h", "3d", "3dhentai", "live2d", "动画", "动态cg", "动态", "ai生成", "ai",
		// engine
		"rpgmaker", "rpg制作大师", "rm",
		// medium / self-referential
		"galgame", "gal", "黄油", "游戏", "游戏性",
	} {
		metaNorms[n] = struct{}{}
	}
}

func isMeta(norm string) bool { _, ok := metaNorms[norm]; return ok }
