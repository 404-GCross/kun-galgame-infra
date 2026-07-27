package tagedges

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/srcvndb"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the wiki tag tables + the src_vndb
// Silver schema co-located in ONE test database — locally the split-pool shape
// collapses to a single DB exactly like production (kun_catalog). CI runs
// `go test -p 1 ./...` so truncating the shared tables here cannot race the
// service-suite truncations.
var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_galgame_wiki_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(
		&model.GalgameTag{}, &model.GalgameTagAlias{}, &model.GalgameTagEdge{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: wiki tag migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := srcvndb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_vndb schema failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"galgame_tag_edge", "galgame_tag_alias", "galgame_tag",
		"src_vndb.tags_parents", "src_vndb.tags",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func seedWikiTag(t *testing.T, name, alias string) int {
	t.Helper()
	tag := &model.GalgameTag{Name: name, Category: "content"}
	require.NoError(t, testDB.Create(tag).Error)
	if alias != "" {
		require.NoError(t, testDB.Create(&model.GalgameTagAlias{GalgameTagID: tag.ID, Name: alias}).Error)
	}
	return tag.ID
}

func seedVndbTag(t *testing.T, id, name string, searchable, applicable bool) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcvndb.Tag{
		ID: id, Cat: "cont", Searchable: searchable, Applicable: applicable, Name: name,
	}).Error)
}

func seedVndbEdge(t *testing.T, child, parent string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcvndb.TagParent{ID: child, Parent: parent, Main: true}).Error)
}

// TestRunProjectsCompressesAndReconciles pins the whole contract in one pass:
// the alias join, direct + compressed edges, the meta-parent filter (the
// "No Romance Plot under Type" case that motivated the DAG route), the prune
// of stale vndb-sourced rows, the user-curated red line, and idempotency.
func TestRunProjectsCompressesAndReconciles(t *testing.T) {
	clean(t)
	ctx := context.Background()

	scifi := seedWikiTag(t, "科幻", "Science Fiction")
	hard := seedWikiTag(t, "硬科幻", "Hard Science Fiction")
	timeTravel := seedWikiTag(t, "时间旅行", "Time Travel")
	noRomance := seedWikiTag(t, "无恋爱剧情", "No Romance Plot")
	romance := seedWikiTag(t, "恋爱", "Romance")
	seedWikiTag(t, "类型", "Type") // mapped but meta — must never become a parent

	seedVndbTag(t, "g1", "Science Fiction", true, true)
	seedVndbTag(t, "g2", "Hard Science Fiction", true, true)
	seedVndbTag(t, "g3", "Middle Unmapped", true, true) // no wiki counterpart
	seedVndbTag(t, "g4", "No Romance Plot", true, true)
	seedVndbTag(t, "g5", "Type", false, false) // meta grouping node
	seedVndbTag(t, "g6", "Time Travel", true, true)
	seedVndbTag(t, "g7", "Romance", true, true)

	seedVndbEdge(t, "g2", "g1") // direct: 科幻 → 硬科幻
	seedVndbEdge(t, "g3", "g1")
	seedVndbEdge(t, "g6", "g3") // compressed: 科幻 → (unmapped) → 时间旅行
	seedVndbEdge(t, "g4", "g5") // meta parent: must yield NO edge
	seedVndbEdge(t, "g7", "g5")

	// Pre-existing rows: one stale vndb edge (pruned), one user edge (kept).
	require.NoError(t, testDB.Create(&model.GalgameTagEdge{ParentID: romance, ChildID: noRomance, Source: "vndb"}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagEdge{ParentID: romance, ChildID: hard, Source: ""}).Error)

	// ── dry run: full plan, zero writes ──
	st, err := Run(ctx, testDB, testDB, Opts{Apply: false})
	require.NoError(t, err)
	require.Equal(t, 6, st.WikiTags)
	require.Equal(t, 7, st.VndbTags)
	require.Equal(t, 6, st.Mapped) // g3 is the only unmapped VNDB tag
	require.Equal(t, 0, st.Ambiguous)
	require.Equal(t, 2, st.Planned) // 科幻→硬科幻 + 科幻→时间旅行, nothing else
	require.Equal(t, 1, st.Compressed)
	require.Equal(t, 2, st.PlannedNew)
	require.Equal(t, 1, st.PlannedPrune)
	require.Equal(t, int64(0), st.Inserted)
	require.Equal(t, int64(0), st.Pruned)
	var n int64
	testDB.Model(&model.GalgameTagEdge{}).Count(&n)
	require.Equal(t, int64(2), n) // untouched: the two pre-seeded rows

	// ── apply ──
	st, err = Run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)
	require.Equal(t, int64(2), st.Inserted)
	require.Equal(t, int64(1), st.Pruned)

	var edges []model.GalgameTagEdge
	require.NoError(t, testDB.Order("parent_id, child_id").Find(&edges).Error)
	require.Len(t, edges, 3)
	type pair struct{ p, c int }
	got := map[pair]string{}
	for _, e := range edges {
		got[pair{e.ParentID, e.ChildID}] = e.Source
	}
	require.Equal(t, "vndb", got[pair{scifi, hard}])
	require.Equal(t, "vndb", got[pair{scifi, timeTravel}])
	require.Equal(t, "", got[pair{romance, hard}]) // user-curated row survived
	// The meta-parent case: 无恋爱剧情 gained no parent edge at all.
	for p := range got {
		require.NotEqual(t, noRomance, p.c, "无恋爱剧情 must not receive a hierarchy parent")
	}

	// ── idempotency: a second --apply writes zero ──
	st, err = Run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)
	require.Equal(t, int64(0), st.Inserted)
	require.Equal(t, int64(0), st.Pruned)
	require.Equal(t, 0, st.PlannedNew)
	require.Equal(t, 0, st.PlannedPrune)
}

// TestRunDropsAmbiguousNames pins the ambiguity red line: a normalized name
// claimed by two different wiki tags maps to NEITHER (counted, never guessed).
func TestRunDropsAmbiguousNames(t *testing.T) {
	clean(t)
	ctx := context.Background()

	a := seedWikiTag(t, "标签甲", "Shared Alias")
	b := seedWikiTag(t, "标签乙", "Shared Alias")
	seedWikiTag(t, "父标签", "Parent Tag")

	seedVndbTag(t, "g1", "Parent Tag", true, true)
	seedVndbTag(t, "g2", "Shared Alias", true, true)
	seedVndbEdge(t, "g2", "g1")

	st, err := Run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)
	require.Equal(t, 1, st.Ambiguous)
	require.Equal(t, 1, st.Mapped) // only Parent Tag joined; g2 stayed unmapped
	require.Equal(t, 0, st.Planned)

	var edges []model.GalgameTagEdge
	require.NoError(t, testDB.Find(&edges).Error)
	require.Empty(t, edges)
	_ = a
	_ = b
}
