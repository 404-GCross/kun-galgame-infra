package main

import (
	"context"
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
	resumeRemap := flag.Bool("resume-remap", false,
		"Skip wiki-side migration; only retry step 13 (moyu patch_id remap) using saved galgame_migrations records. "+
			"Use this when steps 1-11 already committed but step 13 was rolled back.")
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
		fmt.Println()
		fmt.Println("  # If step 13 (moyu remap) failed previously and you need to retry just that:")
		fmt.Println("  go run ./cmd/migrate-moyu-galgame --moyu-dsn=\"...\" --resume-remap")
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

	if *resumeRemap {
		if err := runResumeRemap(srcDB, tdb, *dryRun); err != nil {
			slog.Error("resume failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := run(srcDB, tdb, *dryRun); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

// runResumeRemap retries just step 13 using the (source_db='moyu',
// source_id, galgame_id) tuples that were written during the original
// run. This handles the case where steps 1-11 committed (wiki has new
// galgame rows + galgame_migrations entries) but step 13 was rolled back
// (moyu still has original patch.ids).
//
// Refusing to fall back to the full `run()` path is intentional: that
// path does not handle "wiki already has rows" cleanly and would attempt
// to re-INSERT noVNDBPatches, leading to duplicates.
func runResumeRemap(srcDB, tdb *gorm.DB, dryRun bool) error {
	type row struct {
		SourceID  int
		GalgameID int
	}
	var rows []row
	if err := tdb.Table("galgame_migrations").
		Select("source_id, galgame_id").
		Where("source_db = ?", "moyu").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("load galgame_migrations: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("no moyu records in galgame_migrations — nothing to resume. " +
			"If you intended a fresh run, drop --resume-remap")
	}

	mapping := make(map[int]int, len(rows))
	for _, r := range rows {
		mapping[r.SourceID] = r.GalgameID
	}
	slog.Info("resume: loaded mapping from galgame_migrations", "entries", len(mapping))

	if dryRun {
		fmt.Printf("\n[DRY RUN] Would remap %d patches in moyu DB\n", len(mapping))
		return nil
	}

	if err := remapMoyuPatchIDs(context.Background(), srcDB, mapping); err != nil {
		return err
	}

	var minID, maxID int
	srcDB.Table("patch").Select("MIN(id)").Scan(&minID)
	srcDB.Table("patch").Select("MAX(id)").Scan(&maxID)
	slog.Info("moyu patch_id remap complete (resume)", "new_min_id", minID, "new_max_id", maxID)
	return nil
}

func run(srcDB, tdb *gorm.DB, dryRun bool) error {
	// ── Step 0: Re-run guard ──
	// galgame_migrations is the canonical record of "what's already been
	// migrated". Any pre-existing 'moyu' rows mean a previous run wrote
	// state to wiki. The full path doesn't handle that cleanly (mainly
	// because no-vndb_id patches have no other dedup key), so refuse and
	// point the user at --resume-remap.
	var existingMoyuMigrations int64
	tdb.Table("galgame_migrations").Where("source_db = ?", "moyu").Count(&existingMoyuMigrations)
	if existingMoyuMigrations > 0 && !dryRun {
		return fmt.Errorf(
			"wiki already has %d 'moyu' records in galgame_migrations — refusing to re-run.\n"+
				"  - If step 13 (moyu patch_id remap) failed previously and you want to retry it:\n"+
				"      run with --resume-remap\n"+
				"  - If you want a clean re-run (e.g. after restoring both DBs from backup):\n"+
				"      DELETE FROM galgame_migrations WHERE source_db = 'moyu';  (run on wiki DB)",
			existingMoyuMigrations,
		)
	}

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
		// Moyu's `released` is a free-form legacy string; convert via the
		// shared parser. Only overwrite when the wiki row has no date.
		date, prec := model.ParseLegacyReleased(p.Released)
		if tba := prec == model.PrecisionTBA; date != nil || tba {
			result := tdb.Exec(
				"UPDATE galgame SET release_date = ?, release_date_tba = ? "+
					"WHERE id = ? AND release_date IS NULL AND release_date_tba = false",
				date, tba, wikiID,
			)
			if result.RowsAffected > 0 {
				updatedReleased++
			}
		}
		// Audit trail: this moyu patch corresponds to an existing wiki galgame
		// (vndb_id-merge case). Recorded so step 13 / --resume-remap knows
		// where to redirect this patch.id.
		tdb.Exec(
			"INSERT INTO galgame_migrations (source_db, source_id, galgame_id, created_at) "+
				"VALUES (?, ?, ?, NOW()) ON CONFLICT (source_db, source_id) DO NOTHING",
			"moyu", p.ID, wikiID,
		)
	}
	slog.Info("updated existing galgames", "bid", updatedBid, "released", updatedReleased)

	// ── Step 5: Insert new galgames ──
	slog.Info("Step 5: Inserting new galgames...")
	allNew := append(newPatches, noVNDBPatches...)
	if len(allNew) > 0 {
		batchExec(tdb, "galgame",
			[]string{"id", "vndb_id", "bid", "release_date", "release_date_tba",
				"name_en_us", "name_ja_jp", "name_zh_cn", "name_zh_tw",
				"banner", "intro_en_us", "intro_ja_jp", "intro_zh_cn", "intro_zh_tw",
				"content_limit", "status", "view", "resource_update_time",
				"original_language", "age_limit", "user_id", "series_id", "created", "updated"},
			allNew, func(p MoyuPatch) []any {
				galgameID := patchToGalgameID[p.ID]
				vndbID := ""
				if p.VNDBID != nil {
					vndbID = *p.VNDBID
				}
				rDate, rPrec := model.ParseLegacyReleased(p.Released)
				return []any{
					galgameID, vndbID, p.BID, rDate, rPrec == model.PrecisionTBA,
					p.NameEnUS, p.NameJaJP, p.NameZhCN, p.NameZhCN, // name_zh_tw = name_zh_cn
					p.Banner, p.IntroductionEnUS, p.IntroductionJaJP, p.IntroductionZhCN, p.IntroductionZhCN, // intro_zh_tw = intro_zh_cn
					p.ContentLimit, p.Status, p.View, p.ResourceUpdateTime,
					"ja-jp", "r18", p.UserID, nil, p.Created, p.Updated,
				}
			})

		// Audit trail. Critical for re-run safety: noVNDBPatches have no
		// other dedup key (no vndb_id, no bid guarantee), so the only way
		// a re-run can know "this patch already became wiki galgame X" is
		// through this table. ON CONFLICT DO NOTHING tolerates
		// idempotent re-execution within a single run.
		batchExec(tdb, "galgame_migrations",
			[]string{"source_db", "source_id", "galgame_id", "created_at"},
			allNew, func(p MoyuPatch) []any {
				return []any{"moyu", p.ID, patchToGalgameID[p.ID], time.Now()}
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

		rDate, rPrec := model.ParseLegacyReleased(p.Released)
		var rDateStr *string
		if rDate != nil {
			s := rDate.UTC().Format("2006-01-02")
			rDateStr = &s
		}
		snapshot := model.Snapshot{
			VNDBID: vndbID, BangumiID: p.BID, ReleaseDate: rDateStr, ReleaseDateTBA: rPrec == model.PrecisionTBA,
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
	// Atomic: single transaction over patch.id + 13 FK columns. If any
	// step fails, the whole remap rolls back — no half-shifted state to
	// recover from manually.
	slog.Info("Step 12: Remapping patch_id in moyu database (transactional)...")
	if err := remapMoyuPatchIDs(context.Background(), srcDB, patchToGalgameID); err != nil {
		return fmt.Errorf("remap patch_id: %w", err)
	}

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
// Patch-id remap (transactional, two-pass offset)
// ---------------------------------------------------------------------------

// remapMoyuPatchIDs rewrites moyu's `patch.id` and every FK column that
// references it, mapping old (moyu) ids to new (wiki galgame) ids.
//
// Algorithm and shape mirror migrate-users' remapUserIDsGeneric:
//
//   1. Single transaction over the whole remap. Any failure → full rollback;
//      the moyu DB never lands in a half-shifted state.
//   2. ALTER TABLE … DISABLE TRIGGER ALL on every affected table — needed
//      because patch.id and child patch_id columns are linked by FK
//      constraints whose triggers would block intermediate values.
//      Trigger state is transactional, so a rollback restores it
//      automatically; the deferred ENABLE handles the commit path.
//   3. CREATE TEMP TABLE _patch_id_map ON COMMIT DROP — temp table goes
//      away with the transaction either way.
//   4. Two-pass offset (+10_000_000) avoids PK collisions when the
//      old-id and new-id ranges overlap.
//   5. Reset patch.id sequence so subsequent INSERTs don't collide.
//
// All 13 FK columns referencing patch.id (per prisma/moyu schema audit)
// are listed in fkTables — patch_alias, patch_link, patch_cover,
// patch_screenshot, patch_resource, patch_comment, patch_release,
// patch_tag_relation, patch_company_relation, patch_char_relation,
// patch_person_relation, user_patch_favorite_relation,
// user_patch_contribute_relation.
//
// Tables that are mentioned but don't exist in this DB are skipped
// silently (e.g. `patch_company_relation` may not have been created in
// some environments).
func remapMoyuPatchIDs(ctx context.Context, srcDB *gorm.DB, mapping map[int]int) error {
	if len(mapping) == 0 {
		slog.Info("remap: empty mapping, nothing to do")
		return nil
	}

	fkTables := []string{
		"patch_alias",
		"patch_link",
		"patch_tag_relation",
		"patch_company_relation",
		"patch_cover",
		"patch_screenshot",
		"patch_resource",
		"patch_comment",
		"patch_char_relation",
		"patch_person_relation",
		"patch_release",
		"user_patch_favorite_relation",
		"user_patch_contribute_relation",
	}

	return srcDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// All affected tables, including parent `patch`.
		tableSet := map[string]bool{`"patch"`: true}
		for _, t := range fkTables {
			tableSet[fmt.Sprintf(`"%s"`, t)] = true
		}

		// Filter to tables that actually exist in this DB.
		var existingTables []string
		tx.Raw("SELECT tablename FROM pg_tables WHERE schemaname = 'public'").Scan(&existingTables)
		existsMap := make(map[string]bool, len(existingTables))
		for _, t := range existingTables {
			existsMap[t] = true
		}
		activeTables := make([]string, 0, len(tableSet))
		for t := range tableSet {
			name := strings.Trim(t, `"`)
			if existsMap[name] {
				activeTables = append(activeTables, t)
			}
		}

		// Disable FK triggers on every affected table.
		for _, t := range activeTables {
			if err := tx.Exec(fmt.Sprintf("ALTER TABLE %s DISABLE TRIGGER ALL", t)).Error; err != nil {
				return fmt.Errorf("disable triggers on %s: %w", t, err)
			}
		}
		// On commit, restore. (On rollback, postgres restores automatically
		// since DISABLE TRIGGER is itself transactional — but the defer is
		// still correct for the commit path.)
		defer func() {
			for _, t := range activeTables {
				_ = tx.Exec(fmt.Sprintf("ALTER TABLE %s ENABLE TRIGGER ALL", t)).Error
			}
		}()

		// Temp mapping table — drops on commit so re-runs don't collide.
		if err := tx.Exec(
			"CREATE TEMP TABLE _patch_id_map (old_id INT PRIMARY KEY, new_id INT NOT NULL) ON COMMIT DROP",
		).Error; err != nil {
			return fmt.Errorf("create temp table: %w", err)
		}

		// Bulk insert mapping rows in 1000-row batches.
		const insertBatch = 1000
		batch := make([]string, 0, insertBatch)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			sql := "INSERT INTO _patch_id_map (old_id, new_id) VALUES " + strings.Join(batch, ",")
			if err := tx.Exec(sql).Error; err != nil {
				return fmt.Errorf("insert mapping batch: %w", err)
			}
			batch = batch[:0]
			return nil
		}
		for oldID, newID := range mapping {
			batch = append(batch, fmt.Sprintf("(%d,%d)", oldID, newID))
			if len(batch) >= insertBatch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}
		slog.Info("remap: mapping table populated", "entries", len(mapping))

		const offset = 10_000_000

		// Pass 1: shift every mapped id (and its FKs) into the +offset range.
		// FK columns are processed first; patch.id is last so child rows
		// always have the parent visible in some form during the shift.
		for _, t := range fkTables {
			if !existsMap[t] {
				continue
			}
			sql := fmt.Sprintf(
				`UPDATE "%s" SET patch_id = "%s".patch_id + %d FROM _patch_id_map WHERE "%s".patch_id = _patch_id_map.old_id`,
				t, t, offset, t,
			)
			res := tx.Exec(sql)
			if res.Error != nil {
				return fmt.Errorf("remap pass1 %s.patch_id: %w", t, res.Error)
			}
			slog.Info("remap pass1", "table", t, "rows", res.RowsAffected)
		}
		if res := tx.Exec(fmt.Sprintf(
			`UPDATE "patch" SET id = id + %d FROM _patch_id_map WHERE "patch".id = _patch_id_map.old_id`, offset,
		)); res.Error != nil {
			return fmt.Errorf("remap pass1 patch.id: %w", res.Error)
		} else {
			slog.Info("remap pass1", "table", "patch", "rows", res.RowsAffected)
		}

		// Pass 2: replace shifted ids with their final new ids.
		// Order is reversed (parent first) for the same reason — keep child
		// rows pointing at a valid parent at every intermediate step.
		if res := tx.Exec(fmt.Sprintf(
			`UPDATE "patch" SET id = _patch_id_map.new_id FROM _patch_id_map WHERE "patch".id = _patch_id_map.old_id + %d`, offset,
		)); res.Error != nil {
			return fmt.Errorf("remap pass2 patch.id: %w", res.Error)
		} else {
			slog.Info("remap pass2", "table", "patch", "rows", res.RowsAffected)
		}
		for _, t := range fkTables {
			if !existsMap[t] {
				continue
			}
			sql := fmt.Sprintf(
				`UPDATE "%s" SET patch_id = _patch_id_map.new_id FROM _patch_id_map WHERE "%s".patch_id = _patch_id_map.old_id + %d`,
				t, t, offset,
			)
			res := tx.Exec(sql)
			if res.Error != nil {
				return fmt.Errorf("remap pass2 %s.patch_id: %w", t, res.Error)
			}
			slog.Info("remap pass2", "table", t, "rows", res.RowsAffected)
		}

		// Resync the patch.id sequence so subsequent INSERTs don't collide.
		if err := tx.Exec(
			`SELECT setval(pg_get_serial_sequence('"patch"', 'id'), (SELECT COALESCE(MAX(id), 1) FROM "patch"))`,
		).Error; err != nil {
			slog.Warn("failed to reset patch ID sequence", "error", err)
		}

		return nil
	})
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
