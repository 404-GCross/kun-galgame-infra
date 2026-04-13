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
// Source models (moyu Prisma schema)
// ---------------------------------------------------------------------------

type MoyuPatch struct {
	ID                 int       `gorm:"column:id"`
	VNDBID             *string   `gorm:"column:vndb_id"`
	BID                *int      `gorm:"column:bid"`
	NameEnUS           string    `gorm:"column:name_en_us"`
	NameZhCN           string    `gorm:"column:name_zh_cn"`
	NameJaJP           string    `gorm:"column:name_ja_jp"`
	Banner             string    `gorm:"column:banner"`
	IntroductionZhCN   string    `gorm:"column:introduction_zh_cn"`
	IntroductionJaJP   string    `gorm:"column:introduction_ja_jp"`
	IntroductionEnUS   string    `gorm:"column:introduction_en_us"`
	Released           string    `gorm:"column:released"`
	ContentLimit       string    `gorm:"column:content_limit"`
	Status             int       `gorm:"column:status"`
	View               int       `gorm:"column:view"`
	ResourceUpdateTime time.Time `gorm:"column:resource_update_time"`
	UserID             int       `gorm:"column:user_id"`
	Created            time.Time `gorm:"column:created"`
	Updated            time.Time `gorm:"column:updated"`
}

func (MoyuPatch) TableName() string { return "patch" }

type MoyuAlias struct {
	ID      int       `gorm:"column:id"`
	Name    string    `gorm:"column:name"`
	PatchID int       `gorm:"column:patch_id"`
	Created time.Time `gorm:"column:created"`
	Updated time.Time `gorm:"column:updated"`
}

func (MoyuAlias) TableName() string { return "patch_alias" }

type MoyuLink struct {
	ID      int       `gorm:"column:id"`
	PatchID int       `gorm:"column:patch_id"`
	Name    string    `gorm:"column:name"`
	URL     string    `gorm:"column:url"`
	Created time.Time `gorm:"column:created"`
	Updated time.Time `gorm:"column:updated"`
}

func (MoyuLink) TableName() string { return "patch_link" }

type MoyuTag struct {
	ID       int    `gorm:"column:id"`
	Name     string `gorm:"column:name"`
	Provider string `gorm:"column:provider"`
	NameEnUS string `gorm:"column:name_en_us"`
	Category string `gorm:"column:category"`
}

func (MoyuTag) TableName() string { return "patch_tag" }

type MoyuTagRelation struct {
	PatchID      int `gorm:"column:patch_id"`
	TagID        int `gorm:"column:tag_id"`
	SpoilerLevel int `gorm:"column:spoiler_level"`
}

func (MoyuTagRelation) TableName() string { return "patch_tag_relation" }

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

const batchSize = 5000

