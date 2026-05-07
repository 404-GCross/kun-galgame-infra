package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// Source models (match kungal Prisma schema exactly)
// ---------------------------------------------------------------------------

type SrcGalgame struct {
	ID                 int       `gorm:"column:id"`
	VNDBID             string    `gorm:"column:vndb_id"`
	NameEnUS           string    `gorm:"column:name_en_us"`
	NameJaJP           string    `gorm:"column:name_ja_jp"`
	NameZhCN           string    `gorm:"column:name_zh_cn"`
	NameZhTW           string    `gorm:"column:name_zh_tw"`
	Banner             string    `gorm:"column:banner"`
	IntroEnUS          string    `gorm:"column:intro_en_us"`
	IntroJaJP          string    `gorm:"column:intro_ja_jp"`
	IntroZhCN          string    `gorm:"column:intro_zh_cn"`
	IntroZhTW          string    `gorm:"column:intro_zh_tw"`
	ContentLimit       string    `gorm:"column:content_limit"`
	Status             int       `gorm:"column:status"`
	View               int       `gorm:"column:view"`
	ResourceUpdateTime time.Time `gorm:"column:resource_update_time"`
	OriginalLanguage   string    `gorm:"column:original_language"`
	AgeLimit           string    `gorm:"column:age_limit"`
	UserID             int       `gorm:"column:user_id"`
	SeriesID           *int      `gorm:"column:series_id"`
	Created            time.Time `gorm:"column:created"`
	Updated            time.Time `gorm:"column:updated"`
}

func (SrcGalgame) TableName() string { return "galgame" }

type SrcSeries struct {
	ID          int       `gorm:"column:id"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	Created     time.Time `gorm:"column:created"`
	Updated     time.Time `gorm:"column:updated"`
}

func (SrcSeries) TableName() string { return "galgame_series" }

type SrcAlias struct {
	ID        int       `gorm:"column:id"`
	Name      string    `gorm:"column:name"`
	GalgameID int       `gorm:"column:galgame_id"`
	Created   time.Time `gorm:"column:created"`
	Updated   time.Time `gorm:"column:updated"`
}

func (SrcAlias) TableName() string { return "galgame_alias" }

type SrcTag struct {
	ID          int       `gorm:"column:id"`
	Name        string    `gorm:"column:name"`
	Category    string    `gorm:"column:category"`
	Description string    `gorm:"column:description"`
	Created     time.Time `gorm:"column:created"`
	Updated     time.Time `gorm:"column:updated"`
}

func (SrcTag) TableName() string { return "galgame_tag" }

type SrcTagAlias struct {
	ID           int       `gorm:"column:id"`
	Name         string    `gorm:"column:name"`
	GalgameTagID int       `gorm:"column:galgame_tag_id"`
	Created      time.Time `gorm:"column:created"`
	Updated      time.Time `gorm:"column:updated"`
}

func (SrcTagAlias) TableName() string { return "galgame_tag_alias" }

type SrcTagRelation struct {
	GalgameID    int       `gorm:"column:galgame_id"`
	TagID        int       `gorm:"column:tag_id"`
	SpoilerLevel int       `gorm:"column:spoiler_level"`
	Created      time.Time `gorm:"column:created"`
	Updated      time.Time `gorm:"column:updated"`
}

func (SrcTagRelation) TableName() string { return "galgame_tag_relation" }

type SrcOfficial struct {
	ID          int       `gorm:"column:id"`
	Name        string    `gorm:"column:name"`
	Link        string    `gorm:"column:link"`
	Category    string    `gorm:"column:category"`
	Lang        string    `gorm:"column:lang"`
	Description string    `gorm:"column:description"`
	Created     time.Time `gorm:"column:created"`
	Updated     time.Time `gorm:"column:updated"`
}

func (SrcOfficial) TableName() string { return "galgame_official" }

type SrcOfficialAlias struct {
	ID                int       `gorm:"column:id"`
	Name              string    `gorm:"column:name"`
	GalgameOfficialID int       `gorm:"column:galgame_official_id"`
	Created           time.Time `gorm:"column:created"`
	Updated           time.Time `gorm:"column:updated"`
}

func (SrcOfficialAlias) TableName() string { return "galgame_official_alias" }

type SrcOfficialRelation struct {
	GalgameID  int       `gorm:"column:galgame_id"`
	OfficialID int       `gorm:"column:official_id"`
	Created    time.Time `gorm:"column:created"`
	Updated    time.Time `gorm:"column:updated"`
}

func (SrcOfficialRelation) TableName() string { return "galgame_official_relation" }

type SrcEngine struct {
	ID          int       `gorm:"column:id"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	Alias       string    `gorm:"column:alias;type:jsonb"`
	Created     time.Time `gorm:"column:created"`
	Updated     time.Time `gorm:"column:updated"`
}

