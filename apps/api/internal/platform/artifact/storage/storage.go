// Package storage is the artifact-service object-store client: an S3-compatible
// wrapper (Backblaze B2 in prod, MinIO in dev) specialised for LARGE private
// blobs. Unlike internal/platform/image/storage (which buffers bytes through
// PutObject), this client never touches file bytes — it only issues presigned
// PUT / GET URLs and drives S3 multipart, so GB-scale uploads/downloads go
// client⇄B2 directly. See docs/artifact/.
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"api/pkg/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Client is an artifact-service-scoped S3 client (presign + multipart).
type Client struct {
	s3      *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// CompletedPart is one finished multipart part (number + ETag returned by the
// client's PUT to the presigned part URL).
type CompletedPart struct {
	PartNumber int32
	ETag       string
}

// UploadedPart is one part already stored for an in-progress multipart upload,
// as reported by ListParts. Used to resume an interrupted upload: the client
// skips these and only re-sends the missing parts.
type UploadedPart struct {
	PartNumber int32
	ETag       string
	Size       int64
}

// NewClient creates an S3 client from the given config. Works with B2 /
// MinIO / R2 / S3 via the UsePathStyle knob (B2: false; MinIO dev: true).
func NewClient(cfg config.S3Config) (*Client, error) {
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("artifact storage: AccessKeyID and SecretAccessKey are required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("artifact storage: Bucket is required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("artifact storage: load aws config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &Client{
		s3:      s3Client,
		presign: s3.NewPresignClient(s3Client),
		bucket:  cfg.Bucket,
	}, nil
}

// Bucket returns the bucket name the client is bound to.
func (c *Client) Bucket() string { return c.bucket }

// PresignPut returns a presigned single-PUT URL for the given key. The client
// uploads the whole object body to this URL directly. No headers are baked
// into the signature beyond the path so the caller can PUT raw bytes.
func (c *Client) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("artifact storage: presign put %q: %w", key, err)
	}
	return req.URL, nil
}

// PresignGet returns a presigned GET URL. downloadName, when non-empty, sets
// Content-Disposition so the browser saves the original filename.
func (c *Client) PresignGet(ctx context.Context, key, downloadName string, ttl time.Duration) (string, error) {
	in := &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}
	if downloadName != "" {
		in.ResponseContentDisposition = aws.String(ContentDisposition(downloadName))
	}
	req, err := c.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("artifact storage: presign get %q: %w", key, err)
	}
	return req.URL, nil
}

// CreateMultipart starts a multipart upload and returns its UploadId. The
// download filename and content type are baked here as object metadata: B2 maps
// Content-Disposition → b2-content-disposition and preserves both onto the
// finished object, so the attachment name is set ONCE at upload start and the
// completion path never pays an O(size) server-side copy. downloadName /
// contentType may be empty (then left unset).
func (c *Client) CreateMultipart(ctx context.Context, key, downloadName, contentType string) (string, error) {
	in := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}
	if downloadName != "" {
		in.ContentDisposition = aws.String(ContentDisposition(downloadName))
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		return "", fmt.Errorf("artifact storage: create multipart %q: %w", key, err)
	}
	if out.UploadId == nil {
		return "", fmt.Errorf("artifact storage: create multipart %q: nil upload id", key)
	}
	return *out.UploadId, nil
}

// PresignUploadPart returns a presigned URL for one part (1-based partNumber).
func (c *Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (string, error) {
	req, err := c.presign.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(c.bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("artifact storage: presign part %d of %q: %w", partNumber, key, err)
	}
	return req.URL, nil
}

// ListParts returns the parts already uploaded for an in-progress multipart
// upload, so a resumed client can skip them and re-send only what's missing.
// Follows S3 pagination (PartNumberMarker) to cover uploads with >1000 parts.
func (c *Client) ListParts(ctx context.Context, key, uploadID string) ([]UploadedPart, error) {
	var out []UploadedPart
	var marker *string
	for {
		resp, err := c.s3.ListParts(ctx, &s3.ListPartsInput{
			Bucket:           aws.String(c.bucket),
			Key:              aws.String(key),
			UploadId:         aws.String(uploadID),
			PartNumberMarker: marker,
		})
		if err != nil {
			return nil, fmt.Errorf("artifact storage: list parts %q: %w", key, err)
		}
		for _, p := range resp.Parts {
			up := UploadedPart{}
			if p.PartNumber != nil {
				up.PartNumber = *p.PartNumber
			}
			if p.ETag != nil {
				up.ETag = *p.ETag
			}
			if p.Size != nil {
				up.Size = *p.Size
			}
			out = append(out, up)
		}
		if resp.IsTruncated != nil && *resp.IsTruncated {
			marker = resp.NextPartNumberMarker
			continue
		}
		break
	}
	return out, nil
}

// CompleteMultipart finalises a multipart upload. Parts must be sorted ascending
// by PartNumber (the caller sorts before passing).
func (c *Client) CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	completed := make([]types.CompletedPart, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		})
	}
	_, err := c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(c.bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return fmt.Errorf("artifact storage: complete multipart %q: %w", key, err)
	}
	return nil
}

