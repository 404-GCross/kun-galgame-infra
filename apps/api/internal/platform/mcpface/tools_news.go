package mcpface

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// news:read is not in devapi's self-service scope set (pinned by
// TestScopeNewsReadSelfServiceExcluded): the partners licensed an index, so a
// key carrying it is granted by hand. Every description says so — a tool that
// 403s for almost every caller has to explain why in the only text the model
// ever reads.
const newsGrantNote = " Requires an API key carrying the news:read scope, which is GRANTED BY THE PLATFORM and " +
	"cannot be self-issued from the developer portal; keys without it get 403 here."

func registerNewsTools(s *mcp.Server, t *tools) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "news_list",
		Title:       "Galgame news feed",
		Description: descNewsList,
		Annotations: readOnly,
	}, t.newsList)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "news_sources",
		Title:       "News source registry",
		Description: descNewsSources,
		Annotations: readOnly,
	}, t.newsSources)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "news_get",
		Title:       "Get one news item by id",
		Description: descNewsGet,
		Annotations: readOnly,
	}, t.newsGet)
}

const descNewsList = "Galgame news and editorial columns republished from partner sites, newest UPSTREAM publication " +
	"first, keyset-paginated. What you get is an INDEX, not a mirror: title, preview, banner and the partner's own " +
	"source_url — the article body is never served and is not stored, so answer from the preview and send the reader " +
	"to source_url, which every item carries along with its source block. Two lanes share the feed: news (short " +
	"bulletins) and column (longer pieces); every item states its own. Withdrawn items and items whose upstream " +
	"original disappeared are simply absent. Filter by source, lane, work_id (items anchored to a catalog work) or a " +
	"publication window." + newsGrantNote

type newsListInput struct {
	Source          string `json:"source,omitempty" jsonschema:"Comma-separated source keys to keep (ymgal, galgame_hihyou); omit or all for every source. Discover the keys with news_sources."`
	Lane            string `json:"lane,omitempty" jsonschema:"Comma-separated lanes to keep: news, column. Omit or all for both. An unknown lane is rejected, never silently empty."`
	WorkID          int    `json:"work_id,omitempty" jsonschema:"Keep only items anchored to this catalog work id."`
	PublishedAfter  string `json:"published_after,omitempty" jsonschema:"RFC3339 lower bound on the UPSTREAM publication time."`
	PublishedBefore string `json:"published_before,omitempty" jsonschema:"RFC3339 upper bound on the UPSTREAM publication time."`
	Cursor          string `json:"cursor,omitempty" jsonschema:"Opaque keyset cursor from a prior next_cursor; omit for the first page."`
	Limit           int    `json:"limit,omitempty" jsonschema:"Items per page 1-50 (default 20)."`
}

func (t *tools) newsList(ctx context.Context, req *mcp.CallToolRequest, in newsListInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "source", in.Source)
	setStr(q, "lane", in.Lane)
	setInt(q, "work_id", in.WorkID)
	setStr(q, "published_after", in.PublishedAfter)
	setStr(q, "published_before", in.PublishedBefore)
	setStr(q, "cursor", in.Cursor)
	setInt(q, "limit", in.Limit)
	return t.run(ctx, req, "news_list", "/v1/news", q)
}

const descNewsSources = "The news source registry: for each partner its key, display name, homepage, column entry " +
	"point, publisher uid and the ATTRIBUTION TEXT to render. Takes no arguments. Use it to learn the source keys " +
	"news_list filters on, or to render one standing attribution block — it does NOT replace an item's own source " +
	"block, which an item quoted on its own must still carry." + newsGrantNote

type newsSourcesInput struct{}

func (t *tools) newsSources(ctx context.Context, req *mcp.CallToolRequest, _ newsSourcesInput) (*mcp.CallToolResult, any, error) {
	return t.run(ctx, req, "news_sources", "/v1/news/sources", newQuery())
}

const descNewsGet = "Fetch one news item by its numeric id — same shape as a news_list row, body still not included. " +
	"A 404 here is a CONTRACT, not a lookup failure: an item we withdrew, or whose upstream original is gone, stops " +
	"being addressable, so do not retry it and do not serve a cached copy." + newsGrantNote

type newsGetInput struct {
	ID int `json:"id" jsonschema:"The news item id (required). Find one with news_list."`
}

func (t *tools) newsGet(ctx context.Context, req *mcp.CallToolRequest, in newsGetInput) (*mcp.CallToolResult, any, error) {
	return t.run(ctx, req, "news_get", pathID("/v1/news", in.ID), newQuery())
}