func (SrcEngine) TableName() string { return "galgame_engine" }

type SrcEngineRelation struct {
	GalgameID int       `gorm:"column:galgame_id"`
	EngineID  int       `gorm:"column:engine_id"`
	Created   time.Time `gorm:"column:created"`
	Updated   time.Time `gorm:"column:updated"`
}

func (SrcEngineRelation) TableName() string { return "galgame_engine_relation" }

type SrcLink struct {
	ID        int       `gorm:"column:id"`
	Name      string    `gorm:"column:name"`
	Link      string    `gorm:"column:link"`
	GalgameID int       `gorm:"column:galgame_id"`
	UserID    int       `gorm:"column:user_id"`
	Created   time.Time `gorm:"column:created"`
	Updated   time.Time `gorm:"column:updated"`
}

func (SrcLink) TableName() string { return "galgame_link" }

type SrcContributor struct {
	ID        int       `gorm:"column:id"`
	GalgameID int       `gorm:"column:galgame_id"`
	UserID    int       `gorm:"column:user_id"`
	Created   time.Time `gorm:"column:created"`
	Updated   time.Time `gorm:"column:updated"`
}

func (SrcContributor) TableName() string { return "galgame_contributor" }

// ---------------------------------------------------------------------------
// Migration result
// ---------------------------------------------------------------------------

type MigrationResult struct {
	Series            int
	Tags              int
	TagAliases        int
	Officials         int
	OfficialAliases   int
	Engines           int
	Galgames          int
	Aliases           int
	TagRelations      int
	OfficialRelations int
	EngineRelations   int
	Links             int
	Contributors      int
	Revisions         int
	Skipped           int
	Errors            int
}

func main() {
	kungalDSN := flag.String("kungal-dsn", "", "Kungal source database DSN (required)")
	dryRun := flag.Bool("dry-run", false, "Perform a dry run without making changes")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if *kungalDSN == "" {
		slog.Error("--kungal-dsn is required")
		fmt.Println("Usage:")
		fmt.Println("  go run ./cmd/migrate-galgame-data \\")
		fmt.Println("    --kungal-dsn=\"host=localhost port=5432 user=postgres password=xxx dbname=kungalgame sslmode=disable\"")
		fmt.Println()
		fmt.Println("  Add --dry-run to preview without writing")
		os.Exit(1)
	}

	// Connect to source database
	srcDB, err := gorm.Open(postgres.Open(*kungalDSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		slog.Error("failed to connect to kungal database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to kungal source database")

	// Connect to target database (kun_galgame_wiki)
	targetDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to target database", "error", err)
		os.Exit(1)
	}
	defer targetDB.Close()
	tdb := targetDB.DB().Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	slog.Info("connected to target database", "dbname", cfg.GalgameDatabase.DBName)

	// Connect to OAuth database to get valid user IDs
	oauthDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("failed to connect to oauth database", "error", err)
		os.Exit(1)
	}
	defer oauthDB.Close()

	// Build valid user ID set
	var userIDs []struct{ ID int }
	oauthDB.DB().Table("users").Select("id").Find(&userIDs)
	validUserIDs := make(map[int]bool, len(userIDs))
	for _, u := range userIDs {
		validUserIDs[u.ID] = true
	}
	slog.Info("loaded valid user IDs from oauth database", "count", len(validUserIDs))

	if *dryRun {
		slog.Info("DRY RUN MODE — no changes will be made")
	}

	result, err := runMigration(srcDB, tdb, validUserIDs, *dryRun)
	if err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("==================================================")
	fmt.Println("Migration Results")
	fmt.Println("==================================================")
	fmt.Printf("Series:              %d\n", result.Series)
	fmt.Printf("Tags:                %d\n", result.Tags)
	fmt.Printf("Tag aliases:         %d\n", result.TagAliases)
	fmt.Printf("Officials:           %d\n", result.Officials)
	fmt.Printf("Official aliases:    %d\n", result.OfficialAliases)
	fmt.Printf("Engines:             %d\n", result.Engines)
	fmt.Printf("--------------------------------------------------\n")
	fmt.Printf("Galgames:            %d\n", result.Galgames)
	fmt.Printf("Aliases:             %d\n", result.Aliases)
	fmt.Printf("Tag relations:       %d\n", result.TagRelations)
	fmt.Printf("Official relations:  %d\n", result.OfficialRelations)
	fmt.Printf("Engine relations:    %d\n", result.EngineRelations)
	fmt.Printf("Links:               %d\n", result.Links)
	fmt.Printf("Contributors:        %d\n", result.Contributors)
	fmt.Printf("--------------------------------------------------\n")
	fmt.Printf("Revisions created:   %d\n", result.Revisions)
	fmt.Printf("Skipped (existing):  %d\n", result.Skipped)
	fmt.Printf("Errors:              %d\n", result.Errors)
	fmt.Println("==================================================")
}

