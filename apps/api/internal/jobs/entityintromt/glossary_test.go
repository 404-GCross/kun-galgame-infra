package entityintromt

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlossaryCanonical(t *testing.T) {
	assert.Equal(t, "", Glossary(nil).Canonical(), "an empty glossary serializes to nothing")

	g := Glossary{{Src: "水橋 かおり", Zh: "水桥香织"}, {Src: "星空のメモリア", Zh: "星空的记忆"}}
	assert.Equal(t, "水橋 かおり\t水桥香织\n星空のメモリア\t星空的记忆", g.Canonical())

	rev := Glossary{g[1], g[0]}
	assert.NotEqual(t, g.Canonical(), rev.Canonical(), "order is part of the value")
}

func TestGlossaryPromptSection(t *testing.T) {
	assert.Equal(t, "", Glossary(nil).PromptSection(), "no terms → no section")
	assert.Equal(t, TranslateSystemPrompt, withGlossary(TranslateSystemPrompt, nil),
		"an empty glossary leaves the pinned prompt byte-identical")

	g := Glossary{{Src: "水橋 かおり", Zh: "水桥香织"}, {Src: "星空のメモリア", Zh: "星空的记忆"}}
	assert.Equal(t,
		GlossaryHeader+"\n水橋 かおり → 水桥香织\n星空のメモリア → 星空的记忆\n"+GlossaryRule,
		g.PromptSection())

	full := withGlossary(TranslateSystemPrompt, g)
	assert.True(t, strings.HasPrefix(full, TranslateSystemPrompt), "the base prompt is untouched")
	assert.Contains(t, full, GlossaryRule)
	assert.Contains(t, withGlossary(TranslateSystemPromptEn, g), GlossaryRule,
		"the English lane gets the same section")
}

func TestHashCandidateBackwardCompatible(t *testing.T) {
	const text = "ヒロインの一人。"
	assert.Equal(t, hashSource(text), hashCandidate(text, nil),
		"empty glossary MUST hash exactly like the pre-glossary job")
	assert.Equal(t, hashSource(text), hashCandidate(text, Glossary{}))

	g := Glossary{{Src: "水橋 かおり", Zh: "水桥香织"}}
	assert.NotEqual(t, hashSource(text), hashCandidate(text, g))
	assert.Equal(t, hashSource(text+"\x00"+g.Canonical()), hashCandidate(text, g))
	assert.NotEqual(t, hashCandidate(text, g),
		hashCandidate(text, Glossary{{Src: "水橋 かおり", Zh: "水桥薰"}}),
		"a changed rendering re-translates")
}

func TestGlossaryBuilderCapAndDedup(t *testing.T) {
	var b glossaryBuilder
	b.add("A", "甲")
	b.add("A", "乙")
	b.add(" B ", " 乙 ")
	b.add("", "丙")
	b.add("C", "")
	b.add("D", "D")
	assert.Equal(t, Glossary{{Src: "A", Zh: "甲"}, {Src: "B", Zh: "乙"}}, b.out)

	var capped glossaryBuilder
	for i := range maxGlossaryEntries + 10 {
		capped.add(strings.Repeat("x", i+1), "译")
	}
	assert.Len(t, capped.out, maxGlossaryEntries, "the cap holds")
	assert.Equal(t, "x", capped.out[0].Src, "the cap keeps the HIGHEST-priority entries")
}

func cleanWorks(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec("TRUNCATE catalog_work CASCADE").Error)
}

