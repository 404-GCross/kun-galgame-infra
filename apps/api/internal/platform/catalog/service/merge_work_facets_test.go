package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeWorkCarriesEveryFacet pins the wave-170b fix: every facet table that
// hangs off a work by work_id joined the schema AFTER the merge hook list was
// written, so a work merge stranded all of them on the retired id — production
// caught catalog_work_cover (12 rows moved by hand) and it was never alone.
//
// Each facet gets the same two-row shape: one row whose unique key COLLIDES
// with a row the target already carries (must drop, survivor's row untouched)
// and one source-only row (must land on the target). Nothing may stay behind.
func TestMergeWorkCarriesEveryFacet(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createWork(t, "同じ作品")
	source := createWork(t, "同じ 作品")

	// Registry rows the edge facets point at (real FKs).
	engineA := &model.CatalogEngine{Name: "KiriKiri", Description: "", Aliases: []byte(`[]`)}
	engineB := &model.CatalogEngine{Name: "Ren'Py", Description: "", Aliases: []byte(`[]`)}
	require.NoError(t, testDB.Create(engineA).Error)
	require.NoError(t, testDB.Create(engineB).Error)
	seriesA := &model.CatalogSeries{DisplayName: "series A", SourceID: 2, ExternalID: "SRI-A"}
	seriesB := &model.CatalogSeries{DisplayName: "series B", SourceID: 2, ExternalID: "SRI-B"}
	require.NoError(t, testDB.Create(seriesA).Error)
	require.NoError(t, testDB.Create(seriesB).Error)
	labelA := &model.CatalogLabel{DisplayName: "label A"}
	labelB := &model.CatalogLabel{DisplayName: "label B"}
	require.NoError(t, testDB.Create(labelA).Error)
	require.NoError(t, testDB.Create(labelB).Error)
	charA := createCharacter(t, "主人公")
	charB := createCharacter(t, "ヒロイン")

	create := func(rows ...any) {
		t.Helper()
		for _, r := range rows {
			require.NoError(t, testDB.Create(r).Error)
		}
	}

	// intro — key (work_id, lang, source_id)
	create(
		&model.CatalogWorkIntro{WorkID: target.ID, Lang: "ja", SourceID: 2, Intro: "target ja"},
		&model.CatalogWorkIntro{WorkID: source.ID, Lang: "ja", SourceID: 2, Intro: "loser ja"},
		&model.CatalogWorkIntro{WorkID: source.ID, Lang: "en", SourceID: 2, Intro: "source en"},
	)
	// cover / screenshot — key (work_id, image_hash)
	create(
		&model.CatalogWorkCover{WorkID: target.ID, ImageHash: "hash-A", SortOrder: 0, SourceID: 2},
		&model.CatalogWorkCover{WorkID: source.ID, ImageHash: "hash-A", SortOrder: 9, SourceID: 3},
		&model.CatalogWorkCover{WorkID: source.ID, ImageHash: "hash-B", SortOrder: 1, SourceID: 3},
		&model.CatalogWorkScreenshot{WorkID: target.ID, ImageHash: "shot-A", SourceID: 2},
		&model.CatalogWorkScreenshot{WorkID: source.ID, ImageHash: "shot-A", SourceID: 3},
		&model.CatalogWorkScreenshot{WorkID: source.ID, ImageHash: "shot-B", SourceID: 3},
	)
	// rating — key (work_id, source_id); playtime shares that key
	create(
		&model.CatalogWorkRating{WorkID: target.ID, SourceID: 2, Score: 7},
		&model.CatalogWorkRating{WorkID: source.ID, SourceID: 2, Score: 1},
		&model.CatalogWorkRating{WorkID: source.ID, SourceID: 3, Score: 8},
		&model.CatalogWorkPlaytime{WorkID: target.ID, SourceID: 2, Minutes: 600},
		&model.CatalogWorkPlaytime{WorkID: source.ID, SourceID: 2, Minutes: 60},
		&model.CatalogWorkPlaytime{WorkID: source.ID, SourceID: 3, Minutes: 900},
	)
	// tag — key (work_id, name, source_id)
	create(
		&model.CatalogWorkTag{WorkID: target.ID, Name: "百合", SourceID: 3, Count: 10},
		&model.CatalogWorkTag{WorkID: source.ID, Name: "百合", SourceID: 3, Count: 1},
		&model.CatalogWorkTag{WorkID: source.ID, Name: "拔作", SourceID: 3, Count: 5},
	)
	// popularity — key (work_id, source_id, metric)
	create(
		&model.CatalogWorkPopularity{WorkID: target.ID, SourceID: 2, Metric: 0, Value: 100},
		&model.CatalogWorkPopularity{WorkID: source.ID, SourceID: 2, Metric: 0, Value: 1},
		&model.CatalogWorkPopularity{WorkID: source.ID, SourceID: 2, Metric: 1, Value: 42},
	)
	// platform — key (work_id, platform, source_id)
	create(
		&model.CatalogWorkPlatform{WorkID: target.ID, Platform: "win", SourceID: 3},
		&model.CatalogWorkPlatform{WorkID: source.ID, Platform: "win", SourceID: 3},
		&model.CatalogWorkPlatform{WorkID: source.ID, Platform: "psv", SourceID: 3},
	)
	// engine — key (work_id, engine_id)
	create(
		&model.CatalogWorkEngine{WorkID: target.ID, EngineID: engineA.ID, SourceID: 2},
		&model.CatalogWorkEngine{WorkID: source.ID, EngineID: engineA.ID, SourceID: 3},
		&model.CatalogWorkEngine{WorkID: source.ID, EngineID: engineB.ID, SourceID: 3},
	)
	// series membership — key (series_id, work_id)
	create(
		&model.CatalogSeriesMember{SeriesID: seriesA.ID, WorkID: target.ID},
		&model.CatalogSeriesMember{SeriesID: seriesA.ID, WorkID: source.ID},
		&model.CatalogSeriesMember{SeriesID: seriesB.ID, WorkID: source.ID},
	)
	// brand attribution — PK (work_id, label_id, kind)
	create(
		&model.CatalogWorkLabel{WorkID: target.ID, LabelID: labelA.ID, Kind: model.WorkLabelKindCircle},
		&model.CatalogWorkLabel{WorkID: source.ID, LabelID: labelA.ID, Kind: model.WorkLabelKindCircle},
		&model.CatalogWorkLabel{WorkID: source.ID, LabelID: labelB.ID, Kind: model.WorkLabelKindCircle},
	)
	// roster — key (work_id, character_id); the collision folds first
	createWorkCharacter(t, target.ID, charA.ID, model.WorkCharacterKindUnknown, model.SpoilerNone)
	createWorkCharacter(t, source.ID, charA.ID, model.WorkCharacterKindMain, model.SpoilerSevere)
	createWorkCharacter(t, source.ID, charB.ID, model.WorkCharacterKindMain, model.SpoilerNone)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeWork, source.ID, target.ID, 7, "wave 170b")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	// Two rows each on the survivor, none anywhere on the retired source.
	for _, table := range []string{
		"catalog_work_intro", "catalog_work_cover", "catalog_work_screenshot",
		"catalog_work_rating", "catalog_work_tag", "catalog_work_popularity",
		"catalog_work_playtime", "catalog_work_platform", "catalog_work_engine",
		"catalog_series_member", "catalog_work_label", "catalog_work_character",
	} {
		var onTarget, onSource int64
		require.NoError(t, testDB.Raw(`SELECT count(*) FROM `+table+` WHERE work_id = ?`, target.ID).Scan(&onTarget).Error)
		require.NoError(t, testDB.Raw(`SELECT count(*) FROM `+table+` WHERE work_id = ?`, source.ID).Scan(&onSource).Error)
		assert.EqualValues(t, 2, onTarget, "%s: the source-only row moves, the colliding one drops", table)
		assert.Zero(t, onSource, "%s: no row may stay on the merged-away work", table)
	}

	// The colliding rows kept the SURVIVOR's values (first-source-wins) —
	// spot-checked on the facets whose loser carried a different payload.
	var cover model.CatalogWorkCover
	require.NoError(t, testDB.Where("work_id = ? AND image_hash = ?", target.ID, "hash-A").First(&cover).Error)
	assert.Equal(t, 0, cover.SortOrder, "the colliding cover keeps the survivor's row")
	assert.EqualValues(t, 2, cover.SourceID)

	var rating model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", target.ID, 2).First(&rating).Error)
	assert.EqualValues(t, 7, rating.Score)

	var tag model.CatalogWorkTag
	require.NoError(t, testDB.Where("work_id = ? AND name = ?", target.ID, "百合").First(&tag).Error)
	assert.Equal(t, 10, tag.Count)

	// The roster collision FOLDS rather than merely surviving: unknown kind
	// upgrades to the loser's typed value and spoiler takes the higher.
	var roster model.CatalogWorkCharacter
	require.NoError(t, testDB.Where("work_id = ? AND character_id = ?", target.ID, charA.ID).First(&roster).Error)
	assert.Equal(t, model.WorkCharacterKindMain, roster.Kind, "unknown kind upgrades to the loser's typed value")
	assert.Equal(t, model.SpoilerSevere, roster.Spoiler, "spoiler takes the higher of the two edges")
}

