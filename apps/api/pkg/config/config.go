package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application
type Config struct {
	Server            ServerConfig
	Database          DatabaseConfig
	GalgameDatabase   DatabaseConfig
	CatalogDatabase   DatabaseConfig
	CommunityDatabase DatabaseConfig
	ImagesDatabase    DatabaseConfig
	Redis             RedisConfig
	JWT               JWTConfig
	Auth              AuthConfig
	OIDC              OIDCConfig
	Mail              MailConfig
	Meilisearch       MeilisearchConfig
	ImageService      ImageServiceConfig
	ImageS3           S3Config
	ImageClient       ImageClientConfig
	CatalogClient     CatalogClientConfig
	// GalgameImageClient is a SECOND image client identity, used only by the
	// galgame-image-refping job. The job runs in the oauth container (central
	// scheduler), where ImageClient is the *account* client — but galgame
	// banners/covers were uploaded under the *galgame_wiki* site, and
	// reference-ping is site-scoped, so pinging as account silently 404s every
	// hash. This lets the oauth container ping as galgame_wiki. Falls back to
	// ImageClient when unset (the standalone cmd in the galgame container,
	// where ImageClient already == galgame_wiki, needs no extra env).
	GalgameImageClient ImageClientConfig

	ArtifactsDatabase DatabaseConfig
	ArtifactS3        S3Config
	ArtifactService   ArtifactServiceConfig

	CatalogService   CatalogServiceConfig
	CommunityService CommunityServiceConfig
}

// CommunityServiceConfig holds community-service bind configuration. The
// community service (cmd/community) serves the S2S embed face (thread/post/
// reaction/feedback); see refs/docs/nextmoe-draft/11-community-primitive-design.md.
type CommunityServiceConfig struct {
	Host string // Bind address
	Port int    // Bind port
}

// CatalogServiceConfig holds catalog-service bind configuration. The catalog
// service (cmd/catalog) serves the S2S resolve/redirects/claim face and the
// admin review queues; see docs in refs/docs/nextmoe-draft/17.
type CatalogServiceConfig struct {
	Host string // Bind address
	Port int    // Bind port
}

// ArtifactServiceConfig holds artifact-service-specific configuration. See
// docs/artifact/ for the design. The artifact service is a private-bucket,
// presigned direct-upload/download platform for large files (game packages,
// patches, arbitrary attachments) — distinct from image_service.
type ArtifactServiceConfig struct {
	Host string // Bind address
	Port int    // Bind port

	// UploadEnabled gates the upload endpoints (Init/Complete). Default
	// false — uploads are off until B2 credentials + per-site config are
	// finalized. Download/list/get still serve so existing artifacts resolve.
	UploadEnabled bool

	// MultipartThreshold: reported sizes >= this use S3 multipart; smaller
	// use a single presigned PUT. Default 50MiB.
	MultipartThreshold int64
	// PartSize is the multipart chunk size (5MiB..5GiB per S3). Default 16MiB.
	PartSize int64

	// Presign TTLs: how long the issued upload/download URLs stay valid.
	// Download default is 24h (not 1h) so a paused/interrupted download can be
	// resumed via HTTP Range against the SAME presigned URL well after it
	// started — B2 honors Range on presigned GETs, so resume "just works" inside
	// this window without re-issuing. Uploads stay 1h: a resumed upload calls
	// GET /artifacts/{uuid}/resume to get fresh part URLs anyway.
	PresignUploadTTL   time.Duration // default 1h
	PresignDownloadTTL time.Duration // default 24h

	// GC TTLs (used by internal/jobs/artifact_gc).
	OrphanTTL     time.Duration // status=0 never completed → abort+delete; default 24h
	SoftDeleteTTL time.Duration // deleted_at older than this → physical delete; default 7d

	// ReclaimMinIdle is the safety floor for the MANUAL admin "reclaim now" of an
	// in-progress (status=uploading) upload: refuse if the row's updated_at is
	// more recent than this, so an actively-uploading or just-resumed upload is
	// not interrupted. Deliberately smaller than OrphanTTL (the unattended auto
	// sweep) — this is the "I can see it's stuck, clean it sooner" path. Default 1h.
	ReclaimMinIdle time.Duration

	// Least-privilege cleanup key (DeleteObject only), used ONLY by the GC
	// job. Empty → fall back to the presigner key (see Config.ArtifactCleanupS3).
	CleanupAccessKey string
	CleanupSecretKey string
}

