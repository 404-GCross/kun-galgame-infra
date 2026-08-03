package wikizh

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

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
		fmt.Fprintln(os.Stderr, "SKIP: TEST_DATABASE_DSN is unset")
		os.Exit(0)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

// ensureSnapshot creates the rescue table the job reads. In prod it is created
// by the wave-168 rescue SQL; the shape is mirrored here so the test exercises
// the real query.
func ensureSnapshot(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`CREATE SCHEMA IF NOT EXISTS src_wiki`).Error)
	require.NoError(t, testDB.Exec(`CREATE TABLE IF NOT EXISTS src_wiki.intro_snapshot (
		work_id bigint PRIMARY KEY, galgame_id bigint NOT NULL, site text, claim_state smallint,
		published boolean NOT NULL,
		wiki_zh_cn text NOT NULL DEFAULT '', wiki_zh_tw text NOT NULL DEFAULT '',
		wiki_ja text NOT NULL DEFAULT '', wiki_en text NOT NULL DEFAULT '',
		catalog_ja text NOT NULL DEFAULT '', catalog_en text NOT NULL DEFAULT '',
		catalog_zh_source text NOT NULL DEFAULT '', catalog_zh_mt text NOT NULL DEFAULT '',
		captured_at timestamptz NOT NULL DEFAULT now())`).Error)
	require.NoError(t, testDB.Exec(`TRUNCATE src_wiki.intro_snapshot`).Error)
}

func clean(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"catalog_work_intro", "catalog_work"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	ensureSnapshot(t)
}

var nextProductID int64 = 700000

