package srcvndb

// Row decoders for the releases family (releases / releases_vn /
// releases_producers / releases_platforms / releases_titles) and producers.
// See ingest.go for the machinery.

import (
	"time"

	"gorm.io/gorm"
)

func newReleaseLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (Release, bool) {
		id, _ := get("id")
		return Release{
			ID: id, GTIN: getInt64(get, "gtin"), OLang: getStr(get, "olang"),
			Released: getInt(get, "released"), Voiced: getInt16(get, "voiced"),
			ResoX: getInt16(get, "reso_x"), ResoY: getInt16(get, "reso_y"),
			MinAge:   getInt16Ptr(get, "minage"),
			AniStory: getInt16(get, "ani_story"), AniEro: getInt16(get, "ani_ero"),
			AniStorySp:  getInt16Ptr(get, "ani_story_sp"),
			AniStoryCg:  getInt16Ptr(get, "ani_story_cg"),
			AniCutscene: getInt16Ptr(get, "ani_cutscene"),
			AniEroSp:    getInt16Ptr(get, "ani_ero_sp"),
			AniEroCg:    getInt16Ptr(get, "ani_ero_cg"),
			AniBg:       getBoolPtr(get, "ani_bg"), AniFace: getBoolPtr(get, "ani_face"),
			HasEro: getBool(get, "has_ero"), Patch: getBool(get, "patch"),
			Freeware:   getBool(get, "freeware"),
			Uncensored: getBoolPtr(get, "uncensored"), Official: getBool(get, "official"),
			Catalog: getStr(get, "catalog"), Notes: getStr(get, "notes"),
			Engine: getStr(get, "engine"),
		}, true
	})
}

func newReleaseVNLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (ReleaseVN, bool) {
		id, _ := get("id")
		vid, _ := get("vid")
		return ReleaseVN{ID: id, VID: vid, RType: getStr(get, "rtype")}, true
	})
}

func newReleaseProducerLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (ReleaseProducer, bool) {
		id, _ := get("id")
		pid, _ := get("pid")
		return ReleaseProducer{
			ID: id, PID: pid,
			Developer: getBool(get, "developer"), Publisher: getBool(get, "publisher"),
		}, true
	})
}

func newReleasePlatformLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (ReleasePlatform, bool) {
		id, _ := get("id")
		platform, _ := get("platform")
		return ReleasePlatform{ID: id, Platform: platform}, true
	})
}

func newReleaseTitleLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (ReleaseTitle, bool) {
		id, _ := get("id")
		lang, _ := get("lang")
		return ReleaseTitle{
			ID: id, Lang: lang, MTL: getBool(get, "mtl"),
			Title: getStr(get, "title"), Latin: getStr(get, "latin"),
		}, true
	})
}

func newProducerLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (Producer, bool) {
		id, _ := get("id")
		return Producer{
			ID: id, Type: getStr(get, "type"), Lang: getStr(get, "lang"),
			Name: getStr(get, "name"), Latin: getStr(get, "latin"),
			Alias: getStr(get, "alias"), Description: getStr(get, "description"),
		}, true
	})
}
