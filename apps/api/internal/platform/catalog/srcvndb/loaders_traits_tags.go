package srcvndb

import (
	"time"

	"gorm.io/gorm"
)

func newTraitLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (Trait, bool) {
		id, _ := get("id")
		return Trait{
			ID: id, GID: getStr(get, "gid"), GOrder: getInt16(get, "gorder"),
			DefaultSpoil: getInt16(get, "defaultspoil"),
			Sexual:       getBool(get, "sexual"), Searchable: getBool(get, "searchable"),
			Applicable: getBool(get, "applicable"), Name: getStr(get, "name"),
			Alias: getStr(get, "alias"), Description: getStr(get, "description"),
		}, true
	})
}

func newTraitParentLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (TraitParent, bool) {
		id, _ := get("id")
		parent, _ := get("parent")
		return TraitParent{ID: id, Parent: parent, Main: getBool(get, "main")}, true
	})
}

func newCharTraitLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (CharTrait, bool) {
		id, _ := get("id")
		tid, _ := get("tid")
		return CharTrait{
			ID: id, TID: tid,
			Spoil: getInt16(get, "spoil"), Lie: getBool(get, "lie"),
		}, true
	})
}

func newTagLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (Tag, bool) {
		id, _ := get("id")
		return Tag{
			ID: id, Cat: getStr(get, "cat"),
			DefaultSpoil: getInt16(get, "defaultspoil"),
			Searchable:   getBool(get, "searchable"), Applicable: getBool(get, "applicable"),
			Name: getStr(get, "name"), Alias: getStr(get, "alias"),
			Description: getStr(get, "description"),
		}, true
	})
}

func newTagParentLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (TagParent, bool) {
		id, _ := get("id")
		parent, _ := get("parent")
		return TagParent{ID: id, Parent: parent, Main: getBool(get, "main")}, true
	})
}

func newTagVNLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (TagVN, bool) {
		tag, _ := get("tag")
		vid, _ := get("vid")
		return TagVN{
			Date: getStr(get, "date"), Tag: tag, VID: vid,
			UID: getStr(get, "uid"), Vote: getInt16(get, "vote"),
			Spoiler: getInt16Ptr(get, "spoiler"), Ignore: getBool(get, "ignore"),
			Lie: getBoolPtr(get, "lie"), Notes: getStr(get, "notes"),
		}, true
	})
}
