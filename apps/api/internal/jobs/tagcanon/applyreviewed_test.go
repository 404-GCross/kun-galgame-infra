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

// TestProposeReviewApplyDB is the miniature full-chain rehearsal against a real
// Postgres with the MOCK matcher: three source vocabularies → propose (blocking
// + mock verdicts → JSONL) → make-review (bucket → decisions) → apply-reviewed
// (groups/single rows) → second apply zero-write. It proves the whole 70b
// pipeline end-to-end, including that ONLY exact relations merge (a substring
// pair the mock rates broader never forms a group).
func TestProposeReviewApplyDB(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	vndb, bgm, dl := srcID(t, "vndb"), srcID(t, "bangumi"), srcID(t, "dlsite")
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)

	// vndb vocabulary via catalog_work_tag (wave 149).
	wV1, wV2 := mkBodylessWork(t, medium), mkBodylessWork(t, medium)
	mkWorkTag(t, wV1, "百合", 0, vndb) // usage 1 → cross-source EXACT with bangumi 百合
	mkWorkTag(t, wV1, "巨乳", 0, vndb) // usage 2 → substring (broader) with dlsite 巨乳/爆乳, NOT merged
	mkWorkTag(t, wV2, "巨乳", 0, vndb)
	mkWorkTag(t, wV1, "破处", 0, vndb) // usage 1 → single-source admission

	// bangumi + dlsite vocabulary via catalog_work_tag.
	wB := mkBodylessWork(t, medium)
	wD := mkBodylessWork(t, medium)
	mkWorkTag(t, wB, "百合", 5, bgm)    // exact with vndb 百合
	mkWorkTag(t, wD, "巨乳/爆乳", 40, dl) // substring superset of vndb 巨乳

	dir := t.TempDir()
	verdicts := filepath.Join(dir, "verdicts.jsonl")
	md := filepath.Join(dir, "review.md")
	dec := filepath.Join(dir, "decisions.jsonl")

	// --- propose (mock, offline) — threshold 1 so the small fixtures admit.
	pst, err := Propose(ctx, MockMatcher{Model: "stub"}, ProposeOpts{
		DSN: testDSN, Out: verdicts, SingleThreshold: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, pst.RelationCounts[RelExact], "百合↔百合 (edit distance 0)")
	assert.GreaterOrEqual(t, pst.RelationCounts[RelBroader]+pst.RelationCounts[RelNarrower], 1, "巨乳 ⊂ 巨乳/爆乳")
	assert.GreaterOrEqual(t, pst.SingleProposed, 2, "巨乳 / 破处 / 巨乳/爆乳 admitted (百合 excluded — exact-paired)")

	// --- review — high-confidence auto-approves.
	rst, err := MakeReview(verdicts, md, dec, ReviewOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, rst.HighExact, "the exact pair auto-passes")
	assert.GreaterOrEqual(t, rst.NonExact[RelBroader]+rst.NonExact[RelNarrower], 1, "substring pair留档, not merged")

	// --- apply-reviewed dry then apply.
	ast, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Groups, "one cross-source exact group (百合)")
	assert.Zero(t, tagCount(t), "dry writes nothing")

	ast, err = ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Groups)
	assert.Equal(t, 1, ast.ApprovedPairs)
	// tags: 百合 (group) + the approved single rows (巨乳, 破处, 巨乳/爆乳).
	assert.EqualValues(t, ast.TagsCreated, tagCount(t))
	assert.GreaterOrEqual(t, ast.TagsCreated, 3)
	assert.Zero(t, ast.Errors)

	// 百合 is a cross-source core group with two map rows.
	var yuri model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "百合").First(&yuri).Error)
	assert.EqualValues(t, model.TagTierCore, yuri.Tier)
	var yuriMaps int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_tag_source_map WHERE tag_id = ?`, yuri.ID).Scan(&yuriMaps).Error)
	assert.EqualValues(t, 2, yuriMaps, "vndb + bangumi 百合")

	// 巨乳/爆乳 never merged with 巨乳 (broader, not exact) — both exist as
	// their own single-source rows.
	assert.EqualValues(t, 1, namedTagCount(t, "巨乳"))
	assert.EqualValues(t, 1, namedTagCount(t, "巨乳/爆乳"))

	// --- second apply: idempotent, zero writes.
	ast2, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, ast2.TagsCreated+ast2.MapsCreated+ast2.TierUpdated+ast2.Errors, "second pass writes zero")
	assert.Greater(t, ast2.TagsConflict, 0)
}

// TestApplyReviewedOnlyExactAndTierUpdate isolates two guarantees on hand-built
// decisions: (1) only APPROVED EXACT pairs merge — an approved narrower pair
// forms no group; (2) the explicit tier/kind UPDATE path is idempotent — a
// re-applied EDITED single-source tier takes effect once, then writes zero.
func TestApplyReviewedOnlyExactAndTierUpdate(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	dir := t.TempDir()
	dec := filepath.Join(dir, "decisions.jsonl")

	base := []pairRec{
		// approved exact cross-source pair → one group.
		{Kind: "pair", ASource: "bangumi", AName: "银发", AUsage: 90, BSource: "dlsite", BName: "白毛", BUsage: 80,
			Relation: "exact", Confidence: 0.97, Approve: true, Bucket: "high"},
		// approved NARROWER pair → must NOT merge.
		{Kind: "pair", ASource: "vndb", AName: "催眠", AUsage: 200, BSource: "dlsite", BName: "精神控制", BUsage: 100,
			Relation: "narrower", Confidence: 0.9, Approve: true, Bucket: "hierarchy"},
		// approved single-source admission (starts longtail).
		{Kind: "single", Source: "vndb", Name: "破处", Usage: 6000,
			Tier: i16p(model.TagTierLongtail), Kind_: i16p(model.TagKindContent),
			Confidence: 0.95, Approve: true, Bucket: "high"},
	}
	require.NoError(t, writeRecords(dec, base))

	ast, err := ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.Groups, "only the exact pair merges")
	assert.Equal(t, 1, ast.ApprovedPairs, "the narrower pair is not an approved exact")
	// 催眠 / 精神控制 must have NO canonical rows (narrower never merges).
	assert.EqualValues(t, 0, namedTagCount(t, "催眠"))
	assert.EqualValues(t, 0, namedTagCount(t, "精神控制"))
	// 破处 exists at longtail.
	assert.EqualValues(t, model.TagTierLongtail, tierOf(t, "破处"))
	assert.Equal(t, 0, ast.TierUpdated, "insert already correct — no UPDATE fired")

	// --- edit the single's tier → core, re-apply: exactly one tier UPDATE.
	base[2].Tier = i16p(model.TagTierCore)
	require.NoError(t, writeRecords(dec, base))
	ast, err = ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, ast.TierUpdated, "longtail → core is one explicit UPDATE")
	assert.Zero(t, ast.TagsCreated+ast.MapsCreated, "no new rows — only the retier")
	assert.EqualValues(t, model.TagTierCore, tierOf(t, "破处"))

	// --- re-apply unchanged: zero writes (idempotent).
	ast, err = ApplyReviewed(ctx, ApplyReviewedOpts{DSN: testDSN, Decisions: dec, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, ast.TierUpdated+ast.TagsCreated+ast.MapsCreated, "idempotent second identical apply")
}

// TestHTTPMatcher is the LLM-call-layer gate (httptest): the client posts the
// pinned Chinese system prompt + the pair/name user content and parses the
// strict-JSON reply. Both endpoints are exercised on the SAME /chat/completions
// wire.
func TestHTTPMatcher(t *testing.T) {
	var gotAuth, gotSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		var req mChatRequest
		_ = json.Unmarshal(b, &req)
		gotSystem = req.Messages[0].Content
		// echo a verdict shaped by which prompt was sent.
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

	// invalid relation is rejected (never silently merged).
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"{\"relation\":\"maybe\",\"confidence\":1}"},"finish_reason":"stop"}]}`)
	}))
	defer badSrv.Close()
	_, _, err = NewHTTPMatcher(badSrv.URL, "t", "m", 64).MatchPair(context.Background(), PairInput{})
	assert.ErrorContains(t, err, "invalid relation")

	assert.False(t, NewHTTPMatcher("", "t", "m", 64).Configured(), "no base → not configured")
}

// ── helpers ──────────────────────────────────────────────────────────────────

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
