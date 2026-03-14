package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Redis    RedisConfig
	JWT      JWTConfig
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Host       string
	Port       int
	Env        string
	SiteURL    string
	CORSOrigin string
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

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists
	_ = godotenv.Load()

	cfg := &Config{}

	// Server config
	serverPort, _ := strconv.Atoi(getEnv("KUN_FIBER_SERVER_PORT", "9277"))
	cfg.Server = ServerConfig{
		Host:       getEnv("KUN_FIBER_SERVER_HOST", "0.0.0.0"),
		Port:       serverPort,
		Env:        getEnv("KUN_ENV", "development"),
		SiteURL:    getEnv("KUN_SITE_URL", "http://localhost:9277"),
		CORSOrigin: getEnv("KUN_FRONTEND_CORS_ORIGIN", "http://127.0.0.1:9420"),
	}

	// Database config
	cfg.Database = DatabaseConfig{
		Host:     getEnv("KUN_PG_HOST", "localhost"),
		Port:     getEnv("KUN_PG_PORT", "5432"),
		User:     getEnv("KUN_PG_USER", "postgres"),
		Password: getEnv("KUN_PG_PASSWORD", ""),
		DBName:   getEnv("KUN_PG_DATABASE", "kun_oauth_admin"),
		SSLMode:  getEnv("KUN_PG_SSLMODE", "disable"),
		Timezone: getEnv("KUN_PG_TIMEZONE", "Asia/Shanghai"),
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
