package tagcanon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProposeReviewApplyDB(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	vndb, bgm, dl := srcID(t, "vndb"), srcID(t, "bangumi"), srcID(t, "dlsite")
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)

	wV1, wV2 := mkBodylessWork(t, medium), mkBodylessWork(t, medium)
	mkWorkTag(t, wV1, "百合", 0, vndb)
	mkWorkTag(t, wV1, "巨乳", 0, vndb)
	mkWorkTag(t, wV2, "巨乳", 0, vndb)
	mkWorkTag(t, wV1, "破处", 0, vndb)

	wB := mkBodylessWork(t, medium)
	wD := mkBodylessWork(t, medium)
	mkWorkTag(t, wB, "百合", 5, bgm)
	mkWorkTag(t, wD, "巨乳/爆乳", 40, dl)

	dir := t.TempDir()
	verdicts := filepath.Join(dir, "verdicts.jsonl")
	md := filepath.Join(dir, "review.md")
	dec := filepath.Join(dir, "decisions.jsonl")

	pst, err := Propose(ctx, MockMatcher{Model: "stub"}, ProposeOpts{
		DSN: testDSN, Out: verdicts, SingleThreshold: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, pst.RelationCounts[RelExact], "百合↔百合 (edit distance 0)")
	assert.GreaterOrEqual(t, pst.RelationCounts[RelBroader]+pst.RelationCounts[RelNarrower], 1, "巨乳 ⊂ 巨乳/爆乳")
	assert.GreaterOrEqual(t, pst.SingleProposed, 2, "巨乳 / 破处 / 巨乳/爆乳 admitted (百合 excluded — exact-paired)")

	rst, err := MakeReview(verdicts, md, dec, ReviewOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, rst.HighExact, "the exact pair auto-passes")
	assert.GreaterOrEqual(t, rst.NonExact[RelBroader]+rst.NonExact[RelNarrower], 1, "substring pair留档, not merged")

	ast, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Groups, "one cross-source exact group (百合)")
	assert.Zero(t, tagCount(t), "dry writes nothing")

	ast, err = ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Groups)
	assert.Equal(t, 1, ast.ApprovedPairs)
	assert.EqualValues(t, ast.TagsCreated, tagCount(t))
	assert.GreaterOrEqual(t, ast.TagsCreated, 3)
	assert.Zero(t, ast.Errors)

	var yuri model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "百合").First(&yuri).Error)
	assert.EqualValues(t, model.TagTierCore, yuri.Tier)
	var yuriMaps int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_tag_source_map WHERE tag_id = ?`, yuri.ID).Scan(&yuriMaps).Error)
	assert.EqualValues(t, 2, yuriMaps, "vndb + bangumi 百合")

	assert.EqualValues(t, 1, namedTagCount(t, "巨乳"))
	assert.EqualValues(t, 1, namedTagCount(t, "巨乳/爆乳"))

	ast2, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, ast2.TagsCreated+ast2.MapsCreated+ast2.TierUpdated+ast2.Errors, "second pass writes zero")
	assert.Greater(t, ast2.TagsConflict, 0)
}

func TestApplyReviewedOnlyExactAndTierUpdate(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	dir := t.TempDir()
	dec := filepath.Join(dir, "decisions.jsonl")

	base := []pairRec{
		{Kind: "pair", ASource: "bangumi", AName: "银发", AUsage: 90, BSource: "dlsite", BName: "白毛", BUsage: 80,
			Relation: "exact", Confidence: 0.97, Approve: true, Bucket: "high"},
		{Kind: "pair", ASource: "vndb", AName: "催眠", AUsage: 200, BSource: "dlsite", BName: "精神控制", BUsage: 100,
			Relation: "narrower", Confidence: 0.9, Approve: true, Bucket: "hierarchy"},
		{Kind: "single", Source: "vndb", Name: "破处", Usage: 6000,
			Tier: i16p(model.TagTierLongtail), Kind_: i16p(model.TagKindContent),
			Confidence: 0.95, Approve: true, Bucket: "high"},
	}
	require.NoError(t, writeRecords(dec, base))

	ast, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Groups, "only the exact pair merges")
	assert.Equal(t, 1, ast.ApprovedPairs, "the narrower pair is not an approved exact")
	assert.EqualValues(t, 0, namedTagCount(t, "催眠"))
	assert.EqualValues(t, 0, namedTagCount(t, "精神控制"))
	assert.EqualValues(t, model.TagTierLongtail, tierOf(t, "破处"))
	assert.Equal(t, 0, ast.TierUpdated, "insert already correct — no UPDATE fired")

	base[2].Tier = i16p(model.TagTierCore)
	require.NoError(t, writeRecords(dec, base))
	ast, err = ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.TierUpdated, "longtail → core is one explicit UPDATE")
	assert.Zero(t, ast.TagsCreated+ast.MapsCreated, "no new rows — only the retier")
	assert.EqualValues(t, model.TagTierCore, tierOf(t, "破处"))

	ast, err = ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, ast.TierUpdated+ast.TagsCreated+ast.MapsCreated, "idempotent second identical apply")
}

func TestApplyReviewedMapAndRejectRecords(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	dir := t.TempDir()
	dec := filepath.Join(dir, "decisions.jsonl")

	require.NoError(t, testDB.Create(&model.CatalogTag{Name: "长袜", Tier: model.TagTierLongtail}).Error)
	var target model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "长袜").First(&target).Error)

	recs := []pairRec{
		{Kind: "map", Source: "bangumi", Name: "過膝襪", MapToID: target.ID, Approve: true},
		{Kind: "map", Source: "bangumi", Name: "榨汁姬", MapTo: "榨精",
			Tier: i16p(model.TagTierLongtail), Kind_: i16p(model.TagKindContent), Sexual: true, Approve: true},
		{Kind: "map", Source: "curated", Name: "果汁姬", MapTo: "榨精", Approve: true},
		{Kind: "single", Source: "curated", Name: "血缘姐妹", Usage: 2,
			Tier: i16p(model.TagTierLongtail), Kind_: i16p(model.TagKindContent), Approve: true},
		{Kind: "reject", Source: "bangumi", Name: "竹子社", Reason: "会社别名", By: "wave-208", Approve: true},
		{Kind: "map", Source: "bangumi", Name: "悬空", MapToID: 99999999, Approve: true},
	}
	require.NoError(t, writeRecords(dec, recs))

	dst, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec})
	require.NoError(t, err)
	assert.Equal(t, 4, dst.MapRows)
	assert.Equal(t, 1, dst.RejectRows)
	assert.Equal(t, 1, dst.SingleRows)

	ast, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Errors, "the dangling map_to_id is a counted error")
	assert.Equal(t, 4, ast.MapsCreated, "過膝襪 + 榨汁姬 + 果汁姬 + the single's own map")
	assert.Equal(t, 2, ast.TagsCreated, "榨精 + 血缘姐妹")
	assert.Equal(t, 1, ast.TagsConflict, "果汁姬 resolves the already-created 榨精")
	assert.Equal(t, 1, ast.RejectsCreated)

	var mapped int64
	require.NoError(t, testDB.Raw(`SELECT tag_id FROM catalog_tag_source_map WHERE source_name = ?`, "過膝襪").Scan(&mapped).Error)
	assert.Equal(t, target.ID, mapped, "map row keeps the SOURCE name and points at the existing canonical")

	var juice model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "榨精").First(&juice).Error)
	assert.True(t, juice.Sexual, "canonical created by a map record carries the reviewed sexual flag")
	var juiceMaps int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_tag_source_map WHERE tag_id = ?`, juice.ID).Scan(&juiceMaps).Error)
	assert.EqualValues(t, 2, juiceMaps, "bangumi 榨汁姬 + curated 果汁姬 both land on 榨精")

	var single model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "血缘姐妹").First(&single).Error)
	assert.False(t, single.Sexual)
	var curatedMapped int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_tag_source_map m
		JOIN catalog_source s ON s.id = m.source_id AND s.key = 'curated'
		WHERE m.source_name IN (?, ?)`, "血缘姐妹", "果汁姬").Scan(&curatedMapped).Error)
	assert.EqualValues(t, 2, curatedMapped, "curated source resolves and writes")

	var rej int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_tag_rejection WHERE source_name = ? AND rejected_by = ?`,
		"竹子社", "wave-208").Scan(&rej).Error)
	assert.EqualValues(t, 1, rej)

	ast2, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, ast2.TagsCreated+ast2.MapsCreated+ast2.RejectsCreated, "second pass writes zero")
	assert.Equal(t, 1, ast2.RejectsConflict)
}

