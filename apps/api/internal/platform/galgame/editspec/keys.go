// Package editspec declares the galgame family's editing-engine registration
// (E2a, doc 21 §2.2): the eternal field keys of galgame.game, their validators
// and apply closures carrying the galgame DB pool. The engine stays
// family-agnostic — this package is the galgame side of the dependency
// injection, wired at the cmd/catalog assembly point (charter ruling 1),
// mirroring internal/platform/catalog/editspec.
package editspec

import "fmt"

// Eternal field keys of galgame.game (§2.2b — never renamed, never reused).
// Every key is "galgame.game." + the historical snapshot key, a mechanical 1:1
// rename of the historical snapshot vocabulary (the E2b legacy transform and
// the old-wire strangler adapter that consumed it retired at E3b). `gid` is not
// registered (permanently immutable, SEO); `galgame.game.status` is the one key
// with no old-snapshot counterpart (management direct-edit field, 03 号裁定 1).
const (
	TypeGame = "galgame.game"

	FieldVNDBID           = "galgame.game.vndb_id"
	FieldBID              = "galgame.game.bid"
	FieldReleaseDate      = "galgame.game.release_date"
	FieldReleaseDateTBA   = "galgame.game.release_date_tba"
	FieldReleasePrecision = "galgame.game.release_precision"
	FieldNameEnUS         = "galgame.game.name_en_us"
	FieldNameJaJP         = "galgame.game.name_ja_jp"
	FieldNameZhCN         = "galgame.game.name_zh_cn"
	FieldNameZhTW         = "galgame.game.name_zh_tw"
	FieldBanner           = "galgame.game.banner"
	FieldIntroEnUS        = "galgame.game.intro_en_us"
	FieldIntroJaJP        = "galgame.game.intro_ja_jp"
	FieldIntroZhCN        = "galgame.game.intro_zh_cn"
	FieldIntroZhTW        = "galgame.game.intro_zh_tw"
	FieldContentLimit     = "galgame.game.content_limit"
	FieldOriginalLanguage = "galgame.game.original_language"
	FieldAgeLimit         = "galgame.game.age_limit"
	FieldSeriesID         = "galgame.game.series_id"
	FieldAliases          = "galgame.game.aliases"
	FieldTagIDs           = "galgame.game.tag_ids"
	FieldOfficialIDs      = "galgame.game.official_ids"
	FieldEngineIDs        = "galgame.game.engine_ids"
	FieldLinks            = "galgame.game.links"
	FieldCovers           = "galgame.game.covers"
	FieldScreenshots      = "galgame.game.screenshots"
	FieldStatus           = "galgame.game.status"
)

// SiteGalgameWiki is the tenant key every old-wire galgame edit is filed under
// (the wiki face has no per-site identity of its own; the key matches the
// image-service site scope already used for galgame bytes).
const SiteGalgameWiki = "galgame_wiki"

// SiteKungal is the kungal forum's tenant key (E3a — the galgame family's
// full-featured edit home; matches the forum OAuth client's catalog_site
// binding). Its overlay in RegisterGame sends every default-policy field
// through the kungal review queue: propose stays open (mirroring the old
// wiki SubmitPR = any logged-in user), nothing automerges (direct-edit
// powers open later per need). The special keys (bid locked, vndb_id,
// status) keep their E2a field policies — no overlay entries.
const SiteKungal = "kungal"

// OldToNew maps a historical snapshot / changed_fields key to its eternal
// field key. The status column was never a snapshot key — it has no entry.
var OldToNew = map[string]string{
	"vndb_id":           FieldVNDBID,
	"bid":               FieldBID,
	"release_date":      FieldReleaseDate,
	"release_date_tba":  FieldReleaseDateTBA,
	"release_precision": FieldReleasePrecision,
	"name_en_us":        FieldNameEnUS,
	"name_ja_jp":        FieldNameJaJP,
	"name_zh_cn":        FieldNameZhCN,
	"name_zh_tw":        FieldNameZhTW,
	"banner":            FieldBanner,
	"intro_en_us":       FieldIntroEnUS,
	"intro_ja_jp":       FieldIntroJaJP,
	"intro_zh_cn":       FieldIntroZhCN,
	"intro_zh_tw":       FieldIntroZhTW,
	"content_limit":     FieldContentLimit,
	"original_language": FieldOriginalLanguage,
	"age_limit":         FieldAgeLimit,
	"series_id":         FieldSeriesID,
	"aliases":           FieldAliases,
	"tag_ids":           FieldTagIDs,
	"official_ids":      FieldOfficialIDs,
	"engine_ids":        FieldEngineIDs,
	"links":             FieldLinks,
	"covers":            FieldCovers,
	"screenshots":       FieldScreenshots,
}

// NewToOld is the inverse rename for the old-wire adapter, plus the status
// key's old-wire spelling (a new-era-only changed_fields label — old snapshots
// never carried it, so the wire snapshot mapper DROPS it instead).
var NewToOld = func() map[string]string {
	m := make(map[string]string, len(OldToNew)+1)
	for oldKey, newKey := range OldToNew {
		m[newKey] = oldKey
	}
	m[FieldStatus] = "status"
	return m
}()

// RekeyKeysOldToNew maps a changed_fields list from the historical snapshot
// vocabulary to the eternal field keys, preserving order. The galgame
// create/submit birth-record path uses it: model.KeysOf reports old-vocabulary
// keys, and the engine's RecordCreated wants eternal keys. Unknown old keys
// hard-fail — the surveyed corpus is exactly the registered vocabulary, so an
// unknown key means the mapping table is incomplete.
func RekeyKeysOldToNew(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		newKey, ok := OldToNew[k]
		if !ok {
			return nil, fmt.Errorf("editspec: changed_fields key %q has no eternal-key mapping", k)
		}
		out = append(out, newKey)
	}
	return out, nil
}
