package tagcanon

import (
	"os"
	"regexp"
	"strings"
)

func DefaultTagMapPath() string {
	if p := os.Getenv("KUN_VNDB_TAGMAP_PATH"); p != "" {
		return p
	}
	return "docs/tagMap.ts"
}

var (
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
