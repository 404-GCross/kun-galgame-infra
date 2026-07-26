// Package srcvndb owns the Silver layer of the VNDB source: the src_vndb
// Postgres schema inside kun_catalog, populated deterministically from the
// VNDB database dump (vndb.org/d14, PostgreSQL COPY format) by
// cmd/ingest-vndb.
//
// Pipeline discipline (mirrors srcbangumi): the dump files are Bronze (never
// cleaned); Silver rows are VNDB data VERBATIM. VNDB ids keep their letter
// prefix ("c1" / "v38" / "ch175652") exactly as the dump carries them — the
// same convention catalog already uses for VNDB WORK anchors ("v10"), so
// chars_vns.vid joins catalog_external_ref.external_id with no transform. Every
// judgment call (character creation, roster kind mapping, cross-source
// attach) belongs to the Gold side (the importer).
//
// SCHEMA NOTE (survey finding, step 47): the current VNDB dump (the multi-
// language rewrite) does NOT carry character names on the chars table. Names
// live in a separate per-language table (chars_names: id, lang, name, latin) —
// `name` is the native/script form, `latin` the romanization. The loader maps
// columns by NAME from each file's .header, so a column reorder in a future
// dump does not break the load.
//
// NULL DISCIPLINE (step 72): text columns flatten the COPY \N sentinel to ""
// and numerics to 0 (the package's original convention) EXCEPT where the dump
// distinguishes NULL from a meaningful zero value — those columns are staged
// as nullable pointers (e.g. releases.minage: 0 = all-ages rating, NULL =
// unknown; vn_staff.eid: edition 0 exists, NULL = base edition).
//
// The schema is FULLY REBUILDABLE staging: the ingest tool owns it (CREATE
// SCHEMA + AutoMigrate at startup, whole-table replacement per run) and it is
// deliberately outside cmd/migrate-catalog's Gold migration order.
package srcvndb

import "time"

// Char mirrors ALL columns of one db/chars row (full re-stage, step 72 — the
// body/birthday columns feed the C2 character-attribute wave). Ids keep the
// VNDB "c" prefix verbatim. Names are NOT here (see the package doc) — they
// are in CharName. `Main` is the VNDB instance_of base character id (variant
// escape hatch). Weight/Age are pointers: the dump distinguishes NULL
// (unknown) from a stated 0 there, while the s_* measurements, birthday and
// height use 0 = unset in the dump itself.
type Char struct {
	// No default tags on any loaded column: the GORM default tag drops the Go
	// value in this batch-insert path (the default-tag zero-value trap), so
	// every column is plain not-null and the loader writes the value verbatim.
	ID          string    `gorm:"primaryKey" json:"id"`                       // "c1"
	Image       string    `gorm:"not null" json:"image"`                      // "ch175652" or "" (no portrait)
	BloodT      string    `gorm:"column:bloodt;not null" json:"bloodt"`       // a/b/ab/o/unknown
	CupSize     string    `gorm:"column:cup_size;not null" json:"cup_size"`   // ""/aaa/.../z
	Sex         string    `gorm:"not null" json:"sex"`                        // m/f/b/n/"" (apparent sex)
	SpoilSex    string    `gorm:"column:spoil_sex;not null" json:"spoil_sex"` // real sex (spoiler) or ""
	Gender      string    `gorm:"not null" json:"gender"`
	SpoilGender string    `gorm:"column:spoil_gender;not null" json:"spoil_gender"`
	Main        string    `gorm:"not null" json:"main"` // instance_of base char id ("c16") or ""
	MainSpoil   int16     `gorm:"not null" json:"main_spoil"`
	SBust       int16     `gorm:"column:s_bust;not null" json:"s_bust"`   // cm, 0 = unset
	SWaist      int16     `gorm:"column:s_waist;not null" json:"s_waist"` // cm, 0 = unset
	SHip        int16     `gorm:"column:s_hip;not null" json:"s_hip"`     // cm, 0 = unset
	Birthday    int16     `gorm:"not null" json:"birthday"`               // mmdd (920 = Sep 20), 0 = unset
	Height      int16     `gorm:"not null" json:"height"`                 // cm, 0 = unset
	Weight      *int16    `gorm:"column:weight" json:"weight"`            // kg, NULL = unknown
	Age         *int16    `gorm:"column:age" json:"age"`                  // years, NULL = unknown
	Description string    `gorm:"not null" json:"description"`
	IngestedAt  time.Time `gorm:"not null" json:"ingested_at"`
}

