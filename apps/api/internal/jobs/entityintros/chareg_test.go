package entityintros

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backdated is stamped on freshly created works so any TouchWorks bump is
// unambiguous (GORM writes now() on create).
var backdated = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func mkWork(t *testing.T, name string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(&w).Error)
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, backdated, w.ID).Error)
	return w.ID
}

func mkRosterEdge(t *testing.T, workID, characterID int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkCharacter{
		WorkID: workID, CharacterID: characterID,
		Kind: model.WorkCharacterKindMain, Spoiler: 0, MatchedBy: "import:test",
	}).Error)
}

func workUpdatedAt(t *testing.T, id int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(`SELECT updated_at FROM catalog_work WHERE id = ?`, id).Scan(&ts).Error)
	return ts
}

// mkAppearance inserts one erogamespace appearance row. Only raw is written —
// pk / game / character_id are generated columns, exactly as in the mirror.
func mkAppearance(t *testing.T, game, character, stage int, netabare bool, text string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"game": game, "character": character, "stage": stage,
		"netabare": netabare, "formal_explanation": text,
	})
	require.NoError(t, err)
	require.NoError(t, testDB.Exec(
		`INSERT INTO `+egSchema+`.appearances (raw) VALUES (?::jsonb)`, string(raw)).Error)
}

func charIntro(t *testing.T, characterID int64) model.CatalogCharacterIntro {
	t.Helper()
	var row model.CatalogCharacterIntro
	require.NoError(t, testDB.Where("character_id = ?", characterID).First(&row).Error)
	return row
}

