// Package search wraps the Meilisearch SDK with config-driven initialization.
package search

import (
	"fmt"

	"api/pkg/config"

	"github.com/meilisearch/meilisearch-go"
)

// Client wraps meilisearch.ServiceManager with index-prefix awareness.
type Client struct {
	svc    meilisearch.ServiceManager
	prefix string
}

// NewClient creates a Meilisearch client from config. Does not make network calls.
func NewClient(cfg config.MeilisearchConfig) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("meilisearch host is required")
	}

	svc := meilisearch.New(cfg.Host, meilisearch.WithAPIKey(cfg.APIKey))

	return &Client{
		svc:    svc,
		prefix: cfg.IndexPrefix,
	}, nil
}

// Index returns a handle to the named index with the configured prefix applied.
// E.g. if prefix="dev_" and uid="galgames", hits "dev_galgames".
func (c *Client) Index(uid string) meilisearch.IndexManager {
	return c.svc.Index(c.prefix + uid)
}

// IndexUID returns the fully-qualified (prefixed) index uid.
func (c *Client) IndexUID(uid string) string {
	return c.prefix + uid
}

// Svc returns the underlying ServiceManager for low-level operations
// (creating indexes, listing stats, etc.)
func (c *Client) Svc() meilisearch.ServiceManager {
	return c.svc
}

// Health pings the Meilisearch instance. Returns nil if reachable.
func (c *Client) Health() error {
	_, err := c.svc.Health()
	return err
}