func TestHTTPMatcher(t *testing.T) {
	var gotAuth, gotSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		var req mChatRequest
		_ = json.Unmarshal(b, &req)
		gotSystem = req.Messages[0].Content
		if req.Messages[0].Content == PairMatchSystemPrompt {
			_, _ = io.WriteString(w, `{"model":"glm-5.2","choices":[{"message":{"role":"assistant","content":"{\"relation\":\"exact\",\"confidence\":0.95,\"reason\":\"同义\"}"},"finish_reason":"stop"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"model":"glm-5.2","choices":[{"message":{"role":"assistant","content":"{\"tier\":\"core\",\"kind\":\"content\",\"confidence\":0.9,\"reason\":\"高用量内容\"}"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	m := NewHTTPMatcher(srv.URL, "sekret", "glm-5.2", 512)
	require.True(t, m.Configured())

	pv, mdl, err := m.MatchPair(context.Background(), PairInput{
		ASourceKey: "vndb", AName: "百合", AOrig: "Yuri", AUsage: 900,
		BSourceKey: "bangumi", BName: "百合", BOrig: "百合", BUsage: 40,
	})
	require.NoError(t, err)
	assert.Equal(t, RelExact, pv.Relation)
	assert.InDelta(t, 0.95, pv.Confidence, 0.001)
	assert.Equal(t, "同义", pv.Reason)
	assert.Equal(t, "glm-5.2", mdl)
	assert.Equal(t, "Bearer sekret", gotAuth)
	assert.Equal(t, PairMatchSystemPrompt, gotSystem)

	nv, _, err := m.ClassifyName(context.Background(), NameInput{SourceKey: "vndb", Name: "破处", Orig: "Defloration", Usage: 6000})
	require.NoError(t, err)
	assert.EqualValues(t, model.TagTierCore, nv.Tier)
	assert.EqualValues(t, model.TagKindContent, nv.Kind)
	assert.Equal(t, NameClassifySystemPrompt, gotSystem)

	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"{\"relation\":\"maybe\",\"confidence\":1}"},"finish_reason":"stop"}]}`)
	}))
	defer badSrv.Close()
	_, _, err = NewHTTPMatcher(badSrv.URL, "t", "m", 64).MatchPair(context.Background(), PairInput{})
	assert.ErrorContains(t, err, "invalid relation")

	assert.False(t, NewHTTPMatcher("", "t", "m", 64).Configured(), "no base → not configured")
}

func tagCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_tag").Scan(&n).Error)
	return n
}

func namedTagCount(t *testing.T, name string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_tag WHERE name = ?", name).Scan(&n).Error)
	return n
}

func tierOf(t *testing.T, name string) int16 {
	t.Helper()
	var tier int16
	require.NoError(t, testDB.Raw("SELECT tier FROM catalog_tag WHERE name = ?", name).Scan(&tier).Error)
	return tier
}
