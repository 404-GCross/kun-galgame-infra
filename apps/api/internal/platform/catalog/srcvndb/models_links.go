package srcvndb

type VNExtlink struct {
	ID   string `gorm:"primaryKey;column:id" json:"id"`
	Link int    `gorm:"primaryKey;column:link;index" json:"link"`
}

func (VNExtlink) TableName() string { return "src_vndb.vn_extlinks" }

type ProducerExtlink struct {
	ID   string `gorm:"primaryKey;column:id" json:"id"`
	Link int    `gorm:"primaryKey;column:link;index" json:"link"`
}

func (ProducerExtlink) TableName() string { return "src_vndb.producers_extlinks" }

type StaffExtlink struct {
	ID   string `gorm:"primaryKey;column:id" json:"id"`
	Link int    `gorm:"primaryKey;column:link;index" json:"link"`
}

func (StaffExtlink) TableName() string { return "src_vndb.staff_extlinks" }

type ProducerRelation struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"`
	PID      string `gorm:"primaryKey;column:pid" json:"pid"`
	Relation string `gorm:"primaryKey;column:relation" json:"relation"`
}

func (ProducerRelation) TableName() string { return "src_vndb.producers_relations" }
