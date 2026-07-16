package model

// Galgame represents a visual novel entry
type Galgame struct {
	ID int `gorm:"primaryKey;autoIncrement" json:"id"`
	// vndb_id is optional — user-submitted originals (no VNDB entry) may
	// leave this empty. Uniqueness is enforced by a partial unique index
	// created in migrate-catalog: UNIQUE on vndb_id WHERE vndb_id <> ''.
	// (GORM AutoMigrate cannot express partial unique, so raw SQL.)
	//
	// Format is constrained at the DB level (chk_galgame_vndb_id_format in
	// migrate-catalog): vndb_id must be '' or a canonical VNDB visual-novel
	// id (`^v[0-9]+$`). This is the path-independent backstop behind the
	// service-layer vndbIDRegex check — a *release* id (`r123`) or a
	// slash-prefixed id (`/v123`) is a different value to the exact-match
	// unique index, so without this constraint such variants created
	// duplicate galgames for the same VN.
	VNDBID    string `gorm:"column:vndb_id;size:10;not null;default:'';index" json:"vndb_id"`
	BangumiID *int   `gorm:"column:bid;uniqueIndex" json:"bid,omitempty"`
	// ReleaseDate is the (best-known) galgame release date. nil = unknown.
	// Stored as a SQL `date` column (no time-of-day, no time zone).
	// ReleaseDateTBA is independent: a game can be "scheduled, exact date
	// TBA" (date=nil, tba=true) or "announced for 2026 H2, no precise date".
	//
	// Type is the custom Date (see date.go), NOT *time.Time — so JSON
	// serializes as "YYYY-MM-DD" (the documented + validated contract),
	// not RFC3339 "2019-08-16T00:00:00Z". Date's Scanner/Valuer + the
	// gorm `type:date` tag keep it a date-only PG column.
	ReleaseDate    *Date `gorm:"column:release_date;type:date;index" json:"release_date"`
	ReleaseDateTBA bool  `gorm:"column:release_date_tba;not null;default:false" json:"release_date_tba"`
	// ReleasePrecision records how precise ReleaseDate is (day/month/year/tba/
	// unknown) — the single source of truth for date precision. ReleaseDate is
	// normalized (day-unknown → 1st of month, month-unknown → Jan 1), so a
	// calendar must read this flag to place "year only" games out of specific
	// months and to render "2026-06 (day TBD)" vs "2026-06-15". ReleaseDateTBA
	// is kept in sync (= ReleasePrecision == "tba") for existing readers; it is
	// scheduled to be retired once all readers move to ReleasePrecision.
	// See docs/galgame_wiki/06-release-calendar-design.md §2.
	ReleasePrecision string `gorm:"column:release_precision;size:10;not null;default:'unknown'" json:"release_precision"`
	NameEnUS         string `gorm:"column:name_en_us;size:1000;default:''" json:"name_en_us"`
	NameJaJP         string `gorm:"column:name_ja_jp;size:1000;default:''" json:"name_ja_jp"`
	NameZhCN         string `gorm:"column:name_zh_cn;size:1000;default:''" json:"name_zh_cn"`
	NameZhTW         string `gorm:"column:name_zh_tw;size:1000;default:''" json:"name_zh_tw"`
	// Banner is the legacy URL string. Kept as permanent fallback during
	// migration period and as the original record for old galgames. The
	// migration cmd `migrate-galgame-banners-to-image-service` reads from
	// here, uploads to image_service, and inserts a galgame_cover row
	// with sort_order=0.
	//
	// (The dedicated `banner_image_hash` column was retired in PR5 —
	// galgame_cover is now the single source of truth for image-service
	// references; see docs/galgame_wiki/99-final-upgrade-plan.md §5.5.)
	Banner string `gorm:"size:233;default:''" json:"banner"`

	// Migration bookkeeping for the one-shot migration script. After
	// migration succeeds, status=1; failures bump attempts and after 3
	// fails set status=2 (permanent failure, skip).
	BannerMigrationStatus   int16 `gorm:"not null;default:0" json:"-"` // 0/1/2
	BannerMigrationAttempts int16 `gorm:"not null;default:0" json:"-"`

	IntroEnUS          string    `gorm:"column:intro_en_us;type:text;default:''" json:"intro_en_us"`
	IntroJaJP          string    `gorm:"column:intro_ja_jp;type:text;default:''" json:"intro_ja_jp"`
	IntroZhCN          string    `gorm:"column:intro_zh_cn;type:text;default:''" json:"intro_zh_cn"`
	IntroZhTW          string    `gorm:"column:intro_zh_tw;type:text;default:''" json:"intro_zh_tw"`
	ContentLimit       string    `gorm:"column:content_limit;size:10;default:'sfw'" json:"content_limit"`
	Status             int       `gorm:"default:0" json:"status"`
	View               int       `gorm:"default:0" json:"view"`
	ResourceUpdateTime Timestamp `gorm:"column:resource_update_time;autoCreateTime" json:"resource_update_time"`
	OriginalLanguage   string    `gorm:"column:original_language;default:'ja-jp'" json:"original_language"`
	AgeLimit           string    `gorm:"column:age_limit;size:10;default:'r18'" json:"age_limit"`
	UserID             int       `gorm:"column:user_id;not null;index" json:"user_id"`
	SeriesID           *int      `gorm:"column:series_id;index" json:"series_id"`
	Created            Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated            Timestamp `gorm:"autoUpdateTime" json:"updated"`

	// CatalogWorkID is the cross-face pointer to this galgame's catalog_work id
	// (the platform-domain registry identity). Nullable, no default: NULL until
	// the work has been claimed. It is NOT maintained on the write/create path
	// (the wiki has no live catalog claim) — reconcile-galgame-works writes it
	// back when it registers/reconciles the claim, so a stale-but-eventually-
	// consistent pointer is the contract. Consumers (kungal step 36/37) use it
	// to reach catalog credits/people for this title.
	CatalogWorkID *int64 `gorm:"column:catalog_work_id" json:"catalog_work_id,omitempty"`

	// Relations (galgame service owns these)
	Series      *GalgameSeries            `gorm:"foreignKey:SeriesID" json:"series,omitempty"`
	Alias       []GalgameAlias            `gorm:"foreignKey:GalgameID" json:"alias,omitempty"`
	Link        []GalgameLink             `gorm:"foreignKey:GalgameID" json:"link,omitempty"`
	PR          []GalgamePR               `gorm:"foreignKey:GalgameID" json:"pr,omitempty"`
	History     []GalgameHistory          `gorm:"foreignKey:GalgameID" json:"history,omitempty"`
	Contributor []GalgameContributor      `gorm:"foreignKey:GalgameID" json:"contributor,omitempty"`
	Official    []GalgameOfficialRelation `gorm:"foreignKey:GalgameID" json:"official,omitempty"`
	Engine      []GalgameEngineRelation   `gorm:"foreignKey:GalgameID" json:"engine,omitempty"`
	Tag         []GalgameTagRelation      `gorm:"foreignKey:GalgameID" json:"tag,omitempty"`
	Cover       []GalgameCover            `gorm:"foreignKey:GalgameID" json:"covers,omitempty"`
	Screenshot  []GalgameScreenshot       `gorm:"foreignKey:GalgameID" json:"screenshots,omitempty"`

	// EffectiveBannerHash is a derived, read-only field: the image_hash of
	// the cover with sort_order=0 (= the "pinned" banner). Populated by
	// the service layer after preload. Zero-value (nil pointer) when no
	// cover exists. Not stored — `gorm:"-"` keeps it out of writes.
	EffectiveBannerHash *string `gorm:"-" json:"effective_banner_hash,omitempty"`

	// EffectivePortraitHash is the derived, read-only image_hash of the pinned
	// PORTRAIT cover (the row flagged portrait_pinned) — the vertical companion
	// to EffectiveBannerHash for the portrait-first UI. nil when the galgame has
	// no pinned portrait (no landscape fallback; the frontend handles that).
	// Populated by PopulateEffectivePortrait after preload. `gorm:"-"` = derived.
	EffectivePortraitHash *string `gorm:"-" json:"effective_portrait_hash,omitempty"`
}

func (Galgame) TableName() string { return "galgame" }

// GalgameSeries represents a series of galgames
type GalgameSeries struct {
	ID           int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string    `gorm:"size:1000;uniqueIndex;default:''" json:"name"`
	Description  string    `gorm:"size:2000;default:''" json:"description"`
	Created      Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated      Timestamp `gorm:"autoUpdateTime" json:"updated"`
	GalgameCount int       `gorm:"column:cnt;->;-:migration" json:"galgame_count"`

	Galgame []Galgame `gorm:"foreignKey:SeriesID" json:"galgame,omitempty"`
}

func (GalgameSeries) TableName() string { return "galgame_series" }

// GalgameAlias represents an alias for a galgame
type GalgameAlias struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"default:''" json:"name"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	Created   Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated   Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameAlias) TableName() string { return "galgame_alias" }

// GalgameContributor represents a contributor to a galgame
type GalgameContributor struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated   Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameContributor) TableName() string { return "galgame_contributor" }