func (Char) TableName() string { return "src_vndb.chars" }

// CharName mirrors one db/chars_names row — a character's name in ONE language.
// (id, lang) is unique in the dump. `Name` is the native/script form; `Latin`
// is the romanization (empty when the name is already latin).
type CharName struct {
	ID    string `gorm:"primaryKey;column:id" json:"id"`     // char id "c1"
	Lang  string `gorm:"primaryKey;column:lang" json:"lang"` // "ja","en","zh-Hans",...
	Name  string `gorm:"not null" json:"name"`
	Latin string `gorm:"not null" json:"latin"`
}

func (CharName) TableName() string { return "src_vndb.chars_names" }

// CharVN mirrors one db/chars_vns row — a character's appearance in a VN
// (rid narrows it to a release; "" = the work-level row). No natural key is
// clean (rid is often absent), so a surrogate identity PK carries the table.
type CharVN struct {
	Seq int64 `gorm:"primaryKey;autoIncrement:false;type:bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY;default:(-)" json:"-"`
	// Explicit column tags: GORM would snake-case VID/RID to v_id/r_id (the
	// acronym column-naming trap) — pin them to the dump's names.
	ID    string `gorm:"column:id;not null;index" json:"id"`   // char "c1"
	VID   string `gorm:"column:vid;not null;index" json:"vid"` // vn "v38"
	RID   string `gorm:"column:rid;not null" json:"rid"`       // release "r20624" or ""
	Role  string `gorm:"not null" json:"role"`                 // main/primary/side/appears
	Spoil int16  `gorm:"not null" json:"spoil"`
}

func (CharVN) TableName() string { return "src_vndb.chars_vns" }

// VN mirrors the identity-relevant columns of one db/vn row — a visual novel.
// Ids keep the VNDB "v" prefix verbatim ("v1"), so vn.id joins
// catalog_external_ref.external_id (source=vndb) with no transform. Only the
// subset the media-aggregation waves need is staged: `description` (the English
// blurb, BBCode-ish — VNDB writes descriptions in English regardless of the
// VN's own original language) and `olang` (the VN's ORIGINAL language, NOT the
// blurb's — kept for future use, never the intro's language). `image`/`c_image`
// (the cover ids "cv123") are staged now for the step-53 cover wave.
type VN struct {
	// No default tags on any loaded column (the default-tag zero-value trap in
	// this batch-insert path): every column is plain not-null, verbatim.
	ID          string `gorm:"primaryKey" json:"id"`                   // "v1"
	OLang       string `gorm:"column:olang;not null" json:"olang"`     // VN original language ("ja")
	Image       string `gorm:"not null" json:"image"`                  // cover image id "cv20339" or ""
	CImage      string `gorm:"column:c_image;not null" json:"c_image"` // reversible-flag cover "cv77859" or ""
	Description string `gorm:"not null" json:"description"`            // English blurb, BBCode-ish
	// Step-91 full-width restage: the remaining dump columns, verbatim. The
	// c_* columns are VNDB's cached vote aggregates (NULL = no votes — staged
	// as pointers, never fake zeros); length is the hand-entered 1-5 bucket
	// (0 = unset; the c_length fallback, NOT projected this wave); alias is
	// the newline-separated alias list (step-94 feedstock).
	CVotecount int       `gorm:"column:c_votecount;not null" json:"c_votecount"`
	CRating    *float64  `gorm:"column:c_rating;type:numeric" json:"c_rating"`
	CAverage   *float64  `gorm:"column:c_average;type:numeric" json:"c_average"`
	CLength    *int      `gorm:"column:c_length" json:"c_length"` // median playtime MINUTES
	CLengthnum int       `gorm:"column:c_lengthnum;not null" json:"c_lengthnum"`
	Length     int16     `gorm:"not null" json:"length"`
	Devstatus  int16     `gorm:"not null" json:"devstatus"` // 0=finished 1=in dev 2=cancelled
	Alias      string    `gorm:"not null" json:"alias"`
	IngestedAt time.Time `gorm:"not null" json:"ingested_at"`
}

