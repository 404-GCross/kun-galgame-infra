package wikirescue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// w146_test.go — the W1-pre INTRO remainder, steps u and v (refs/proj/146,
// refs/plans/10-data-layer-retirement/02-w1pre-bridge-nativization.md §6.6).
//
// The two steps split one population — claimed bodies whose ja and en intro
// columns are blank while a zh column holds text — on a language verdict, so the
// suite pins four things per step: the SPLIT (each verdict goes to the right
// step, or to neither), the WRITE (u empties the held column and fills the ja one;
// v lands a source-shaped zh row), the RED LINE (u's correction survives a step-q
// pass, which a direct catalog insert would not), and CONVERGENCE (a second pass
// plans nothing).
//
// TestMain / the pool wiring live in w2_test.go; the mirror fixtures in w140_test.go.

// Realistic verdict fixtures. The classifier reads the text, so a fake string
// would prove nothing — these are shaped like the rows the survey found.
const (
	// jaMisfiled is a Japanese original typed into the Chinese column: kana
	// density ~0.55, far above kanaJaDensity.
	jaMisfiled = "夏の終わりに、彼女は静かに微笑んだ。あの日の約束を、僕はまだ覚えている。学園の屋上から見えた海の色を、きっと一生忘れないだろう。"
	// zhTranslated is a genuine Chinese translation quoting two Japanese proper
	// nouns: kana present but density ~0.06, well under kanaGrayDensity.
	zhTranslated = "夏日的尾声，她静静地微笑了。主人公「なつき」与青梅竹马在学园屋顶上眺望大海，那约定至今仍然清晰地留在记忆之中，成为无法忘却的风景。"
	// zhTwTranslated is the same kind of text in the traditional column.
	zhTwTranslated = "夏日的尾聲，她靜靜地微笑了。主人公與青梅竹馬在學園屋頂上眺望大海，那約定至今仍然清晰地留在記憶之中，成為無法忘卻的風景。"
	// grayBilingual holds the Chinese translation AND the Japanese original in one
	// field — the band no single-column move can split (density ~0.30).
	grayBilingual = "游戏介绍：夏日的尾声，她静静地微笑了，那约定至今仍留在记忆中。\n夏の終わりに、彼女は静かに微笑んだ。あの日の約束を、僕はまだ覚えている。"
	// grayShortJa is Japanese but too short for a density to be evidence (under
	// kanaMinCJK), so it is parked instead of moved.
	grayShortJa = "犯人は誰だ！"
)

// introRemainderGalgameIDs are the wiki body ids this suite owns. It needs more
// bodies than the mirror suite (one per language verdict, times the two zh
// columns), so it clears its own range on entry — a targeted DELETE, never a
// TRUNCATE, since the galgame table is shared with the other suites.
var introRemainderGalgameIDs = []int64{7401, 7402, 7403, 7404, 7405, 7406}

// resetIntroRemainder prepares the mirror fixtures plus the one wiki column steps
// u and v need that no earlier wave touched: `updated`, the body's own change
// marker that step u bumps.
func resetIntroRemainder(t *testing.T) {
	t.Helper()
	resetMirror(t)
	require.NoError(t, testDB.Exec(
		`ALTER TABLE galgame ADD COLUMN IF NOT EXISTS updated timestamptz NOT NULL DEFAULT now()`).Error)
	require.NoError(t, testDB.Exec(
		`DELETE FROM galgame WHERE id IN ?`, introRemainderGalgameIDs).Error)
}