// ImageClientConfig is caller-side configuration for processes (cmd/galgame,
// cmd/oauth, migration scripts, etc) that talk to image_service.
type ImageClientConfig struct {
	BaseURL      string // e.g. http://127.0.0.1:9278 (image_service URL from this caller's perspective)
	ClientID     string // OAuth client id this caller authenticates as
	ClientSecret string // OAuth client secret
}

// CatalogClientConfig is caller-side configuration for the galgame backend's
// proxy to the catalog S2S read face (the internal data browser). Empty
// ClientID/Secret → the proxy soft-503s ("catalog browsing not configured").
type CatalogClientConfig struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
}

// ImageServiceConfig holds image-service-specific configuration
type ImageServiceConfig struct {
	Host        string // Bind address
	Port        int    // Bind port
	CDNBase     string // Public URL prefix, e.g. https://cdn.example.com/img
	PresetsPath string // Path to image_presets.yaml

	// UploadEnabled gates POST /image/upload. Default false — uploads
	// are disabled until calling-side integration is finalized. The
	// service still serves GET /image/:hash, POST /image/reference-ping,
	// and /healthz / /metrics so existing hashes stay resolvable.
	UploadEnabled bool
}

// S3Config holds S3-compatible object storage configuration
type S3Config struct {
	Endpoint        string // e.g., http://127.0.0.1:9000
	Region          string // e.g., us-east-1 or auto for R2
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	UsePathStyle    bool // true for MinIO, false for AWS S3
}

// MeilisearchConfig holds Meilisearch-related configuration
type MeilisearchConfig struct {
	Host        string // e.g. http://127.0.0.1:7700
	APIKey      string // empty for dev, filled in prod
	IndexPrefix string // optional, e.g. "dev_" / "staging_"
}

// MailConfig holds email-related configuration
type MailConfig struct {
	From     string
	Host     string
	Port     int
	Account  string
	Password string
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Host        string
	Port        int
	Env         string
	SiteURL     string
	FrontendURL string
	CORSOrigin  string
}

// DatabaseConfig holds database-related configuration
type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
	Timezone string
}

// RedisConfig holds Redis-related configuration
type RedisConfig struct {
	Enabled  bool
	Host     string
	Port     int
	Username string
	Password string
	DB       int
}

// JWTConfig holds JWT-related configuration
type JWTConfig struct {
	Secret     string
	CookieName string
	Expires    string
}