func (VN) TableName() string { return "src_vndb.vn" }

// VNRelation mirrors one db/vn_relations row — a directed vn↔vn relation edge
// (REL1 feedstock). `Relation` is the VNDB relation kind (seq/preq/set/alt/
// char/side/par/ser/fan/orig); `Official` marks officially-recognized
// relations. (id, vid) is the dump's natural key.
type VNRelation struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"`   // "v1"
	VID      string `gorm:"primaryKey;column:vid" json:"vid"` // related vn "v9650"
	Relation string `gorm:"not null" json:"relation"`
	Official bool   `gorm:"not null" json:"official"`
}

func (VNRelation) TableName() string { return "src_vndb.vn_relations" }

// Image mirrors one db/images row — ONLY the "ch" (character portrait) rows are
// staged (the loader drops sf/cv). c_sexual_avg / c_violence_avg are the
// moderation flags on a 0-200 scale (average vote * 100; 0=safe, 100=avg 1.0,
// 200=explicit) — survey finding, step 47.
type Image struct {
	ID             string `gorm:"primaryKey" json:"id"` // "ch12"
	Width          int    `gorm:"not null" json:"width"`
	Height         int    `gorm:"not null" json:"height"`
	VoteCount      int    `gorm:"column:c_votecount;not null" json:"c_votecount"`
	SexualAvg      int16  `gorm:"column:c_sexual_avg;not null" json:"c_sexual_avg"`
	SexualStddev   int16  `gorm:"column:c_sexual_stddev;not null" json:"c_sexual_stddev"`
	ViolenceAvg    int16  `gorm:"column:c_violence_avg;not null" json:"c_violence_avg"`
	ViolenceStddev int16  `gorm:"column:c_violence_stddev;not null" json:"c_violence_stddev"`
	Weight         int16  `gorm:"column:c_weight;not null" json:"c_weight"`
}

func (Image) TableName() string { return "src_vndb.images" }

// PortraitBackfill is the step-47 output CONSUMED BY step 48: the set of
// in-gate VNDB characters (those that acquired a catalog entity) whose portrait
// passes the moderation threshold. It is NOT loaded from the dump — the roster
// VNDB wave (importer) rebuilds it on --apply by joining chars + images to the
// new source-2 character anchors. Step 48 rsyncs ch/<image_id> and sets the
// catalog character's image_hash; this step touches neither bytes nor
// image_hash. One portrait per catalog character (PK).
type PortraitBackfill struct {
	CatalogCharacterID int64  `gorm:"primaryKey;autoIncrement:false" json:"catalog_character_id"`
	VNDBCharID         string `gorm:"not null" json:"vndb_char_id"` // "c1"
	ImageID            string `gorm:"not null" json:"image_id"`     // "ch175652"
	SexualAvg          int16  `gorm:"not null" json:"sexual_avg"`
	ViolenceAvg        int16  `gorm:"not null" json:"violence_avg"`
}

func (PortraitBackfill) TableName() string { return "src_vndb.portrait_backfill" }

// IngestRun records one ingestion (per-table counts, timing) — the audit trail
// of the replace-batch runs (mirrors srcbangumi.IngestRun).
type IngestRun struct {
	ID         int64     `gorm:"primaryKey;autoIncrement:false;type:bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY;default:(-)" json:"id"`
	DumpLabel  string    `gorm:"not null" json:"dump_label"`
	Counts     string    `gorm:"not null;type:jsonb;default:'{}'" json:"counts"`
	DurationMS int64     `gorm:"not null" json:"duration_ms"`
	StartedAt  time.Time `gorm:"not null" json:"started_at"`
}

func (IngestRun) TableName() string { return "src_vndb.ingest_run" }
