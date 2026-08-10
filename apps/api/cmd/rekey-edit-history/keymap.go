package main

import "api/internal/platform/catalog/editspec"

const wikiKeyPrefix = "galgame.game."

const (
	wikiVNDBID           = wikiKeyPrefix + "vndb_id"
	wikiBID              = wikiKeyPrefix + "bid"
	wikiReleaseDate      = wikiKeyPrefix + "release_date"
	wikiReleaseDateTBA   = wikiKeyPrefix + "release_date_tba"
	wikiReleasePrecision = wikiKeyPrefix + "release_precision"
	wikiNameEnUS         = wikiKeyPrefix + "name_en_us"
	wikiNameJaJP         = wikiKeyPrefix + "name_ja_jp"
	wikiNameZhCN         = wikiKeyPrefix + "name_zh_cn"
	wikiNameZhTW         = wikiKeyPrefix + "name_zh_tw"
	wikiBanner           = wikiKeyPrefix + "banner"
	wikiIntroEnUS        = wikiKeyPrefix + "intro_en_us"
	wikiIntroJaJP        = wikiKeyPrefix + "intro_ja_jp"
	wikiIntroZhCN        = wikiKeyPrefix + "intro_zh_cn"
	wikiIntroZhTW        = wikiKeyPrefix + "intro_zh_tw"
	wikiContentLimit     = wikiKeyPrefix + "content_limit"
	wikiOriginalLanguage = wikiKeyPrefix + "original_language"
	wikiAgeLimit         = wikiKeyPrefix + "age_limit"
	wikiSeriesID         = wikiKeyPrefix + "series_id"
	wikiAliases          = wikiKeyPrefix + "aliases"
	wikiTagIDs           = wikiKeyPrefix + "tag_ids"
	wikiOfficialIDs      = wikiKeyPrefix + "official_ids"
	wikiEngineIDs        = wikiKeyPrefix + "engine_ids"
	wikiLinks            = wikiKeyPrefix + "links"
	wikiCovers           = wikiKeyPrefix + "covers"
	wikiScreenshots      = wikiKeyPrefix + "screenshots"
	wikiStatus           = wikiKeyPrefix + "status"
)

var nameFold = []struct{ Key, Lang string }{
	{wikiNameJaJP, "ja"},
	{wikiNameEnUS, "en"},
	{wikiNameZhCN, "zh-Hans"},
	{wikiNameZhTW, "zh-Hant"},
}

const aliasesFoldKind = int64(1)

var introFold = []struct{ Key, Lang string }{
	{wikiIntroEnUS, "en"},
	{wikiIntroJaJP, "ja"},
	{wikiIntroZhCN, "zh-Hans"},
	{wikiIntroZhTW, "zh-Hant"},
}

var olangMap = map[string]string{
	"ja-jp": "ja",
	"zh-cn": "zh-Hans",
	"zh-tw": "zh-Hant",
	"en-us": "en",
	"ko-kr": "ko",
	"ru":    "ru",
}

var ageLimitMap = map[string]int64{
	"all": 0,
	"r18": 2,
}

var retiredKeys = map[string]string{
	wikiReleaseDate:      "03 §6-1: work-level release date is an authorized cut; dates live on curated catalog_release rows",
	wikiReleaseDateTBA:   "03 §6-1: TBA is the absence of a release row, not a flag",
	wikiReleasePrecision: "03 §6-1: precision is the nullable y/m/d shape of a release row",
	wikiStatus:           "03 §3: lifecycle is claim_state end to end, reached by semantic actions, never by patching a field",
	wikiVNDBID:           "03 §2: vndb_id is an identity ref (catalog_external_ref source 2), not a work field — no editable key exists",
	wikiBID:              "03 §2: bid is an identity ref (catalog_external_ref source 3), not a work field — no editable key exists",
	wikiBanner:           "catalog has no banner concept: work media is covers + screenshots, and no catalog column carries a banner URL (STOP item 1)",
	wikiSeriesID:         "no wiki→catalog series id space exists: catalog_series holds dlsite series only, and galgame_series was never mirrored (STOP item 3)",
}

var mappedTargets = map[string]string{
	wikiAliases:          editspec.FieldWorkTitles,
	wikiNameJaJP:         editspec.FieldWorkTitles,
	wikiNameEnUS:         editspec.FieldWorkTitles,
	wikiNameZhCN:         editspec.FieldWorkTitles,
	wikiNameZhTW:         editspec.FieldWorkTitles,
	wikiIntroEnUS:        editspec.FieldWorkIntros,
	wikiIntroJaJP:        editspec.FieldWorkIntros,
	wikiIntroZhCN:        editspec.FieldWorkIntros,
	wikiIntroZhTW:        editspec.FieldWorkIntros,
	wikiContentLimit:     editspec.FieldWorkDisplayNSFW,
	wikiOriginalLanguage: editspec.FieldWorkOLang,
	wikiAgeLimit:         editspec.FieldWorkContentRating,
	wikiTagIDs:           editspec.FieldWorkTagIDs,
	wikiOfficialIDs:      editspec.FieldWorkLabels,
	wikiEngineIDs:        editspec.FieldWorkEngineIDs,
	wikiLinks:            editspec.FieldWorkLinks,
	wikiCovers:           editspec.FieldWorkCovers,
	wikiScreenshots:      editspec.FieldWorkScreenshots,
}

var foldKeys = map[string]bool{
	wikiAliases:  true,
	wikiNameJaJP: true, wikiNameEnUS: true, wikiNameZhCN: true, wikiNameZhTW: true,
	wikiIntroEnUS: true, wikiIntroJaJP: true, wikiIntroZhCN: true, wikiIntroZhTW: true,
}

const labelEdgeKind = int64(3)
