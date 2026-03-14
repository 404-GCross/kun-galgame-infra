package storage

import (
	"context"
	"io"
	"time"
)

// Storage interface for object storage operations
type Storage interface {
	// Upload uploads a file to storage
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error

	// Download downloads a file from storage
	Download(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete deletes a file from storage
	Delete(ctx context.Context, key string) error

	// GetPresignedURL generates a presigned URL for upload
	GetPresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)

	// GetDownloadURL generates a presigned URL for download
	GetDownloadURL(ctx context.Context, key string, expiry time.Duration) (string, error)

	// Exists checks if a file exists
	Exists(ctx context.Context, key string) (bool, error)

	// Move moves a file from one key to another
	Move(ctx context.Context, srcKey, dstKey string) error
}

// Config holds storage configuration
type Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	Region          string
	UseSSL          bool
}

// Client is the storage client
// TODO: Implement with MinIO or S3-compatible storage
type Client struct {
	config Config
}

// NewClient creates a new storage client
func NewClient(cfg Config) (*Client, error) {
	// TODO: Initialize MinIO client
	return &Client{config: cfg}, nil
}

// Upload uploads a file to storage
func (c *Client) Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	// TODO: Implement
	return nil
}

// Download downloads a file from storage
func (c *Client) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	// TODO: Implement
	return nil, nil
}

// Delete deletes a file from storage
func (c *Client) Delete(ctx context.Context, key string) error {
	// TODO: Implement
	return nil
}

// GetPresignedURL generates a presigned URL for upload
func (c *Client) GetPresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	// TODO: Implement
	return "", nil
}

// GetDownloadURL generates a presigned URL for download
func (c *Client) GetDownloadURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	// TODO: Implement
	return "", nil
}

// Exists checks if a file exists
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	// TODO: Implement
	return false, nil
}

// Move moves a file from one key to another
func (c *Client) Move(ctx context.Context, srcKey, dstKey string) error {
	// TODO: Implement
	return nil
}