// batchSize controls the number of rows per INSERT statement
const batchSize = 5000

func runMigration(srcDB, tdb *gorm.DB, validUserIDs map[int]bool, dryRun bool) (*MigrationResult, error) {
	result := &MigrationResult{}

	// ── Step 1: Migrate series ──
	slog.Info("Step 1: Migrating series...")
	var srcSeries []SrcSeries
	srcDB.Order("id ASC").Find(&srcSeries)
	if !dryRun && len(srcSeries) > 0 {
		batchExec(tdb, "galgame_series", []string{"id", "name", "description", "created", "updated"},
			srcSeries, func(s SrcSeries) []any {
				return []any{s.ID, s.Name, s.Description, s.Created, s.Updated}
			})
	}
	result.Series = len(srcSeries)
	slog.Info("Series migrated", "count", result.Series)

	// ── Step 2: Migrate tags ──
	slog.Info("Step 2: Migrating tags...")
	var srcTags []SrcTag
	srcDB.Order("id ASC").Find(&srcTags)
	if !dryRun && len(srcTags) > 0 {
		batchExec(tdb, "galgame_tag", []string{"id", "name", "category", "description", "created", "updated"},
			srcTags, func(t SrcTag) []any {
				return []any{t.ID, t.Name, t.Category, t.Description, t.Created, t.Updated}
			})
	}
	result.Tags = len(srcTags)

	var srcTagAliases []SrcTagAlias
	srcDB.Order("id ASC").Find(&srcTagAliases)
	if !dryRun && len(srcTagAliases) > 0 {
		batchExec(tdb, "galgame_tag_alias", []string{"id", "name", "galgame_tag_id", "created", "updated"},
			srcTagAliases, func(a SrcTagAlias) []any {
				return []any{a.ID, a.Name, a.GalgameTagID, a.Created, a.Updated}
			})
	}
	result.TagAliases = len(srcTagAliases)
	slog.Info("Tags migrated", "tags", result.Tags, "aliases", result.TagAliases)

	// ── Step 3: Migrate officials ──
	slog.Info("Step 3: Migrating officials...")
	var srcOfficials []SrcOfficial
	srcDB.Order("id ASC").Find(&srcOfficials)
	if !dryRun && len(srcOfficials) > 0 {
		batchExec(tdb, "galgame_official", []string{"id", "name", "link", "category", "lang", "description", "created", "updated"},
			srcOfficials, func(o SrcOfficial) []any {
				return []any{o.ID, o.Name, o.Link, o.Category, o.Lang, o.Description, o.Created, o.Updated}
			})
	}
	result.Officials = len(srcOfficials)

	var srcOfficialAliases []SrcOfficialAlias
	srcDB.Order("id ASC").Find(&srcOfficialAliases)
	if !dryRun && len(srcOfficialAliases) > 0 {
		batchExec(tdb, "galgame_official_alias", []string{"id", "name", "galgame_official_id", "created", "updated"},
			srcOfficialAliases, func(a SrcOfficialAlias) []any {
				return []any{a.ID, a.Name, a.GalgameOfficialID, a.Created, a.Updated}
			})
	}
	result.OfficialAliases = len(srcOfficialAliases)
	slog.Info("Officials migrated", "officials", result.Officials, "aliases", result.OfficialAliases)

	// ── Step 4: Migrate engines ──
	slog.Info("Step 4: Migrating engines...")
	var srcEngines []SrcEngine
	srcDB.Order("id ASC").Find(&srcEngines)
	if !dryRun && len(srcEngines) > 0 {
		skippedEngines := 0
		for _, e := range srcEngines {
			alias := e.Alias
			if alias == "" {
				alias = "[]"
			}
			if err := tdb.Exec("INSERT INTO galgame_engine (id, name, description, alias, created, updated) VALUES (?, ?, ?, ?::jsonb, ?, ?) ON CONFLICT (id) DO NOTHING",
				e.ID, e.Name, e.Description, alias, e.Created, e.Updated).Error; err != nil {
				skippedEngines++
			}
		}
		if skippedEngines > 0 {
			slog.Warn("skipped engines with errors", "count", skippedEngines)
		}
	}
	result.Engines = len(srcEngines)
	slog.Info("Engines migrated", "count", result.Engines)

	// ── Step 5: Migrate galgames ──
	slog.Info("Step 5: Migrating galgames...")
	var srcGalgames []SrcGalgame
	srcDB.Order("id ASC").Find(&srcGalgames)

	existingIDs := make(map[int]bool)
	var existingRows []struct{ ID int }
	tdb.Model(&model.Galgame{}).Select("id").Find(&existingRows)
	for _, e := range existingRows {
		existingIDs[e.ID] = true
	}

	// Filter out existing and those with invalid user_id
	var newGalgames []SrcGalgame
	var skippedOrphanUser int
	for _, g := range srcGalgames {
		if existingIDs[g.ID] {
			result.Skipped++
		} else if !validUserIDs[g.UserID] {
			skippedOrphanUser++
		} else {
			newGalgames = append(newGalgames, g)
		}
	}
	if skippedOrphanUser > 0 {
		slog.Warn("skipped galgames with invalid user_id", "count", skippedOrphanUser)
	}

	if !dryRun && len(newGalgames) > 0 {
		batchExec(tdb, "galgame",
			[]string{"id", "vndb_id", "name_en_us", "name_ja_jp", "name_zh_cn", "name_zh_tw", "banner",
				"intro_en_us", "intro_ja_jp", "intro_zh_cn", "intro_zh_tw",
				"content_limit", "status", "view", "resource_update_time",
				"original_language", "age_limit", "user_id", "series_id", "created", "updated"},
			newGalgames, func(g SrcGalgame) []any {
				return []any{g.ID, g.VNDBID, g.NameEnUS, g.NameJaJP, g.NameZhCN, g.NameZhTW, g.Banner,
					g.IntroEnUS, g.IntroJaJP, g.IntroZhCN, g.IntroZhTW,
					g.ContentLimit, g.Status, g.View, g.ResourceUpdateTime,
					g.OriginalLanguage, g.AgeLimit, g.UserID, g.SeriesID, g.Created, g.Updated}
			})

		// Migration audit trail. For kungal, source_id == galgame_id (we
		// preserve IDs during copy), but recording it explicitly keeps the
		// table symmetric with the moyu side and lets future scripts find
		// legacy kungal ids without inferring from row state.
		batchExec(tdb, "galgame_migrations",
			[]string{"source_db", "source_id", "galgame_id", "created_at"},
			newGalgames, func(g SrcGalgame) []any {
				return []any{"kungal", g.ID, g.ID, time.Now()}
			})
	}
	result.Galgames = len(newGalgames)
	slog.Info("Galgames migrated", "count", result.Galgames, "skipped", result.Skipped)

	// ── Step 6: Migrate relation tables (batch, with orphan filtering) ──
	slog.Info("Step 6: Migrating relations...")

	// Build valid ID sets for FK filtering
	validGalgameIDs := make(map[int]bool, len(srcGalgames))
	for _, g := range srcGalgames {
		validGalgameIDs[g.ID] = true
	}
	validTagIDs := make(map[int]bool, len(srcTags))
	for _, t := range srcTags {
		validTagIDs[t.ID] = true
	}
	validOfficialIDs := make(map[int]bool, len(srcOfficials))
	for _, o := range srcOfficials {
		validOfficialIDs[o.ID] = true
	}
	validEngineIDs := make(map[int]bool, len(srcEngines))
	for _, e := range srcEngines {
		validEngineIDs[e.ID] = true
	}

	var srcAliases []SrcAlias
	srcDB.Order("id ASC").Find(&srcAliases)
	var cleanAliases []SrcAlias
	for _, a := range srcAliases {
		if validGalgameIDs[a.GalgameID] {
			cleanAliases = append(cleanAliases, a)
		}
	}
	if !dryRun && len(cleanAliases) > 0 {
		batchExec(tdb, "galgame_alias", []string{"id", "name", "galgame_id", "created", "updated"},
			cleanAliases, func(a SrcAlias) []any {
				return []any{a.ID, a.Name, a.GalgameID, a.Created, a.Updated}
			})
	}
	result.Aliases = len(cleanAliases)
	if skipped := len(srcAliases) - len(cleanAliases); skipped > 0 {
		slog.Warn("skipped orphan aliases", "count", skipped)
	}

	var srcTagRels []SrcTagRelation
	srcDB.Find(&srcTagRels)
	var cleanTagRels []SrcTagRelation
	for _, r := range srcTagRels {
		if validGalgameIDs[r.GalgameID] && validTagIDs[r.TagID] {
			cleanTagRels = append(cleanTagRels, r)
		}
	}
	if !dryRun && len(cleanTagRels) > 0 {
		batchExec(tdb, "galgame_tag_relation", []string{"galgame_id", "tag_id", "spoiler_level", "created", "updated"},
			cleanTagRels, func(r SrcTagRelation) []any {
				return []any{r.GalgameID, r.TagID, r.SpoilerLevel, r.Created, r.Updated}
			})
	}
	result.TagRelations = len(cleanTagRels)
	if skipped := len(srcTagRels) - len(cleanTagRels); skipped > 0 {
		slog.Warn("skipped orphan tag relations", "count", skipped)
	}

	var srcOfficialRels []SrcOfficialRelation
	srcDB.Find(&srcOfficialRels)
	var cleanOfficialRels []SrcOfficialRelation
	for _, r := range srcOfficialRels {
		if validGalgameIDs[r.GalgameID] && validOfficialIDs[r.OfficialID] {
			cleanOfficialRels = append(cleanOfficialRels, r)
		}
	}
	if !dryRun && len(cleanOfficialRels) > 0 {
		batchExec(tdb, "galgame_official_relation", []string{"galgame_id", "official_id", "created", "updated"},
			cleanOfficialRels, func(r SrcOfficialRelation) []any {
				return []any{r.GalgameID, r.OfficialID, r.Created, r.Updated}
			})
	}
	result.OfficialRelations = len(cleanOfficialRels)
	if skipped := len(srcOfficialRels) - len(cleanOfficialRels); skipped > 0 {
		slog.Warn("skipped orphan official relations", "count", skipped)
	}

	var srcEngineRels []SrcEngineRelation
	srcDB.Find(&srcEngineRels)
	var cleanEngineRels []SrcEngineRelation
	for _, r := range srcEngineRels {
		if validGalgameIDs[r.GalgameID] && validEngineIDs[r.EngineID] {
			cleanEngineRels = append(cleanEngineRels, r)
		}
	}
	if !dryRun && len(cleanEngineRels) > 0 {
		batchExec(tdb, "galgame_engine_relation", []string{"galgame_id", "engine_id", "created", "updated"},
			cleanEngineRels, func(r SrcEngineRelation) []any {
				return []any{r.GalgameID, r.EngineID, r.Created, r.Updated}
			})
	}
	result.EngineRelations = len(cleanEngineRels)
	if skipped := len(srcEngineRels) - len(cleanEngineRels); skipped > 0 {
		slog.Warn("skipped orphan engine relations", "count", skipped)
	}

	var srcLinks []SrcLink
	srcDB.Order("id ASC").Find(&srcLinks)
	var cleanLinks []SrcLink
	for _, l := range srcLinks {
		if validGalgameIDs[l.GalgameID] && validUserIDs[l.UserID] {
			cleanLinks = append(cleanLinks, l)
		}
	}
	if !dryRun && len(cleanLinks) > 0 {
		batchExec(tdb, "galgame_link", []string{"id", "name", "link", "galgame_id", "user_id", "created", "updated"},
			cleanLinks, func(l SrcLink) []any {
				return []any{l.ID, l.Name, l.Link, l.GalgameID, l.UserID, l.Created, l.Updated}
			})
	}
	result.Links = len(cleanLinks)
	if skipped := len(srcLinks) - len(cleanLinks); skipped > 0 {
		slog.Warn("skipped orphan links", "count", skipped)
	}

	var srcContributors []SrcContributor
	srcDB.Order("id ASC").Find(&srcContributors)
	var cleanContributors []SrcContributor
	for _, c := range srcContributors {
		if validGalgameIDs[c.GalgameID] && validUserIDs[c.UserID] {
			cleanContributors = append(cleanContributors, c)
		}
	}
	if !dryRun && len(cleanContributors) > 0 {
		batchExec(tdb, "galgame_contributor", []string{"id", "galgame_id", "user_id", "created", "updated"},
			cleanContributors, func(c SrcContributor) []any {
				return []any{c.ID, c.GalgameID, c.UserID, c.Created, c.Updated}
			})
	}
	result.Contributors = len(cleanContributors)
	if skipped := len(srcContributors) - len(cleanContributors); skipped > 0 {
		slog.Warn("skipped orphan contributors", "count", skipped)
	}

	slog.Info("Relations migrated",
		"aliases", result.Aliases, "tag_rels", result.TagRelations,
		"official_rels", result.OfficialRelations, "engine_rels", result.EngineRelations,
		"links", result.Links, "contributors", result.Contributors)

	// ── Step 7: Create revision 1 for each galgame ──
	slog.Info("Step 7: Creating initial revisions...")

	aliasMap := make(map[int][]string)
	tagRelMap := make(map[int][]int)
	officialRelMap := make(map[int][]int)
	engineRelMap := make(map[int][]int)
	linkMap := make(map[int][]model.SnapshotLink)

	for _, a := range srcAliases {
		aliasMap[a.GalgameID] = append(aliasMap[a.GalgameID], a.Name)
	}
	for _, r := range srcTagRels {
		tagRelMap[r.GalgameID] = append(tagRelMap[r.GalgameID], r.TagID)
	}
	for _, r := range srcOfficialRels {
		officialRelMap[r.GalgameID] = append(officialRelMap[r.GalgameID], r.OfficialID)
	}
	for _, r := range srcEngineRels {
		engineRelMap[r.GalgameID] = append(engineRelMap[r.GalgameID], r.EngineID)
	}
	for _, l := range srcLinks {
		linkMap[l.GalgameID] = append(linkMap[l.GalgameID], model.SnapshotLink{Name: l.Name, Link: l.Link})
	}

	existingRevIDs := make(map[int]bool)
	var existingRevs []struct{ GalgameID int }
	tdb.Model(&model.GalgameRevision{}).Select("DISTINCT galgame_id").Find(&existingRevs)
	for _, r := range existingRevs {
		existingRevIDs[r.GalgameID] = true
	}

	// Build all revision rows in memory, then batch insert
	type revRow struct {
		GalgameID int
		UserID    int
		Snapshot  string
		Created   time.Time
	}
	var revRows []revRow

	for _, g := range srcGalgames {
		if existingRevIDs[g.ID] {
			continue
		}

		aliases := aliasMap[g.ID]
		if aliases == nil {
			aliases = []string{}
		}
		sort.Strings(aliases)

		tagIDs := tagRelMap[g.ID]
		if tagIDs == nil {
			tagIDs = []int{}
		}
		sort.Ints(tagIDs)

		officialIDs := officialRelMap[g.ID]
		if officialIDs == nil {
			officialIDs = []int{}
		}
		sort.Ints(officialIDs)

		engineIDs := engineRelMap[g.ID]
		if engineIDs == nil {
			engineIDs = []int{}
		}
		sort.Ints(engineIDs)

		links := linkMap[g.ID]
		if links == nil {
			links = []model.SnapshotLink{}
		}

		snapshot := model.Snapshot{
			VNDBID: g.VNDBID, NameEnUS: g.NameEnUS, NameJaJP: g.NameJaJP, NameZhCN: g.NameZhCN, NameZhTW: g.NameZhTW,
			Banner: g.Banner, IntroEnUS: g.IntroEnUS, IntroJaJP: g.IntroJaJP, IntroZhCN: g.IntroZhCN, IntroZhTW: g.IntroZhTW,
			ContentLimit: g.ContentLimit, OriginalLanguage: g.OriginalLanguage, AgeLimit: g.AgeLimit,
			SeriesID: g.SeriesID, Aliases: aliases, TagIDs: tagIDs, OfficialIDs: officialIDs, EngineIDs: engineIDs, Links: links,
		}

		snapshotJSON, err := json.Marshal(snapshot)
		if err != nil {
			result.Errors++
			continue
		}

		revRows = append(revRows, revRow{GalgameID: g.ID, UserID: g.UserID, Snapshot: string(snapshotJSON), Created: g.Created})
	}

	if !dryRun && len(revRows) > 0 {
		batchExec(tdb, "galgame_revision",
			[]string{"galgame_id", "revision", "user_id", "action", "note", "snapshot", "is_minor", "created"},
			revRows, func(r revRow) []any {
				return []any{r.GalgameID, 1, r.UserID, "created", "初始版本（从历史数据迁移）", r.Snapshot, false, r.Created}
			})
	}
	result.Revisions = len(revRows)
	slog.Info("Revisions created", "count", result.Revisions)

	// ── Step 8: Reset sequences ──
	if !dryRun {
		slog.Info("Step 8: Resetting sequences...")
		for _, seq := range []struct{ table, column string }{
			{"galgame", "id"}, {"galgame_series", "id"}, {"galgame_alias", "id"},
			{"galgame_tag", "id"}, {"galgame_tag_alias", "id"},
			{"galgame_official", "id"}, {"galgame_official_alias", "id"},
			{"galgame_engine", "id"}, {"galgame_link", "id"},
			{"galgame_contributor", "id"}, {"galgame_revision", "id"},
		} {
			tdb.Exec(fmt.Sprintf(
				"SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 1))",
				seq.table, seq.column, seq.column, seq.table))
		}
		slog.Info("Sequences reset")
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Batch insert helpers
// ---------------------------------------------------------------------------

// buildBatchInsert builds a multi-row INSERT ... ON CONFLICT DO NOTHING SQL string
func buildBatchInsert[T any](table string, columns []string, rows []T, valueFn func(T) []any) string {
	if len(rows) == 0 {
		return ""
	}

	// Build: INSERT INTO table (col1, col2) VALUES ($1,$2), ($3,$4) ... ON CONFLICT DO NOTHING
	nCols := len(columns)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("INSERT INTO %s (%s) VALUES ", table, strings.Join(columns, ", ")))

	paramIdx := 1
	for i := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j := 0; j < nCols; j++ {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("$%d", paramIdx))
			paramIdx++
		}
		sb.WriteString(")")
	}
	sb.WriteString(" ON CONFLICT DO NOTHING")
	return sb.String()
}

// batchExec executes batch inserts in chunks of batchSize
func batchExec[T any](db *gorm.DB, table string, columns []string, rows []T, valueFn func(T) []any) {
	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		sql := buildBatchInsert(table, columns, chunk, valueFn)
		if sql == "" {
			continue
		}

		// Flatten all values
		var args []any
		for _, row := range chunk {
			args = append(args, valueFn(row)...)
		}

		if err := db.Exec(sql, args...).Error; err != nil {
			// Batch failed — fallback to row-by-row to skip only the bad rows
			skipped := 0
			for _, row := range chunk {
				singleSQL := buildBatchInsert(table, columns, []T{row}, valueFn)
				singleArgs := valueFn(row)
				if singleErr := db.Exec(singleSQL, singleArgs...).Error; singleErr != nil {
					skipped++
				}
			}
			if skipped > 0 {
				slog.Warn("batch fallback: skipped rows with FK violations", "table", table, "offset", i, "skipped", skipped)
			}
		}

		if end%5000 == 0 || end == len(rows) {
			slog.Info("batch progress", "table", table, "done", end, "total", len(rows))
		}
	}
}
