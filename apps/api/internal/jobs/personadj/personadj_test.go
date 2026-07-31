package personadj

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

const twoPackets = `{"bucket":"person_edge","key":"pe:1:2","user":"A) 甲\nB) 乙","meta":{"vndb_probe":"diff"}}
{"bucket":"character_cv","key":"cv:3:4","user":"A) 丙\nB) 丁"}
`

func TestLoadPacketsFiltersAndValidates(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "packets.jsonl", twoPackets)

	all, err := LoadPackets(p, "")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, BucketPersonEdge, all[0].Bucket)
	// meta is carried through verbatim and is never part of the prompt.
	assert.JSONEq(t, `{"vndb_probe":"diff"}`, string(all[0].Meta))

	only, err := LoadPackets(p, BucketCharacterCV)
	require.NoError(t, err)
	require.Len(t, only, 1)
	assert.Equal(t, "cv:3:4", only[0].Key)

	_, err = LoadPackets(writeFile(t, dir, "dup.jsonl",
		`{"bucket":"e4_split","key":"k","user":"x"}`+"\n"+`{"bucket":"e4_split","key":"k","user":"y"}`+"\n"), "")
	assert.ErrorContains(t, err, "already used")

	_, err = LoadPackets(writeFile(t, dir, "bad.jsonl", `{"bucket":"nope","key":"k","user":"x"}`+"\n"), "")
	assert.ErrorContains(t, err, "unknown bucket")
}

func TestParseVerdictVocabularyAndFences(t *testing.T) {
	pe := Packet{Key: "pe:1:2", Bucket: BucketPersonEdge}
	v, err := parseVerdict(pe, "```json\n{\"verdict\":\"MERGE\",\"confidence\":0.9,\"entity_kind\":\"公司\",\"reason\":\"同一家\"}\n```", "glm")
	require.NoError(t, err)
	assert.Equal(t, VerdictMerge, v.Verdict)
	assert.Equal(t, KindOrganization, v.EntityKind)
	assert.Equal(t, "glm", v.Model)

	// A thinking model that leaks prose around the object still parses.
	v, err = parseVerdict(pe, "我认为是同一人。\n{\"verdict\":\"distinct\",\"confidence\":0.7,\"reason\":\"同名异人\"}\n以上。", "glm")
	require.NoError(t, err)
	assert.Equal(t, VerdictDistinct, v.Verdict)
	assert.Equal(t, KindUnknown, v.EntityKind)

	// Out-of-vocabulary answers are errors, never a silent "unsure" — an
	// unjudged packet must be visible in the batch's error count.
	_, err = parseVerdict(pe, `{"verdict":"probably","confidence":0.9}`, "glm")
	assert.ErrorContains(t, err, "invalid verdict")
	_, err = parseVerdict(pe, `not json at all`, "glm")
	assert.ErrorContains(t, err, "decode verdict")

	// The e4 lane carries detach_sources; the character lane carries no kind.
	e4, err := parseVerdict(Packet{Key: "e4:1", Bucket: BucketE4Split},
		`{"verdict":"distinct","confidence":0.8,"detach_sources":[" EG ",""],"reason":"裸姓"}`, "glm")
	require.NoError(t, err)
	assert.Equal(t, []string{"eg"}, e4.DetachSources)
	cv, err := parseVerdict(Packet{Key: "cv:1:2", Bucket: BucketCharacterCV},
		`{"verdict":"merge","confidence":0.8,"entity_kind":"person","reason":"同一角色"}`, "glm")
	require.NoError(t, err)
	assert.Empty(t, cv.EntityKind)
}

