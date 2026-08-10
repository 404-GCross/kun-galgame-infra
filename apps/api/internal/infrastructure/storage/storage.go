package storage

import (
	"context"
	"io"
	"time"
)

type Storage interface {
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error

	Download(ctx context.Context, key string) (io.ReadCloser, error)

	Delete(ctx context.Context, key string) error

	GetPresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)

	GetDownloadURL(ctx context.Context, key string, expiry time.Duration) (string, error)

	Exists(ctx context.Context, key string) (bool, error)

	Move(ctx context.Context, srcKey, dstKey string) error
}

type Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	Region          string
	UseSSL          bool
}

type Client struct {
	config Config
}

func NewClient(cfg Config) (*Client, error) {
	return &Client{config: cfg}, nil
}

func (c *Client) Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	return nil
}

func (c *Client) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	return nil
}

func (c *Client) GetPresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", nil
}

func (c *Client) GetDownloadURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", nil
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	return false, nil
}

func (c *Client) Move(ctx context.Context, srcKey, dstKey string) error {
	return nil
}
