package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapWikiOLang(t *testing.T) {
	for raw, want := range map[string]string{
		"ja-jp":  "ja",
		"en-us":  "en",
		"zh-cn":  "zh-Hans",
		"zh-tw":  "zh-Hant",
		"ko-kr":  "ko",
		"ru":     "ru",
		"pt-br":  "pt-br",
		"es":     "es",
		" en-us": "en",
	} {
		got, ok := MapWikiOLang(raw)
		assert.Equalf(t, want, got, "MapWikiOLang(%q)", raw)
		assert.Truef(t, ok, "MapWikiOLang(%q) carries a usable language", raw)
	}

	for _, raw := range []string{"", "   ", "others", "ck"} {
		got, ok := MapWikiOLang(raw)
		assert.Equalf(t, OLangDefault, got, "MapWikiOLang(%q) falls back", raw)
		assert.Falsef(t, ok, "MapWikiOLang(%q) must report the fallback", raw)
	}
}
