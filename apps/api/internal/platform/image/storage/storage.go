// Package storage is a thin S3-compatible object store client scoped to the
// image service. It wraps aws-sdk-go-v2 so the rest of the image code doesn't
// need to know about S3 types.
//
// This is intentionally separate from internal/infrastructure/storage (which
// is a stubbed generic interface) — the image service has specific needs
// (byte PutObject, HEAD, CopyObject) that don't match the presigned-URL shape
// the stub was designed for.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"api/pkg/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Client is an image-service-scoped S3 client.
type Client struct {
	s3     *s3.Client
	bucket string
}

// NewClient creates an S3 client from the given config. Supports MinIO /
// R2 / OSS / COS / AWS S3 via the UsePathStyle knob.
func NewClient(cfg config.S3Config) (*Client, error) {
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("storage: AccessKeyID and SecretAccessKey are required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("storage: Bucket is required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &Client{s3: s3Client, bucket: cfg.Bucket}, nil
}

// Bucket returns the bucket name the client is bound to.
func (c *Client) Bucket() string { return c.bucket }

// Put writes the given bytes to the given key with the given content type.
// The body is held in memory before upload (acceptable since image payloads
// are bounded by max_file_size).
func (c *Client) Put(ctx context.Context, key string, body []byte, contentType string) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	return nil
}

// Get fetches an object's contents. Caller must close the returned reader.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}
	return out.Body, nil
}

// Exists reports whether an object exists at the given key.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	// Distinguish "not found" from real errors.
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
		return false, nil
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	return false, fmt.Errorf("storage: head %q: %w", key, err)
}

// Delete removes an object.
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

// EnsureBucket creates the bucket if it does not yet exist. Intended for
// local dev (MinIO) so the service can bootstrap from an empty state.
// Production should pre-provision buckets.
func (c *Client) EnsureBucket(ctx context.Context) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err == nil {
		return nil
	}
	_, err = c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		// A concurrent create/"already exists" is not an error.
		var alreadyOwned *types.BucketAlreadyOwnedByYou
		var alreadyExists *types.BucketAlreadyExists
		if errors.As(err, &alreadyOwned) || errors.As(err, &alreadyExists) {
			return nil
		}
		return fmt.Errorf("storage: create bucket %q: %w", c.bucket, err)
	}
	return nil
}
