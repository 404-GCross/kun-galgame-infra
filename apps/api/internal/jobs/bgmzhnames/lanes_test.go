package bgmzhnames

import (
	"context"
	"fmt"
	"testing"
	"time"

	"api/internal/platform/catalog/model"
	srcb "api/internal/platform/catalog/srcbangumi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// The person and label lanes share the character lane's TestMain (one real
// Postgres carrying the catalog Gold schema and the src_bangumi Silver schema),
// so these tests only add the fixtures their own tables need.

func cleanEntities(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_label_alias", "catalog_name_alias", "catalog_external_ref",
		"catalog_label", "catalog_person", "catalog_credit_name",
		"src_bangumi.person",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

// mkBGMPerson creates one src_bangumi.person row. Bangumi files companies and
// groups in this same table, so the label lane uses it too — kind 1=individual,
// 2=company, 3=group.
func mkBGMPerson(t *testing.T, id int64, kind int, name, infobox string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Person{
		ID: id, Type: kind, Name: name, InfoboxRaw: "", InfoboxParsed: datatypes.JSON(infobox),
		ParserVersion: "test", IngestedAt: time.Now(),
	}).Error)
}

func mkEntityAnchor(t *testing.T, entityType int16, entityID int64, source int16, externalID string, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: entityType, EntityID: entityID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: "test",
	}).Error)
}

func mkLabel(t *testing.T, name string) int64 {
	t.Helper()
	l := model.CatalogLabel{DisplayName: name, Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(&l).Error)
	return l.ID
}

// mkPerson creates a credit name and the person it is the primary name of.
// primaryName == "" leaves primary_credit_name_id NULL — the person the lane
// must count and skip.
func mkPerson(t *testing.T, display, primaryName string) (personID int64, creditNameID int64) {
	t.Helper()
	p := model.CatalogPerson{DisplayName: display}
	if primaryName != "" {
		cn := model.CatalogCreditName{
			Name: primaryName, Lang: "ja",
			Kind: model.CreditNameKindMain, LinkVisibility: model.LinkVisibilityPublic,
		}
		require.NoError(t, testDB.Create(&cn).Error)
		p.PrimaryCreditNameID = &cn.ID
		creditNameID = cn.ID
	}
	require.NoError(t, testDB.Create(&p).Error)
	return p.ID, creditNameID
}

// zhOnly keeps the zh-Hans rows of a scanned alias set, in id order.
func zhOnly[T any](rows []T, lang func(T) string) []T {
	var out []T
	for _, r := range rows {
		if lang(r) == LangZhHans {
			out = append(out, r)
		}
	}
	return out
}

func labelAliasesOf(t *testing.T, labelID int64) []model.CatalogLabelAlias {
	t.Helper()
	var rows []model.CatalogLabelAlias
	require.NoError(t, testDB.Where("label_id = ?", labelID).Order("id").Find(&rows).Error)
	return rows
}

func nameAliasesOf(t *testing.T, creditNameID int64) []model.CatalogNameAlias {
	t.Helper()
	var rows []model.CatalogNameAlias
	require.NoError(t, testDB.Where("credit_name_id = ?", creditNameID).Order("id").Find(&rows).Error)
	return rows
}

