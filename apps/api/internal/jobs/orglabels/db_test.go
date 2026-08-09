package orglabels

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"
	srcv "api/internal/platform/catalog/srcvndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		// No password: pgx reads PGPASSWORD / ~/.pgpass for localhost.
		dsn = "host=localhost port=5432 user=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: no test db: %v\n", err)
		os.Exit(0)
	}
	for _, step := range []func(*gorm.DB) error{migrate.Run, seed.Run, srcb.EnsureSchema, srcv.EnsureSchema} {
		if err := step(db); err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: setup: %v\n", err)
			os.Exit(0)
		}
	}
	// erogamespace staging stand-ins (same shape the EG adapter reads).
	for _, s := range []string{
		`CREATE TABLE IF NOT EXISTS brands (id int, pk text, raw jsonb, synced_at timestamptz)`,
		`CREATE TABLE IF NOT EXISTS games (id bigint, raw jsonb)`,
		// a leftover games table (importer suite) may predate these columns.
		`ALTER TABLE games ADD COLUMN IF NOT EXISTS brand_id integer`,
		`ALTER TABLE games ADD COLUMN IF NOT EXISTS raw jsonb`,
		`ALTER TABLE brands ADD COLUMN IF NOT EXISTS raw jsonb`,
	} {
		if err := db.Exec(s).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: %s: %v\n", s, err)
			os.Exit(0)
		}
	}
	testDB = db
	os.Exit(m.Run())
}

// cleanAll truncates every table this suite touches, so each run starts fresh.
func cleanAll(t *testing.T) {
	t.Helper()
	tables := []string{
		"catalog_external_ref", "catalog_match_rejection",
		"catalog_work_label", "catalog_label_intro", "catalog_label_alias",
		"catalog_revision", "catalog_label", "catalog_work",
		"src_vndb.producers", "src_vndb.releases_vn", "src_vndb.releases_producers",
		"src_bangumi.person", "src_bangumi.subject_person", "brands", "games",
	}
	for _, tbl := range tables {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkWork(t *testing.T, id int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWork{
		ID: id, MediumID: 1, OLang: "ja", DisplayName: fmt.Sprintf("W%d", id),
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
	}).Error)
}

func mkLabel(t *testing.T, id int64, name string, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogLabel{ID: id, DisplayName: name, Kind: kind}).Error)
}

func mkEdge(t *testing.T, work, label int64, kind int16) {
	t.Helper()
	src := model.LabelKindGameBrand
	require.NoError(t, testDB.Create(&model.CatalogWorkLabel{WorkID: work, LabelID: label, Kind: kind, SourceID: &src}).Error)
}

func mkWorkAnchor(t *testing.T, source int16, ext string, work int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by, created_at)
		 VALUES (5, ?, ?, ?, 0, 'rule:test-work', now())`, work, source, ext).Error)
}

func countRefs(t *testing.T, entityType, source int16) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_external_ref WHERE entity_type=? AND source_id=?`, entityType, source).Scan(&n).Error)
	return n
}

