package model

import "strings"

const OLangDefault = "ja"

func MapWikiOLang(raw string) (olang string, ok bool) {
	switch v := strings.TrimSpace(raw); v {
	case "", "others", "ck":
		return OLangDefault, false
	case "ja-jp":
		return "ja", true
	case "en-us":
		return "en", true
	case "zh-cn":
		return "zh-Hans", true
	case "zh-tw":
		return "zh-Hant", true
	case "ko-kr":
		return "ko", true
	default:
		return v, true
	}
}