// TestMergeLabelAndPersonCarryIntros pins the same gap on the two entity faces
// whose intro tables (refs/proj/83 E2b / step 65) also postdate the hook list.
func TestMergeLabelAndPersonCarryIntros(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	lTarget := &model.CatalogLabel{DisplayName: "ケロQ"}
	lSource := &model.CatalogLabel{DisplayName: "keroQ"}
	require.NoError(t, testDB.Create(lTarget).Error)
	require.NoError(t, testDB.Create(lSource).Error)
	require.NoError(t, testDB.Create(&model.CatalogLabelIntro{
		LabelID: lTarget.ID, Lang: "ja", SourceID: 2, Intro: "target ja"}).Error)
	require.NoError(t, testDB.Create(&model.CatalogLabelIntro{
		LabelID: lSource.ID, Lang: "ja", SourceID: 2, Intro: "loser ja"}).Error)
	require.NoError(t, testDB.Create(&model.CatalogLabelIntro{
		LabelID: lSource.ID, Lang: "en", SourceID: 2, Intro: "source en"}).Error)

	lp, err := testMerge.ProposeMerge(ctx, model.EntityTypeLabel, lSource.ID, lTarget.ID, 7, "wave 170b")
	require.NoError(t, err)
	approveAndForceExecutable(t, lp.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, lp.ID, nil))

	var labelIntros []model.CatalogLabelIntro
	require.NoError(t, testDB.Where("label_id = ?", lTarget.ID).Order("lang").Find(&labelIntros).Error)
	require.Len(t, labelIntros, 2)
	assert.Equal(t, "source en", labelIntros[0].Intro, "the source-only language moves")
	assert.Equal(t, "target ja", labelIntros[1].Intro, "the colliding key keeps the survivor's text")
	var strandedLabel int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_label_intro WHERE label_id = ?`, lSource.ID).
		Scan(&strandedLabel).Error)
	assert.Zero(t, strandedLabel)

	pTarget := createPerson(t, "田中ロミオ")
	pSource := createPerson(t, "田中 ロミオ")
	require.NoError(t, testDB.Create(&model.CatalogPersonIntro{
		PersonID: pTarget.ID, Lang: "ja", SourceID: 2, Intro: "target ja"}).Error)
	require.NoError(t, testDB.Create(&model.CatalogPersonIntro{
		PersonID: pSource.ID, Lang: "ja", SourceID: 2, Intro: "loser ja"}).Error)
	require.NoError(t, testDB.Create(&model.CatalogPersonIntro{
		PersonID: pSource.ID, Lang: "en", SourceID: 2, Intro: "source en"}).Error)

	pp, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, pSource.ID, pTarget.ID, 7, "wave 170b")
	require.NoError(t, err)
	approveAndForceExecutable(t, pp.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, pp.ID, nil))

	var personIntros []model.CatalogPersonIntro
	require.NoError(t, testDB.Where("person_id = ?", pTarget.ID).Order("lang").Find(&personIntros).Error)
	require.Len(t, personIntros, 2)
	assert.Equal(t, "source en", personIntros[0].Intro)
	assert.Equal(t, "target ja", personIntros[1].Intro)
	var strandedPerson int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_person_intro WHERE person_id = ?`, pSource.ID).
		Scan(&strandedPerson).Error)
	assert.Zero(t, strandedPerson)
}

