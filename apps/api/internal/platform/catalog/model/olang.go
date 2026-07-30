package model

// This file owns catalog_work.olang: its vocabulary, its default, and the one
// translation that feeds it from the galgame wiki's product-locale spelling.
//
// olang is the ORIGINAL language of a work in the UPSTREAM (VNDB) BCP-47
// spelling — ja / en / zh-Hans / zh-Hant / ko / ru / es / pt-br … — never the
// product-locale form (ja-jp / zh-cn) the deprecated wiki face published. It is
// the public /v1 population gate (service.PublicOLang), so a creation lane that
// stamps a placeholder instead of the truth does not merely mislabel one row: it
// silently disables that gate for everyone.
//
// That is exactly what happened. Until wave 144 every registry row carried a
// flat 'ja' (no lane had ever written a real value), which made the release
// calendar's default ja+zh family gate a global no-op and flooded both consumer
// sites with western VNs. The rule this file encodes: a lane that KNOWS the
// original language writes it; a lane that genuinely does not know writes
// OLangDefault deliberately, never as an accident.

import "strings"

// OLangDefault is the value a lane writes when it has no original-language
// signal at all (the DLsite / eges / Bangumi cross-media importers, whose
// catalogues are Japanese by construction). The column carries NO database
// default on purpose — every write path states its choice.
const OLangDefault = "ja"

// MapWikiOLang translates a galgame-wiki `galgame.original_language` value into
// the catalog olang vocabulary.
//
// The wiki stores the PRODUCT-LOCALE spelling for the five locales it renders
// UI in and passes everything else through verbatim, because that column is
// itself written by the VNDB sync (jobs/vndbsync mapLang: ja→ja-jp, en→en-us,
// zh-Hans→zh-cn, zh-Hant→zh-tw, ko→ko-kr, everything else verbatim). This
// function is that map's exact inverse, so a VN synced into the wiki and then
// claimed into the registry lands on the olang VNDB published for it.
//
// ok=false marks input that carries no usable language — empty/whitespace, and
// the two junk values the live wiki actually holds ('others' and the typo 'ck').
// The returned olang is always usable (OLangDefault in that case); ok exists so
// a backfill can COUNT how much of its population it had to guess at rather than
// quietly folding those rows into the ja bucket.
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
		// ru / es / pt-br / uk / fr / … — the wiki already stores the upstream
		// spelling for these, so passing them through IS the translation.
		return v, true
	}
}
