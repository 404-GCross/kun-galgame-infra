package srcvndb

// Link-graph staging models (wave 186): the remaining extlink junction tables
// (vn / producers / staff — releases_extlinks landed in wave 167) and the
// producer-relation edges that power the label relation graph.

// VNExtlink mirrors one db/vn_extlinks row: vn "v1" carries extlink #15.
type VNExtlink struct {
	ID   string `gorm:"primaryKey;column:id" json:"id"`           // "v1"
	Link int    `gorm:"primaryKey;column:link;index" json:"link"` // extlinks.id
}

func (VNExtlink) TableName() string { return "src_vndb.vn_extlinks" }

// ProducerExtlink mirrors one db/producers_extlinks row: producer "p1" carries
// extlink #15.
type ProducerExtlink struct {
	ID   string `gorm:"primaryKey;column:id" json:"id"`           // "p1"
	Link int    `gorm:"primaryKey;column:link;index" json:"link"` // extlinks.id
}

func (ProducerExtlink) TableName() string { return "src_vndb.producers_extlinks" }

// StaffExtlink mirrors one db/staff_extlinks row: staff "s1" carries extlink
// #15.
type StaffExtlink struct {
	ID   string `gorm:"primaryKey;column:id" json:"id"`           // "s1"
	Link int    `gorm:"primaryKey;column:link;index" json:"link"` // extlinks.id
}

func (StaffExtlink) TableName() string { return "src_vndb.staff_extlinks" }

// ProducerRelation mirrors one db/producers_relations row. VNDB stores the
// graph mirrored — every edge appears once per direction with the inverse
// relation code (par/sub, imp/ipa, spa/ori, new/old), so a consumer never has
// to invert.
type ProducerRelation struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"`   // "p1"
	PID      string `gorm:"primaryKey;column:pid" json:"pid"` // "p2"
	Relation string `gorm:"primaryKey;column:relation" json:"relation"`
}

func (ProducerRelation) TableName() string { return "src_vndb.producers_relations" }
