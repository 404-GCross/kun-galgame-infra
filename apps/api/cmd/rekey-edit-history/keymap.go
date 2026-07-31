package main

import "api/internal/platform/catalog/editspec"

// The wiki→catalog field-key table (03 定案 §2 / §6, wave 154 W3 key table).
//
// The wiki family registered 26 eternal keys; catalog.work registers 13. This
// is NOT a 26→13 rename: it is a fold plus an authorized amputation, and the
// only honest way to migrate history is to say per key which of the two it is.
//
// THREE CLASSES, and the rule that decides them:
//
//  1. MAPPED — a catalog key exists AND the value transform is total and
//     round-trips (the transformed value passes the catalog field's own
//     Validate, and re-reading it from the catalog tables would produce the
//     same JSON). 17 wiki keys land on 9 catalog keys this way.
//
//  2. RETIRED IN PLACE — no catalog counterpart exists, by ruling or by fact.
//     The row KEEPS the historical key spelling (`galgame.game.<field>`) and
//     its original value. This is deliberate and is not laziness:
//
//     - the key IS the historical fact. "This revision changed the wiki's
//     release_date" is true; inventing a catalog-shaped name for a column
//     the catalog decided never to have would assert a field that never
//     existed, which is precisely what 03 §6 forbids.
//     - the engine already treats unregistered keys as first class: Diff
//     renders them generically ("historic snapshots still render, just
//     generically", engine.go) and Revert skips them ("Deprecated /
//     no-longer-registered keys in old snapshots are skipped: they still
//     render in history but have no write path anymore", revert.go). So a
//     retired key is readable history with no write path — exactly the
//     status it should have.
//     - after N5 no code anywhere references these strings. They are inert
//     data, one grep from a full dump.
//
//  3. CONDITIONALLY RETIRED — the key is in class 1, but THIS row's value
//     cannot be transformed faithfully (an id outside the mapped vocabulary,
//     a value outside the catalog enum, a fold with nothing to fold). Such a
//     row keeps the wiki key, exactly as class 2, and is counted separately in
//     the ledger. A partial value is never written: half a tag list is silent
//     data loss the moment somebody reverts to it.
//
// Every mapped value is additionally run through the catalog field's real
// Validate closure before it is written (transform.go, validateOrRetire). A
// value that would not survive its own spec is demoted to class 3 rather than
// stored — so "everything under a catalog key is spec-valid" holds by
// construction, not by inspection.
const wikiKeyPrefix = "galgame.game."

// Wiki field keys, spelled once. (The galgame editspec package is deleted in
// the same wave this tool runs in — P5 — so the tool cannot import it and
// still be runnable afterwards. These 26 strings are the migration's own
// frozen copy of a vocabulary that is about to stop existing.)
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

// nameFold is the four wiki name columns with the catalog title language they
// carry. Order and language codes are lifted VERBATIM from the live mirror
// step p (jobs/wikirescue/titlemirror.go titlePairs) so a migrated snapshot and
// the rows the mirror actually wrote describe the same titles.
var nameFold = []struct{ Key, Lang string }{
	{wikiNameJaJP, "ja"},
	{wikiNameEnUS, "en"},
	{wikiNameZhCN, "zh-Hans"},
	{wikiNameZhTW, "zh-Hant"},
}

// introFold is the same for the four synopsis columns; the language order is
// catalog's own (editspec.introLangs).
var introFold = []struct{ Key, Lang string }{
	{wikiIntroEnUS, "en"},
	{wikiIntroJaJP, "ja"},
	{wikiIntroZhCN, "zh-Hans"},
	{wikiIntroZhTW, "zh-Hant"},
}

// olangMap recodes the wiki's original_language vocabulary onto BCP-47 (the
// catalog.work.olang whitelist). Measured corpus: ja-jp 12,219 / zh-cn 252 /
// zh-tw 51 / en-us 42 / ru 9 / ko-kr 6 / others 2. `others` has no BCP-47
// meaning and is therefore NOT mapped — those rows keep the wiki key.
var olangMap = map[string]string{
	"ja-jp": "ja",
	"zh-cn": "zh-Hans",
	"zh-tw": "zh-Hant",
	"en-us": "en",
	"ko-kr": "ko",
	"ru":    "ru",
}

// ageLimitMap recodes the wiki's editorial age_limit onto catalog content
// ratings. The projection is NOT invented here: it is the one the live
// releasemeta rating job already applies to wiki-claimed works
// (jobs/releasemeta/rating.go ②: "r18" → R18, "all" → all-ages, anything else
// → no verdict). Anything else keeps the wiki key, same as that job refuses a
// verdict.
var ageLimitMap = map[string]int64{
	"all": 0, // model.ContentRatingAllAges
	"r18": 2, // model.ContentRatingR18
}

// retiredKeys are class 2: no catalog counterpart, by ruling or by fact. The
// reason string is printed in the ledger and is the audit trail for "why was
// nothing invented here".
var retiredKeys = map[string]string{
	wikiReleaseDate:      "03 §6-1: work-level release date is an authorized cut; dates live on curated catalog_release rows",
	wikiReleaseDateTBA:   "03 §6-1: TBA is the absence of a release row, not a flag",
	wikiReleasePrecision: "03 §6-1: precision is the nullable y/m/d shape of a release row",
	wikiStatus:           "03 §3: lifecycle is claim_state end to end, reached by semantic actions, never by patching a field",
	wikiVNDBID:           "03 §2: vndb_id is an identity ref (catalog_external_ref source 2), not a work field — no editable key exists",
	wikiBID:              "03 §2: bid is an identity ref (catalog_external_ref source 3), not a work field — no editable key exists",
	wikiBanner:           "catalog has no banner concept: work media is covers + screenshots, and no catalog column carries a banner URL (STOP item 1)",
	wikiAliases:          "catalog.work.titles cannot represent a lang-less alias — the mirror writes alias rows with lang='' and the field's validator rejects that (STOP item 2)",
	wikiSeriesID:         "no wiki→catalog series id space exists: catalog_series holds dlsite series only, and galgame_series was never mirrored (STOP item 3)",
}

// mappedTargets lists, for the ledger, which catalog key each mappable wiki key
// aims at. Folds are many→one.
var mappedTargets = map[string]string{
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

// foldKeys are the keys whose mapping is a MANY→ONE fold. They are mapped in
// snapshots (which carry the entity's full state, so the fold is total and
// true) and retired in place in proposal patches and amendment deltas (which
// carry a SUBSET, so a fold would record "the work's only title is this" —
// a statement the proposal never made). See transform.go.
var foldKeys = map[string]bool{
	wikiNameJaJP: true, wikiNameEnUS: true, wikiNameZhCN: true, wikiNameZhTW: true,
	wikiIntroEnUS: true, wikiIntroJaJP: true, wikiIntroZhCN: true, wikiIntroZhTW: true,
}

// labelEdgeKind is the work_label kind a wiki official projects onto: brand
// (3). Adopted verbatim from the live official mirror step
// (jobs/wikirescue/official.go pendingBrandEdgeWorks, `wl.kind = 3`), not
// chosen here — the migrated history must describe the same edges the mirror
// actually wrote.
const labelEdgeKind = int64(3)
