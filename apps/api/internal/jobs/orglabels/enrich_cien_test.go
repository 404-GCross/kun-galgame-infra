package orglabels

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ensureCienTable(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`CREATE TABLE IF NOT EXISTS cien_profiles (
		creator_id integer PRIMARY KEY,
		creator_name text,
		description text,
		dlsite_maker_ids text[],
		twitter_url text,
		external_links jsonb,
		http_status integer)`).Error)
	require.NoError(t, testDB.Exec(`ALTER TABLE cien_profiles ADD COLUMN IF NOT EXISTS external_links jsonb`).Error)
	require.NoError(t, testDB.Exec(`TRUNCATE cien_profiles`).Error)
}

func mkLabelAnchor(t *testing.T, source int16, ext string, label int64, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by, created_at)
		 VALUES (3, ?, ?, ?, ?, 'rule:test-label', now())`, label, source, ext, kind).Error)
}

func mkCien(t *testing.T, id int64, desc, makersLit, twitter string, status int) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO cien_profiles (creator_id, creator_name, description, dlsite_maker_ids, twitter_url, http_status)
		 VALUES (?, ?, NULLIF(?, ''), NULLIF(?, '')::text[], NULLIF(?, ''), ?)`,
		id, "c", desc, makersLit, twitter, status).Error)
}

func TestEnrichCien(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)
	ensureCienTable(t)
	ctx := context.Background()

	mkLabel(t, 800, "サークルA", model.LabelKindDoujinCircle)
	mkLabel(t, 801, "サークルB", model.LabelKindDoujinCircle)
	mkLabel(t, 802, "CircleC", model.LabelKindDoujinCircle)
	mkLabel(t, 803, "サークルA別名義", model.LabelKindDoujinCircle)

	mkLabelAnchor(t, sourceDlsite, "RG100", 800, model.LinkKindExact)
	mkLabelAnchor(t, sourceDlsite, "RG100", 803, model.LinkKindProbable)
	mkLabelAnchor(t, sourceDlsite, "RG200", 801, model.LinkKindExact)
	mkLabelAnchor(t, sourceDlsite, "RG300", 802, model.LinkKindProbable)

	require.NoError(t, testDB.Create(&model.CatalogLabelIntro{
		LabelID: 800, Lang: "ja", Intro: "vndb の紹介", SourceID: sourceVNDB}).Error)
	require.NoError(t, testDB.Create(&model.CatalogLabelIntro{
		LabelID: 803, Lang: "ja", Intro: "既存 cien 紹介", SourceID: sourceCien}).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeLabel, EntityID: 801, SourceID: sourceCien,
		ExternalID: "102", LinkKind: model.LinkKindRelated, MatchedBy: "rule:eg-cien"}).Error)

	mkCien(t, 101, "ゲームサークルです。よろしくお願いします。", "{RG100}", "https://twitter.com/Foo_Bar", 200)
	mkCien(t, 102, "短い", "{RG200}", "", 200)
	mkCien(t, 103, "リンクなしサークルの長い自己紹介文です。", "{VG400}", "", 200)
	mkCien(t, 104, "後から同じメーカーを主張するサークルです。", "{RG100}", "", 200)
	mkCien(t, 105, "We make English visual novels since 2020.", "{RG300}", "", 200)
	mkCien(t, 106, "404 なので除外されるはずの行です。", "{RG200}", "", 404)

	st, err := enrichCien(ctx, testDB, testDB, false)
	require.NoError(t, err)
	assert.Equal(t, 5, st.Creators200, "404 row excluded")
	assert.Equal(t, 4, st.CreatorsWithDesc, "102's 2-rune desc is short")
	assert.Equal(t, 1, st.MakerConflicts, "104's RG100 claim loses to 101 (creator_id ASC)")
	assert.Equal(t, 2, st.NoLabelMatch, "103 (unanchored VG400) + 104 (conflict left it empty)")
	assert.Equal(t, 3, st.MappedCreators)
	assert.Equal(t, 4, st.MappedLabels)
	assert.Equal(t, 1, st.ShortSkipped, "102 mapped but no intro")
	assert.Equal(t, 2, st.IntroPlanned, "800 ja (vndb row does not block) + 802 en")
	assert.Equal(t, 1, st.IntroSkipDup, "803 already has a cien ja row")
	assert.Equal(t, 2, st.TwitterPlanned, "101's handle onto 800+803")
	assert.Equal(t, 4, st.CienLinkPlanned, "800:101 803:101 801:102 802:105")
	assert.Zero(t, st.IntroWritten+st.TwitterWritten+st.CienLinkWritten+st.Errors)
	var n int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_label_intro WHERE source_id = ?`, sourceCien).Scan(&n).Error)
	assert.EqualValues(t, 1, n, "dry run wrote nothing (only the pre-seeded row)")

	st, err = enrichCien(ctx, testDB, testDB, true)
	require.NoError(t, err)
	assert.Equal(t, 2, st.IntroWritten)
	assert.Equal(t, 2, st.TwitterWritten)
	assert.Equal(t, 3, st.CienLinkWritten, "801:102 dedups against the pre-seeded E2 row")
	assert.Zero(t, st.Errors)

	var intros []model.CatalogLabelIntro
	require.NoError(t, testDB.Where("label_id = ?", 800).Order("source_id").Find(&intros).Error)
	require.Len(t, intros, 2)
	assert.Equal(t, sourceVNDB, intros[0].SourceID)
	assert.Equal(t, sourceCien, intros[1].SourceID)
	assert.Equal(t, "ja", intros[1].Lang)
	assert.Equal(t, "ゲームサークルです。よろしくお願いします。", intros[1].Intro, "verbatim")
	require.NoError(t, testDB.Where("label_id = ?", 802).Find(&intros).Error)
	require.Len(t, intros, 1)
	assert.Equal(t, "en", intros[0].Lang)
	assert.Equal(t, sourceCien, intros[0].SourceID)
	var refs []model.CatalogExternalRef
	require.NoError(t, testDB.Where("source_id = ? AND matched_by = ?", sourceTwitter, ruleCienIntroTwitter).
		Order("entity_id").Find(&refs).Error)
	require.Len(t, refs, 2)
	assert.EqualValues(t, 800, refs[0].EntityID)
	assert.Equal(t, "foo_bar", refs[0].ExternalID)
	assert.Equal(t, model.LinkKindRelated, refs[0].LinkKind)
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_external_ref WHERE source_id = ? AND link_kind = ?`,
		sourceCien, model.LinkKindRelated).Scan(&n).Error)
	assert.EqualValues(t, 4, n)

	st, err = enrichCien(ctx, testDB, testDB, true)
	require.NoError(t, err)
	assert.Zero(t, st.IntroWritten+st.TwitterWritten+st.CienLinkWritten+st.Errors, "second pass writes zero")
	assert.Equal(t, 0, st.IntroPlanned)
	assert.Equal(t, 3, st.IntroSkipDup, "800 ja + 803 ja + 802 en all present now")
	assert.Equal(t, 2, st.TwitterPlanned, "plan recounts; ON CONFLICT absorbs")
	assert.Equal(t, 4, st.CienLinkPlanned)
}

