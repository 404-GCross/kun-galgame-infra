package srcvndb

// Row decoders for the staff family (staff / staff_alias / vn_staff /
// vn_seiyuu). See ingest.go for the machinery.

import (
	"time"

	"gorm.io/gorm"
)

func newStaffLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (Staff, bool) {
		id, _ := get("id")
		return Staff{
			ID: id, Gender: getStr(get, "gender"), Lang: getStr(get, "lang"),
			Main: getInt(get, "main"), Description: getStr(get, "description"),
			Prod: getStr(get, "prod"),
		}, true
	})
}

func newStaffAliasLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (StaffAlias, bool) {
		id, _ := get("id")
		return StaffAlias{
			AID: getInt(get, "aid"), ID: id,
			Name: getStr(get, "name"), Latin: getStr(get, "latin"),
		}, true
	})
}

func newVNStaffLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (VNStaff, bool) {
		id, _ := get("id")
		return VNStaff{
			ID: id, AID: getInt(get, "aid"), Role: getStr(get, "role"),
			EID: getIntPtr(get, "eid"), Note: getStr(get, "note"),
		}, true
	})
}

func newVNSeiyuuLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (VNSeiyuu, bool) {
		id, _ := get("id")
		cid, _ := get("cid")
		return VNSeiyuu{
			ID: id, CID: cid, AID: getInt(get, "aid"), Note: getStr(get, "note"),
		}, true
	})
}
