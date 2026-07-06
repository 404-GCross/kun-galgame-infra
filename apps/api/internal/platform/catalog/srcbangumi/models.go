// Package srcbangumi owns the Silver layer of the Bangumi source: the
// src_bangumi Postgres schema inside kun_catalog, populated deterministically
// from the Bangumi Archive dump by cmd/ingest-bangumi.
//
// Pipeline discipline (doc 17 §2, invariant 20): Bronze (the dump files) is
// never cleaned; Silver rows are Bangumi data VERBATIM plus exactly two
// deterministic transforms — the in-house infobox parse (failures exposed in
// parse_error with infobox_parsed NULL, never half-parsed data) and NFKC
// name-normalization generated columns. Every judgment call (person type
// routing, nsfw mapping, reconciliation) belongs to the Gold side.
//
// The schema is FULLY REBUILDABLE staging: the ingest tool owns it (CREATE
// SCHEMA + AutoMigrate at startup, whole-table replacement per run) and it
// is deliberately outside cmd/migrate-catalog's Gold migration order.
package srcbangumi

import (
	"time"

	"gorm.io/datatypes"
)

// ParserVersion stamps every ingested row with the infobox parser that
// produced infobox_parsed. Bump when parser behavior changes on purpose.
const ParserVersion = "inhouse-v1"

// Subject mirrors one subject.jsonlines row (books/anime/music/games/real;
// type stays raw — 1/2/3/4/6).
type Subject struct {
	ID   int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Type int    `gorm:"not null" json:"type"`
	Name string `gorm:"not null" json:"name"`
	// NameNorm / NameCNNorm are NFKC-folded generated columns (raw SQL,
	// tool-owned) — they absorb the U+3000/U+00A0 the parser deliberately
	// leaves in values (reference-parser quirk, step 08).
	NameNorm   string `gorm:"->;-:migration" json:"name_norm"`
	NameCN     string `gorm:"column:name_cn;not null" json:"name_cn"`
	NameCNNorm string `gorm:"column:name_cn_norm;->;-:migration" json:"name_cn_norm"`
	// The infobox trio: raw wiki text verbatim, the in-house parse as JSON,
	// and the parse error ('' = parsed fine). Half-parsed data never lands:
	// on error InfoboxParsed is NULL.
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

// Person mirrors one person.jsonlines row. Type stays raw (1=individual
// 2=company 3=group) — the Person/Org/Label routing is a Gold decision.
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

// Character mirrors one character.jsonlines row (role 1=character 2=mecha
// 3=organization, raw).
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

// SubjectRelation mirrors subject-relations.jsonlines. The 2026-06-30 dump
// carries exactly ONE fully identical duplicate row (subject 469753,
// relation 4007 — same order value), so no natural key is unique in the raw
// file; the four-column PK plus insert-time dedup (ON CONFLICT DO NOTHING)
// is the deterministic answer, and the table lands at wc-1 rows.
type SubjectRelation struct {
	SubjectID        int64 `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	RelationType     int   `gorm:"primaryKey;autoIncrement:false" json:"relation_type"`
	RelatedSubjectID int64 `gorm:"primaryKey;autoIncrement:false" json:"related_subject_id"`
	Order            int   `gorm:"primaryKey;autoIncrement:false;column:item_order" json:"order"`
}

func (SubjectRelation) TableName() string { return "src_bangumi.subject_relation" }

// SubjectPerson mirrors subject-persons.jsonlines ((person, subject,
// position) is unique in the dump).
type SubjectPerson struct {
	PersonID  int64  `gorm:"primaryKey;autoIncrement:false" json:"person_id"`
	SubjectID int64  `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	Position  int    `gorm:"primaryKey;autoIncrement:false" json:"position"`
	AppearEps string `gorm:"not null" json:"appear_eps"`
}

func (SubjectPerson) TableName() string { return "src_bangumi.subject_person" }

// SubjectCharacter mirrors subject-characters.jsonlines ((character,
// subject) is unique in the dump).
type SubjectCharacter struct {
	CharacterID int64 `gorm:"primaryKey;autoIncrement:false" json:"character_id"`
	SubjectID   int64 `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	Type        int   `gorm:"not null" json:"type"`
	Order       int   `gorm:"not null;column:item_order" json:"order"`
}

func (SubjectCharacter) TableName() string { return "src_bangumi.subject_character" }

// PersonCharacter mirrors person-characters.jsonlines — the VA links
// ((person, subject, character) is unique in the dump).
type PersonCharacter struct {
	PersonID    int64  `gorm:"primaryKey;autoIncrement:false" json:"person_id"`
	SubjectID   int64  `gorm:"primaryKey;autoIncrement:false" json:"subject_id"`
	CharacterID int64  `gorm:"primaryKey;autoIncrement:false" json:"character_id"`
	Type        int    `gorm:"not null" json:"type"`
	Summary     string `gorm:"not null" json:"summary"`
}

func (PersonCharacter) TableName() string { return "src_bangumi.person_character" }

// IngestRun records one ingestion execution (per-table row counts,
// parse_error counts, timing) — the audit trail of the replace-batch runs.
type IngestRun struct {
	ID int64 `gorm:"primaryKey;autoIncrement:false;type:bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY;default:(-)" json:"id"`
	// DumpLabel identifies the source dump (directory basename).
	DumpLabel     string         `gorm:"not null" json:"dump_label"`
	ParserVersion string         `gorm:"not null" json:"parser_version"`
	Counts        datatypes.JSON `gorm:"type:jsonb" json:"counts"`
	ParseErrors   datatypes.JSON `gorm:"type:jsonb" json:"parse_errors"`
	DurationMS    int64          `gorm:"not null" json:"duration_ms"`
	StartedAt     time.Time      `gorm:"not null" json:"started_at"`
}

func (IngestRun) TableName() string { return "src_bangumi.ingest_run" }
