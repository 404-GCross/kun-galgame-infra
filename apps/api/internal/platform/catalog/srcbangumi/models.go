package srcbangumi

import (
	"time"

	"gorm.io/datatypes"
)

const ParserVersion = "inhouse-v1"

type Subject struct {
	ID            int64          `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Type          int            `gorm:"not null" json:"type"`
	Name          string         `gorm:"not null" json:"name"`
	NameNorm      string         `gorm:"->;-:migration" json:"name_norm"`
	NameCN        string         `gorm:"column:name_cn;not null" json:"name_cn"`
	NameCNNorm    string         `gorm:"column:name_cn_norm;->;-:migration" json:"name_cn_norm"`
	InfoboxRaw    string         `gorm:"not null" json:"infobox_raw"`
	InfoboxParsed datatypes.JSON `gorm:"type:jsonb" json:"infobox_parsed"`
	ParseError    string         `gorm:"not null" json:"parse_error"`
	Platform      int            `gorm:"not null" json:"platform"`
	Summary       string         `gorm:"not null" json:"summary"`
	NSFW          bool           `gorm:"not null" json:"nsfw"`
	Date          string         `gorm:"not null" json:"date"`
	Series        bool           `gorm:"not null" json:"series"`
	Score         float64        `gorm:"not null" json:"score"`
	Rank          int            `gorm:"not null" json:"rank"`
	Tags          datatypes.JSON `gorm:"type:jsonb" json:"tags"`
	MetaTags      datatypes.JSON `gorm:"type:jsonb" json:"meta_tags"`
	ScoreDetails  datatypes.JSON `gorm:"type:jsonb" json:"score_details"`
	Favorite      datatypes.JSON `gorm:"type:jsonb" json:"favorite"`
	ParserVersion string         `gorm:"not null" json:"parser_version"`
	IngestedAt    time.Time      `gorm:"not null" json:"ingested_at"`
}

func (Subject) TableName() string { return "src_bangumi.subject" }

type Person struct {
	ID            int64          `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Name          string         `gorm:"not null" json:"name"`
	NameNorm      string         `gorm:"->;-:migration" json:"name_norm"`
	Type          int            `gorm:"not null" json:"type"`
	Career        datatypes.JSON `gorm:"type:jsonb" json:"career"`
	InfoboxRaw    string         `gorm:"not null" json:"infobox_raw"`
	InfoboxParsed datatypes.JSON `gorm:"type:jsonb" json:"infobox_parsed"`
	ParseError    string         `gorm:"not null" json:"parse_error"`
	Summary       string         `gorm:"not null" json:"summary"`
	Comments      int            `gorm:"not null" json:"comments"`
	Collects      int            `gorm:"not null" json:"collects"`
	ParserVersion string         `gorm:"not null" json:"parser_version"`
	IngestedAt    time.Time      `gorm:"not null" json:"ingested_at"`
}

func (Person) TableName() string { return "src_bangumi.person" }

type Character struct {
	ID            int64          `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Role          int            `gorm:"not null" json:"role"`
	Name          string         `gorm:"not null" json:"name"`
	NameNorm      string         `gorm:"->;-:migration" json:"name_norm"`
	InfoboxRaw    string         `gorm:"not null" json:"infobox_raw"`
	InfoboxParsed datatypes.JSON `gorm:"type:jsonb" json:"infobox_parsed"`
	ParseError    string         `gorm:"not null" json:"parse_error"`
	Summary       string         `gorm:"not null" json:"summary"`
	Comments      int            `gorm:"not null" json:"comments"`
	Collects      int            `gorm:"not null" json:"collects"`
	ParserVersion string         `gorm:"not null" json:"parser_version"`
	IngestedAt    time.Time      `gorm:"not null" json:"ingested_at"`
}

func (Character) TableName() string { return "src_bangumi.character" }

type SubjectRelation struct {
	SubjectID        int64 `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	RelationType     int   `gorm:"primaryKey;autoIncrement:false" json:"relation_type"`
	RelatedSubjectID int64 `gorm:"primaryKey;autoIncrement:false" json:"related_subject_id"`
	Order            int   `gorm:"primaryKey;autoIncrement:false;column:item_order" json:"order"`
}

func (SubjectRelation) TableName() string { return "src_bangumi.subject_relation" }

type SubjectPerson struct {
	PersonID  int64  `gorm:"primaryKey;autoIncrement:false" json:"person_id"`
	SubjectID int64  `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	Position  int    `gorm:"primaryKey;autoIncrement:false" json:"position"`
	AppearEps string `gorm:"not null" json:"appear_eps"`
}

func (SubjectPerson) TableName() string { return "src_bangumi.subject_person" }

type SubjectCharacter struct {
	CharacterID int64 `gorm:"primaryKey;autoIncrement:false" json:"character_id"`
	SubjectID   int64 `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	Type        int   `gorm:"not null" json:"type"`
	Order       int   `gorm:"not null;column:item_order" json:"order"`
}

func (SubjectCharacter) TableName() string { return "src_bangumi.subject_character" }

type PersonCharacter struct {
	PersonID    int64  `gorm:"primaryKey;autoIncrement:false" json:"person_id"`
	SubjectID   int64  `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	CharacterID int64  `gorm:"primaryKey;autoIncrement:false" json:"character_id"`
	Type        int    `gorm:"not null" json:"type"`
	Summary     string `gorm:"not null" json:"summary"`
}

func (PersonCharacter) TableName() string { return "src_bangumi.person_character" }

type IngestRun struct {
	ID            int64          `gorm:"primaryKey;autoIncrement:false;type:bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY;default:(-)" json:"id"`
	DumpLabel     string         `gorm:"not null" json:"dump_label"`
	ParserVersion string         `gorm:"not null" json:"parser_version"`
	Counts        datatypes.JSON `gorm:"type:jsonb" json:"counts"`
	ParseErrors   datatypes.JSON `gorm:"type:jsonb" json:"parse_errors"`
	DurationMS    int64          `gorm:"not null" json:"duration_ms"`
	StartedAt     time.Time      `gorm:"not null" json:"started_at"`
}

func (IngestRun) TableName() string { return "src_bangumi.ingest_run" }