// OIDCConfig holds the OIDC OpenID Provider settings.
//
// Issuer is the canonical identifier (== Server.SiteURL) used as the token
// `iss`, the discovery `issuer`, and the base the .well-known documents are
// served from — all three MUST match (a classic OIDC footgun).
//
// KeyEncKey (KUN_OIDC_KEY_ENC_KEY) is the KEK that encrypts signing private
// keys at rest. Only cmd/oauth needs it — it generates/decrypts keys; the
// separate resource-server binaries verify with public keys only. When empty,
// cmd/oauth skips signing-key bootstrap + the jwks/discovery endpoints.
//
// Design: docs/auth/03-oidc-standardization-design.md.
type OIDCConfig struct {
	Issuer    string
	KeyEncKey string
	// SignAsymmetric flips access-token signing from the legacy HS256 to the
	// active ES256 key. Default false. MUST stay false until every verifier
	// (oauth + galgame/image/artifact) is deployed with accept-both
	// verification (Phase 1), else in-flight-issued ES256 tokens 401 on the
	// services that only know HS256. Env: KUN_OIDC_SIGN_ASYMMETRIC.
	SignAsymmetric bool
	// JWKSURL is the OP's JWK Set URL that the resource-server verifiers
	// (galgame / image / artifact) fetch to validate ES256/RS256 tokens — the
	// INTERNAL address, e.g. http://oauth:9277/oauth/jwks. Empty => asymmetric
	// verification disabled (HS256-only), which is safe until asymmetric signing
	// is enabled. Env: KUN_OIDC_JWKS_URL.
	JWKSURL string
	// StandardWire makes the OAuth/OIDC protocol endpoints (/oauth/token,
	// /oauth/userinfo, /oauth/revoke) emit spec-compliant top-level JSON +
	// standard OAuth error objects instead of the private {code,message,data}
	// envelope. Default false. Flip only after every first-party RP is a
	// tolerant reader (Phase 3 expand→contract). Env: KUN_OIDC_STANDARD_WIRE.
	StandardWire bool
}