func TestAnchorAndEnrich_VNDB(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	// Works: 901,902 attributed to label 800 (brand); 903 unattributed.
	mkWork(t, 901)
	mkWork(t, 902)
	mkWork(t, 903)
	mkLabel(t, 800, "アージュ", model.LabelKindGameBrand)
	mkEdge(t, 901, 800, model.WorkLabelKindBrand)
	mkEdge(t, 902, 800, model.WorkLabelKindBrand)
	mkWorkAnchor(t, sourceVNDB, "v901", 901)
	mkWorkAnchor(t, sourceVNDB, "v902", 902)
	mkWorkAnchor(t, sourceVNDB, "v903", 903)

	// Producer p1 (co) → works 901,902 (share 2 → exact coworks).
	// Producer p2 (co) → work 903 (unattributed, no name match → new label).
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.producers (id,type,lang,name,latin,alias,description) VALUES
		('p1','co','ja','アージュ','age','エイジ'||E'\n'||'age-alias','age is a Japanese developer of visual novels'),
		('p2','co','ja','新ブランド','','','')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_vn (id,vid,rtype) VALUES
		('r1','v901','complete'),('r2','v902','complete'),('r3','v903','complete')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_producers (id,pid,developer,publisher) VALUES
		('r1','p1',true,false),('r2','p1',true,false),('r3','p2',true,false)`).Error)

	ctx := context.Background()
	st, err := anchorAll(ctx, testDB, testDB, "vndb", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.AnchorsExact, "p1 exact-anchors label 800")
	assert.Equal(t, 1, st.NewLabels, "p2 mints a new label")
	assert.Equal(t, 1, st.NewEdges, "…with one work_label edge on work 903")

	// p1 → label 800 exact.
	var labelID int64
	require.NoError(t, testDB.Raw(
		`SELECT entity_id FROM catalog_external_ref WHERE entity_type=3 AND source_id=? AND external_id='p1' AND link_kind=0`,
		sourceVNDB).Scan(&labelID).Error)
	assert.Equal(t, int64(800), labelID)

	// p2 minted a label with a self exact ref + revision + edge on 903.
	var newLabel int64
	require.NoError(t, testDB.Raw(
		`SELECT entity_id FROM catalog_external_ref WHERE entity_type=3 AND source_id=? AND external_id='p2' AND link_kind=0`,
		sourceVNDB).Scan(&newLabel).Error)
	assert.NotZero(t, newLabel)
	var edgeCount, revCount int64
	testDB.Raw(`SELECT count(*) FROM catalog_work_label WHERE label_id=? AND work_id=903`, newLabel).Scan(&edgeCount)
	testDB.Raw(`SELECT count(*) FROM catalog_revision WHERE entity_type=3 AND entity_id=? AND revision=1`, newLabel).Scan(&revCount)
	assert.Equal(t, int64(1), edgeCount)
	assert.Equal(t, int64(1), revCount)

	// ── second run: zero new writes (idempotent) ─────────────────────────────
	before := countRefs(t, 3, sourceVNDB)
	st2, err := anchorAll(ctx, testDB, testDB, "vndb", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 0, st2.AnchorsExact)
	assert.Equal(t, 0, st2.NewLabels)
	assert.Equal(t, 2, st2.Already, "both p1 and p2 already anchored")
	assert.Equal(t, before, countRefs(t, 3, sourceVNDB), "no new refs on rerun")

	// ── intro + alias enrichment ─────────────────────────────────────────────
	est, err := enrichAll(ctx, testDB, testDB, "intro", true)
	require.NoError(t, err)
	assert.Equal(t, 1, est.IntroWritten, "p1 description → label 800 intro")
	var lang, intro string
	require.NoError(t, testDB.Raw(
		`SELECT lang, intro FROM catalog_label_intro WHERE label_id=800 AND source_id=?`, sourceVNDB).Row().Scan(&lang, &intro))
	assert.Equal(t, "en", lang)
	assert.Contains(t, intro, "age is a Japanese developer")

	ast, err := enrichAll(ctx, testDB, testDB, "alias", true)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ast.AliasWritten, 2, "both alias lines land as spelling variants")
	var aliasKinds []int16
	require.NoError(t, testDB.Raw(`SELECT kind FROM catalog_label_alias WHERE label_id=800 ORDER BY name`).Scan(&aliasKinds).Error)
	for _, k := range aliasKinds {
		assert.Equal(t, model.AliasKindSpellingVariant, k)
	}

	// enrichment is idempotent too.
	est2, err := enrichAll(ctx, testDB, testDB, "intro", true)
	require.NoError(t, err)
	assert.Equal(t, 0, est2.IntroWritten)
	ast2, err := enrichAll(ctx, testDB, testDB, "alias", true)
	require.NoError(t, err)
	assert.Equal(t, 0, ast2.AliasWritten)
}

// TestAnchor_ProbableIdempotent locks the fill-missing invariant that a label
// already probable-anchored by a source is never re-anchored on a second run:
// two same-named producers with no shared works both name-match one label; the
// first wins a probable anchor, the second conflicts, and a rerun writes zero.
func TestAnchor_ProbableIdempotent(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkLabel(t, 810, "SharedCorp", model.LabelKindGameBrand) // no edges, no anchors
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.producers (id,type,lang,name,latin,alias,description) VALUES
		('p4','co','en','SharedCorp','','',''),
		('p5','co','en','SharedCorp','','','')`).Error)

	ctx := context.Background()
	st, err := anchorAll(ctx, testDB, testDB, "vndb", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.AnchorsProbable, "one of the two same-name producers claims label 810")
	assert.Equal(t, 1, st.Conflict, "the other conflicts")

	st2, err := anchorAll(ctx, testDB, testDB, "vndb", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 0, st2.AnchorsProbable, "rerun writes no new probable")
	assert.Equal(t, 0, st2.AnchorsExact)
	assert.Equal(t, 0, st2.NewLabels)
}