// mkZhBody inserts a wiki body carrying only intro text — the four intro columns
// spelled out, since these steps read all four and w140's mkBody pins zh_tw empty.
func mkZhBody(t *testing.T, id int64, introJa, introEn, introZhCN, introZhTW string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO galgame
		(id, catalog_work_id, user_id, name_ja_jp, name_en_us, name_zh_cn, name_zh_tw,
		 intro_ja_jp, intro_en_us, intro_zh_cn, intro_zh_tw)
		VALUES (?, 0, 0, '', '', '', '', ?, ?, ?, ?)`,
		id, introJa, introEn, introZhCN, introZhTW).Error)
}

// wikiIntros reads one body's four intro columns back.
func wikiIntros(t *testing.T, galgameID int64) (ja, en, zhCN, zhTW string) {
	t.Helper()
	var got struct {
		IntroJaJP string `gorm:"column:intro_ja_jp"`
		IntroEnUS string `gorm:"column:intro_en_us"`
		IntroZhCN string `gorm:"column:intro_zh_cn"`
		IntroZhTW string `gorm:"column:intro_zh_tw"`
	}
	require.NoError(t, testDB.Raw(
		`SELECT intro_ja_jp, intro_en_us, intro_zh_cn, intro_zh_tw FROM galgame WHERE id = ?`,
		galgameID).Scan(&got).Error)
	return got.IntroJaJP, got.IntroEnUS, got.IntroZhCN, got.IntroZhTW
}

// workUpdatedAt reads one work's changes-feed watermark.
func workUpdatedAt(t *testing.T, workID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(
		`SELECT updated_at FROM catalog_work WHERE id = ?`, workID).Scan(&ts).Error)
	return ts
}

// runnerWithArtifacts wires an apply/dry runner that parks its JSON into dir.
func runnerWithArtifacts(t *testing.T, apply bool, dir string) *Runner {
	t.Helper()
	r, err := New(testDB, testDB, Opts{Apply: apply, Step: "all", ArtifactDir: dir})
	require.NoError(t, err)
	return r
}

// TestKanaClassifier pins the detector itself, without a database: the two
// verdicts, the gray band that protects the ambiguous cases, and the two
// character-range decisions the density depends on.
func TestKanaClassifier(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want kanaClass
	}{
		{"japanese prose", jaMisfiled, kanaJa},
		{"chinese prose quoting japanese names", zhTranslated, kanaZh},
		{"traditional chinese prose", zhTwTranslated, kanaZh},
		{"bilingual field", grayBilingual, kanaGray},
		{"japanese but too short to measure", grayShortJa, kanaGray},
		{"no CJK at all", "ACE of Diamond // UNOFFICIAL DISC", kanaZh},
		{"empty", "", kanaZh},
	} {
		got, density, cjk := classifyKana(tc.text)
		assert.Equal(t, tc.want, got, "%s (density %.3f over %d CJK chars)", tc.name, density, cjk)
	}

	// The katakana middle dot is Chinese punctuation too, so it must NOT count as
	// kana — a name list separated by it would otherwise read as Japanese.
	kana, han := kanaCounts("弗雷德里克・肖邦与约翰・塞巴斯蒂安")
	assert.Zero(t, kana, "U+30FB is punctuation, not kana")
	assert.Equal(t, 15, han)
	// The prolonged sound mark is genuine Japanese orthography and does count.
	kana, _ = kanaCounts("ゲーム")
	assert.Equal(t, 3, kana, "ー is kana")
}

// TestIntroLangFix pins step u: only the Japanese verdict is reattributed, the
// held column is emptied while the text moves VERBATIM, the gray band is parked
// with its evidence and left alone, a body that still has an original is not in
// the population at all, NO catalog row is written (the red line), and the second
// pass plans nothing.
func TestIntroLangFix(t *testing.T) {
	resetIntroRemainder(t)
	ctx := context.Background()

	mkClaimedWork(t, 7401, "misfiled ja")
	mkClaimedWork(t, 7402, "real zh")
	bilingual := mkClaimedWork(t, 7403, "gray")
	mkClaimedWork(t, 7404, "misfiled ja in zh_tw")
	mkClaimedWork(t, 7405, "zh is a translation")
	mkZhBody(t, 7401, "", "", jaMisfiled, "")
	mkZhBody(t, 7402, "", "", zhTranslated, "")
	mkZhBody(t, 7403, "", "", grayBilingual, "")
	mkZhBody(t, 7404, "", "", "", jaMisfiled)
	// An en original survives, so this body's zh column IS a translation — ruling ①
	// discards it and step u must not read it as a misfile.
	mkZhBody(t, 7405, "", "An English synopsis.", zhTranslated, "")
	// An UNCLAIMED body with the same misfile: outside the population.
	mkZhBody(t, 7406, "", "", jaMisfiled, "")

	dir := t.TempDir()
	dry, err := runnerWithArtifacts(t, false, dir).runStep(ctx, "u")
	require.NoError(t, err)
	assert.Equal(t, 4, dry.Source, "the claimed ja/en-blank bodies with zh text; the en-original and unclaimed ones are not candidates")
	assert.Equal(t, 2, dry.Planned, "the two Japanese misfiles")
	assert.Equal(t, 1, dry.Parked, "the bilingual field")
	assert.Zero(t, dry.Written, "a dry run writes nothing")
	ja, _, zhCN, _ := wikiIntros(t, 7401)
	assert.Empty(t, ja, "dry")
	assert.Equal(t, jaMisfiled, zhCN, "dry")

	// The gray artifact carries the evidence a human ruling needs.
	b, err := os.ReadFile(filepath.Join(dir, "parked-u-gray.json"))
	require.NoError(t, err)
	var parked []parkedGrayIntro
	require.NoError(t, json.Unmarshal(b, &parked))
	require.Len(t, parked, 1)
	assert.Equal(t, bilingual, parked[0].WorkID)
	assert.EqualValues(t, 7403, parked[0].GalgameID)
	assert.Equal(t, "intro_zh_cn", parked[0].Column)
	assert.Greater(t, parked[0].Density, kanaGrayDensity)
	assert.Less(t, parked[0].Density, kanaJaDensity)
	assert.NotEmpty(t, parked[0].Head)

	st, err := runnerWithArtifacts(t, true, dir).runStep(ctx, "u")
	require.NoError(t, err)
	assert.Equal(t, 2, st.Written)

	ja, _, zhCN, _ = wikiIntros(t, 7401)
	assert.Equal(t, jaMisfiled, ja, "the text moves verbatim")
	assert.Empty(t, zhCN, "and the held column is emptied")
	ja, _, zhCN, zhTW := wikiIntros(t, 7404)
	assert.Equal(t, jaMisfiled, ja, "a zh_tw misfile lands in the same ja column")
	assert.Empty(t, zhTW, "and empties zh_tw")
	assert.Empty(t, zhCN, "zh_cn was never the held column here")
	_, _, zhCN, _ = wikiIntros(t, 7402)
	assert.Equal(t, zhTranslated, zhCN, "a real translation is step v's business")
	_, _, zhCN, _ = wikiIntros(t, 7403)
	assert.Equal(t, grayBilingual, zhCN, "the gray band is untouched")
	_, en, zhCN, _ := wikiIntros(t, 7405)
	assert.Equal(t, "An English synopsis.", en)
	assert.Equal(t, zhTranslated, zhCN, "not a misfile — an original survives")
	_, _, zhCN, _ = wikiIntros(t, 7406)
	assert.Equal(t, jaMisfiled, zhCN, "an unclaimed body is outside the population")

	// THE RED LINE: step u writes the wiki table and NOTHING else. A ja row
	// inserted here would land inside step q's ownership scope and be deleted by
	// q's next set-diff (see stepIntroLangFix).
	var catalogRows int64
	require.NoError(t, testDB.Model(&model.CatalogWorkIntro{}).Count(&catalogRows).Error)
	assert.Zero(t, catalogRows, "step u must not write catalog_work_intro")

	second, err := runnerWithArtifacts(t, true, dir).runStep(ctx, "u")
	require.NoError(t, err)
	assert.Equal(t, 2, second.Source, "the reattributed bodies have left the population")
	assert.Zero(t, second.Planned)
	assert.Zero(t, second.Written, "converged")
}

// TestIntroLangFixSurvivesIntroMirror is the OWNERSHIP mutual proof behind step
// u's design (refs/proj/146 §1's red line): the same catalog row is DELETED by
// step q when the wiki ja column is empty behind it, and PRESERVED when step u has
// filled that column — which is exactly why the correction has to be made upstream
// instead of inserted straight into catalog_work_intro.
func TestIntroLangFixSurvivesIntroMirror(t *testing.T) {
	resetIntroRemainder(t)
	ctx := context.Background()

	// Arm 1 — the WRONG way: a wiki-attributed ja row on a work whose wiki ja
	// column is blank. It is inside q's scope, so q's set-diff deletes it.
	forged := mkClaimedWork(t, 7401, "forged ja row")
	mkZhBody(t, 7401, "", "", jaMisfiled, "")
	require.NoError(t, testDB.Create(&model.CatalogWorkIntro{
		WorkID: forged, Lang: "ja", Intro: jaMisfiled, SourceID: sourceID(t, "galgame_wiki")}).Error)

	q, err := newRunner(t, true).runStep(ctx, "q")
	require.NoError(t, err)
	assert.Equal(t, 1, q.Delete, "an unbacked row in q's scope is deleted — the red line, demonstrated")
	var n int64
	require.NoError(t, testDB.Model(&model.CatalogWorkIntro{}).Where("work_id = ?", forged).Count(&n).Error)
	assert.Zero(t, n)

	// Arm 2 — the RIGHT way: step u fixes the source, then q materializes it and a
	// re-run of q keeps it.
	u, err := newRunner(t, true).runStep(ctx, "u")
	require.NoError(t, err)
	assert.Equal(t, 1, u.Written)

	q, err = newRunner(t, true).runStep(ctx, "q")
	require.NoError(t, err)
	assert.Equal(t, 1, q.Insert, "the reattributed original materializes")
	assert.Zero(t, q.Delete)
	var row model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id = ? AND lang = ?", forged, "ja").First(&row).Error)
	assert.Equal(t, jaMisfiled, row.Intro)
	assert.EqualValues(t, introProvenanceSource, row.Provenance)
	assert.Equal(t, sourceID(t, "galgame_wiki"), row.SourceID)

	again, err := newRunner(t, true).runStep(ctx, "q")
	require.NoError(t, err)
	assert.Equal(t, 1, again.Already, "and SURVIVES the next daily pass")
	assert.Zero(t, again.Delete)
	assert.Zero(t, again.Written)
}

// TestIntroOrphanAdopt pins step v: an orphan Chinese intro is adopted as a
// source-shaped zh row in the right language, a work that has an original anywhere
// is discarded by ruling ①, the Japanese and gray verdicts are never filed as
// Chinese (whatever order the steps run in), the changes feed is touched, and the
// second pass plans nothing.
func TestIntroOrphanAdopt(t *testing.T) {
	resetIntroRemainder(t)
	ctx := context.Background()

	orphan := mkClaimedWork(t, 7401, "orphan zh")
	twOnly := mkClaimedWork(t, 7402, "orphan zh_tw")
	hasBangumi := mkClaimedWork(t, 7403, "has a bangumi summary")
	stillJapanese := mkClaimedWork(t, 7404, "u has not run yet")
	gray := mkClaimedWork(t, 7405, "bilingual")
	mkZhBody(t, 7401, "", "", zhTranslated, "")
	mkZhBody(t, 7402, "", "", "", zhTwTranslated)
	mkZhBody(t, 7403, "", "", zhTranslated, "")
	mkZhBody(t, 7404, "", "", jaMisfiled, "")
	mkZhBody(t, 7405, "", "", grayBilingual, "")
	// An original DOES exist for this work, from another source entirely — so its
	// wiki Chinese text is a translation ruling ① discards, not an orphan.
	require.NoError(t, testDB.Create(&model.CatalogWorkIntro{
		WorkID: hasBangumi, Lang: "ja", Intro: "バンガミのあらすじ。", SourceID: sourceID(t, "bangumi")}).Error)

	before := workUpdatedAt(t, orphan)
	dry, err := newRunner(t, false).runStep(ctx, "v")
	require.NoError(t, err)
	assert.Equal(t, 5, dry.Source)
	assert.Equal(t, 3, dry.Anchored, "the three Chinese verdicts")
	assert.Equal(t, 2, dry.Planned, "the bangumi-covered work is not an orphan")
	assert.Equal(t, 1, dry.Skipped)
	assert.Zero(t, dry.Written)

	st, err := newRunner(t, true).runStep(ctx, "v")
	require.NoError(t, err)
	assert.Equal(t, 2, st.Written)
	assert.Equal(t, 2, st.Touched)

	var row model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id = ?", orphan).First(&row).Error)
	assert.Equal(t, "zh-Hans", row.Lang, "zh_cn → zh-Hans")
	assert.Equal(t, zhTranslated, row.Intro, "verbatim")
	assert.Equal(t, sourceID(t, "galgame_wiki"), row.SourceID)
	assert.EqualValues(t, introProvenanceSource, row.Provenance, "a SOURCE row, not a machine one")
	assert.Empty(t, row.SrcHash)
	assert.Empty(t, row.MTModel)
	// A FRESH destination: GORM folds a non-zero primary key in the destination
	// struct into the WHERE clause, so reusing `row` here would query by its id.
	var hantRow model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id = ?", twOnly).First(&hantRow).Error)
	assert.Equal(t, "zh-Hant", hantRow.Lang, "zh_tw-only → zh-Hant")
	assert.Equal(t, zhTwTranslated, hantRow.Intro)
	assert.Greater(t, workUpdatedAt(t, orphan).UnixNano(), before.UnixNano(), "the changes feed sees the adoption")

	for _, tc := range []struct {
		work int64
		why  string
	}{
		{stillJapanese, "japanese text is never filed as Chinese, even before step u runs"},
		{gray, "the gray band waits for a ruling"},
	} {
		var n int64
		require.NoError(t, testDB.Model(&model.CatalogWorkIntro{}).Where("work_id = ?", tc.work).Count(&n).Error)
		assert.Zero(t, n, tc.why)
	}
	var bgm int64
	require.NoError(t, testDB.Model(&model.CatalogWorkIntro{}).Where("work_id = ?", hasBangumi).Count(&bgm).Error)
	assert.EqualValues(t, 1, bgm, "the bangumi original is neither touched nor joined by a wiki zh row")

	second, err := newRunner(t, true).runStep(ctx, "v")
	require.NoError(t, err)
	assert.Zero(t, second.Planned, "the adopted works are no longer orphans")
	assert.Zero(t, second.Written, "converged")
	assert.Equal(t, 3, second.Skipped)
}

// TestIntroRemainderStepsRespectStepQScope pins the structural safety claim step v
// rests on: zh is outside step q's lang scope, so a daily q pass neither deletes
// nor rewrites an adopted row — and the intromt machine lane's rows keep their own
// key too.
func TestIntroRemainderStepsRespectStepQScope(t *testing.T) {
	resetIntroRemainder(t)
	ctx := context.Background()

	work := mkClaimedWork(t, 7401, "adopted then mirrored")
	mkZhBody(t, 7401, "", "", zhTranslated, "")

	v, err := newRunner(t, true).runStep(ctx, "v")
	require.NoError(t, err)
	assert.Equal(t, 1, v.Written)

	q, err := newRunner(t, true).runStep(ctx, "q")
	require.NoError(t, err)
	assert.Zero(t, q.Delete, "zh is not in q's lang scope")
	assert.Zero(t, q.Update)

	var rows []model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id = ?", work).Find(&rows).Error)
	require.Len(t, rows, 1, "the adopted zh row still stands alone")
	assert.Equal(t, "zh-Hans", rows[0].Lang)
	assert.Equal(t, zhTranslated, rows[0].Intro)
}

// TestIntroRemainderStepsSkipUnclaimed pins the population boundary for the two
// new steps, the same way TestMirrorStepsSkipUnclaimed does for p..t: a body whose
// work is soft-deleted, merely pointed at, or absent contributes nothing.
func TestIntroRemainderStepsSkipUnclaimed(t *testing.T) {
	resetIntroRemainder(t)
	ctx := context.Background()

	mkZhBody(t, 7401, "", "", jaMisfiled, "")   // no catalog_work at all
	mkZhBody(t, 7402, "", "", zhTranslated, "") // claimed by a SOFT-DELETED work
	mkZhBody(t, 7403, "", "", zhTranslated, "") // pointed at by a BODYLESS work
	dead := mkClaimedWork(t, 7402, "soft-deleted")
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET deleted_at = now() WHERE id = ?`, dead).Error)
	pointer := mkWork(t, "not-a-claim")
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET product_work_id = ? WHERE id = ?`, 7403, pointer).Error)

	for _, step := range []string{"u", "v"} {
		st, err := newRunner(t, true).runStep(ctx, step)
		require.NoError(t, err, "step %s", step)
		assert.Zero(t, st.Source, "step %s sees no candidate", step)
		assert.Zero(t, st.Planned, "step %s plans nothing", step)
		assert.Zero(t, st.Written, "step %s writes nothing", step)
	}
	var n int64
	require.NoError(t, testDB.Model(&model.CatalogWorkIntro{}).Count(&n).Error)
	assert.Zero(t, n)
	_, _, zhCN, _ := wikiIntros(t, 7401)
	assert.Equal(t, jaMisfiled, zhCN, "the wiki side is untouched too")
}

// TestParseStepsIntroRemainder pins the ORDER trap the runbook depends on: q sorts
// before u no matter how the selection is typed, which is why the wave runs u and q
// as separate invocations (a single "--step u,q" would materialize nothing).
func TestParseStepsIntroRemainder(t *testing.T) {
	sel, err := ParseSteps("v,u,q")
	require.NoError(t, err)
	assert.Equal(t, []string{"q", "u", "v"}, sel, "canonical order: q runs BEFORE u in one invocation")

	all, err := ParseSteps("all")
	require.NoError(t, err)
	assert.Equal(t, "v", all[len(all)-1], "the one-shot remainder sorts last")
}
