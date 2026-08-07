package srcvndb

// Row decoders for the link-graph family (vn_extlinks / producers_extlinks /
// staff_extlinks / producers_relations). See ingest.go for the machinery.

import (
	"time"

	"gorm.io/gorm"
)

func newVNExtlinkLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (VNExtlink, bool) {
		id, _ := get("id")
		return VNExtlink{ID: id, Link: getInt(get, "link")}, true
	})
}

func newProducerExtlinkLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (ProducerExtlink, bool) {
		id, _ := get("id")
		return ProducerExtlink{ID: id, Link: getInt(get, "link")}, true
	})
}

func newStaffExtlinkLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (StaffExtlink, bool) {
		id, _ := get("id")
		return StaffExtlink{ID: id, Link: getInt(get, "link")}, true
	})
}

func newProducerRelationLoader(tx *gorm.DB, _ time.Time) tableLoader {
	return newLoader(tx, func(get getter) (ProducerRelation, bool) {
		id, _ := get("id")
		pid, _ := get("pid")
		relation, _ := get("relation")
		if id == "" || pid == "" || relation == "" {
			return ProducerRelation{}, false
		}
		return ProducerRelation{ID: id, PID: pid, Relation: relation}, true
	})
}
