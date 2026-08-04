package tagcanon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseTagMapShapes pins the four shapes docs/tagMap.ts actually uses. The
// parser moved here when the wiki platform package was retired, and its own
// package's tests all covered the DB half that left with the tables — so the
// half that survived arrived untested. Each case below is a real shape from the
// file, and a miss is silent: an unparsed key just keeps its English name.
func TestParseTagMapShapes(t *testing.T) {
	const src = `export const tagMap = {
  'Protagonist': '主人公',
  "Protagonist's Pronoun Choice": '主角自称',
  Pokémon: '宝可梦',
  ADV: '文字冒险',
  'Some Very Long English Tag Name That Prettier Wraps':
    '被折行的条目',
  'Trailing': '结尾',
}`
	dir := t.TempDir()
	path := filepath.Join(dir, "tagMap.ts")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseTagMap(path)
	if err != nil {
		t.Fatalf("ParseTagMap: %v", err)
	}
	want := map[string]string{
		"Protagonist":                  "主人公",
		"Protagonist's Pronoun Choice": "主角自称", // double-quoted, because the key has an apostrophe
		"Pokémon":                      "宝可梦",  // bareword, non-ASCII
		"ADV":                          "文字冒险",
		"Some Very Long English Tag Name That Prettier Wraps": "被折行的条目", // wrapped onto two lines
		"Trailing": "结尾",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
}

// TestParseTagMapMissingFile: a missing tagMap is an error the caller must see,
// not an empty map that silently leaves every tag under its English name.
func TestParseTagMapMissingFile(t *testing.T) {
	if _, err := ParseTagMap(filepath.Join(t.TempDir(), "nope.ts")); err == nil {
		t.Fatal("want an error for a missing tagMap, got nil")
	}
}

func TestDefaultTagMapPathHonoursEnv(t *testing.T) {
	t.Setenv("KUN_VNDB_TAGMAP_PATH", "/custom/tagMap.ts")
	if p := DefaultTagMapPath(); p != "/custom/tagMap.ts" {
		t.Errorf("env override ignored: %q", p)
	}
	t.Setenv("KUN_VNDB_TAGMAP_PATH", "")
	if p := DefaultTagMapPath(); p != "docs/tagMap.ts" {
		t.Errorf("default path: %q", p)
	}
}
