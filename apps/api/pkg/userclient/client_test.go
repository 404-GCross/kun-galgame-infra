package userclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer returns a small httptest server that responds to /users/batch
// and tracks how many requests it has seen.
type fakeServer struct {
	*httptest.Server
	hits  atomic.Int32
	users map[uint]UserBrief
}

func newFakeServer(users map[uint]UserBrief) *fakeServer {
	fs := &fakeServer{users: users}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.hits.Add(1)

		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}

		idsParam := r.URL.Query().Get("ids")
		if idsParam == "" {
			http.Error(w, "ids required", http.StatusBadRequest)
			return
		}

		var resp struct {
			Code    int           `json:"code"`
			Message string        `json:"message"`
			Data    batchResponse `json:"data"`
		}
		resp.Message = "成功"

		seen := map[uint]bool{}
		for p := range strings.SplitSeq(idsParam, ",") {
			var id uint
			fmt.Sscanf(strings.TrimSpace(p), "%d", &id)
			if id == 0 || seen[id] {
				continue
			}
			seen[id] = true
			if u, ok := fs.users[id]; ok {
				resp.Data.Users = append(resp.Data.Users, u)
			} else {
				resp.Data.NotFound = append(resp.Data.NotFound, id)
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return fs
}

// helper to build a client pointed at a fake server
func newTestClient(t *testing.T, srv *fakeServer, ttl time.Duration) *Client {
	t.Helper()
	return New(Config{
		BaseURL:      srv.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		CacheTTL:     ttl,
	})
}

// ---- tests ----

func TestUsers_BasicFetch(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{
		1: {ID: 1, Name: "alice", Avatar: "a.png"},
		2: {ID: 2, Name: "bob", Avatar: "b.png"},
	})
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	got, err := cli.Users(context.Background(), []uint{1, 2})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "alice", got[1].Name)
	assert.Equal(t, "bob", got[2].Name)
	assert.Equal(t, int32(1), srv.hits.Load())
}

func TestUsers_CacheHit(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{1: {ID: 1, Name: "alice"}})
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	_, err := cli.Users(context.Background(), []uint{1})
	require.NoError(t, err)
	_, err = cli.Users(context.Background(), []uint{1})
	require.NoError(t, err)
	_, err = cli.Users(context.Background(), []uint{1})
	require.NoError(t, err)

	assert.Equal(t, int32(1), srv.hits.Load(), "cache should serve subsequent calls")
}

func TestUsers_CacheExpiry(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{1: {ID: 1, Name: "alice"}})
	defer srv.Close()
	cli := newTestClient(t, srv, 50*time.Millisecond)

	_, _ = cli.Users(context.Background(), []uint{1})
	time.Sleep(80 * time.Millisecond)
	_, _ = cli.Users(context.Background(), []uint{1})

	assert.Equal(t, int32(2), srv.hits.Load(), "expired entry should refetch")
}

func TestUsers_PartialCacheHit(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{
		1: {ID: 1, Name: "alice"},
		2: {ID: 2, Name: "bob"},
		3: {ID: 3, Name: "carol"},
	})
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	// Prime cache with [1, 2]
	_, _ = cli.Users(context.Background(), []uint{1, 2})
	require.Equal(t, int32(1), srv.hits.Load())

	// Request [1, 2, 3] — only 3 should hit the server
	got, err := cli.Users(context.Background(), []uint{1, 2, 3})
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.Equal(t, int32(2), srv.hits.Load(), "only the missing id should trigger a fetch")
}

func TestUsers_NotFoundNegativeCache(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{1: {ID: 1, Name: "alice"}})
	defer srv.Close()
	cli := New(Config{
		BaseURL:          srv.URL,
		ClientID:         "x", ClientSecret: "y",
		CacheTTL:         10 * time.Minute,
		NotFoundCacheTTL: 1 * time.Hour,
	})

	got, err := cli.Users(context.Background(), []uint{1, 99})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Contains(t, got, uint(1))
	assert.NotContains(t, got, uint(99))
	assert.Equal(t, int32(1), srv.hits.Load())

	// Re-request 99 — should not refetch (negative cache hit)
	got, err = cli.Users(context.Background(), []uint{99})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, int32(1), srv.hits.Load(), "negative cache should suppress refetch")
}

