package search

import (
	"fmt"

	"api/pkg/config"

	"github.com/meilisearch/meilisearch-go"
)

type Client struct {
	svc    meilisearch.ServiceManager
	prefix string
}

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

func (c *Client) Index(uid string) meilisearch.IndexManager {
	return c.svc.Index(c.prefix + uid)
}

func (c *Client) IndexUID(uid string) string {
	return c.prefix + uid
}

func (c *Client) Svc() meilisearch.ServiceManager {
	return c.svc
}

func (c *Client) Health() error {
	_, err := c.svc.Health()
	return err
}
