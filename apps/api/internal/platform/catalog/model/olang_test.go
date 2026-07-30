package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMapWikiOLang pins the wiki-locale → catalog-olang table, including the two
// properties the rest of wave 144 leans on:
//
//   - it is the EXACT inverse of the VNDB → wiki mapping the wiki sync applies
//     (jobs/vndbsync mapLang), so a VN synced into the wiki and then claimed
//     into the registry lands back on the olang VNDB published;
//   - ok=false marks input that carried no language at all, so a backfill can
//     COUNT its guesses instead of folding them invisibly into the ja bucket.
func TestMapWikiOLang(t *testing.T) {
	for raw, want := range map[string]string{
		"ja-jp": "ja",
		"en-us": "en",
		"zh-cn": "zh-Hans",
		"zh-tw": "zh-Hant",
		"ko-kr": "ko",
		// Everything else the wiki already stores in the upstream spelling.
		"ru":     "ru",
		"pt-br":  "pt-br",
		"es":     "es",
		" en-us": "en",
	} {
		got, ok := MapWikiOLang(raw)
		assert.Equalf(t, want, got, "MapWikiOLang(%q)", raw)
		assert.Truef(t, ok, "MapWikiOLang(%q) carries a usable language", raw)
	}

	// No usable language: always a usable RESULT, but flagged so it can be counted.
	for _, raw := range []string{"", "   ", "others", "ck"} {
		got, ok := MapWikiOLang(raw)
		assert.Equalf(t, OLangDefault, got, "MapWikiOLang(%q) falls back", raw)
		assert.Falsef(t, ok, "MapWikiOLang(%q) must report the fallback", raw)
	}
}