func mkWork(t *testing.T, published bool) int64 {
	t.Helper()
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)
	site := "kungal"
	nextProductID++
	pid := nextProductID
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: "w", Site: &site, ProductWorkID: &pid}
	if !published {
		draft := model.ClaimStateDraft
		w.ClaimState = &draft
	}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkSnapshot(t *testing.T, workID int64, published bool, wikiZh, catalogJa, catalogMT, catalogHuman string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_wiki.intro_snapshot
		(work_id, galgame_id, site, published, wiki_zh_cn, catalog_ja, catalog_zh_mt, catalog_zh_source)
		VALUES (?,?,?,?,?,?,?,?)`,
		workID, workID, "kungal", published, wikiZh, catalogJa, catalogMT, catalogHuman).Error)
}

// TestBucketsAreDisjointAndScoped pins how the two questions are split, and the
// three exclusions that keep the job off text it has no business touching.
func TestBucketsAreDisjointAndScoped(t *testing.T) {
	clean(t)
	ctx := context.Background()

	wNoZh := mkWork(t, true)
	mkSnapshot(t, wNoZh, true, "用户手写的完整中文简介。", "日本語のあらすじ。", "", "")

	wMT := mkWork(t, true)
	mkSnapshot(t, wMT, true, "用户手写版本。", "日本語のあらすじ。", "机器翻译版本。", "")

	wHuman := mkWork(t, true) // a curated zh already exists → never touched
	mkSnapshot(t, wHuman, true, "用户手写版本。", "日本語。", "", "后来有人写的中文。")

	wDraft := mkWork(t, false) // not on the public face
	mkSnapshot(t, wDraft, false, "草稿海的中文。", "日本語。", "", "")

	wEmpty := mkWork(t, true) // wiki column was blank
	mkSnapshot(t, wEmpty, true, "   ", "日本語。", "", "")

	usable, err := LoadCandidates(ctx, testDB, BucketUsable, 0)
	require.NoError(t, err)
	require.Len(t, usable, 1, "only the published, wiki-having, zh-less work")
	assert.Equal(t, wNoZh, usable[0].WorkID)
	assert.Equal(t, "ja", usable[0].SourceLang)
	assert.Equal(t, "日本語のあらすじ。", usable[0].Source)

	compare, err := LoadCandidates(ctx, testDB, BucketCompare, 0)
	require.NoError(t, err)
	require.Len(t, compare, 1, "only the work whose slot a machine row holds")
	assert.Equal(t, wMT, compare[0].WorkID)
	assert.Equal(t, "机器翻译版本。", compare[0].MachineZh)

	// The buckets must not overlap — a work judged twice could be written twice
	// under contradictory verdicts.
	assert.NotEqual(t, usable[0].WorkID, compare[0].WorkID)
}

// TestApplyIsPurelyAdditive is the wave's核心 safety property: a restore INSERTs
// a provenance=0 row and never edits or deletes the machine row, so the read
// face switches over while the machine text survives as a fallback and the
// rollback is exactly the receipts.
func TestApplyIsPurelyAdditive(t *testing.T) {
	clean(t)
	ctx := context.Background()
	var curated, bangumi int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key='curated'`).Scan(&curated).Error)
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key='bangumi'`).Scan(&bangumi).Error)

	w := mkWork(t, true)
	mkSnapshot(t, w, true, "用户手写的完整中文简介。", "日本語のあらすじ。", "机器翻译版本。", "")
	// The machine row really exists in catalog_work_intro too.
	require.NoError(t, testDB.Create(&model.CatalogWorkIntro{
		WorkID: w, Lang: "zh-Hans", Intro: "机器翻译版本。", SourceID: bangumi,
		Provenance: 1, MTModel: "glm", SrcHash: "abc"}).Error)

	vs := []Verdict{{Key: fmt.Sprintf("w%d", w), WorkID: w, Bucket: BucketCompare,
		Verdict: VerdictABetter, Confidence: 0.95}}

	// Dry run decides, writes nothing.
	st, err := Apply(ctx, testDB, vs, false)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Restores)
	assert.Zero(t, st.Written)

	st, err = Apply(ctx, testDB, vs, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Written)
	assert.Len(t, st.ReceiptIDs, 1, "receipts identify exactly the row written")

	// The machine row is untouched…
	var mt model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id=? AND provenance=1", w).First(&mt).Error)
	assert.Equal(t, "机器翻译版本。", mt.Intro)
	assert.Equal(t, "glm", mt.MTModel)
	// …and the restored row sits beside it as first-party source text.
	var human model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id=? AND provenance=0", w).First(&human).Error)
	assert.Equal(t, "用户手写的完整中文简介。", human.Intro)
	assert.Equal(t, curated, human.SourceID)

	// Second apply writes zero.
	st, err = Apply(ctx, testDB, vs, true)
	require.NoError(t, err)
	assert.Zero(t, st.Written)
	assert.Equal(t, 1, st.Skipped, "a human row now exists, so the work is no longer eligible")
}

// TestGateAndVocabulary pins the two ways a verdict fails to cause a write.
func TestGateAndVocabulary(t *testing.T) {
	clean(t)
	ctx := context.Background()

	wLow := mkWork(t, true)
	mkSnapshot(t, wLow, true, "边缘案例的中文。", "日本語。", "", "")
	wBogus := mkWork(t, true)
	mkSnapshot(t, wBogus, true, "另一段中文。", "日本語。", "", "")
	wNo := mkWork(t, true)
	mkSnapshot(t, wNo, true, "残片", "日本語。", "", "")

	vs := []Verdict{
		{WorkID: wLow, Bucket: BucketUsable, Verdict: VerdictUsable, Confidence: 0.60},
		// A verdict the model invented — outside the bucket's vocabulary.
		{WorkID: wBogus, Bucket: BucketUsable, Verdict: "definitely_keep", Confidence: 0.99},
		{WorkID: wNo, Bucket: BucketUsable, Verdict: VerdictUnusable, Confidence: 0.97},
	}
	st, err := Apply(ctx, testDB, vs, true)
	require.NoError(t, err)
	assert.Zero(t, st.Written, "nothing here may be written")
	assert.Equal(t, 1, st.BelowGate, "low confidence is held for human review")
	assert.Equal(t, 1, st.Invalid, "an invented verdict cannot cause a write")
	assert.Equal(t, 1, st.Restores, "only the low-confidence one even asked to restore")

	var n int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_work_intro`).Scan(&n).Error)
	assert.EqualValues(t, 0, n)
}

// TestSnapshotMissingIsAPrecondition pins that a missing rescue capture is
// reported as the precondition it is, rather than silently yielding an empty
// candidate set that reads like "nothing to do".
func TestSnapshotMissingIsAPrecondition(t *testing.T) {
	require.NoError(t, testDB.Exec(`DROP TABLE IF EXISTS src_wiki.intro_snapshot`).Error)
	_, err := LoadCandidates(context.Background(), testDB, BucketUsable, 0)
	require.Error(t, err)
	assert.IsType(t, SnapshotMissingError{}, err)
	ensureSnapshot(t)
}

func TestUserPacketShape(t *testing.T) {
	c := Candidate{WorkID: 42, Bucket: BucketCompare, SourceLang: "ja",
		Source: "原文", WikiZh: "甲", MachineZh: "乙"}
	p := UserPacket(c)
	assert.Contains(t, p, "### key: w42", "the key must round-trip so a chunked reply can be reassembled")
	assert.Contains(t, p, "A(用户手写中文)")
	assert.Contains(t, p, "B(机器翻译中文)")

	// The usable bucket has no B side to show.
	p = UserPacket(Candidate{WorkID: 7, Bucket: BucketUsable, Source: "原文", WikiZh: "甲"})
	assert.NotContains(t, p, "B(机器翻译中文)")
}