func main() {
	moyuDSN := flag.String("moyu-dsn", "", "Moyu source database DSN (required)")
	dryRun := flag.Bool("dry-run", false, "Perform a dry run without making changes")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if *moyuDSN == "" {
		slog.Error("--moyu-dsn is required")
		fmt.Println("Usage:")
		fmt.Println("  go run ./cmd/migrate-moyu-galgame \\")
		fmt.Println("    --moyu-dsn=\"host=localhost port=5432 user=postgres password=xxx dbname=kungalgame_patch sslmode=disable\"")
		os.Exit(1)
	}

	srcDB, err := gorm.Open(postgres.Open(*moyuDSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		slog.Error("failed to connect to moyu database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to moyu source database")

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to wiki database", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	tdb := wikiDB.DB().Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	slog.Info("connected to wiki database", "dbname", cfg.GalgameDatabase.DBName)

	if *dryRun {
		slog.Info("DRY RUN MODE — no changes will be made")
	}

	if err := run(srcDB, tdb, *dryRun); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run(srcDB, tdb *gorm.DB, dryRun bool) error {
	// ── Step 1: Load moyu patches ──
	slog.Info("Step 1: Loading moyu patches...")
	var patches []MoyuPatch
	srcDB.Order("id ASC").Find(&patches)
	slog.Info("loaded patches", "count", len(patches))

	// ── Step 2: Build vndb_id → wiki galgame.id mapping from existing wiki data ──
	slog.Info("Step 2: Building vndb_id mapping from wiki...")
	type wikiRow struct {
		ID     int
		VNDBID string
	}
	var wikiGalgames []wikiRow
	tdb.Table("galgame").Select("id, vndb_id").Find(&wikiGalgames)

	vndbToWikiID := make(map[string]int, len(wikiGalgames))
	maxWikiID := 0
	for _, g := range wikiGalgames {
		vndbToWikiID[g.VNDBID] = g.ID
		if g.ID > maxWikiID {
			maxWikiID = g.ID
		}
	}
	slog.Info("wiki state", "galgames", len(wikiGalgames), "max_id", maxWikiID)

	// ── Step 3: Classify patches and build patch_id → galgame_id mapping ──
	slog.Info("Step 3: Classifying patches...")
	patchToGalgameID := make(map[int]int, len(patches))
	var newPatches []MoyuPatch       // vndb_id not in wiki → create new galgame
	var mergePatches []MoyuPatch     // vndb_id in wiki → only update bid/released
	var noVNDBPatches []MoyuPatch    // no vndb_id → create new galgame
	nextID := maxWikiID + 1

	for _, p := range patches {
		if p.VNDBID != nil && *p.VNDBID != "" {
			if wikiID, exists := vndbToWikiID[*p.VNDBID]; exists {
				// Duplicate vndb_id: keep kungal data, map to existing wiki galgame
				patchToGalgameID[p.ID] = wikiID
				mergePatches = append(mergePatches, p)
			} else {
				// New vndb_id: assign new ID
				patchToGalgameID[p.ID] = nextID
				vndbToWikiID[*p.VNDBID] = nextID
				newPatches = append(newPatches, p)
				nextID++
			}
		} else {
			// No vndb_id: assign new ID
			patchToGalgameID[p.ID] = nextID
			noVNDBPatches = append(noVNDBPatches, p)
			nextID++
		}
	}
	slog.Info("classification",
		"merge", len(mergePatches),
		"new_with_vndb", len(newPatches),
		"new_without_vndb", len(noVNDBPatches),
	)

	if dryRun {
		fmt.Printf("\n[DRY RUN] Would create %d new galgames, merge %d existing\n", len(newPatches)+len(noVNDBPatches), len(mergePatches))
		return nil
	}

	// ── Step 4: Update existing wiki galgames with bid/released ──
	slog.Info("Step 4: Updating existing galgames with bid/released...")
	updatedBid := 0
	updatedReleased := 0
	for _, p := range mergePatches {
		wikiID := patchToGalgameID[p.ID]
		if p.BID != nil {
			result := tdb.Exec("UPDATE galgame SET bid = ? WHERE id = ? AND bid IS NULL", *p.BID, wikiID)
			if result.RowsAffected > 0 {
				updatedBid++
			}
		}
		if p.Released != "" && p.Released != "unknown" {
			result := tdb.Exec("UPDATE galgame SET released = ? WHERE id = ? AND (released = 'unknown' OR released = '')", p.Released, wikiID)
			if result.RowsAffected > 0 {
				updatedReleased++
			}
		}
	}
	slog.Info("updated existing galgames", "bid", updatedBid, "released", updatedReleased)

	// ── Step 5: Insert new galgames ──
	slog.Info("Step 5: Inserting new galgames...")
	allNew := append(newPatches, noVNDBPatches...)
	if len(allNew) > 0 {
		batchExec(tdb, "galgame",
			[]string{"id", "vndb_id", "bid", "released", "name_en_us", "name_ja_jp", "name_zh_cn", "name_zh_tw",
				"banner", "intro_en_us", "intro_ja_jp", "intro_zh_cn", "intro_zh_tw",
				"content_limit", "status", "view", "resource_update_time",
				"original_language", "age_limit", "user_id", "series_id", "created", "updated"},
			allNew, func(p MoyuPatch) []any {
				galgameID := patchToGalgameID[p.ID]
				vndbID := ""
				if p.VNDBID != nil {
					vndbID = *p.VNDBID
				}
				return []any{
					galgameID, vndbID, p.BID, p.Released,
					p.NameEnUS, p.NameJaJP, p.NameZhCN, p.NameZhCN, // name_zh_tw = name_zh_cn
					p.Banner, p.IntroductionEnUS, p.IntroductionJaJP, p.IntroductionZhCN, p.IntroductionZhCN, // intro_zh_tw = intro_zh_cn
					p.ContentLimit, p.Status, p.View, p.ResourceUpdateTime,
					"ja-jp", "r18", p.UserID, nil, p.Created, p.Updated,
				}
			})
	}
	slog.Info("new galgames inserted", "count", len(allNew))

	// ── Step 6: Migrate aliases ──
	slog.Info("Step 6: Migrating aliases...")
	var srcAliases []MoyuAlias
	srcDB.Order("id ASC").Find(&srcAliases)
	var validAliases []MoyuAlias
	for _, a := range srcAliases {
		if _, ok := patchToGalgameID[a.PatchID]; ok {
			validAliases = append(validAliases, a)
		}
	}
	if len(validAliases) > 0 {
		// Use new IDs starting after wiki max alias ID
		var maxAliasID int
		tdb.Table("galgame_alias").Select("COALESCE(MAX(id), 0)").Scan(&maxAliasID)
		aliasID := maxAliasID + 1

		batchExec(tdb, "galgame_alias", []string{"id", "name", "galgame_id", "created", "updated"},
			validAliases, func(a MoyuAlias) []any {
				id := aliasID
				aliasID++
				return []any{id, a.Name, patchToGalgameID[a.PatchID], a.Created, a.Updated}
			})
	}
	slog.Info("aliases migrated", "count", len(validAliases))

	// ── Step 7: Migrate links ──
	slog.Info("Step 7: Migrating links...")
	var srcLinks []MoyuLink
	srcDB.Order("id ASC").Find(&srcLinks)

	// Build patch.user_id lookup for link user_id
	patchUserID := make(map[int]int, len(patches))
	for _, p := range patches {
		patchUserID[p.ID] = p.UserID
	}

	var validLinks []MoyuLink
	for _, l := range srcLinks {
		if _, ok := patchToGalgameID[l.PatchID]; ok {
			validLinks = append(validLinks, l)
		}
	}
	if len(validLinks) > 0 {
		var maxLinkID int
		tdb.Table("galgame_link").Select("COALESCE(MAX(id), 0)").Scan(&maxLinkID)
		linkID := maxLinkID + 1

		batchExec(tdb, "galgame_link", []string{"id", "name", "link", "galgame_id", "user_id", "created", "updated"},
			validLinks, func(l MoyuLink) []any {
				id := linkID
				linkID++
				uid := patchUserID[l.PatchID]
				return []any{id, l.Name, l.URL, patchToGalgameID[l.PatchID], uid, l.Created, l.Updated}
			})
	}
	slog.Info("links migrated", "count", len(validLinks))

	// ── Step 8: Merge tags ──
	slog.Info("Step 8: Merging tags...")
	var srcTags []MoyuTag
	srcDB.Where("provider = 'vndb'").Find(&srcTags)

	// Load existing wiki tags
	type wikiTag struct {
		ID   int
		Name string
	}
	var existingTags []wikiTag
	tdb.Table("galgame_tag").Select("id, name").Find(&existingTags)
	wikiTagByName := make(map[string]int, len(existingTags))
	for _, t := range existingTags {
		wikiTagByName[t.Name] = t.ID
	}

	var maxTagID int
	tdb.Table("galgame_tag").Select("COALESCE(MAX(id), 0)").Scan(&maxTagID)
	nextTagID := maxTagID + 1

	moyuTagToWikiTag := make(map[int]int, len(srcTags))
	newTagCount := 0
	newAliasCount := 0

	for _, mt := range srcTags {
		if wikiID, exists := wikiTagByName[mt.Name]; exists {
			moyuTagToWikiTag[mt.ID] = wikiID
		} else {
			// Create new tag
			category := mt.Category
			if category == "" {
				category = "content"
			}
			if err := tdb.Exec(
				"INSERT INTO galgame_tag (id, name, category, description, created, updated) VALUES (?, ?, ?, '', NOW(), NOW()) ON CONFLICT DO NOTHING",
				nextTagID, mt.Name, category,
			).Error; err == nil {
				moyuTagToWikiTag[mt.ID] = nextTagID
				wikiTagByName[mt.Name] = nextTagID
				nextTagID++
				newTagCount++
			}
		}

		// Add name_en_us as alias if different from name
		if mt.NameEnUS != "" && mt.NameEnUS != mt.Name {
			wikiID := moyuTagToWikiTag[mt.ID]
			if wikiID > 0 {
				tdb.Exec(
					"INSERT INTO galgame_tag_alias (name, galgame_tag_id, created, updated) VALUES (?, ?, NOW(), NOW()) ON CONFLICT DO NOTHING",
					mt.NameEnUS, wikiID,
				)
				newAliasCount++
			}
		}
	}
	slog.Info("tags merged", "moyu_vndb_tags", len(srcTags), "new_tags_created", newTagCount, "new_aliases", newAliasCount)

	// ── Step 9: Migrate tag relations ──
	slog.Info("Step 9: Migrating tag relations...")
	var srcTagRels []MoyuTagRelation
	srcDB.Find(&srcTagRels)

	type tagRelRow struct {
		GalgameID    int
		TagID        int
		SpoilerLevel int
	}
	var validTagRels []tagRelRow
	for _, r := range srcTagRels {
		galgameID, gOK := patchToGalgameID[r.PatchID]
		tagID, tOK := moyuTagToWikiTag[r.TagID]
		if gOK && tOK {
			validTagRels = append(validTagRels, tagRelRow{GalgameID: galgameID, TagID: tagID, SpoilerLevel: r.SpoilerLevel})
		}
	}
	if len(validTagRels) > 0 {
		batchExec(tdb, "galgame_tag_relation",
			[]string{"galgame_id", "tag_id", "spoiler_level", "created", "updated"},
			validTagRels, func(r tagRelRow) []any {
				return []any{r.GalgameID, r.TagID, r.SpoilerLevel, time.Now(), time.Now()}
			})
	}
	slog.Info("tag relations migrated", "total_src", len(srcTagRels), "valid", len(validTagRels))

	// ── Step 10: Create revision 1 for new galgames ──
	slog.Info("Step 10: Creating revisions for new galgames...")

	// Build lookup maps from migrated data
	aliasMap := make(map[int][]string)
	for _, a := range validAliases {
		gid := patchToGalgameID[a.PatchID]
		aliasMap[gid] = append(aliasMap[gid], a.Name)
	}
	linkMap := make(map[int][]model.SnapshotLink)
	for _, l := range validLinks {
		gid := patchToGalgameID[l.PatchID]
		linkMap[gid] = append(linkMap[gid], model.SnapshotLink{Name: l.Name, Link: l.URL})
	}
	tagRelMap := make(map[int][]int)
	for _, r := range validTagRels {
		tagRelMap[r.GalgameID] = append(tagRelMap[r.GalgameID], r.TagID)
	}

	// Check which galgames already have revisions
	existingRevIDs := make(map[int]bool)
	var existingRevs []struct{ GalgameID int }
	tdb.Table("galgame_revision").Select("DISTINCT galgame_id").Find(&existingRevs)
	for _, r := range existingRevs {
		existingRevIDs[r.GalgameID] = true
	}

	type revRow struct {
		GalgameID int
		UserID    int
		Snapshot  string
		Created   time.Time
	}
	var revRows []revRow

	for _, p := range allNew {
		galgameID := patchToGalgameID[p.ID]
		if existingRevIDs[galgameID] {
			continue
		}

		aliases := aliasMap[galgameID]
		if aliases == nil {
			aliases = []string{}
		}
		sort.Strings(aliases)

		tagIDs := tagRelMap[galgameID]
		if tagIDs == nil {
			tagIDs = []int{}
		}
		sort.Ints(tagIDs)

		links := linkMap[galgameID]
		if links == nil {
			links = []model.SnapshotLink{}
		}

		vndbID := ""
		if p.VNDBID != nil {
			vndbID = *p.VNDBID
		}

		snapshot := model.Snapshot{
			VNDBID: vndbID, BangumiID: p.BID, Released: p.Released,
			NameEnUS: p.NameEnUS, NameJaJP: p.NameJaJP, NameZhCN: p.NameZhCN, NameZhTW: p.NameZhCN,
			Banner: p.Banner, IntroEnUS: p.IntroductionEnUS, IntroJaJP: p.IntroductionJaJP,
			IntroZhCN: p.IntroductionZhCN, IntroZhTW: p.IntroductionZhCN,
			ContentLimit: p.ContentLimit, OriginalLanguage: "ja-jp", AgeLimit: "r18",
			Aliases: aliases, TagIDs: tagIDs, OfficialIDs: []int{}, EngineIDs: []int{}, Links: links,
		}

		snapshotJSON, err := json.Marshal(snapshot)
		if err != nil {
			continue
		}

		revRows = append(revRows, revRow{GalgameID: galgameID, UserID: p.UserID, Snapshot: string(snapshotJSON), Created: p.Created})
	}

	if len(revRows) > 0 {
		batchExec(tdb, "galgame_revision",
			[]string{"galgame_id", "revision", "user_id", "action", "note", "snapshot", "is_minor", "created"},
			revRows, func(r revRow) []any {
				return []any{r.GalgameID, 1, r.UserID, "created", "初始版本（从 moyu 迁移）", r.Snapshot, false, r.Created}
			})
	}
	slog.Info("revisions created", "count", len(revRows))

	// ── Step 11: Reset sequences ──
	slog.Info("Step 11: Resetting sequences...")
	for _, seq := range []struct{ table, column string }{
		{"galgame", "id"}, {"galgame_alias", "id"}, {"galgame_link", "id"},
		{"galgame_tag", "id"}, {"galgame_tag_alias", "id"}, {"galgame_revision", "id"},
	} {
		tdb.Exec(fmt.Sprintf(
			"SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 1))",
			seq.table, seq.column, seq.column, seq.table))
	}

	// ── Step 12: Remap patch_id in moyu database ──
	slog.Info("Step 12: Remapping patch_id in moyu database...")
	remapTables := []string{
		"patch_alias", "patch_link", "patch_tag_relation",
		"patch_company_relation", "patch_cover", "patch_screenshot",
		"patch_resource", "patch_comment",
		"patch_char_relation", "patch_person_relation", "patch_release",
		"user_patch_favorite_relation", "user_patch_contribute_relation",
	}

	// Disable FK checks for the duration of the remap
	srcDB.Exec("SET session_replication_role = 'replica'")

	// Two-pass offset remap (same approach as user migration)
	const offset = 10000000

	// Pass 1: shift patch.id first (parent table), then child tables
	result := srcDB.Exec(fmt.Sprintf("UPDATE patch SET id = id + %d", offset))
	slog.Info("remap pass 1", "table", "patch", "rows", result.RowsAffected)
	for _, table := range remapTables {
		result := srcDB.Exec(fmt.Sprintf("UPDATE %s SET patch_id = patch_id + %d", table, offset))
		slog.Info("remap pass 1", "table", table, "rows", result.RowsAffected)
	}

	// Pass 2: create temp mapping table, then batch UPDATE via JOIN
	srcDB.Exec("CREATE TEMP TABLE _patch_id_map (old_id INT PRIMARY KEY, new_id INT NOT NULL)")
	for oldPatchID, newGalgameID := range patchToGalgameID {
		srcDB.Exec("INSERT INTO _patch_id_map (old_id, new_id) VALUES (?, ?)", oldPatchID+offset, newGalgameID)
	}
	slog.Info("remap pass 2: mapping table created", "entries", len(patchToGalgameID))

	srcDB.Exec("UPDATE patch SET id = m.new_id FROM _patch_id_map m WHERE patch.id = m.old_id")
	slog.Info("remap pass 2", "table", "patch")
	for _, table := range remapTables {
		srcDB.Exec(fmt.Sprintf("UPDATE %s t SET patch_id = m.new_id FROM _patch_id_map m WHERE t.patch_id = m.old_id", table))
		slog.Info("remap pass 2", "table", table)
	}
	srcDB.Exec("DROP TABLE _patch_id_map")

	// Re-enable FK checks
	srcDB.Exec("SET session_replication_role = 'DEFAULT'")

	// Verify remap
	var minID, maxID int
	srcDB.Table("patch").Select("MIN(id)").Scan(&minID)
	srcDB.Table("patch").Select("MAX(id)").Scan(&maxID)
	slog.Info("moyu patch_id remap complete", "new_min_id", minID, "new_max_id", maxID)


	// ── Summary ──
	fmt.Println()
	fmt.Println("==================================================")
	fmt.Println("Moyu → Wiki Migration Results")
	fmt.Println("==================================================")
	fmt.Printf("Total patches:       %d\n", len(patches))
	fmt.Printf("Merged (vndb dup):   %d\n", len(mergePatches))
	fmt.Printf("New (with vndb_id):  %d\n", len(newPatches))
	fmt.Printf("New (no vndb_id):    %d\n", len(noVNDBPatches))
	fmt.Printf("Updated bid:         %d\n", updatedBid)
	fmt.Printf("Updated released:    %d\n", updatedReleased)
	fmt.Printf("Aliases migrated:    %d\n", len(validAliases))
	fmt.Printf("Links migrated:      %d\n", len(validLinks))
	fmt.Printf("Tags merged:         %d (new: %d)\n", len(srcTags), newTagCount)
	fmt.Printf("Tag relations:       %d\n", len(validTagRels))
	fmt.Printf("Revisions created:   %d\n", len(revRows))
	fmt.Println("==================================================")

	return nil
}

// ---------------------------------------------------------------------------
// Batch insert helpers (same as migrate-galgame-data)
// ---------------------------------------------------------------------------

func buildBatchInsert[T any](table string, columns []string, rows []T, valueFn func(T) []any) string {
	if len(rows) == 0 {
		return ""
	}
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
		var args []any
		for _, row := range chunk {
			args = append(args, valueFn(row)...)
		}
		if err := db.Exec(sql, args...).Error; err != nil {
			// Fallback to row-by-row
			skipped := 0
			for _, row := range chunk {
				singleSQL := buildBatchInsert(table, columns, []T{row}, valueFn)
				singleArgs := valueFn(row)
				if singleErr := db.Exec(singleSQL, singleArgs...).Error; singleErr != nil {
					skipped++
				}
			}
			if skipped > 0 {
				slog.Warn("batch fallback: skipped rows", "table", table, "offset", i, "skipped", skipped)
			}
		}
		if end%5000 == 0 || end == len(rows) {
			slog.Info("batch progress", "table", table, "done", end, "total", len(rows))
		}
	}
}