// TestMergeEntityRelationRehang covers the polymorphic entity↔entity edge
// table on a label merge: edges between the pair would become self-edges the
// CHECK forbids (dropped), the rest move deduped against the composite PK.
func TestMergeEntityRelationRehang(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := &model.CatalogLabel{DisplayName: "survivor"}
	source := &model.CatalogLabel{DisplayName: "loser"}
	other := &model.CatalogLabel{DisplayName: "third party"}
	dup := &model.CatalogLabel{DisplayName: "already related"}
	for _, l := range []*model.CatalogLabel{target, source, other, dup} {
		require.NoError(t, testDB.Create(l).Error)
	}
	relType := seededEntityRelationTypeID(t)

	mkEdge := func(a, b int64) {
		t.Helper()
		require.NoError(t, testDB.Create(&model.CatalogEntityRelation{
			EntityType: model.EntityTypeLabel, AID: a, BID: b, RelationTypeID: relType}).Error)
	}
	mkEdge(source.ID, target.ID) // between the pair → would self-edge, drops
	mkEdge(source.ID, other.ID)  // a-side moves
	mkEdge(other.ID, source.ID)  // b-side moves
	mkEdge(target.ID, dup.ID)    // survivor already has this edge …
	mkEdge(source.ID, dup.ID)    // … so the loser's duplicate drops

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeLabel, source.ID, target.ID, 7, "wave 170b")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	var edges []model.CatalogEntityRelation
	require.NoError(t, testDB.Where("a_id = ? OR b_id = ?", target.ID, target.ID).
		Order("a_id, b_id").Find(&edges).Error)
	require.Len(t, edges, 3, "two moved edges plus the survivor's own, self-edge and duplicate dropped")

	var stranded int64
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_entity_relation WHERE a_id = ? OR b_id = ?`, source.ID, source.ID).
		Scan(&stranded).Error)
	assert.Zero(t, stranded, "no edge may stay on the merged-away label")
}

// seededEntityRelationTypeID returns a relation type usable on entity edges.
func seededEntityRelationTypeID(t *testing.T) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_relation_type ORDER BY id LIMIT 1`).Scan(&id).Error)
	require.NotZero(t, id, "the seeds must provide at least one relation type")
	return id
}