// A human ruling must outlive the rule that produced the bad anchor. Deleting
// the wrong external_ref row is not enough — the co-work rule re-derives it
// from the same upstream data on the very next run (wave 198: a stripped
// erogamescape anchor was back within the minute, because the store lists a
// disbanded brand's titles under its successor's maker page). The rejection
// row is the durable record, and this pins that the anchorer honours it.
func TestAnchorSkipsAHumanRejectedPairing(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 701)
	mkWork(t, 702)
	mkLabel(t, 600, "アリスソフト", model.LabelKindGameBrand)
	mkEdge(t, 701, 600, model.WorkLabelKindBrand)
	mkEdge(t, 702, 600, model.WorkLabelKindBrand)
	mkWorkAnchor(t, sourceEG, "701", 701)
	mkWorkAnchor(t, sourceEG, "702", 702)
	require.NoError(t, testDB.Exec(`INSERT INTO brands (id, raw) VALUES
		(50, '{"id":50,"kind":"CORPORATION","brandname":"アリスソフト"}'::jsonb)`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO games (id, brand_id) VALUES (701,50),(702,50)`).Error)

	// Two shared works would otherwise be an exact anchor.
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_match_rejection (entity_type, entity_id, source_id, external_id, reason)
		VALUES (3, 600, ?, '50', 'different company, same catalogue via succession')`, sourceEG).Error)

	ctx := context.Background()
	st, err := anchorAll(ctx, testDB, testDB, "eg", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.SkipRejected, "the rejected pairing is skipped and counted")
	assert.Equal(t, 0, st.AnchorsExact, "and never anchored")

	var refs int64
	testDB.Raw(`SELECT count(*) FROM catalog_external_ref WHERE entity_type=3 AND source_id=? AND external_id='50'`,
		sourceEG).Scan(&refs)
	assert.Zero(t, refs, "no anchor row may exist for a rejected pairing")
}

