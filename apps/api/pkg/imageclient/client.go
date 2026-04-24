// Package imageclient is the Go SDK for the image service.
//
// Intended to be imported by calling services (kungal / moyu / galgame
// wiki) as a singleton. The SDK holds no mutable state beyond HTTP
// connection pool settings, so the singleton pattern is purely to avoid
// repeating connection/tuning config.
//
// Typical usage in a calling service:
//
//	var sharedImageClient = imageclient.New(imageclient.Config{
//	    BaseURL:      cfg.ImageServiceBaseURL,
//	    CDNBase:      cfg.ImageCDNBase,
//	    ClientID:     cfg.ImageClientID,
//	    ClientSecret: cfg.ImageClientSecret,
//	})
//
//	hash, url, err := sharedImageClient.Upload(ctx, file, "avatar")
//
// For URL construction (no network needed), use:
//
//	url := imageclient.MainURL(cdnBase, hash, "webp")
//	url := imageclient.VariantURL(cdnBase, hash, "100", "webp")
package imageclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Config is the calling service's configuration for this SDK instance.
type Config struct {
	BaseURL      string // e.g. https://image.api.example.com  (no trailing slash)
	CDNBase      string // e.g. https://cdn.example.com/img   (no trailing slash)
	ClientID     string // OAuth client id
	ClientSecret string // OAuth client secret
	HTTPClient   *http.Client
	Timeout      time.Duration // default 30s
}

// Client is the image service client. Safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client from Config.
func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	cfg.CDNBase = strings.TrimRight(cfg.CDNBase, "/")

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{cfg: cfg, http: httpClient}
}

// ---- URL helpers (pure, no network) ----

// MainURL returns the CDN URL for the main image of a hash. Argument order
// matches VariantURL for consistency.
func MainURL(cdnBase, hash, ext string) string {
	if len(hash) < 4 {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s/%s.%s",
		strings.TrimRight(cdnBase, "/"), hash[:2], hash[2:4], hash, ext)
}

// VariantURL returns the CDN URL for a specific variant of a hash.
func VariantURL(cdnBase, hash, variant, ext string) string {
	if len(hash) < 4 {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s/%s_%s.%s",
		strings.TrimRight(cdnBase, "/"), hash[:2], hash[2:4], hash, variant, ext)
}

// MainURL builds a URL using the client's configured CDN base. Uses "webp"
// as the default extension (V1 outputs are always webp).
func (c *Client) MainURL(hash string) string { return MainURL(c.cfg.CDNBase, hash, "webp") }

// VariantURL builds a URL using the client's configured CDN base.
func (c *Client) VariantURL(hash, variant string) string {
	return VariantURL(c.cfg.CDNBase, hash, variant, "webp")
}

// ---- HTTP calls ----

// UploadResult mirrors the image service /image/upload response payload.
type UploadResult struct {
	Hash         string            `json:"hash"`
	URL          string            `json:"url"`
	VariantURLs  map[string]string `json:"variant_urls"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	SizeBytes    int64             `json:"size_bytes"`
	Deduplicated bool              `json:"deduplicated"`
}

// Error is returned when the image service responds with a non-2xx status.
type Error struct {
	StatusCode int
	Code       int             `json:"code"`
	Message    string          `json:"message"`
	Details    json.RawMessage `json:"details,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("image service error: status=%d code=%d: %s", e.StatusCode, e.Code, e.Message)
}

// Sentinel errors for common conditions callers may want to branch on.
var (
	ErrQuotaExceeded      = errors.New("imageclient: quota exceeded")
	ErrModerationRejected = errors.New("imageclient: rejected by moderation")
	ErrUnauthorized       = errors.New("imageclient: unauthorized")
)

// classifyError maps an image service error code to a sentinel if applicable.
// Returns the original *Error if no sentinel matches.
func classifyError(e *Error) error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case 80008: // ErrImageQuotaExceeded
		return fmt.Errorf("%w: %s", ErrQuotaExceeded, e.Message)
	case 80001, 80002, 80003, 80004, 80005:
		return fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
	case 60002: // ErrModerationRejected
		return fmt.Errorf("%w: %s", ErrModerationRejected, e.Message)
	default:
		return e
	}
}

// Upload uploads a file and waits for processing. filename is used only in
// the multipart content-disposition (does not influence storage key).
func (c *Client) Upload(ctx context.Context, r io.Reader, filename, presetName string) (*UploadResult, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("build multipart: %w", err)
	}
	if _, err := io.Copy(part, r); err != nil {
		return nil, fmt.Errorf("copy file: %w", err)
	}
	if err := mw.WriteField("preset", presetName); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/image/upload", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.basicAuthHeader())
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload http: %w", err)
	}
	defer resp.Body.Close()

	return parseUploadResponse(resp)
}

// parseUploadResponse pulls apart the standard JSON envelope.
func parseUploadResponse(resp *http.Response) (*UploadResult, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var e Error
		_ = json.Unmarshal(raw, &e)
		e.StatusCode = resp.StatusCode
		return nil, classifyError(&e)
	}
	var env struct {
		Code    int          `json:"code"`
		Message string       `json:"message"`
		Data    UploadResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse upload response: %w", err)
	}
	return &env.Data, nil
}

// ReferencePingResult mirrors the image service response.
type ReferencePingResult struct {
	Updated  int64    `json:"updated"`
	NotFound []string `json:"not_found"`
}

// ReferencePing refreshes last_referenced_at for a batch of hashes.
func (c *Client) ReferencePing(ctx context.Context, hashes []string) (*ReferencePingResult, error) {
	if len(hashes) == 0 {
		return &ReferencePingResult{}, nil
	}
	if len(hashes) > 1000 {
		return nil, fmt.Errorf("imageclient: batch size %d exceeds limit 1000", len(hashes))
	}

	body, _ := json.Marshal(struct {
		Hashes []string `json:"hashes"`
	}{Hashes: hashes})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/image/reference-ping", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.basicAuthHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ping http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var e Error
		_ = json.Unmarshal(raw, &e)
		e.StatusCode = resp.StatusCode
		return nil, classifyError(&e)
	}
	var env struct {
		Data ReferencePingResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse ping response: %w", err)
	}
	return &env.Data, nil
}

// Health pings /healthz to confirm the service is reachable.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("health: status %d", resp.StatusCode)
	}
	return nil
}

// basicAuthHeader produces a Basic auth header value from client id/secret.
func (c *Client) basicAuthHeader() string {
	creds := c.cfg.ClientID + ":" + c.cfg.ClientSecret
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
}
