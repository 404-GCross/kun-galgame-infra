package catalogclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	Timeout      time.Duration
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 15 * time.Second
		}
		hc = &http.Client{Timeout: timeout}
	}
	return &Client{cfg: cfg, http: hc}
}

func (c *Client) GetJSON(ctx context.Context, path, rawQuery string) (status int, body []byte, err error) {
	u := c.cfg.BaseURL + "/api/v1/catalog/" + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.ClientID+":"+c.cfg.ClientSecret)))
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read catalog response: %w", err)
	}
	return resp.StatusCode, b, nil
}
