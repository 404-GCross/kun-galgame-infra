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
	Server          ServerConfig
	Database        DatabaseConfig
	GalgameDatabase DatabaseConfig
	ImagesDatabase  DatabaseConfig
	Redis           RedisConfig
	JWT             JWTConfig
	Auth            AuthConfig
	Mail            MailConfig
	Meilisearch     MeilisearchConfig
	ImageService    ImageServiceConfig
	ImageS3         S3Config
	ImageClient     ImageClientConfig
}

// ImageClientConfig is caller-side configuration for processes (cmd/galgame,
// cmd/oauth, migration scripts, etc) that talk to image_service.
type ImageClientConfig struct {
	BaseURL      string // e.g. http://127.0.0.1:9278 (image_service URL from this caller's perspective)
	ClientID     string // OAuth client id this caller authenticates as
	ClientSecret string // OAuth client secret
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

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
