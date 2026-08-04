package vndbcovers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAPI points the client at a stub server with the throttle disabled —
// the ~1 req/s spacing is a politeness rule for the real API, not behaviour
// worth spending seconds of test time on.
func newTestAPI(t *testing.T, h http.HandlerFunc) *vndbAPI {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	api := newVNDBAPI(srv.URL)
	api.gap = 0
	return api
}

func TestFetchImagesPostsAnIDFilterAndKeysTheAnswer(t *testing.T) {
	var gotBody map[string]any
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/vn", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("User-Agent"))
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &gotBody))
		_, _ = io.WriteString(w, canned)
	})

	got, err := api.fetchImages(context.Background(), []string{"v17", "v99", "v250"})
	require.NoError(t, err)

	assert.Equal(t, vnFields, gotBody["fields"])
	assert.Equal(t, float64(3), gotBody["results"])
	filters, _ := json.Marshal(gotBody["filters"])
	assert.JSONEq(t, `["or",["id","=","v17"],["id","=","v99"],["id","=","v250"]]`, string(filters))

	require.Len(t, got, 3)
	require.NotNil(t, got["v17"])
	assert.Equal(t, []int{256, 400}, got["v17"].Dims)
	assert.Nil(t, got["v250"], "a vn with no cover is present but nil")
	_, known := got["v4444"]
	assert.False(t, known, "an id VNDB never answered for stays absent")
}

func TestFetchImagesRetriesA429(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			// The server's own Retry-After is honoured; 1s keeps the test
			// honest about the real wait without burning the default 30s.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"id":"v1","image":null}],"more":false}`)
	})

	got, err := api.fetchImages(context.Background(), []string{"v1"})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, calls, "the 429 is retried, not surfaced")
	require.Len(t, got, 1)
}

func TestFetchImagesFailsHardOnA4xx(t *testing.T) {
	calls := 0
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	})
	_, err := api.fetchImages(context.Background(), []string{"v1"})
	require.Error(t, err)
	assert.Equal(t, 1, calls, "a malformed query is terminal — never retried")
}

func TestFetchImagesBatches(t *testing.T) {
	calls := 0
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"results":[],"more":false}`)
	})
	ids := make([]string, apiBatchSize*2+1)
	for i := range ids {
		ids[i] = "v1"
	}
	_, err := api.fetchImages(context.Background(), ids)
	require.NoError(t, err)
	assert.Equal(t, 3, calls, "the id list is split into batches of apiBatchSize")
}
