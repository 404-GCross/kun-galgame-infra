package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"api/internal/infrastructure/database"
	artifactModel "api/internal/platform/artifact/model"
	"api/internal/platform/artifact/storage"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

const maxSingleCopyBytes = 5 * 1024 * 1024 * 1024

func main() {
	var (
		moyuDB      = flag.String("moyu-db", "kungalgame_patch", "moyu database name (same PG server as kun_artifacts)")
		srcBucket   = flag.String("src-bucket", "kun-galgame-patch", "old moyu B2 bucket (copy source)")
		site        = flag.String("site", "moyu", "artifact site_key for the adopted rows")
		apply       = flag.Bool("apply", false, "actually copy + write; default is a dry run")
		limit       = flag.Int("limit", 0, "max resources to process (0 = all)")
		concurrency = flag.Int("concurrency", 8, "parallel copy workers")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fatal("load config", err)
	}
	logger.Init(cfg.Server.Env)

	if cfg.ArtifactS3.AccessKeyID == "" || cfg.ArtifactS3.SecretAccessKey == "" {
		fatal("config", fmt.Errorf("KUN_ARTIFACT_S3_ACCESS_KEY/SECRET_KEY must be an account-wide key (read %s + write %s)", *srcBucket, cfg.ArtifactS3.Bucket))
	}
	dstBucket := cfg.ArtifactS3.Bucket
	if *concurrency < 1 {
		*concurrency = 1
	}

	s3c, err := newS3(cfg.ArtifactS3)
	if err != nil {
		fatal("s3 client", err)
	}

	artDB, err := database.NewPostgresDB(cfg.ArtifactsDatabase)
	if err != nil {
		fatal("open artifacts db", err)
	}
	defer artDB.Close()
	moyuCfg := cfg.ArtifactsDatabase
	moyuCfg.DBName = *moyuDB
	patchDB, err := database.NewPostgresDB(moyuCfg)
	if err != nil {
		fatal("open moyu db", err)
	}
	defer patchDB.Close()

	ctx := context.Background()

	type row struct {
		ID    int64
		S3Key string
	}
	var rows []row
	q := patchDB.DB().WithContext(ctx).
		Table("patch_resource").
		Select("id", "s3_key").
		Where("storage = ? AND s3_key <> '' AND (artifact_uuid = '' OR artifact_uuid IS NULL)", "s3").
		Order("id")
	if *limit > 0 {
		q = q.Limit(*limit)
	}
	if err := q.Scan(&rows).Error; err != nil {
		fatal("query resources", err)
	}

	slog.Info("adopt-moyu-resources", "to_process", len(rows), "src", *srcBucket, "dst", dstBucket, "site", *site, "apply", *apply)

	var copied, skippedMissing, skippedTooBig, failed int64
	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	for _, r := range rows {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()

			name := path.Base(r.S3Key)
			id := deterministicUUID(r.ID)
			dstKey := *site + "/" + id + extForKey(name)

			head, err := s3c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(*srcBucket), Key: aws.String(r.S3Key)})
			if err != nil {
				atomic.AddInt64(&skippedMissing, 1)
				slog.Warn("source missing / head failed", "id", r.ID, "s3_key", r.S3Key, "err", err)
				return
			}
			var size int64
			if head.ContentLength != nil {
				size = *head.ContentLength
			}
			if size > maxSingleCopyBytes {
				atomic.AddInt64(&skippedTooBig, 1)
				slog.Warn("too big for single CopyObject; needs multipart-copy (skipped)", "id", r.ID, "size", size, "s3_key", r.S3Key)
				return
			}
			ctype := ""
			if head.ContentType != nil {
				ctype = *head.ContentType
			}

			if !*apply {
				slog.Info("DRY-RUN would adopt", "id", r.ID, "name", name, "size", size, "dst", dstKey)
				atomic.AddInt64(&copied, 1)
				return
			}

			// CopySource must be URL-encoded (keys carry CJK); EscapedPath keeps "/".
			// EscapedPath leaves "+" literal, but B2 decodes a literal "+" in
			// x-amz-copy-source as a space → NoSuchKey, so percent-encode it.
			copySource := strings.ReplaceAll((&url.URL{Path: *srcBucket + "/" + r.S3Key}).EscapedPath(), "+", "%2B")
			in := &s3.CopyObjectInput{
				Bucket:             aws.String(dstBucket),
				Key:                aws.String(dstKey),
				CopySource:         aws.String(copySource),
				MetadataDirective:  types.MetadataDirectiveReplace,
				ContentDisposition: aws.String(storage.ContentDisposition(name)),
			}
			if ctype != "" {
				in.ContentType = aws.String(ctype)
			}
			if _, err := s3c.CopyObject(ctx, in); err != nil {
				atomic.AddInt64(&failed, 1)
				slog.Error("copy failed", "id", r.ID, "src", copySource, "err", err)
				return
			}

			a := &artifactModel.Artifact{
				UUID:           id,
				SiteKey:        *site,
				UploaderSub:    "migration",
				UploaderClient: "adopt-moyu-resources",
				Name:           name,
				FileKey:        dstKey,
				FileSize:       size,
				ReportedSize:   size,
				MimeType:       ctype,
				Status:         artifactModel.StatusReady,
				Public:         true,
			}
			if err := artDB.DB().WithContext(ctx).
				Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "uuid"}}, DoNothing: true}).
				Create(a).Error; err != nil {
				atomic.AddInt64(&failed, 1)
				slog.Error("insert artifact row failed", "id", r.ID, "err", err)
				return
			}
			if err := patchDB.DB().WithContext(ctx).Exec(
				"UPDATE patch_resource SET artifact_uuid = ? WHERE id = ? AND (artifact_uuid = '' OR artifact_uuid IS NULL)",
				id, r.ID,
			).Error; err != nil {
				atomic.AddInt64(&failed, 1)
				slog.Error("link patch_resource failed", "id", r.ID, "uuid", id, "err", err)
				return
			}
			atomic.AddInt64(&copied, 1)
		})
	}
	wg.Wait()

	slog.Info("done",
		"processed", copied, "skipped_missing", skippedMissing,
		"skipped_too_big", skippedTooBig, "failed", failed, "apply", *apply)
	if failed > 0 {
		os.Exit(1)
	}
}

func deterministicUUID(resourceID int64) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "kungal-artifact:moyu:patch_resource:%d", resourceID)).String()
}

func extForKey(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if len(ext) < 2 {
		return ""
	}
	for _, r := range ext[1:] {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return ext
}

func newS3(cfg config.S3Config) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	}), nil
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