// AuthConfig holds auth-flow-tunable parameters that aren't tied to a
// single subsystem (JWT / Mail / Redis).
type AuthConfig struct {
	// VerificationCodeTTL is the lifetime of an email-delivered 6-digit
	// verification code (registration + email-change flows). Same value
	// doubles as the per-email rate-limit window — you can't request a
	// second code for the same address until the previous one expires.
	//
	// Default 15 minutes. Tradeoff: long enough that users with slow
	// inboxes / who switch tabs to fetch the code can still complete
	// the flow; short enough that a code leaked via a shared email
	// client isn't usable an hour later.
	//
	// Configured via `KUN_AUTH_VERIFICATION_CODE_TTL_MINUTES`. Floor of
	// 1 minute (the code generator + SMTP round-trip alone consume ~2-5
	// seconds and below 1 minute leaves no margin for a normal user).
	VerificationCodeTTL time.Duration
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists
	_ = godotenv.Load()

	cfg := &Config{}

	// Server config
	serverPort, _ := strconv.Atoi(getEnv("KUN_FIBER_SERVER_PORT", "9277"))
	cfg.Server = ServerConfig{
		Host:        getEnv("KUN_FIBER_SERVER_HOST", "127.0.0.1"),
		Port:        serverPort,
		Env:         getEnv("KUN_ENV", "development"),
		SiteURL:     getEnv("KUN_SITE_URL", "http://127.0.0.1:9277"),
		FrontendURL: getEnv("KUN_FRONTEND_URL", "http://127.0.0.1:9420"),
		CORSOrigin:  getEnv("KUN_FRONTEND_CORS_ORIGIN", "http://127.0.0.1:9420,http://127.0.0.1:9421"),
	}

	// Database config
	cfg.Database = DatabaseConfig{
		Host:     getEnv("KUN_PG_HOST", "localhost"),
		Port:     getEnv("KUN_PG_PORT", "5432"),
		User:     getEnv("KUN_PG_USER", "postgres"),
		Password: getEnv("KUN_PG_PASSWORD", ""),
		DBName:   getEnv("KUN_PG_DATABASE", "kun_galgame_infra"),
		SSLMode:  getEnv("KUN_PG_SSLMODE", "disable"),
		Timezone: getEnv("KUN_PG_TIMEZONE", "Asia/Shanghai"),
	}

	// Galgame wiki database config (defaults to same server, different db name)
	cfg.GalgameDatabase = DatabaseConfig{
		Host:     getEnv("KUN_GALGAME_PG_HOST", cfg.Database.Host),
		Port:     getEnv("KUN_GALGAME_PG_PORT", cfg.Database.Port),
		User:     getEnv("KUN_GALGAME_PG_USER", cfg.Database.User),
		Password: getEnv("KUN_GALGAME_PG_PASSWORD", cfg.Database.Password),
		DBName:   getEnv("KUN_GALGAME_PG_DATABASE", "kun_galgame_wiki"),
		SSLMode:  getEnv("KUN_GALGAME_PG_SSLMODE", cfg.Database.SSLMode),
		Timezone: getEnv("KUN_GALGAME_PG_TIMEZONE", cfg.Database.Timezone),
	}

	// Catalog database config (defaults to same server, different db name)
	cfg.CatalogDatabase = DatabaseConfig{
		Host:     getEnv("KUN_CATALOG_PG_HOST", cfg.Database.Host),
		Port:     getEnv("KUN_CATALOG_PG_PORT", cfg.Database.Port),
		User:     getEnv("KUN_CATALOG_PG_USER", cfg.Database.User),
		Password: getEnv("KUN_CATALOG_PG_PASSWORD", cfg.Database.Password),
		DBName:   getEnv("KUN_CATALOG_PG_DATABASE", "kun_catalog"),
		SSLMode:  getEnv("KUN_CATALOG_PG_SSLMODE", cfg.Database.SSLMode),
		Timezone: getEnv("KUN_CATALOG_PG_TIMEZONE", cfg.Database.Timezone),
	}

	// Community database config (defaults to same server, different db name).
	// The community primitive (cmd/community) owns kun_community; see
	// refs/docs/nextmoe-draft/11-community-primitive-design.md.
	cfg.CommunityDatabase = DatabaseConfig{
		Host:     getEnv("KUN_COMMUNITY_PG_HOST", cfg.Database.Host),
		Port:     getEnv("KUN_COMMUNITY_PG_PORT", cfg.Database.Port),
		User:     getEnv("KUN_COMMUNITY_PG_USER", cfg.Database.User),
		Password: getEnv("KUN_COMMUNITY_PG_PASSWORD", cfg.Database.Password),
		DBName:   getEnv("KUN_COMMUNITY_PG_DATABASE", "kun_community"),
		SSLMode:  getEnv("KUN_COMMUNITY_PG_SSLMODE", cfg.Database.SSLMode),
		Timezone: getEnv("KUN_COMMUNITY_PG_TIMEZONE", cfg.Database.Timezone),
	}

	// Redis config
	redisEnabled, _ := strconv.ParseBool(getEnv("REDIS_ENABLED", "false"))
	redisPort, _ := strconv.Atoi(getEnv("REDIS_PORT", "6379"))
	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))
	cfg.Redis = RedisConfig{
		Enabled:  redisEnabled,
		Host:     getEnv("REDIS_HOST", "localhost"),
		Port:     redisPort,
		Username: getEnv("REDIS_USERNAME", ""),
		Password: getEnv("REDIS_PASSWORD", ""),
		DB:       redisDB,
	}

	// JWT config
	cfg.JWT = JWTConfig{
		Secret:     getEnv("JWT_SECRET", ""),
		CookieName: getEnv("JWT_COOKIE_NAME", "kun_token"),
		Expires:    getEnv("JWT_EXPIRES", "90d"),
	}

	// OIDC config. Issuer is the OAuth server's public base URL (== SiteURL);
	// KeyEncKey encrypts signing private keys at rest (only cmd/oauth needs it).
	signAsym, _ := strconv.ParseBool(getEnv("KUN_OIDC_SIGN_ASYMMETRIC", "false"))
	stdWire, _ := strconv.ParseBool(getEnv("KUN_OIDC_STANDARD_WIRE", "false"))
	cfg.OIDC = OIDCConfig{
		Issuer:         cfg.Server.SiteURL,
		KeyEncKey:      getEnv("KUN_OIDC_KEY_ENC_KEY", ""),
		SignAsymmetric: signAsym,
		JWKSURL:        getEnv("KUN_OIDC_JWKS_URL", ""),
		StandardWire:   stdWire,
	}

	// Auth config — verification-code TTL and other auth-flow knobs.
	// Floored at 1 minute so a misconfiguration can't ship sub-second
	// codes that no human can possibly type in time.
	codeTTLMinutes, _ := strconv.Atoi(getEnv("KUN_AUTH_VERIFICATION_CODE_TTL_MINUTES", "15"))
	if codeTTLMinutes < 1 {
		codeTTLMinutes = 1
	}
	cfg.Auth = AuthConfig{
		VerificationCodeTTL: time.Duration(codeTTLMinutes) * time.Minute,
	}

	// Mail config
	mailPort, _ := strconv.Atoi(getEnv("KUN_VISUAL_NOVEL_EMAIL_PORT", "587"))
	cfg.Mail = MailConfig{
		From:     getEnv("KUN_VISUAL_NOVEL_EMAIL_FROM", "鲲 Galgame OAuth"),
		Host:     getEnv("KUN_VISUAL_NOVEL_EMAIL_HOST", ""),
		Port:     mailPort,
		Account:  getEnv("KUN_VISUAL_NOVEL_EMAIL_ACCOUNT", ""),
		Password: getEnv("KUN_VISUAL_NOVEL_EMAIL_PASSWORD", ""),
	}

	// Meilisearch config
	cfg.Meilisearch = MeilisearchConfig{
		Host:        getEnv("KUN_MEILISEARCH_HOST", "http://127.0.0.1:7700"),
		APIKey:      getEnv("KUN_MEILISEARCH_API_KEY", ""),
		IndexPrefix: getEnv("KUN_MEILISEARCH_INDEX_PREFIX", ""),
	}

	// Images database config (defaults to same server, different db name)
	cfg.ImagesDatabase = DatabaseConfig{
		Host:     getEnv("KUN_IMAGES_PG_HOST", cfg.Database.Host),
		Port:     getEnv("KUN_IMAGES_PG_PORT", cfg.Database.Port),
		User:     getEnv("KUN_IMAGES_PG_USER", cfg.Database.User),
		Password: getEnv("KUN_IMAGES_PG_PASSWORD", cfg.Database.Password),
		DBName:   getEnv("KUN_IMAGES_PG_DATABASE", "kun_images"),
		SSLMode:  getEnv("KUN_IMAGES_PG_SSLMODE", cfg.Database.SSLMode),
		Timezone: getEnv("KUN_IMAGES_PG_TIMEZONE", cfg.Database.Timezone),
	}

	// Image Service config
	imagePort, _ := strconv.Atoi(getEnv("KUN_IMAGE_SERVICE_PORT", "9278"))
	imageUploadEnabled, _ := strconv.ParseBool(getEnv("KUN_IMAGE_UPLOAD_ENABLED", "false"))
	cfg.ImageService = ImageServiceConfig{
		Host:          getEnv("KUN_IMAGE_SERVICE_HOST", "127.0.0.1"),
		Port:          imagePort,
		CDNBase:       getEnv("KUN_IMAGE_PUBLIC_BASE_URL", "http://127.0.0.1:9000/kun-images-dev"),
		PresetsPath:   getEnv("KUN_IMAGE_PRESETS_PATH", "apps/api/configs/image_presets.yaml"),
		UploadEnabled: imageUploadEnabled,
	}

	// S3 (object storage) config
	s3UsePathStyle, _ := strconv.ParseBool(getEnv("KUN_IMAGE_S3_FORCE_PATH_STYLE", "true"))
	cfg.ImageS3 = S3Config{
		Endpoint:        getEnv("KUN_IMAGE_S3_ENDPOINT", "http://127.0.0.1:9000"),
		Region:          getEnv("KUN_IMAGE_S3_REGION", "us-east-1"),
		AccessKeyID:     getEnv("KUN_IMAGE_S3_ACCESS_KEY", ""),
		SecretAccessKey: getEnv("KUN_IMAGE_S3_SECRET_KEY", ""),
		Bucket:          getEnv("KUN_IMAGE_S3_BUCKET", "kun-images-dev"),
		UsePathStyle:    s3UsePathStyle,
	}

	// Image client config (caller-side: cmd/galgame, cmd/oauth, migration scripts)
	defaultBase := fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
	cfg.ImageClient = ImageClientConfig{
		BaseURL:      getEnv("KUN_IMAGE_CLIENT_BASE_URL", defaultBase),
		ClientID:     getEnv("KUN_IMAGE_CLIENT_ID", ""),
		ClientSecret: getEnv("KUN_IMAGE_CLIENT_SECRET", ""),
	}

	// Catalog client for the galgame backend's internal-browser proxy (step 19).
	cfg.CatalogClient = CatalogClientConfig{
		BaseURL:      getEnv("KUN_CATALOG_CLIENT_BASE_URL", "http://catalog:9281"),
		ClientID:     getEnv("KUN_CATALOG_CLIENT_ID", ""),
		ClientSecret: getEnv("KUN_CATALOG_CLIENT_SECRET", ""),
	}

	// Second image identity for galgame-image-refping (see Config.GalgameImageClient).
	// Set KUN_GALGAME_IMAGE_CLIENT_ID / _SECRET in the oauth container to the
	// galgame_wiki client (= the galgame container's KUN_IMAGE_CLIENT_ID/SECRET).
	// Empty ClientID → the job falls back to cfg.ImageClient.
	cfg.GalgameImageClient = ImageClientConfig{
		BaseURL:      getEnv("KUN_GALGAME_IMAGE_CLIENT_BASE_URL", cfg.ImageClient.BaseURL),
		ClientID:     getEnv("KUN_GALGAME_IMAGE_CLIENT_ID", ""),
		ClientSecret: getEnv("KUN_GALGAME_IMAGE_CLIENT_SECRET", ""),
	}

	// Artifacts database config (defaults to same server, different db name)
	cfg.ArtifactsDatabase = DatabaseConfig{
		Host:     getEnv("KUN_ARTIFACTS_PG_HOST", cfg.Database.Host),
		Port:     getEnv("KUN_ARTIFACTS_PG_PORT", cfg.Database.Port),
		User:     getEnv("KUN_ARTIFACTS_PG_USER", cfg.Database.User),
		Password: getEnv("KUN_ARTIFACTS_PG_PASSWORD", cfg.Database.Password),
		DBName:   getEnv("KUN_ARTIFACTS_PG_DATABASE", "kun_artifacts"),
		SSLMode:  getEnv("KUN_ARTIFACTS_PG_SSLMODE", cfg.Database.SSLMode),
		Timezone: getEnv("KUN_ARTIFACTS_PG_TIMEZONE", cfg.Database.Timezone),
	}

	// Artifact S3/B2 (object storage) config. Prod points at Backblaze B2
	// (S3-compatible): endpoint s3.<region>.backblazeb2.com, UsePathStyle=false.
	// Dev defaults to local MinIO (path style).
	artifactPathStyle, _ := strconv.ParseBool(getEnv("KUN_ARTIFACT_S3_FORCE_PATH_STYLE", "true"))
	cfg.ArtifactS3 = S3Config{
		Endpoint:        getEnv("KUN_ARTIFACT_S3_ENDPOINT", "http://127.0.0.1:9000"),
		Region:          getEnv("KUN_ARTIFACT_S3_REGION", "us-east-1"),
		AccessKeyID:     getEnv("KUN_ARTIFACT_S3_ACCESS_KEY", ""),
		SecretAccessKey: getEnv("KUN_ARTIFACT_S3_SECRET_KEY", ""),
		Bucket:          getEnv("KUN_ARTIFACT_S3_BUCKET", "kun-artifacts-dev"),
		UsePathStyle:    artifactPathStyle,
	}

	// Artifact service config.
	artifactPort, _ := strconv.Atoi(getEnv("KUN_ARTIFACT_PORT", "9279"))
	artifactUploadEnabled, _ := strconv.ParseBool(getEnv("KUN_ARTIFACT_UPLOAD_ENABLED", "false"))
	cfg.ArtifactService = ArtifactServiceConfig{
		Host:               getEnv("KUN_ARTIFACT_HOST", "127.0.0.1"),
		Port:               artifactPort,
		UploadEnabled:      artifactUploadEnabled,
		MultipartThreshold: getEnvInt64("KUN_ARTIFACT_MULTIPART_THRESHOLD", 50*1024*1024),
		PartSize:           getEnvInt64("KUN_ARTIFACT_PART_SIZE", 16*1024*1024),
		PresignUploadTTL:   time.Duration(getEnvInt64("KUN_ARTIFACT_PRESIGN_UPLOAD_TTL_SECONDS", 3600)) * time.Second,
		PresignDownloadTTL: time.Duration(getEnvInt64("KUN_ARTIFACT_PRESIGN_DOWNLOAD_TTL_SECONDS", 86400)) * time.Second,
		OrphanTTL:          time.Duration(getEnvInt64("KUN_ARTIFACT_ORPHAN_TTL_HOURS", 24)) * time.Hour,
		SoftDeleteTTL:      time.Duration(getEnvInt64("KUN_ARTIFACT_SOFTDELETE_TTL_HOURS", 168)) * time.Hour,
		ReclaimMinIdle:     time.Duration(getEnvInt64("KUN_ARTIFACT_RECLAIM_MIN_IDLE_SECONDS", 3600)) * time.Second,
		CleanupAccessKey:   getEnv("KUN_ARTIFACT_S3_CLEANUP_ACCESS_KEY", ""),
		CleanupSecretKey:   getEnv("KUN_ARTIFACT_S3_CLEANUP_SECRET_KEY", ""),
	}

	// Catalog service config. Port 9281 = next free slot after oauth 9277 /
	// image 9278 / artifact 9279 / galgame 9280.
	catalogPort, _ := strconv.Atoi(getEnv("KUN_CATALOG_PORT", "9281"))
	cfg.CatalogService = CatalogServiceConfig{
		Host: getEnv("KUN_CATALOG_HOST", "127.0.0.1"),
		Port: catalogPort,
	}

	// Community service config. Port 9282 = next free slot after oauth 9277 /
	// image 9278 / artifact 9279 / galgame 9280 / catalog 9281.
	communityPort, _ := strconv.Atoi(getEnv("KUN_COMMUNITY_PORT", "9282"))
	cfg.CommunityService = CommunityServiceConfig{
		Host: getEnv("KUN_COMMUNITY_HOST", "127.0.0.1"),
		Port: communityPort,
	}

	// Validate required fields
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks required configuration fields
func (c *Config) validate() error {
	if c.Database.Password == "" {
		return fmt.Errorf("KUN_PG_PASSWORD is required")
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	return nil
}

// DSN returns the PostgreSQL connection string
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode, c.Timezone,
	)
}

// Addr returns the Redis address
func (c *RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// IsDevelopment returns true if running in development mode
func (c *ServerConfig) IsDevelopment() bool {
	return c.Env == "development"
}

// IsProduction returns true if running in production mode
func (c *ServerConfig) IsProduction() bool {
	return c.Env == "production"
}

// ArtifactCleanupS3 returns the S3 config for the GC job: the artifact bucket
// with the least-privilege cleanup credentials (DeleteObject only) if set,
// else falling back to the presigner credentials.
func (c *Config) ArtifactCleanupS3() S3Config {
	s3 := c.ArtifactS3
	if c.ArtifactService.CleanupAccessKey != "" {
		s3.AccessKeyID = c.ArtifactService.CleanupAccessKey
		s3.SecretAccessKey = c.ArtifactService.CleanupSecretKey
	}
	return s3
}

// getEnvInt64 parses an int64 env var, returning the default on missing/invalid.
func getEnvInt64(key string, defaultValue int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return defaultValue
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
