package model

import "time"

// Galgame represents a visual novel entry
type Galgame struct {
	ID                 int       `gorm:"primaryKey;autoIncrement" json:"id"`
	VNDBID             string    `gorm:"column:vndb_id;size:10;uniqueIndex;not null" json:"vndb_id"`
	BangumiID          *int      `gorm:"column:bid;uniqueIndex" json:"bid,omitempty"`
	Released           string    `gorm:"column:released;size:107;default:'unknown'" json:"released"`
	NameEnUS           string    `gorm:"column:name_en_us;size:1000;default:''" json:"name_en_us"`
	NameJaJP           string    `gorm:"column:name_ja_jp;size:1000;default:''" json:"name_ja_jp"`
	NameZhCN           string    `gorm:"column:name_zh_cn;size:1000;default:''" json:"name_zh_cn"`
	NameZhTW           string    `gorm:"column:name_zh_tw;size:1000;default:''" json:"name_zh_tw"`
	Banner             string    `gorm:"size:233;default:''" json:"banner"`
	IntroEnUS          string    `gorm:"column:intro_en_us;type:text;default:''" json:"intro_en_us"`
	IntroJaJP          string    `gorm:"column:intro_ja_jp;type:text;default:''" json:"intro_ja_jp"`
	IntroZhCN          string    `gorm:"column:intro_zh_cn;type:text;default:''" json:"intro_zh_cn"`
	IntroZhTW          string    `gorm:"column:intro_zh_tw;type:text;default:''" json:"intro_zh_tw"`
	ContentLimit       string    `gorm:"column:content_limit;size:10;default:'sfw'" json:"content_limit"`
	Status             int       `gorm:"default:0" json:"status"`
	View               int       `gorm:"default:0" json:"view"`
	ResourceUpdateTime time.Time `gorm:"column:resource_update_time;autoCreateTime" json:"resource_update_time"`
	OriginalLanguage   string    `gorm:"column:original_language;default:'ja-jp'" json:"original_language"`
	AgeLimit           string    `gorm:"column:age_limit;size:10;default:'r18'" json:"age_limit"`
	UserID             int       `gorm:"column:user_id;not null;index" json:"user_id"`
	SeriesID           *int      `gorm:"column:series_id;index" json:"series_id"`
	Created            time.Time `gorm:"autoCreateTime" json:"created"`
	Updated            time.Time `gorm:"autoUpdateTime" json:"updated"`

	// Relations (galgame service owns these)
	Series       *GalgameSeries             `gorm:"foreignKey:SeriesID" json:"series,omitempty"`
	Alias        []GalgameAlias             `gorm:"foreignKey:GalgameID" json:"alias,omitempty"`
	Link         []GalgameLink              `gorm:"foreignKey:GalgameID" json:"link,omitempty"`
	PR           []GalgamePR                `gorm:"foreignKey:GalgameID" json:"pr,omitempty"`
	History      []GalgameHistory           `gorm:"foreignKey:GalgameID" json:"history,omitempty"`
	Contributor  []GalgameContributor       `gorm:"foreignKey:GalgameID" json:"contributor,omitempty"`
	Official     []GalgameOfficialRelation  `gorm:"foreignKey:GalgameID" json:"official,omitempty"`
	Engine       []GalgameEngineRelation    `gorm:"foreignKey:GalgameID" json:"engine,omitempty"`
	Tag          []GalgameTagRelation       `gorm:"foreignKey:GalgameID" json:"tag,omitempty"`
}

func (Galgame) TableName() string { return "galgame" }

// GalgameSeries represents a series of galgames
type GalgameSeries struct {
	ID          int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"size:1000;uniqueIndex;default:''" json:"name"`
	Description string    `gorm:"size:2000;default:''" json:"description"`
	Created      time.Time `gorm:"autoCreateTime" json:"created"`
	Updated      time.Time `gorm:"autoUpdateTime" json:"updated"`
	GalgameCount int       `gorm:"column:cnt;->;-:migration" json:"galgame_count"`

	Galgame []Galgame `gorm:"foreignKey:SeriesID" json:"galgame,omitempty"`
}

func (GalgameSeries) TableName() string { return "galgame_series" }

// GalgameAlias represents an alias for a galgame
type GalgameAlias struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"default:''" json:"name"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	Created   time.Time `gorm:"autoCreateTime" json:"created"`
	Updated   time.Time `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameAlias) TableName() string { return "galgame_alias" }

// GalgameContributor represents a contributor to a galgame
type GalgameContributor struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   time.Time `gorm:"autoCreateTime" json:"created"`
	Updated   time.Time `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameContributor) TableName() string { return "galgame_contributor" }