func TestUsers_Singleflight(t *testing.T) {
	// Slow server — sleep 100ms per request — so concurrent calls have a
	// chance to coalesce
	users := map[uint]UserBrief{1: {ID: 1, Name: "alice"}}
	srv := &fakeServer{users: users}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.hits.Add(1)
		time.Sleep(100 * time.Millisecond)
		var resp struct {
			Code    int           `json:"code"`
			Message string        `json:"message"`
			Data    batchResponse `json:"data"`
		}
		resp.Data.Users = []UserBrief{users[1]}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	const N = 20
	var wg sync.WaitGroup
	for range N {
		wg.Go(func() {
			_, err := cli.Users(context.Background(), []uint{1})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	// All concurrent identical-key requests should coalesce
	assert.Equal(t, int32(1), srv.hits.Load(),
		"singleflight should coalesce concurrent calls into 1 server hit")
}

func TestUser_ConvenienceWrapper(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{1: {ID: 1, Name: "alice"}})
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	u, err := cli.User(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "alice", u.Name)

	missing, err := cli.User(context.Background(), 99)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestUsers_Invalidate(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{1: {ID: 1, Name: "alice"}})
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	_, _ = cli.Users(context.Background(), []uint{1})
	cli.Invalidate(1)
	_, _ = cli.Users(context.Background(), []uint{1})

	assert.Equal(t, int32(2), srv.hits.Load(), "invalidated entries should refetch")
}

func TestUsers_ChunkLargeRequest(t *testing.T) {
	users := make(map[uint]UserBrief, 250)
	for i := uint(1); i <= 250; i++ {
		users[i] = UserBrief{ID: i, Name: fmt.Sprintf("u%d", i)}
	}
	srv := newFakeServer(users)
	defer srv.Close()
	cli := New(Config{
		BaseURL:      srv.URL,
		ClientID:     "x", ClientSecret: "y",
		CacheTTL:     10 * time.Minute,
		MaxBatch:     100, // server max
	})

	ids := make([]uint, 250)
	for i := uint(1); i <= 250; i++ {
		ids[i-1] = i
	}
	got, err := cli.Users(context.Background(), ids)
	require.NoError(t, err)
	assert.Len(t, got, 250)
	assert.Equal(t, int32(3), srv.hits.Load(), "250 ids / 100 max = 3 requests")
}

func TestSearch_BasicQuery(t *testing.T) {
	// Pre-ranked server: returns matches in a fixed order
	matches := []UserBrief{
		{ID: 7, Name: "alice"},
		{ID: 8, Name: "alicia"},
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		assert.Equal(t, "/users/search", r.URL.Path)
		assert.Equal(t, "ali", r.URL.Query().Get("q"))
		assert.Equal(t, "5", r.URL.Query().Get("limit"))
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Basic "))

		var resp struct {
			Code int `json:"code"`
			Data struct {
				Users []UserBrief `json:"users"`
			} `json:"data"`
		}
		resp.Data.Users = matches
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cli := New(Config{BaseURL: srv.URL, ClientID: "x", ClientSecret: "y"})
	got, err := cli.Search(context.Background(), "ali", 5)
	require.NoError(t, err)
	assert.Equal(t, matches, got)
	assert.Equal(t, int32(1), hits.Load())
}

func TestSearch_NotCached(t *testing.T) {
	// Each Search call should hit the server — search results aren't cached
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"code":0,"data":{"users":[]}}`))
	}))
	defer srv.Close()

	cli := New(Config{BaseURL: srv.URL, ClientID: "x", ClientSecret: "y"})
	for range 3 {
		_, err := cli.Search(context.Background(), "anything", 10)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(3), hits.Load(), "Search must not cache")
}

func TestSearch_EmptyQueryRejected(t *testing.T) {
	cli := New(Config{BaseURL: "http://nowhere.invalid", ClientID: "x", ClientSecret: "y"})
	_, err := cli.Search(context.Background(), "   ", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search query required")
}

func TestSearch_QueryEscaped(t *testing.T) {
	// Verify special chars in `q` survive URL encoding
	var seenQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQ = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`{"code":0,"data":{"users":[]}}`))
	}))
	defer srv.Close()

	cli := New(Config{BaseURL: srv.URL, ClientID: "x", ClientSecret: "y"})
	_, err := cli.Search(context.Background(), "foo bar&baz=1", 5)
	require.NoError(t, err)
	assert.Equal(t, "foo bar&baz=1", seenQ)
}

func TestUsers_Dedupe(t *testing.T) {
	srv := newFakeServer(map[uint]UserBrief{1: {ID: 1, Name: "alice"}})
	defer srv.Close()
	cli := newTestClient(t, srv, 10*time.Minute)

	got, err := cli.Users(context.Background(), []uint{1, 1, 1, 1})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, int32(1), srv.hits.Load())
}