func TestAnchorAndEnrich_EG(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 701)
	mkWork(t, 702)
	mkLabel(t, 600, "アリスソフト", model.LabelKindGameBrand)
	mkEdge(t, 701, 600, model.WorkLabelKindBrand)
	mkEdge(t, 702, 600, model.WorkLabelKindBrand)
	mkWorkAnchor(t, sourceEG, "701", 701) // EG anchors external_id = game id
	mkWorkAnchor(t, sourceEG, "702", 702)

	// Brand 50 (CORPORATION) → games 701,702 → works 701,702 (share 2 → exact).
	require.NoError(t, testDB.Exec(`INSERT INTO brands (id, raw) VALUES
		(50, '{"id":50,"kind":"CORPORATION","brandname":"アリスソフト","makername":"株式会社アリスソフト","brandfurigana":"アリスソフト","makerfurigana":"カブシキガイシャアリスソフト","url":"https://www.alicesoft.com/","twitter":"alice_soft","cien":"12345"}'::jsonb)`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO games (id, brand_id) VALUES (701,50),(702,50)`).Error)

	ctx := context.Background()
	st, err := anchorAll(ctx, testDB, testDB, "eg", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.AnchorsExact, "brand 50 exact-anchors label 600")
	var labelID int64
	require.NoError(t, testDB.Raw(
		`SELECT entity_id FROM catalog_external_ref WHERE entity_type=3 AND source_id=? AND external_id='50' AND link_kind=0`,
		sourceEG).Scan(&labelID).Error)
	assert.Equal(t, int64(600), labelID)

	// alias (furigana → search hint) + link (site/twitter/cien).
	ast, err := enrichAll(ctx, testDB, testDB, "alias", true)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ast.AliasWritten, 1)
	var hintCount int64
	testDB.Raw(`SELECT count(*) FROM catalog_label_alias WHERE label_id=600 AND kind=?`, model.AliasKindSearchHint).Scan(&hintCount)
	assert.GreaterOrEqual(t, hintCount, int64(1))

	lst, err := enrichAll(ctx, testDB, testDB, "link", true)
	require.NoError(t, err)
	assert.Equal(t, 3, lst.LinkWritten, "official site + twitter + cien")
	var links []struct {
		SourceID   int16  `gorm:"column:source_id"`
		ExternalID string `gorm:"column:external_id"`
		LinkKind   int16  `gorm:"column:link_kind"`
	}
	require.NoError(t, testDB.Raw(
		`SELECT source_id, external_id, link_kind FROM catalog_external_ref WHERE entity_type=3 AND entity_id=600 AND link_kind=2 ORDER BY source_id`).
		Scan(&links).Error)
	require.Len(t, links, 3)
	assert.Equal(t, sourceOfficialSite, links[0].SourceID)
	assert.Equal(t, "www.alicesoft.com", links[0].ExternalID)
	assert.Equal(t, sourceTwitter, links[1].SourceID)
	assert.Equal(t, "alice_soft", links[1].ExternalID)
	assert.Equal(t, sourceCien, links[2].SourceID)
	assert.Equal(t, "12345", links[2].ExternalID)

	// links idempotent.
	lst2, err := enrichAll(ctx, testDB, testDB, "link", true)
	require.NoError(t, err)
	assert.Equal(t, 0, lst2.LinkWritten)
}

func TestAnchorAndEnrich_Bangumi(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 501)
	mkWork(t, 502)
	mkLabel(t, 400, "ういんどみる", model.LabelKindGameBrand)
	mkEdge(t, 501, 400, model.WorkLabelKindBrand)
	mkEdge(t, 502, 400, model.WorkLabelKindBrand)
	mkWorkAnchor(t, sourceBangumi, "9001", 501) // external_id = subject id
	mkWorkAnchor(t, sourceBangumi, "9002", 502)

	// Person 300 (type 2 company) → subjects 9001,9002 (share 2 → exact).
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.person (id,name,type,summary,comments,collects,parser_version,ingested_at,infobox_raw,infobox_parsed,parse_error) VALUES
		(300,'ういんどみる',2,'ういんどみるはゲームブランド',0,0,'v1',now(),'',
		 '{"Fields":[{"Key":"官网","Value":"http://windmill.suki.jp/"},{"Key":"Twitter","Value":"windmill_web"}]}'::jsonb,'')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_person (person_id,subject_id,position,appear_eps) VALUES
		(300,9001,1,''),(300,9002,1,'')`).Error)

	ctx := context.Background()
	st, err := anchorAll(ctx, testDB, testDB, "bangumi", 0, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.AnchorsExact)
	var labelID int64
	require.NoError(t, testDB.Raw(
		`SELECT entity_id FROM catalog_external_ref WHERE entity_type=3 AND source_id=? AND external_id='300' AND link_kind=0`,
		sourceBangumi).Scan(&labelID).Error)
	assert.Equal(t, int64(400), labelID)

	// intro (summary → ja) + link (infobox site + twitter).
	est, err := enrichAll(ctx, testDB, testDB, "intro", true)
	require.NoError(t, err)
	assert.Equal(t, 1, est.IntroWritten)
	var lang string
	require.NoError(t, testDB.Raw(`SELECT lang FROM catalog_label_intro WHERE label_id=400 AND source_id=?`, sourceBangumi).Row().Scan(&lang))
	assert.Equal(t, "ja", lang)

	lst, err := enrichAll(ctx, testDB, testDB, "link", true)
	require.NoError(t, err)
	assert.Equal(t, 2, lst.LinkWritten, "infobox official site + twitter")
	var siteID, twID int64
	testDB.Raw(`SELECT count(*) FROM catalog_external_ref WHERE entity_id=400 AND source_id=? AND external_id='windmill.suki.jp'`, sourceOfficialSite).Scan(&siteID)
	testDB.Raw(`SELECT count(*) FROM catalog_external_ref WHERE entity_id=400 AND source_id=? AND external_id='windmill_web'`, sourceTwitter).Scan(&twID)
	assert.Equal(t, int64(1), siteID)
	assert.Equal(t, int64(1), twID)
}

// A merged-away label must leave the name index the moment it is retired.
// Otherwise it keeps competing for identity: the spine sees two same-named
// labels, declines to anchor, and files the merge candidate again — so
// executing the merge could never clear the ambiguity that raised it.
func TestNameIndexExcludesRetiredLabels(t *testing.T) {
	cleanAll(t)
	mkLabel(t, 41, "NEXTON", model.LabelKindPublisher)
	mkLabel(t, 13231, "NEXTON", model.LabelKindPublisher)
	dlsite := sourceDlsite
	require.NoError(t, testDB.Create(&model.CatalogLabelAlias{
		LabelID: 13231, Lang: "ja", Name: "ネクストン", SourceID: &dlsite}).Error)

	norms, err := loadLabelNorms(testDB)
	require.NoError(t, err)
	require.Len(t, norms["nexton"], 2, "both are live, so both answer to the name")

	require.NoError(t, testDB.Delete(&model.CatalogLabel{}, 13231).Error)

	norms, err = loadLabelNorms(testDB)
	require.NoError(t, err)
	assert.Equal(t, []int64{41}, norms["nexton"], "the retired twin is gone from the index")
	assert.Empty(t, norms["ネクストン"], "and so is the alias it carried")

	disp, err := loadLabelDisplayNorms(testDB)
	require.NoError(t, err)
	assert.Equal(t, []int64{41}, disp["nexton"])
}
