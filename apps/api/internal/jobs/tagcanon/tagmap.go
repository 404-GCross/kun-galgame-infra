package tagcanon

// The tagMap reader, relocated here from internal/platform/galgame/vndbresolve
// when the wiki platform package was retired with its tables (wave 149 T4).
//
// It came along because it was the ONLY thing tagcanon still needed from that
// package, and it needed nothing from the wiki: tagMap.ts is a file on disk,
// the English→Chinese localization layer, and parsing it touches no database at
// all. The rest of vndbresolve — which really did create galgame_tag /
// galgame_official rows — left with the tables.
//
// docs/tagMap.ts is also load-bearing at runtime for the vndbsync image, which
// is why .dockerignore carries an explicit `!docs/tagMap.ts` re-include.

import (
	"os"
	"regexp"
	"strings"
)

// DefaultTagMapPath resolves the tagMap path: env override, else repo-root
// default (consistent with other path-style config defaults).
func DefaultTagMapPath() string {
	if p := os.Getenv("KUN_VNDB_TAGMAP_PATH"); p != "" {
		return p
	}
	return "docs/tagMap.ts"
}

// ParseTagMap reads docs/tagMap.ts into an english→chinese map. It must tolerate
// every shape the file actually uses, or a caller misses a key's Chinese
// mapping:
//   - keys single-quoted, double-quoted (when the key has an apostrophe, e.g.
//     "Protagonist's Pronoun Choice"), or bareword incl. non-ASCII (Pokémon);
//   - long entries Prettier-wraps onto two lines (`key:\n    'value'`).
var (
	// tagMapWrapRegex rejoins a Prettier-wrapped `key:\n    'value'` so the
	// line-oriented match below sees `key: 'value'`. It only fires on a `:` that
	// is followed (after whitespace/newline) by the opening value quote, so it
	// never merges two separate single-line entries (those have `,` before the
	// newline, not `:`).
	tagMapWrapRegex = regexp.MustCompile(`:[ \t]*\r?\n[ \t]*'`)
	tagMapLineRegex = regexp.MustCompile(`^\s*(?:'([^']+)'|"([^"]+)"|([\pL\pN_][\pL\pN_\s./()\-]*[\pL\pN_]|[\pL\pN_]))\s*:\s*'([^']+)'`)
)

func ParseTagMap(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	joined := tagMapWrapRegex.ReplaceAllString(string(data), ": '")
	result := make(map[string]string)
	for _, line := range strings.Split(joined, "\n") {
		m := tagMapLineRegex.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if key == "" {
			key = m[2]
		}
		if key == "" {
			key = m[3]
		}
		if value := m[4]; key != "" && value != "" {
			result[key] = value
		}
	}
	return result, nil
}