func TestEnrichCienExtLinks(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)
	ensureCienTable(t)
	ctx := context.Background()

	mkLabel(t, 900, "サークルEXT", model.LabelKindDoujinCircle)
	mkLabelAnchor(t, sourceDlsite, "RG900", 900, model.LinkKindExact)

	require.NoError(t, testDB.Exec(
		`INSERT INTO cien_profiles (creator_id, creator_name, description, dlsite_maker_ids, twitter_url, external_links, http_status)
		 VALUES (?, ?, ?, ?::text[], '', ?::jsonb, 200)`,
		201, "c", "外部リンク豊富なサークルの長い紹介文です。", "{RG900}",
		`[{"url":"https://www.pixiv.net/users/12345","type":"else"},`+
			`{"url":"https://www.dmm.co.jp/dc/doujin/-/list/=/article=maker/id=99/","type":"else"},`+
			`{"url":"https://store.steampowered.com/app/7777","type":"else"},`+
			`{"url":"https://skeb.jp/@someone","type":"else"},`+
			`{"url":"https://www.youtube.com/@chan","type":"youtube"}]`).Error)

	st, err := enrichCien(ctx, testDB, testDB, true)
	require.NoError(t, err)
	assert.Equal(t, 3, st.ExtLinkPlanned, "pixiv+dmm+steam whitelisted; skeb+youtube skipped")
	assert.Equal(t, 3, st.ExtLinkWritten)
	assert.Zero(t, st.Errors)

	var refs []model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_id = ? AND matched_by = ?", 900, ruleCienExtLink).
		Order("source_id").Find(&refs).Error)
	require.Len(t, refs, 3)
	assert.Equal(t, sourceSteam, refs[0].SourceID)
	assert.Equal(t, sourcePixiv, refs[1].SourceID)
	assert.Equal(t, sourceDmm, refs[2].SourceID)
	for _, r := range refs {
		assert.Equal(t, model.LinkKindRelated, r.LinkKind)
	}

	st, err = enrichCien(ctx, testDB, testDB, true)
	require.NoError(t, err)
	assert.Zero(t, st.ExtLinkWritten, "second pass writes zero")
	assert.Equal(t, 3, st.ExtLinkPlanned, "plan recounts; ON CONFLICT absorbs")
}