// AbortMultipart cancels an in-progress multipart upload, freeing its parts.
func (c *Client) AbortMultipart(ctx context.Context, key, uploadID string) error {
	_, err := c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(c.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("artifact storage: abort multipart %q: %w", key, err)
	}
	return nil
}

// HeadSize returns the actual object size, used to verify the uploaded bytes
// match the size the caller declared at init.
func (c *Client) HeadSize(ctx context.Context, key string) (int64, error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("artifact storage: head %q: %w", key, err)
	}
	if out.ContentLength == nil {
		return 0, nil
	}
	return *out.ContentLength, nil
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
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
		return false, nil
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	return false, fmt.Errorf("artifact storage: head %q: %w", key, err)
}

// Delete removes an object. Used by the GC job (with the cleanup-scoped key).
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("artifact storage: delete %q: %w", key, err)
	}
	return nil
}

// SetContentDisposition rewrites the object's metadata in place via a
// server-side copy onto its own key (MetadataDirective=REPLACE), baking an
// attachment Content-Disposition that carries downloadName. Used ONLY for
// single-PUT uploads (always smaller than the multipart threshold, so the copy
// is small and sub-second); multipart objects bake the same metadata at
// CreateMultipartUpload instead, so a GB-scale completion never pays this
// O(size) copy. B2 caps a single CopyObject at 5 GB — fine here, since single-
// PUT objects are far under it (multipart, which can reach far past 5 GB, never
// takes this path).
func (c *Client) SetContentDisposition(ctx context.Context, key, downloadName, contentType string) error {
	in := &s3.CopyObjectInput{
		Bucket:             aws.String(c.bucket),
		Key:                aws.String(key),
		CopySource:         aws.String(c.bucket + "/" + key),
		MetadataDirective:  types.MetadataDirectiveReplace,
		ContentDisposition: aws.String(ContentDisposition(downloadName)),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := c.s3.CopyObject(ctx, in); err != nil {
		return fmt.Errorf("artifact storage: set content-disposition %q: %w", key, err)
	}
	return nil
}

// EnsureBucket creates the bucket if missing. Intended for local dev (MinIO);
// production should pre-provision the (private) bucket.
func (c *Client) EnsureBucket(ctx context.Context) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(c.bucket)})
	if err == nil {
		return nil
	}
	_, err = c.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(c.bucket)})
	if err != nil {
		var alreadyOwned *types.BucketAlreadyOwnedByYou
		var alreadyExists *types.BucketAlreadyExists
		if errors.As(err, &alreadyOwned) || errors.As(err, &alreadyExists) {
			return nil
		}
		return fmt.Errorf("artifact storage: create bucket %q: %w", c.bucket, err)
	}
	return nil
}

// ContentDisposition builds an RFC 5987 attachment header that survives
// non-ASCII filenames (filename* + UTF-8), matching the production pattern.
func ContentDisposition(name string) string {
	return fmt.Sprintf("attachment; filename*=UTF-8''%s", percentEncode(name))
}

// percentEncode escapes a filename for the filename* (RFC 5987) field. Keeps
// unreserved chars; percent-encodes everything else byte-wise (UTF-8).
func percentEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b []byte
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b = append(b, ch)
			continue
		}
		b = append(b, '%', hex[ch>>4], hex[ch&0x0f])
	}
	return string(b)
}