// TestRunLabelLane drives the label lane end to end: the company/group staging
// join, the infobox guard, the drop of a name that IS the display name, the uq
// absorption, the never-steal-a-primary rule, the probable-anchor exclusion, the
// deliberate absence of any work touch, and second-pass idempotency.
func TestRunLabelLane(t *testing.T) {
	cleanEntities(t)
	ctx := context.Background()
	src := bangumiSource(t)

	whirlpool := mkLabel(t, "Whirlpool")  // company, two Chinese names
	circle := mkLabel(t, "チーム暗黒媒体")       // group (bangumi type 3)
	sameName := mkLabel(t, "型月")          // display_name IS the Chinese name
	dup := mkLabel(t, "AUGUST")           // already carries the projected name
	hasPrimary := mkLabel(t, "Key")       // already has a human zh primary
	guard := mkLabel(t, "scalar-fields")  // dirty infobox
	latinOnly := mkLabel(t, "genDESIGN")  // 简体中文名 holds a Latin string
	probable := mkLabel(t, "probable")    // non-exact anchor
	unanchored := mkLabel(t, "no-anchor") // no anchor at all
	deleted := mkLabel(t, "soft-deleted") // out of the universe

	infoboxes := map[int64]struct {
		bgm     int64
		kind    int
		infobox string
	}{
		whirlpool: {1, 2, `{"Fields":[
			{"Key":"简体中文名","Value":"漩涡社","Items":null},
			{"Key":"别名","Value":"","Items":[
				{"Key":"","Value":"株式会社ワールプール"},
				{"Key":"英文名","Value":"Whirlpool Co., Ltd."},
				{"Key":"第二中文名","Value":"漩涡"},
				{"Key":"日文名","Value":"ワールプール"}]}]}`},
		circle:     {2, 3, `{"Fields":[{"Key":"简体中文名","Value":"暗黑媒体","Items":null}]}`},
		sameName:   {3, 2, `{"Fields":[{"Key":"简体中文名","Value":"型月","Items":null}]}`},
		dup:        {4, 2, `{"Fields":[{"Key":"简体中文名","Value":"八月","Items":null}]}`},
		hasPrimary: {5, 2, `{"Fields":[{"Key":"简体中文名","Value":"键社","Items":null}]}`},
		guard:      {6, 2, `{"Fields":"简体中文名"}`},
		latinOnly:  {7, 2, `{"Fields":[{"Key":"简体中文名","Value":"genDESIGN","Items":null}]}`},
		probable:   {8, 2, `{"Fields":[{"Key":"简体中文名","Value":"不该写入","Items":null}]}`},
		deleted:    {9, 2, `{"Fields":[{"Key":"简体中文名","Value":"也不该写入","Items":null}]}`},
	}
	for labelID, f := range infoboxes {
		mkBGMPerson(t, f.bgm, f.kind, fmt.Sprintf("bgm-%d", f.bgm), f.infobox)
		kind := model.LinkKindExact
		if labelID == probable {
			kind = model.LinkKindProbable
		}
		mkEntityAnchor(t, model.EntityTypeLabel, labelID, src, fmt.Sprintf("%d", f.bgm), kind)
	}
	_ = unanchored
	require.NoError(t, testDB.Delete(&model.CatalogLabel{}, deleted).Error)

	require.NoError(t, testDB.Create(&model.CatalogLabelAlias{
		LabelID: dup, Name: "八月", Lang: LangZhHans, Kind: model.AliasKindTranslation,
	}).Error)
	require.NoError(t, testDB.Create(&model.CatalogLabelAlias{
		LabelID: hasPrimary, Name: "钥匙社", Lang: LangZhHans,
		Kind: model.AliasKindTranslation, IsPrimaryForLocale: true,
	}).Error)
	// The same text under the label's existing '' lang: a different language, so
	// the unique key does not absorb the zh-Hans row.
	require.NoError(t, testDB.Create(&model.CatalogLabelAlias{
		LabelID: whirlpool, Name: "漩涡社", Lang: "", Kind: model.AliasKindTranslation,
	}).Error)

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, Opts{DSN: testDSN, Lane: LaneLabel})
	require.NoError(t, err)
	assert.Equal(t, 7, st.Anchored, "probable / unanchored / soft-deleted labels are out of the universe")
	assert.Equal(t, 1, st.SkippedGuard, "the scalar Fields row")
	assert.Equal(t, 1, st.SkippedNonChinese, "genDESIGN fails the Han test")
	assert.Equal(t, 1, st.SkippedSameAsOwner, "型月 already IS the display name")
	assert.Equal(t, 2, st.NoSupply, "the latin-only label and the same-as-display-name one")
	assert.Equal(t, 4, st.Candidates, "whirlpool + circle + dup + has-primary")
	assert.Equal(t, 5, st.Names)
	assert.Equal(t, 4, st.WouldInsert)
	assert.Equal(t, 1, st.SkippedDup, "AUGUST already carries 八月")
	assert.Zero(t, st.Inserted)
	var before int64
	require.NoError(t, testDB.Model(&model.CatalogLabelAlias{}).Count(&before).Error)
	require.EqualValues(t, 3, before, "a dry run writes nothing")

	// --- apply.
	st, err = Run(ctx, Opts{DSN: testDSN, Lane: LaneLabel, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 4, st.Inserted)
	assert.Equal(t, 2, st.PrimarySet,
		"whirlpool + circle; AUGUST's only name was absorbed and hasPrimary keeps its human one")
	assert.Zero(t, st.Touched, "the label lane has no changes-feed touch discipline")
	assert.Zero(t, st.Errors+st.Conflict)

	wp := zhOnly(labelAliasesOf(t, whirlpool), func(a model.CatalogLabelAlias) string { return a.Lang })
	require.Len(t, wp, 2)
	assert.Equal(t, "漩涡社", wp[0].Name)
	assert.True(t, wp[0].IsPrimaryForLocale, "the main 简体中文名 claims the free primary")
	assert.Equal(t, model.AliasKindTranslation, wp[0].Kind)
	assert.Nil(t, wp[0].Latin, "latin is never written by this wave")
	assert.Equal(t, "漩涡", wp[1].Name)
	assert.False(t, wp[1].IsPrimaryForLocale, "only one row per label claims the primary")
	assert.Len(t, labelAliasesOf(t, whirlpool), 3, "the '' lang row with the same text is untouched")

	hp := zhOnly(labelAliasesOf(t, hasPrimary), func(a model.CatalogLabelAlias) string { return a.Lang })
	require.Len(t, hp, 2)
	assert.True(t, hp[0].IsPrimaryForLocale, "the human primary survives")
	assert.Equal(t, "键社", hp[1].Name)
	assert.False(t, hp[1].IsPrimaryForLocale)

	for _, id := range []int64{sameName, guard, latinOnly, probable, unanchored, deleted} {
		assert.Empty(t, labelAliasesOf(t, id), "label %d must gain nothing", id)
	}
	var offLang int64
	require.NoError(t, testDB.Model(&model.CatalogLabelAlias{}).
		Where("lang <> ? AND lang <> ?", LangZhHans, "").Count(&offLang).Error)
	assert.Zero(t, offLang, "every written row is zh-Hans")

	// --- second apply: zero writes, and the skip is the preload, not the backstop.
	st, err = Run(ctx, Opts{DSN: testDSN, Lane: LaneLabel, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.Inserted)
	assert.Zero(t, st.WouldInsert)
	assert.Zero(t, st.Errors+st.Conflict)
	assert.Equal(t, 5, st.SkippedDup, "every projected name is now present")
}

// TestRunPersonLane drives the person lane: the alias hangs off the PRIMARY
// credit name, a person without one is counted and skipped, and no work is
// touched.
func TestRunPersonLane(t *testing.T) {
	cleanEntities(t)
	ctx := context.Background()
	src := bangumiSource(t)

	pOgata, cnOgata := mkPerson(t, "緒方剛志", "緒方剛志")
	pSame, cnSame := mkPerson(t, "田中貴之", "田中贵之") // the credit name IS the Chinese name
	pDup, cnDup := mkPerson(t, "鈴平ひろ", "鈴平ひろ")
	pNoPrimary, _ := mkPerson(t, "no-primary", "")
	pCompanyKind, cnCompany := mkPerson(t, "company-typed", "ワールプール")

	rows := map[int64]struct {
		bgm     int64
		kind    int
		infobox string
	}{
		pOgata: {11, 1, `{"Fields":[
			{"Key":"简体中文名","Value":"绪方刚志","Items":null},
			{"Key":"别名","Value":"","Items":[
				{"Key":"","Value":"緒方剛志"},
				{"Key":"罗马字","Value":"Ogata Takeshi"},
				{"Key":"第二中文名","Value":"绪方刚"}]}]}`},
		pSame:        {12, 1, `{"Fields":[{"Key":"简体中文名","Value":"田中贵之","Items":null}]}`},
		pDup:         {13, 1, `{"Fields":[{"Key":"简体中文名","Value":"铃平广","Items":null}]}`},
		pNoPrimary:   {14, 1, `{"Fields":[{"Key":"简体中文名","Value":"不该写入","Items":null}]}`},
		pCompanyKind: {15, 2, `{"Fields":[{"Key":"简体中文名","Value":"漩涡社","Items":null}]}`},
	}
	for personID, f := range rows {
		mkBGMPerson(t, f.bgm, f.kind, fmt.Sprintf("bgm-%d", f.bgm), f.infobox)
		mkEntityAnchor(t, model.EntityTypePerson, personID, src, fmt.Sprintf("%d", f.bgm), model.LinkKindExact)
	}
	// A CREDIT-NAME anchor must never be followed: it would smuggle in the
	// deferred identity judgement.
	mkEntityAnchor(t, model.EntityTypeCreditName, cnOgata, src, "999", model.LinkKindExact)
	mkBGMPerson(t, 999, 1, "credit-name-anchor", `{"Fields":[{"Key":"简体中文名","Value":"不该跟随","Items":null}]}`)

	require.NoError(t, testDB.Create(&model.CatalogNameAlias{
		CreditNameID: cnDup, Name: "铃平广", Lang: LangZhHans, Kind: model.AliasKindTranslation,
	}).Error)

	st, err := Run(ctx, Opts{DSN: testDSN, Lane: LanePerson})
	require.NoError(t, err)
	assert.Equal(t, 5, st.Anchored)
	assert.Equal(t, 1, st.SkippedNoOwner, "the person with a NULL primary_credit_name_id")
	assert.Equal(t, 1, st.SkippedSameAsOwner, "田中贵之 already IS the credit name")
	assert.Equal(t, 3, st.Candidates, "ogata + dup + the company-typed row (the anchor decides, not the type)")
	assert.Equal(t, 4, st.Names)
	assert.Equal(t, 3, st.WouldInsert)
	assert.Equal(t, 1, st.SkippedDup)
	assert.Zero(t, st.Inserted)

	st, err = Run(ctx, Opts{DSN: testDSN, Lane: LanePerson, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 3, st.Inserted)
	assert.Equal(t, 2, st.PrimarySet, "ogata + the company-typed row; pDup's pre-existing row is not re-elected")
	assert.Zero(t, st.Touched, "the person lane has no changes-feed touch discipline")
	assert.Zero(t, st.Errors+st.Conflict)

	og := nameAliasesOf(t, cnOgata)
	require.Len(t, og, 2, "the untagged 别名 item is never collected")
	assert.Equal(t, "绪方刚志", og[0].Name)
	assert.True(t, og[0].IsPrimaryForLocale)
	assert.Equal(t, LangZhHans, og[0].Lang)
	assert.Equal(t, "绪方刚", og[1].Name)
	assert.False(t, og[1].IsPrimaryForLocale)

	assert.Empty(t, nameAliasesOf(t, cnSame), "an alias equal to its own credit name is never written")
	assert.Len(t, nameAliasesOf(t, cnDup), 1, "the projected name was absorbed by the unique key")
	assert.Len(t, nameAliasesOf(t, cnCompany), 1)

	st, err = Run(ctx, Opts{DSN: testDSN, Lane: LanePerson, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.Inserted, "second pass writes zero")
	assert.Zero(t, st.WouldInsert)
	assert.Zero(t, st.Errors+st.Conflict)
}

// TestUnknownLane pins the lane switch: an unknown value is refused before any
// connection is opened.
func TestUnknownLane(t *testing.T) {
	_, err := Run(context.Background(), Opts{DSN: testDSN, Lane: "org"})
	require.ErrorContains(t, err, "unknown lane")
}
