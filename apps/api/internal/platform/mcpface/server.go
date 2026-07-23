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

// serverName / serverVersion identify this server in the MCP initialize
// handshake. The version tracks the tool-surface generation (M1), not the /v1
// contract — the face's version lives upstream (design §1).
const (
	serverName    = "nextmoe-catalog"
	serverVersion = "0.1.0-m1"
)

// instructions is the server-level guidance shown to an MCP client on connect.
const instructions = "NextMoe catalog: read-only tools over the public galgame aggregate and " +
	"cross-media identity registry (VNDB / Bangumi / DLsite / ErogameScape). " +
	"Send `Authorization: Bearer nm_<api-key>` on the MCP endpoint; get a key at " + devPortalURL + ". " +
	"Use *_lookup / *_get when you already hold an id or external id, and *_search for natural language."

// tools binds the tool handlers to the single pass-through upstream client. It
// holds no other state — no DB, no cache (design §1).
type tools struct {
	up *Upstream
}

// NewServer builds the MCP server, registers the M1 tool surface, and returns it
// ready to be driven by any transport. The same server instance is safe to reuse
// across requests (the Streamable HTTP handler may hand it out repeatedly).
func NewServer(up *Upstream) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions})
	registerTools(s, up)
	return s
}

// run is the shared body of every tool handler: form-check the caller's key,
// forward the GET upstream verbatim, log ONE structured line (tool + upstream
// status + latency + key fingerprint only), and map the response to a tool
// result. Every handler is a one-liner over its own (path, query).
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

// ─────────────────────────── query helpers ───────────────────────────
//
// Each only writes a param when the caller supplied it, so the forwarded URL
// carries exactly the caller's intent and the upstream applies its own defaults.

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

// pathID renders "/v1/<seg>/<id>" for the by-id detail tools.
func pathID(prefix string, id int) string {
	return prefix + "/" + strconv.Itoa(id)
}
