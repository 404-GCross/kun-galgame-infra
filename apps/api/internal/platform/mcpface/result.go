package mcpface

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// textResult wraps a raw upstream JSON body as a successful tool result. The
// body is passed through byte-for-byte — the MCP layer never reshapes the
// public /v1 response (design §1 / §4).
func textResult(body []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}
}

// errorResult wraps a human-readable message as a tool error (isError=true),
// which the LLM sees as a failed tool call rather than a protocol crash.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// authError is the tool result returned when the Authorization form check fails
// (missing / non-Bearer / non-nm_ key). It points the caller at the portal.
func authError() *mcp.CallToolResult {
	return errorResult(
		"Missing or malformed API key. Configure your MCP client to send the header " +
			"`Authorization: Bearer nm_<api-key>` on the MCP endpoint. " +
			"Mint or inspect a key at " + devPortalURL + ".",
	)
}

// mapUpstream turns an upstream (status, body) into a tool result. A 2xx passes
// the body through verbatim; a 4xx/5xx becomes a tool error carrying a plain-
// language hint plus the upstream error body so the model can act on it. The
// authentication-class statuses (401/403/429) get an explicit human pointer
// (design §3).
func mapUpstream(status int, body []byte) *mcp.CallToolResult {
	if status >= 200 && status < 300 {
		return textResult(body)
	}

	var hint string
	switch status {
	case 401:
		hint = "Authentication failed: the API key was rejected (invalid, expired, or revoked). " +
			"Check the key at " + devPortalURL + "."
	case 403:
		hint = "Forbidden: the API key lacks the scope or tier required for this resource. " +
			"Review the key's scopes at " + devPortalURL + "."
	case 429:
		hint = "Rate limit or daily quota exceeded for this API key. " +
			"Slow down and retry later; check usage at " + devPortalURL + "."
	case 404:
		hint = "Not found: no such record on the NextMoe catalog (it may be unpublished or NSFW-gated for this key)."
	default:
		if status >= 500 {
			hint = fmt.Sprintf("Upstream service error (status %d). Try again later.", status)
		} else {
			hint = fmt.Sprintf("The request was rejected (status %d).", status)
		}
	}
	return errorResult(hint + "\n\nupstream response:\n" + string(body))
}
