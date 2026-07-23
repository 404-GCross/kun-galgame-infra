// Command mcp is the NextMoe MCP (Model Context Protocol) server — a thin,
// stateless protocol adapter that re-exposes the already-public /v1 read faces
// as MCP tools for AI agents. It holds NO state of its own: every tool call is a
// pass-through GET to the public /v1 face (KUN_MCP_UPSTREAM_BASE), forwarding the
// caller's API key verbatim, so all authz / rate-limit / quota / usage metering
// happens upstream on the same key. No database, no cache (design:
// docs/developer-platform/09-mcp-server.md).
//
// Routes:
//
//	POST/GET /mcp       — the Streamable HTTP MCP endpoint (stateless)
//	GET      /healthz   — liveness, no auth
//
// The `healthcheck` subcommand (container HEALTHCHECK) probes /healthz and exits.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"api/internal/platform/mcpface"
	"api/pkg/health"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultPort = 9285
	healthPath  = "/healthz"
	mcpPath     = "/mcp"
)

func main() {
	port := envInt("KUN_MCP_PORT", defaultPort)

	// Container HEALTHCHECK path: probe the already-running server and exit. Must
	// run before anything else (it needs no config beyond the port).
	health.MaybeProbe(port, healthPath)

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	upstreamBase := os.Getenv("KUN_MCP_UPSTREAM_BASE")
	if upstreamBase == "" {
		slog.Error("KUN_MCP_UPSTREAM_BASE is required (the public /v1 face base, e.g. http://catalog:9281)")
		os.Exit(1)
	}
	host := os.Getenv("KUN_MCP_HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	up := mcpface.NewUpstream(upstreamBase)
	server := mcpface.NewServer(up)

	// Stateless Streamable HTTP: no session stickiness, so the process scales
	// horizontally like every other Fiber service (design §2). JSONResponse keeps
	// single-shot tool calls as plain application/json (spec §2.1.5).
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	mux := http.NewServeMux()
	mux.Handle(mcpPath, handler)
	mux.Handle(mcpPath+"/", handler)
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	addr := host + ":" + strconv.Itoa(port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("mcp server listening", "addr", addr, "upstream", upstreamBase, "endpoint", mcpPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("mcp server", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("mcp server shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("mcp server shutdown", "error", err)
	}
}

// envInt reads an integer env var, falling back to def when unset or unparseable.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