// TestCharEGLane drives the char-eg lane end to end through the real Run entry
// point: text selection across several appearances, the netabare exclusion,
// fill-missing against a foreign-source ja row, the dry-run forecast, the
// host-work touch and second-pass idempotency.
func TestCharEGLane(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	chLongest := mkCharacter(t, "eg-longest")
	chTie := mkCharacter(t, "eg-tie")
	chDupJa := mkCharacter(t, "eg-dup-ja")
	chCRLF := mkCharacter(t, "eg-crlf")
	chNoSupply := mkCharacter(t, "eg-no-supply")
	chNetabareOnly := mkCharacter(t, "eg-netabare-only")

	for ch, egID := range map[int64]string{
		chLongest: "500", chTie: "501", chDupJa: "502",
		chCRLF: "503", chNoSupply: "504", chNetabareOnly: "505",
	} {
		mkAnchor(t, model.EntityTypeCharacter, ch, reg.egSource, egID, model.LinkKindExact)
	}

	// Longest wins, and the longest text of all is a spoiler that must lose.
	const longest = "とても長い紹介文です。ここが最長になります。"
	mkAppearance(t, 20, 500, 1, false, "短い紹介。")
	mkAppearance(t, 10, 500, 1, false, longest)
	mkAppearance(t, 30, 500, 1, true, longest+"ネタバレはさらに長い。")
	// Equal lengths → the smaller game id wins, regardless of insertion order.
	mkAppearance(t, 40, 501, 1, false, "ゲーム四十の紹介。")
	mkAppearance(t, 15, 501, 1, false, "ゲーム十五の紹介。")
	mkAppearance(t, 60, 502, 1, false, "EG 側の紹介文。")
	mkAppearance(t, 70, 503, 1, false, "一行目です。\r\n二行目です。")
	// 504 has no appearance at all; 505 has only a spoiler one.
	mkAppearance(t, 80, 505, 1, true, "ネタバレのみ。")

	// A ja intro from ANOTHER source makes chDupJa a fill-missing skip.
	require.NoError(t, testDB.Create(&model.CatalogCharacterIntro{
		CharacterID: chDupJa, Lang: "ja", Intro: "既にある日本語紹介。", SourceID: reg.bangumiSource,
	}).Error)

	// Host works: the two that list chLongest must be bumped, the one that
	// lists the skipped character must not.
	hostA := mkWork(t, "host-a")
	hostB := mkWork(t, "host-b")
	hostSkipped := mkWork(t, "host-skipped")
	mkRosterEdge(t, hostA, chLongest)
	mkRosterEdge(t, hostB, chLongest)
	mkRosterEdge(t, hostSkipped, chDupJa)

	// --- dry run: decides, writes nothing, touches nothing.
	st, err := Run(ctx, Opts{DSN: testDSN, EGDSN: egTestDSN, Only: LaneCharEG})
	require.NoError(t, err)
	assert.Equal(t, 4, st.CharEG.Candidates, "chLongest chTie chDupJa chCRLF")
	assert.Equal(t, 2, st.CharEG.NoSupply, "chNoSupply has no row, chNetabareOnly only a spoiler one")
	assert.Equal(t, 3, st.CharEG.JaNew, "chLongest chTie chCRLF")
	assert.Equal(t, 1, st.CharEG.SkipDupLang, "chDupJa already has ja from bangumi")
	assert.Zero(t, st.CharEG.JaWritten)
	assert.Zero(t, st.CharEG.Touched)
	assert.EqualValues(t, 1, charIntroCount(t, ""), "only the fixture row exists")
	assert.Equal(t, backdated.UTC(), workUpdatedAt(t, hostA).UTC(), "dry run moves no watermark")

	// --- apply.
	st, err = Run(ctx, Opts{DSN: testDSN, EGDSN: egTestDSN, Apply: true, Only: LaneCharEG})
	require.NoError(t, err)
	assert.Equal(t, 3, st.CharEG.JaWritten)
	assert.Equal(t, 2, st.CharEG.Touched, "hostA + hostB; hostSkipped gained nothing")
	assert.Zero(t, st.CharEG.Errors+st.CharEG.Conflict)

	longestRow := charIntro(t, chLongest)
	assert.Equal(t, "ja", longestRow.Lang)
	assert.Equal(t, reg.egSource, longestRow.SourceID)
	assert.Equal(t, longest, longestRow.Intro, "longest non-spoiler text wins; the netabare one never lands")

	assert.Equal(t, "ゲーム十五の紹介。", charIntro(t, chTie).Intro, "equal length → smallest game id")
	assert.Equal(t, "一行目です。\n二行目です。", charIntro(t, chCRLF).Intro, "CRLF→LF, otherwise verbatim")
	assert.EqualValues(t, 1, charIntroCount(t, "WHERE character_id = ?", chDupJa), "the bangumi row is untouched")
	assert.EqualValues(t, 0, charIntroCount(t, "WHERE character_id = ?", chNoSupply))
	assert.EqualValues(t, 0, charIntroCount(t, "WHERE character_id = ?", chNetabareOnly))

	bumpedA, bumpedB := workUpdatedAt(t, hostA), workUpdatedAt(t, hostB)
	assert.True(t, bumpedA.After(backdated), "host work of a written character is bumped")
	assert.True(t, bumpedB.After(backdated))
	assert.Equal(t, backdated.UTC(), workUpdatedAt(t, hostSkipped).UTC(), "a skipped character bumps nothing")

	// --- second apply: zero writes, and therefore zero touches.
	st, err = Run(ctx, Opts{DSN: testDSN, EGDSN: egTestDSN, Apply: true, Only: LaneCharEG})
	require.NoError(t, err)
	assert.Zero(t, st.CharEG.JaWritten, "second pass writes zero")
	assert.Zero(t, st.CharEG.Touched, "second pass touches zero")
	assert.Zero(t, st.CharEG.Errors+st.CharEG.Conflict)
	assert.Equal(t, 4, st.CharEG.SkipDupLang, "all four candidates now have ja")
	assert.EqualValues(t, 4, charIntroCount(t, ""), "1 fixture + 3 writes")
	assert.Equal(t, bumpedA.UTC(), workUpdatedAt(t, hostA).UTC(), "watermark does not drift on a re-run")
	assert.Equal(t, bumpedB.UTC(), workUpdatedAt(t, hostB).UTC())
}

// TestCharEGRequiresDSN pins the "never guess a DSN" discipline: the lane is
// refused without an erogamespace DSN, while a --only run on an in-DB lane
// still works without one.
func TestCharEGRequiresDSN(t *testing.T) {
	clean(t)
	ctx := context.Background()

	_, err := Run(ctx, Opts{DSN: testDSN, Only: LaneCharEG})
	require.Error(t, err, "char-eg without --eg-dsn is refused")

	_, err = Run(ctx, Opts{DSN: testDSN})
	require.Error(t, err, "the default all-lane run includes char-eg, so it needs the DSN too")

	_, err = Run(ctx, Opts{DSN: testDSN, Only: LaneCharVNDB})
	require.NoError(t, err, "an in-DB lane needs no erogamespace access")
}