func anyRoleID(t *testing.T) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_role ORDER BY id LIMIT 1`).Scan(&id).Error)
	require.NotZero(t, id, "the seed must carry the role vocabulary")
	return id
}

func mkTitledWork(t *testing.T, ja, zh string) int64 {
	t.Helper()
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: ja}
	require.NoError(t, testDB.Create(&w).Error)
	require.NoError(t, testDB.Create(&model.CatalogWorkTitle{
		WorkID: w.ID, Lang: "ja", Title: ja, Kind: model.WorkTitleKindOfficial}).Error)
	if zh != "" {
		require.NoError(t, testDB.Create(&model.CatalogWorkTitle{
			WorkID: w.ID, Lang: "zh-Hans", Title: zh, Kind: model.WorkTitleKindOfficial}).Error)
	}
	require.NoError(t, testDB.Create(&model.CatalogWorkTitle{
		WorkID: w.ID, Lang: "ja", Title: ja + " searchhint", Kind: model.WorkTitleKindSearchHint}).Error)
	return w.ID
}

func mkCharAlias(t *testing.T, charID int64, name, lang string, primary bool) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogCharacterAlias{
		CharacterID: charID, Name: name, Lang: lang,
		Kind: model.AliasKindTranslation, IsPrimaryForLocale: primary,
	}).Error)
}

func mkRoster(t *testing.T, workID, charID int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkCharacter{
		WorkID: workID, CharacterID: charID, Kind: 0, Spoiler: 0, MatchedBy: "import:test",
	}).Error)
}

func mkNamedPerson(t *testing.T, ja, zh string) (int64, int64) {
	t.Helper()
	p := model.CatalogPerson{DisplayName: ja}
	require.NoError(t, testDB.Create(&p).Error)
	cn := model.CatalogCreditName{PersonID: &p.ID, Name: ja, Lang: "ja",
		Kind: 0, LinkVisibility: model.LinkVisibilityPublic}
	require.NoError(t, testDB.Create(&cn).Error)
	require.NoError(t, testDB.Model(&model.CatalogPerson{}).Where("id = ?", p.ID).
		Update("primary_credit_name_id", cn.ID).Error)
	if zh != "" {
		require.NoError(t, testDB.Create(&model.CatalogNameAlias{
			CreditNameID: cn.ID, Name: zh, Lang: "zh-Hans",
			Kind: model.AliasKindTranslation, IsPrimaryForLocale: true,
		}).Error)
	}
	return p.ID, cn.ID
}

func mkCredit(t *testing.T, workID, creditNameID int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogCredit{
		WorkID: workID, CreditNameID: creditNameID, RoleID: anyRoleID(t), Spoiler: 0,
	}).Error)
}

func mkLabelAlias(t *testing.T, labelID int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogLabelAlias{
		LabelID: labelID, Name: name, Lang: "zh-Hans",
		Kind: model.AliasKindTranslation, IsPrimaryForLocale: true,
	}).Error)
}

func mkLabelWork(t *testing.T, workID, labelID int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkLabel{
		WorkID: workID, LabelID: labelID, Kind: 0,
	}).Error)
}

func laneByKey(t *testing.T, key string) laneDef {
	t.Helper()
	l, err := selectLanes(key)
	require.NoError(t, err)
	return l[0]
}

func TestLoadGlossaries_CharacterLane(t *testing.T) {
	clean(t)
	cleanWorks(t)
	ctx := context.Background()

	ch := mkCharacter(t, "水橋 かおり")
	mkCharAlias(t, ch, "水桥薰", "zh-Hant", false)
	mkCharAlias(t, ch, "水桥香织", "zh-Hans", true)
	w1 := mkTitledWork(t, "星空のメモリア", "星空的记忆")
	w2 := mkTitledWork(t, "夏空のペルソナ", "夏空的人格")
	wNoZh := mkTitledWork(t, "訳のない作品", "")
	for _, w := range []int64{w1, w2, wNoZh} {
		mkRoster(t, w, ch)
	}

	bare := mkCharacter(t, "名無し")

	gs, err := loadGlossaries(ctx, testDB, laneByKey(t, LaneCharacter), []int64{ch, bare})
	require.NoError(t, err)
	assert.Equal(t, Glossary{
		{Src: "水橋 かおり", Zh: "水桥香织"},
		{Src: "星空のメモリア", Zh: "星空的记忆"},
		{Src: "夏空のペルソナ", Zh: "夏空的人格"},
	}, gs[ch], "own name first (primary alias), then host works by id")
	assert.NotContains(t, gs, bare)

	again, err := loadGlossaries(ctx, testDB, laneByKey(t, LaneCharacter), []int64{ch, bare})
	require.NoError(t, err)
	assert.Equal(t, gs[ch].Canonical(), again[ch].Canonical(), "deterministic — the hash depends on it")
}

func TestLoadGlossaries_PersonLane(t *testing.T) {
	clean(t)
	cleanWorks(t)
	ctx := context.Background()

	p, cn := mkNamedPerson(t, "田中 涼子", "田中凉子")
	w1 := mkTitledWork(t, "星空のメモリア", "星空的记忆")
	w2 := mkTitledWork(t, "夏空のペルソナ", "夏空的人格")
	mkCredit(t, w1, cn)
	mkCredit(t, w2, cn)

	noName, _ := mkNamedPerson(t, "無名 太郎", "")

	gs, err := loadGlossaries(ctx, testDB, laneByKey(t, LanePerson), []int64{p, noName})
	require.NoError(t, err)
	assert.Equal(t, Glossary{
		{Src: "田中 涼子", Zh: "田中凉子"},
		{Src: "星空のメモリア", Zh: "星空的记忆"},
		{Src: "夏空のペルソナ", Zh: "夏空的人格"},
	}, gs[p])
	assert.NotContains(t, gs, noName)
}

func TestLoadGlossaries_LabelLane(t *testing.T) {
	clean(t)
	cleanWorks(t)
	ctx := context.Background()

	la := mkLabel(t, "ういんどみる")
	mkLabelAlias(t, la, "风车")
	for i := range maxWorksPerEntity + 5 {
		w := mkTitledWork(t, "作品"+string(rune('A'+i)), "作品译"+string(rune('A'+i)))
		mkLabelWork(t, w, la)
	}

	gs, err := loadGlossaries(ctx, testDB, laneByKey(t, LaneLabel), []int64{la})
	require.NoError(t, err)
	g := gs[la]
	assert.Equal(t, GlossaryEntry{Src: "ういんどみる", Zh: "风车"}, g[0], "own name first")
	assert.Len(t, g, 1+maxWorksPerEntity, "the per-entity work cap bounds the query")
	assert.Equal(t, "作品A", g[1].Src, "works in work-id order")
}

func TestGlossaryReachesPromptAndHash(t *testing.T) {
	clean(t)
	cleanWorks(t)
	ctx := context.Background()
	vndb, _ := srcIDs(t)

	const withText = "ヒロインの一人。星空のメモリアに登場する。"
	const bareText = "学園の片隅にいつもいる、名前のない生徒。"

	ch := mkCharacter(t, "水橋 かおり")
	mkCharIntro(t, ch, "ja", withText, vndb)
	mkCharAlias(t, ch, "水桥香织", "zh-Hans", true)

	bare := mkCharacter(t, "名無し")
	mkCharIntro(t, bare, "ja", bareText, vndb)

	for _, seed := range []struct {
		id   int64
		text string
	}{{ch, withText}, {bare, bareText}} {
		require.NoError(t, testDB.Create(&model.CatalogCharacterIntro{
			CharacterID: seed.id, Lang: "zh-Hans", Intro: "旧的机器译文", SourceID: vndb,
			Provenance: 1, SrcHash: hashSource(seed.text), MTModel: "old-mt",
		}).Error)
	}

	tr := &recordingTranslator{}
	st, err := Run(ctx, tr, Opts{DSN: testDSN, Apply: true, Lane: LaneCharacter})
	require.NoError(t, err)
	require.Len(t, st, 1)
	assert.Equal(t, 2, st[0].Candidates)
	assert.Equal(t, 1, st[0].WithGlossary, "only the aliased character has terms")
	assert.Equal(t, 1, st[0].WouldRetranslate, "the glossary changed the prompt → re-translate once")
	assert.Equal(t, 1, st[0].SkipUnchanged, "the bare character keeps the plain hash → untouched")
	require.Len(t, tr.seen, 1)
	assert.Equal(t, Glossary{{Src: "水橋 かおり", Zh: "水桥香织"}}, tr.seen[0],
		"the candidate's term list reached the LLM seam")

	row, ok := readMachine(t, ch)
	require.True(t, ok)
	assert.Equal(t, hashCandidate(withText, tr.seen[0]), row.SrcHash)
	assert.NotEqual(t, hashSource(withText), row.SrcHash)

	untouched, ok := readMachine(t, bare)
	require.True(t, ok)
	assert.Equal(t, "旧的机器译文", untouched.Intro, "no glossary → no re-translation")

	tr2 := &recordingTranslator{}
	st, err = Run(ctx, tr2, Opts{DSN: testDSN, Apply: true, Lane: LaneCharacter})
	require.NoError(t, err)
	assert.Equal(t, 2, st[0].SkipUnchanged)
	assert.Empty(t, tr2.seen, "unchanged glossary + source → no LLM call")

	w := mkTitledWork(t, "星空のメモリア", "星空的记忆")
	mkRoster(t, w, ch)
	tr3 := &recordingTranslator{}
	st, err = Run(ctx, tr3, Opts{DSN: testDSN, Apply: true, Lane: LaneCharacter})
	require.NoError(t, err)
	assert.Equal(t, 1, st[0].Retranslated)
	assert.Equal(t, 1, st[0].SkipUnchanged)
	require.Len(t, tr3.seen, 1)
	assert.Len(t, tr3.seen[0], 2, "the new title pair joined the term list")
}

type recordingTranslator struct{ seen []Glossary }

func (r *recordingTranslator) Translate(_ context.Context, text string, _ SourceLang, gloss Glossary) (string, string, error) {
	r.seen = append(r.seen, gloss)
	return "[译] " + text, "gloss-mt", nil
}

func TestMockTranslatorShowsGlossary(t *testing.T) {
	m := MockTranslator{Model: "stub"}
	none, _, err := m.Translate(context.Background(), "原文", SourceJa, nil)
	require.NoError(t, err)
	assert.Contains(t, none, "[gloss:0]")

	some, _, err := m.Translate(context.Background(), "原文", SourceJa, Glossary{{Src: "A", Zh: "甲"}})
	require.NoError(t, err)
	assert.Contains(t, some, "[gloss:1]")
	assert.NotEqual(t, none, some, "glossary presence is visible in the mock output")
}