// A truncated thinking-model answer is the failure this discipline exists for:
// the body looks like valid JSON but the generation never finished.
func TestHTTPJudgeRefusesNonStopFinishReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"model":"glm","choices":[{"message":{"role":"assistant","content":"{\"verdict\":\"merge\",\"confidence\":0.99,\"reason\":\"x\"}"},"finish_reason":"length"}]}`)
	}))
	defer srv.Close()
	j := NewHTTPJudge(srv.URL, "t", "glm", 128, 0)
	_, err := j.Judge(context.Background(), Packet{Key: "k", Bucket: BucketPersonEdge, User: "u"})
	assert.ErrorContains(t, err, "finish_reason")
}

func TestHTTPJudgeRetriesThenSucceeds(t *testing.T) {
	old := retrySchedule
	retrySchedule = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retrySchedule = old }()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var req chatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		// The pinned system prompt is what goes on the wire, and temperature is 0.
		assert.Equal(t, SystemPrompt(BucketPersonEdge), req.Messages[0].Content)
		assert.Equal(t, "u", req.Messages[1].Content)
		assert.Zero(t, req.Temperature)
		fmt.Fprint(w, `{"model":"glm-5.2","choices":[{"message":{"content":"{\"verdict\":\"unsure\",\"confidence\":0.4,\"reason\":\"证据不足\"}"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	v, err := NewHTTPJudge(srv.URL, "t", "glm", 0, 600).Judge(context.Background(),
		Packet{Key: "k", Bucket: BucketPersonEdge, User: "u"})
	require.NoError(t, err)
	assert.Equal(t, VerdictUnsure, v.Verdict)
	assert.Equal(t, "glm-5.2", v.Model)
	assert.EqualValues(t, 2, calls)
}

// failingJudge fails a chosen key so the batch's error lane is exercised.
type failingJudge struct{ failKey string }

func (f failingJudge) Judge(_ context.Context, p Packet) (Verdict, error) {
	if p.Key == f.failKey {
		return Verdict{}, fmt.Errorf("boom")
	}
	return Verdict{Key: p.Key, Bucket: p.Bucket, Verdict: VerdictMerge, Confidence: 0.9, Model: "fake"}, nil
}

func TestRunBatchResumesAndRecordsErrors(t *testing.T) {
	dir := t.TempDir()
	packets := writeFile(t, dir, "packets.jsonl", twoPackets)
	verdicts := filepath.Join(dir, "verdicts.jsonl")
	errs := filepath.Join(dir, "errors.jsonl")

	st, err := RunBatch(context.Background(), failingJudge{failKey: "cv:3:4"}, BatchOpts{
		PacketsPath: packets, VerdictsPath: verdicts, ErrorsPath: errs, Workers: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Packets)
	assert.Equal(t, 1, st.Judged)
	assert.Equal(t, 1, st.Errors)
	assert.Equal(t, 1, st.Merge)

	// Second pass: the judged key is skipped, the failed one is retried — that
	// is the whole resume contract.
	st2, err := RunBatch(context.Background(), failingJudge{}, BatchOpts{
		PacketsPath: packets, VerdictsPath: verdicts, ErrorsPath: errs, Workers: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, st2.Skipped)
	assert.Equal(t, 1, st2.Judged)
	assert.Equal(t, 0, st2.Errors)

	got, err := LoadVerdicts(verdicts)
	require.NoError(t, err)
	require.Len(t, got, 2)

	raw, err := os.ReadFile(errs)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(string(raw)), "\n")+1)
	assert.Contains(t, string(raw), "boom")

	// A third pass has nothing left to do.
	st3, err := RunBatch(context.Background(), failingJudge{}, BatchOpts{
		PacketsPath: packets, VerdictsPath: verdicts, Workers: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, st3.Skipped)
	assert.Equal(t, 0, st3.Judged)
}

func TestMockJudgeAndPromptsArePinned(t *testing.T) {
	for _, b := range []Bucket{BucketPersonEdge, BucketCharacterCV, BucketE4Split, BucketPersonConflict} {
		assert.True(t, ValidBucket(b))
		assert.NotEmpty(t, SystemPrompt(b))
		assert.Contains(t, SystemPrompt(b), "unsure")
	}
	assert.False(t, ValidBucket("nope"))

	v, err := MockJudge{}.Judge(context.Background(), Packet{Key: "k", Bucket: BucketPersonEdge})
	require.NoError(t, err)
	assert.Equal(t, VerdictUnsure, v.Verdict)
	assert.True(t, strings.HasPrefix(v.Model, "mock:"))

	_, err = MockJudge{Rule: func(Packet) (string, float64) { return "yes", 1 }}.
		Judge(context.Background(), Packet{Key: "k", Bucket: BucketPersonEdge})
	assert.ErrorContains(t, err, "invalid verdict")
}

// TestPaceLimiterSpacesRequests pins the pacer: N requests at R rpm take at
// least (N-1)/R minutes however many goroutines ask. Without it the batch burns
// its per-minute quota in seconds and then sits in backoff.
func TestPaceLimiterSpacesRequests(t *testing.T) {
	p := newPaceLimiter(6000) // 10ms apart
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, p.wait(context.Background()))
		}()
	}
	wg.Wait()
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)

	// A nil pacer (rpm <= 0) is the unpaced path and must not block.
	assert.Nil(t, newPaceLimiter(0))
	require.NoError(t, (*paceLimiter)(nil).wait(context.Background()))
}

