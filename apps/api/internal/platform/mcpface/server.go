package mcpface

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "nextmoe-catalog"
	serverVersion = "0.1.0-m1"
)

const instructions = "NextMoe catalog: read-only tools over the public galgame aggregate and " +
	"cross-media identity registry (VNDB / Bangumi / DLsite / ErogameScape). " +
	"Send `Authorization: Bearer nm_<api-key>` on the MCP endpoint; get a key at " + devPortalURL + ". " +
	"Use *_lookup / *_get when you already hold an id or external id, and *_search for natural language. " +
	"catalog_works_list browses/filters the works registry in bulk; catalog_changes is the incremental-sync feed. " +
	"R18 content is hidden by default: opt in with nsfw=true on catalog tools, or content_limit=nsfw|all " +
	"on galgame tools (needs a key with the galgame:nsfw scope)."

type tools struct {
	up *Upstream
}

func NewServer(up *Upstream) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions})
	registerTools(s, up)
	return s
}

func (t *tools) run(ctx context.Context, req *mcp.CallToolRequest, tool, path string, query url.Values) (*mcp.CallToolResult, any, error) {
	var header http.Header
	if req != nil && req.Extra != nil {
		header = req.Extra.Header
	}
	token, ok := bearerToken(header)
	if !ok {
		slog.Warn("mcp tool call rejected: bad key form", "tool", tool)
		return authError(), nil, nil
	}

	fp := keyFingerprint(token)
	start := time.Now()
	status, body, err := t.up.Get(ctx, path, query, "Bearer "+token)
	latency := time.Since(start)
	if err != nil {
		slog.Error("mcp tool call: upstream unreachable",
			"tool", tool, "path", path, "key_fp", fp, "latency_ms", latency.Milliseconds(), "err", err.Error())
		return errorResult("Upstream request failed: " + err.Error() + ". Try again later."), nil, nil
	}
	slog.Info("mcp tool call",
		"tool", tool, "path", path, "key_fp", fp, "upstream_status", status, "latency_ms", latency.Milliseconds())
	return mapUpstream(status, body), nil, nil
}

func newQuery() url.Values { return url.Values{} }

func setStr(q url.Values, key, val string) {
	if val != "" {
		q.Set(key, val)
	}
}

func setInt(q url.Values, key string, val int) {
	if val != 0 {
		q.Set(key, strconv.Itoa(val))
	}
}

func setBool(q url.Values, key string, val bool) {
	if val {
		q.Set(key, "1")
	}
}

func pathID(prefix string, id int) string {
	return prefix + "/" + strconv.Itoa(id)
}
