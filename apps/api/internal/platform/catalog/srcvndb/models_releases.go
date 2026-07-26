package srcvndb

// Releases-family staging models (step 72, S2 feedstock): releases plus its
// dump child tables (releases_vn / releases_producers / releases_platforms /
// releases_titles — in this dump generation a release's languages live on
// releases_titles, one row per language) and the producers vocabulary.
//
// Nullable pointers appear where the dump distinguishes NULL from a meaningful
// zero: minage (0 = all-ages rating, NULL = unknown), the ani_* detail fields
// (NULL = not surveyed) and uncensored (NULL = not applicable).

// Release mirrors one db/releases row.
type Release struct {
	ID          string `gorm:"primaryKey" json:"id"`             // "r1"
	GTIN        int64  `gorm:"column:gtin;not null" json:"gtin"` // JAN/EAN/UPC, 0 = none
	OLang       string `gorm:"column:olang;not null" json:"olang"`
	Released    int    `gorm:"not null" json:"released"`             // yyyymmdd, 99999999 = TBA
	Voiced      int16  `gorm:"not null" json:"voiced"`               // 0-4
	ResoX       int16  `gorm:"column:reso_x;not null" json:"reso_x"` // 0 = unknown/non-standard
	ResoY       int16  `gorm:"column:reso_y;not null" json:"reso_y"`
	MinAge      *int16 `gorm:"column:minage" json:"minage"`                // age rating; 0 = all ages, NULL = unknown
	AniStory    int16  `gorm:"column:ani_story;not null" json:"ani_story"` // legacy 0-4 scale
	AniEro      int16  `gorm:"column:ani_ero;not null" json:"ani_ero"`
	AniStorySp  *int16 `gorm:"column:ani_story_sp" json:"ani_story_sp"` // detail flags, NULL = not surveyed
	AniStoryCg  *int16 `gorm:"column:ani_story_cg" json:"ani_story_cg"`
	AniCutscene *int16 `gorm:"column:ani_cutscene" json:"ani_cutscene"`
	AniEroSp    *int16 `gorm:"column:ani_ero_sp" json:"ani_ero_sp"`
	AniEroCg    *int16 `gorm:"column:ani_ero_cg" json:"ani_ero_cg"`
	AniBg       *bool  `gorm:"column:ani_bg" json:"ani_bg"`
	AniFace     *bool  `gorm:"column:ani_face" json:"ani_face"`
	HasEro      bool   `gorm:"column:has_ero;not null" json:"has_ero"`
	Patch       bool   `gorm:"not null" json:"patch"`
	Freeware    bool   `gorm:"not null" json:"freeware"`
	Uncensored  *bool  `gorm:"column:uncensored" json:"uncensored"` // NULL = not applicable
	Official    bool   `gorm:"not null" json:"official"`
	Catalog     string `gorm:"not null" json:"catalog"` // catalog number
	Notes       string `gorm:"not null" json:"notes"`
	Engine      string `gorm:"not null" json:"engine"`
}

func (Release) TableName() string { return "src_vndb.releases" }

// ReleaseVN mirrors one db/releases_vn row — which VNs a release carries and
// how completely (rtype: complete/partial/trial).
type ReleaseVN struct {
	ID    string `gorm:"primaryKey;column:id" json:"id"`         // "r1"
	VID   string `gorm:"primaryKey;column:vid;index" json:"vid"` // "v1"
	RType string `gorm:"column:rtype;not null" json:"rtype"`
}

func (ReleaseVN) TableName() string { return "src_vndb.releases_vn" }

// ReleaseProducer mirrors one db/releases_producers row — a producer's
// involvement in a release (developer and/or publisher).
type ReleaseProducer struct {
	ID        string `gorm:"primaryKey;column:id" json:"id"`         // "r1"
	PID       string `gorm:"primaryKey;column:pid;index" json:"pid"` // "p1"
	Developer bool   `gorm:"not null" json:"developer"`
	Publisher bool   `gorm:"not null" json:"publisher"`
}

func (ReleaseProducer) TableName() string { return "src_vndb.releases_producers" }

// ReleasePlatform mirrors one db/releases_platforms row (platform: win/lin/
// mac/and/ios/psv/swi/...).
type ReleasePlatform struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"` // "r1"
	Platform string `gorm:"primaryKey;column:platform" json:"platform"`
}

func (ReleasePlatform) TableName() string { return "src_vndb.releases_platforms" }

// ReleaseTitle mirrors one db/releases_titles row — the release's title in ONE
// language; the set of rows is also the release's language list. `MTL` marks a
// machine translation. `Title` is the native/script form (may be "" when only
// the latin form is known), `Latin` the romanization.
type ReleaseTitle struct {
	ID    string `gorm:"primaryKey;column:id" json:"id"` // "r1"
	Lang  string `gorm:"primaryKey;column:lang" json:"lang"`
	MTL   bool   `gorm:"column:mtl;not null" json:"mtl"`
	Title string `gorm:"not null" json:"title"`
	Latin string `gorm:"not null" json:"latin"`
}

func (ReleaseTitle) TableName() string { return "src_vndb.releases_titles" }

// Producer mirrors one db/producers row — a company (co), individual (in) or
// amateur group (ng). E2 feedstock.
type Producer struct {
	ID          string `gorm:"primaryKey" json:"id"` // "p1"
	Type        string `gorm:"not null" json:"type"` // co/in/ng
	Lang        string `gorm:"not null" json:"lang"`
	Name        string `gorm:"not null" json:"name"`  // native/script form
	Latin       string `gorm:"not null" json:"latin"` // romanization or ""
	Alias       string `gorm:"not null" json:"alias"` // newline-separated alternative names
	Description string `gorm:"not null" json:"description"`
}

func (Producer) TableName() string { return "src_vndb.producers" }