// TestJudgeBatchAlignment is the safety contract of chunking: a reply that does
// not line up with the input is an error for the WHOLE chunk, never a verdict
// attached to the wrong pair.
func TestJudgeBatchAlignment(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, BatchSystemPrompt(BucketPersonEdge), req.Messages[0].Content)
		assert.Contains(t, req.Messages[1].Content, "### 案例 2")
		fmt.Fprintf(w, `{"model":"glm","choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`,
			mustJSONString(body))
	}))
	defer srv.Close()
	j := NewHTTPJudge(srv.URL, "t", "glm", 0, 0)
	ps := []Packet{
		{Key: "pe:1:2", Bucket: BucketPersonEdge, User: "a"},
		{Key: "pe:3:4", Bucket: BucketPersonEdge, User: "b"},
	}

	body = `[{"id":1,"verdict":"merge","confidence":0.9,"reason":"同一人"},{"id":2,"verdict":"distinct","confidence":0.8,"reason":"同名异人"}]`
	vs, err := j.JudgeBatch(context.Background(), ps)
	require.NoError(t, err)
	require.Len(t, vs, 2)
	assert.Equal(t, "pe:1:2", vs[0].Key)
	assert.Equal(t, VerdictMerge, vs[0].Verdict)
	assert.Equal(t, "pe:3:4", vs[1].Key)
	assert.Equal(t, VerdictDistinct, vs[1].Verdict)

	body = `[{"id":1,"verdict":"merge","confidence":0.9,"reason":"x"}]`
	_, err = j.JudgeBatch(context.Background(), ps)
	assert.ErrorContains(t, err, "1 verdicts for 2 cases")

	body = `[{"id":2,"verdict":"merge","confidence":0.9,"reason":"x"},{"id":1,"verdict":"merge","confidence":0.9,"reason":"y"}]`
	_, err = j.JudgeBatch(context.Background(), ps)
	assert.ErrorContains(t, err, "misaligned")

	body = `[{"id":1,"verdict":"maybe","confidence":0.9,"reason":"x"},{"id":2,"verdict":"merge","confidence":0.9,"reason":"y"}]`
	_, err = j.JudgeBatch(context.Background(), ps)
	assert.ErrorContains(t, err, "invalid verdict")
}

func mustJSONString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// TestChunkPacketsGroupsWithinOneBucket pins that a chunk never mixes buckets
// (they have different prompts) and honours the size.
func TestChunkPacketsGroupsWithinOneBucket(t *testing.T) {
	ps := []Packet{
		{Key: "a", Bucket: BucketPersonEdge}, {Key: "b", Bucket: BucketPersonEdge},
		{Key: "c", Bucket: BucketPersonEdge}, {Key: "d", Bucket: BucketE4Split},
	}
	got := chunkPackets(ps, 2)
	require.Len(t, got, 3)
	assert.Len(t, got[0], 2)
	assert.Len(t, got[1], 1)
	assert.Equal(t, BucketE4Split, got[2][0].Bucket)
	assert.Len(t, chunkPackets(ps, 0), 4)
}

// A whole failed chunk must land in the errors file per packet, so a later
// -chunk 1 run picks every one of them up.
func TestRunBatchChunkFailureMarksEveryPacket(t *testing.T) {
	dir := t.TempDir()
	packets := writeFile(t, dir, "p.jsonl",
		`{"bucket":"person_edge","key":"pe:1:2","user":"a"}`+"\n"+
			`{"bucket":"person_edge","key":"pe:3:4","user":"b"}`+"\n")
	verdicts := filepath.Join(dir, "v.jsonl")
	errs := filepath.Join(dir, "e.jsonl")

	st, err := RunBatch(context.Background(), failingJudge{failKey: "pe:1:2"}, BatchOpts{
		PacketsPath: packets, VerdictsPath: verdicts, ErrorsPath: errs, Workers: 1, Chunk: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Errors, "the whole chunk fails together")
	assert.Zero(t, st.Judged)

	st2, err := RunBatch(context.Background(), failingJudge{}, BatchOpts{
		PacketsPath: packets, VerdictsPath: verdicts, ErrorsPath: errs, Workers: 1, Chunk: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, st2.Judged)
}
