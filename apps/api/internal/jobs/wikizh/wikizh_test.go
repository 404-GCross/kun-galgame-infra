package wikizh

import (
	"context"
	"fmt"
	"os"
	"strings"
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

// TestMain prepares the database when one is offered but NEVER exits early
// without it: the fold, the swap mapping and the packet layout are pure
// functions, and an os.Exit(0) here reported them as "ok" while running none of
// them. Tests that need rows call requireDB and skip individually.
func TestMain(m *testing.M) {
	if dsn := os.Getenv("TEST_DATABASE_DSN"); dsn != "" {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "SKIP db tests: cannot connect: %v\n", err)
		default:
			if err := migrate.Run(db); err != nil {
				fmt.Fprintf(os.Stderr, "SKIP db tests: catalog migrate failed: %v\n", err)
			} else if err := seed.Run(db); err != nil {
				fmt.Fprintf(os.Stderr, "SKIP db tests: catalog seed failed: %v\n", err)
			} else {
				testDB = db
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "SKIP db tests: TEST_DATABASE_DSN is unset")
	}
	os.Exit(m.Run())
}

// requireDB skips one test when no database was supplied, so a DSN-less run is
// honestly reported as "these N skipped" rather than as a silent whole-package
// pass.
func requireDB(t *testing.T) {
	t.Helper()
	if testDB == nil {
		t.Skip("needs TEST_DATABASE_DSN")
	}
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
	requireDB(t)
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
		{WorkID: wLow, Bucket: BucketUsable, Verdict: VerdictUsable, Confidence: 0.85}, // the v1 calibration's relay-MT sat here
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
	requireDB(t)
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

// TestConsensusRequiresUnanimity pins the fold. The v2 calibration ran the
// UNCHANGED compare prompt twice and 6 of 15 verdicts moved, two of them from
// "unsure, no write" to "a_better, 0.90, auto-write" — so agreement across
// rounds is the real signal and confidence is only a floor.
func TestConsensusRequiresUnanimity(t *testing.T) {
	r := func(id int64, v string, c float64) Verdict {
		return Verdict{Key: fmt.Sprintf("w%d", id), WorkID: id, Bucket: BucketCompare, Verdict: v, Confidence: c}
	}
	rounds := [][]Verdict{
		{r(1, VerdictABetter, 0.95), r(2, VerdictABetter, 0.95), r(3, VerdictABetter, 0.95), r(4, VerdictABetter, 0.95)},
		{r(1, VerdictABetter, 0.92), r(2, VerdictBBetter, 0.90), r(3, VerdictABetter, 0.80), r(4, VerdictABetter, 0.95)},
		{r(1, VerdictABetter, 0.99), r(2, VerdictABetter, 0.95), r(3, VerdictABetter, 0.95)}, // work 4 missing
	}
	got, st := Consensus(rounds)
	assert.Equal(t, 3, st.Rounds)
	assert.Equal(t, 4, st.Works)
	assert.Equal(t, 2, st.Unanimous, "works 1 and 3 agreed in every round")
	assert.Equal(t, 1, st.Contested, "work 2 flipped")
	assert.Equal(t, 1, st.Incomplete, "work 4 is missing a round")

	by := map[int64]Verdict{}
	for _, v := range got {
		by[v.WorkID] = v
	}
	// Unanimous keeps the verdict but carries the LOWEST confidence, so an
	// optimistic round cannot satisfy the gate on its own.
	assert.Equal(t, VerdictABetter, by[1].Verdict)
	assert.InDelta(t, 0.92, by[1].Confidence, 0.001)
	assert.Equal(t, VerdictABetter, by[3].Verdict)
	assert.InDelta(t, 0.80, by[3].Confidence, 0.001, "the 0.80 round drags it under the gate")

	// Disagreement and incompleteness both become unsure — never a write.
	for _, id := range []int64{2, 4} {
		assert.Equal(t, VerdictUnsure, by[id].Verdict)
		assert.Zero(t, by[id].Confidence)
		assert.NotEmpty(t, by[id].Reason, "the review pile needs to know WHY it landed there")
	}

	// And that survives Apply: nothing here may be written.
	clean(t)
	for _, id := range []int64{1, 2, 3, 4} {
		w := mkWork(t, true)
		by[id] = Verdict{WorkID: w, Bucket: by[id].Bucket, Verdict: by[id].Verdict, Confidence: by[id].Confidence}
		mkSnapshot(t, w, true, "候选中文。", "日本語。", "机翻。", "")
	}
	var folded []Verdict
	for _, id := range []int64{2, 3, 4} { // the three that must not write
		folded = append(folded, by[id])
	}
	stApply, err := Apply(context.Background(), testDB, folded, true)
	require.NoError(t, err)
	assert.Zero(t, stApply.Written, "disagreement, incompleteness and a sub-gate fold all block the write")
}

// TestConsensusFoldsOnDirection pins the correction that took the review pile
// from 829 to 523. The first fold compared LABELS, so "equivalent" and "unsure"
// counted as dissent and 306 works no round had contradicted were sent to a
// human. Agreement is on the direction; abstentions neither block nor decide.
func TestConsensusFoldsOnDirection(t *testing.T) {
	r := func(id int64, v string, c float64) Verdict {
		return Verdict{Key: fmt.Sprintf("w%d", id), WorkID: id, Bucket: BucketCompare, Verdict: v, Confidence: c}
	}
	rounds := [][]Verdict{
		// 1: two votes and a shrug   → decided (this is the 306)
		// 2: one vote and two shrugs → NOT decided, a lone vote is no consensus
		// 3: genuinely opposed       → contested, the only kind worth a human
		// 4: nobody wanted it        → declined, no write, no human
		// 5: everyone abstained      → abstained
		{r(1, VerdictABetter, 0.95), r(2, VerdictABetter, 0.95), r(3, VerdictABetter, 0.95), r(4, VerdictBBetter, 0.95), r(5, VerdictUnsure, 0.5)},
		{r(1, VerdictABetter, 0.93), r(2, VerdictEquivalent, 0.9), r(3, VerdictBBetter, 0.95), r(4, VerdictEquivalent, 0.9), r(5, VerdictEquivalent, 0.4)},
		{r(1, VerdictEquivalent, 0.10), r(2, VerdictUnsure, 0.5), r(3, VerdictABetter, 0.95), r(4, VerdictBBetter, 0.95), r(5, VerdictUnsure, 0.5)},
	}
	got, st := Consensus(rounds)
	assert.Equal(t, 1, st.Leaning, "work 1: a majority wanted it, one abstained, none objected")
	assert.Equal(t, 1, st.Contested, "only work 3 has rounds pointing opposite ways")
	assert.Equal(t, 1, st.Declined, "work 4: nobody wanted it — no write, and no human either")
	assert.Equal(t, 2, st.Abstained, "works 2 and 5")
	assert.Zero(t, st.Unanimous)

	by := map[int64]Verdict{}
	for _, v := range got {
		by[v.WorkID] = v
	}
	// The gate floor comes from the DECIDING rounds only. Work 1's third round
	// abstained at 0.10; letting that in would veto the decision with a
	// confidence in not deciding.
	assert.Equal(t, VerdictABetter, by[1].Verdict)
	assert.InDelta(t, 0.93, by[1].Confidence, 0.001)
	// A lone vote is not a consensus, however sure of itself it is.
	assert.Equal(t, VerdictUnsure, by[2].Verdict)
	assert.Zero(t, by[2].Confidence)
	// Declining needs no majority — not writing is the safe default.
	assert.Equal(t, VerdictBBetter, by[4].Verdict)
	assert.False(t, restores(BucketCompare, by[4].Verdict))
}

// TestSwapVerdictIsAnInvolution: the adversarial compare round presents the
// texts in the opposite order, so the reply must be mapped back before it
// reaches the verdict file. Applying the mapping twice returns the original —
// which is what makes it safe to run over a whole round.
func TestSwapVerdictIsAnInvolution(t *testing.T) {
	for _, v := range []string{VerdictABetter, VerdictBBetter, VerdictEquivalent, VerdictUnsure} {
		assert.Equal(t, v, SwapVerdict(SwapVerdict(v)), v)
	}
	assert.Equal(t, VerdictBBetter, SwapVerdict(VerdictABetter))
	assert.Equal(t, VerdictABetter, SwapVerdict(VerdictBBetter))
	// A tie or an abstention has no side, so swapping must not invent one.
	assert.Equal(t, VerdictEquivalent, SwapVerdict(VerdictEquivalent))
	assert.Equal(t, VerdictUnsure, SwapVerdict(VerdictUnsure))
}

// TestAdversarialPacketSwapsOnlyCompare: the usable bucket has no A/B, so its
// re-framing lives in the prompt and its packet must be untouched.
func TestAdversarialPacketSwapsOnlyCompare(t *testing.T) {
	c := Candidate{WorkID: 1, Bucket: BucketCompare, Source: "原文", WikiZh: "USERTEXT", MachineZh: "MACHINETEXT"}
	swapped := AdversarialPacket(c)
	a := strings.Index(swapped, "MACHINETEXT")
	b := strings.Index(swapped, "USERTEXT")
	assert.Positive(t, a)
	assert.Positive(t, b)
	assert.Less(t, a, b, "under swap the machine text must be presented first")

	u := Candidate{WorkID: 2, Bucket: BucketUsable, Source: "原文", WikiZh: "USERTEXT"}
	assert.Equal(t, UserPacket(u), AdversarialPacket(u))
}

// TestTiebreakIsScoped pins the adversarial round's authority: it decides the
// works the ordinary rounds contested and NOTHING else. A fourth ordinary vote
// on a 2-1 split leaves it split, which is why this is a separate mechanism
// rather than another round — but a round that can only speak where the others
// deadlocked must not be able to overturn the ones they settled.
func TestTiebreakIsScoped(t *testing.T) {
	v := func(id int64, verdict string, c float64) Verdict {
		return Verdict{Key: fmt.Sprintf("w%d", id), WorkID: id, Bucket: BucketCompare, Verdict: verdict, Confidence: c}
	}
	folded := []Verdict{
		v(1, VerdictABetter, 0.95), // decided by the rounds — off limits
		v(2, VerdictUnsure, 0),     // contested → the tiebreak decides for
		v(3, VerdictUnsure, 0),     // contested → the tiebreak decides against
		v(4, VerdictUnsure, 0),     // contested, tiebreak abstains
		v(5, VerdictUnsure, 0),     // contested, no tiebreak returned
	}
	tie := []Verdict{
		v(1, VerdictBBetter, 0.99), // must be ignored
		v(2, VerdictABetter, 0.93),
		v(3, VerdictBBetter, 0.91),
		v(4, VerdictUnsure, 0.5),
	}
	got, st := Tiebreak(folded, tie)
	by := map[int64]Verdict{}
	for _, g := range got {
		by[g.WorkID] = g
	}
	assert.Equal(t, VerdictABetter, by[1].Verdict, "a settled work is not the tiebreak's to reopen")
	assert.InDelta(t, 0.95, by[1].Confidence, 0.001)
	assert.Equal(t, VerdictABetter, by[2].Verdict)
	assert.Equal(t, VerdictBBetter, by[3].Verdict)
	// An abstaining or absent tiebreak leaves the work in the pile rather than
	// quietly resolving it.
	assert.Equal(t, VerdictUnsure, by[4].Verdict)
	assert.Equal(t, VerdictUnsure, by[5].Verdict)

	assert.Equal(t, 4, st.Eligible)
	assert.Equal(t, 1, st.ResolvedFor)
	assert.Equal(t, 1, st.ResolvedAgainst)
	assert.Equal(t, 1, st.StillUnsure)
	assert.Equal(t, 1, st.NoTiebreak)
}
