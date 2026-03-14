package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	authModel "api/internal/platform/auth/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// KungalUser represents a user from kungal-nuxt (kungalgame database)
type KungalUser struct {
	ID          uint      `gorm:"column:id"`
	Name        string    `gorm:"column:name"`
	Email       string    `gorm:"column:email"`
	Password    string    `gorm:"column:password"` // bcrypt hash
	Avatar      string    `gorm:"column:avatar"`
	Bio         string    `gorm:"column:bio"`
	Moemoepoint int       `gorm:"column:moemoepoint"`
	Status      int       `gorm:"column:status"`
	IP          string    `gorm:"column:ip"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (KungalUser) TableName() string {
	return "user" // Prisma uses singular table names
}

// MoyuUser represents a user from moyu-nextjs (kungalgame_patch database)
type MoyuUser struct {
	ID          uint      `gorm:"column:id"`
	Name        string    `gorm:"column:name"`
	Email       string    `gorm:"column:email"`
	Password    string    `gorm:"column:password"` // argon2 hash (custom format)
	Avatar      string    `gorm:"column:avatar"`
	Bio         string    `gorm:"column:bio"`
	Moemoepoint int       `gorm:"column:moemoepoint"`
	Status      int       `gorm:"column:status"`
	IP          string    `gorm:"column:ip"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (MoyuUser) TableName() string {
	return "user"
}

// MigrationResult tracks migration statistics
type MigrationResult struct {
	KungalUsersTotal  int
	MoyuUsersTotal    int
	NewUsersCreated   int
	UsersMerged       int
	Errors            int
	SkippedDuplicates int
}

func main() {
	// Parse flags
	dryRun := flag.Bool("dry-run", false, "Perform a dry run without making changes")
	kungalDSN := flag.String("kungal-dsn", "", "Kungal database DSN (required)")
	moyuDSN := flag.String("moyu-dsn", "", "Moyu database DSN (required)")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize logger
	logger.Init(cfg.Server.Env)

	// Validate DSNs
	if *kungalDSN == "" || *moyuDSN == "" {
		slog.Error("both --kungal-dsn and --moyu-dsn are required")
		fmt.Println("\nUsage:")
		fmt.Println("  go run ./cmd/migrate-users \\")
		fmt.Println("    --kungal-dsn=\"host=localhost port=5432 user=postgres password=xxx dbname=kungalgame sslmode=disable\" \\")
		fmt.Println("    --moyu-dsn=\"host=localhost port=5432 user=postgres password=xxx dbname=kungalgame_patch sslmode=disable\"")
		fmt.Println("\nOptions:")
		fmt.Println("  --dry-run    Perform a dry run without making changes")
		os.Exit(1)
	}

	if *dryRun {
		slog.Info("DRY RUN MODE - No changes will be made")
	}

	// Connect to target database (kun_oauth_admin)
	targetDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("failed to connect to target database", "error", err)
		os.Exit(1)
	}
	defer targetDB.Close()

	// Connect to kungal database
	kungalDB, err := connectDB(*kungalDSN, "kungal")
	if err != nil {
		slog.Error("failed to connect to kungal database", "error", err)
		os.Exit(1)
	}

	// Connect to moyu database
	moyuDB, err := connectDB(*moyuDSN, "moyu")
	if err != nil {
		slog.Error("failed to connect to moyu database", "error", err)
		os.Exit(1)
	}

	// Run migration
	result, err := migrateUsers(context.Background(), targetDB.DB(), kungalDB, moyuDB, *dryRun)
	if err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Print results
	printResults(result, *dryRun)
}

func connectDB(dsn, name string) (*gorm.DB, error) {
	gormConfig := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	}

	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s database: %w", name, err)
	}

	slog.Info("Connected to database", "name", name)
	return db, nil
}

func migrateUsers(ctx context.Context, targetDB, kungalDB, moyuDB *gorm.DB, dryRun bool) (*MigrationResult, error) {
	result := &MigrationResult{}

	// Check if migration table exists (should be created by migrate command)
	if !targetDB.Migrator().HasTable(&authModel.UserMigration{}) {
		return nil, fmt.Errorf("user_migrations table not found. Please run 'make migrate' first")
	}

	// Fetch all users from kungal
	var kungalUsers []KungalUser
	if err := kungalDB.WithContext(ctx).Find(&kungalUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch kungal users: %w", err)
	}
	result.KungalUsersTotal = len(kungalUsers)
	slog.Info("Fetched kungal users", "count", len(kungalUsers))

	// Fetch all users from moyu
	var moyuUsers []MoyuUser
	if err := moyuDB.WithContext(ctx).Find(&moyuUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch moyu users: %w", err)
	}
	result.MoyuUsersTotal = len(moyuUsers)
	slog.Info("Fetched moyu users", "count", len(moyuUsers))

	// Build email to moyu user map for merging
	moyuByEmail := make(map[string]*MoyuUser)
	for i := range moyuUsers {
		email := strings.ToLower(strings.TrimSpace(moyuUsers[i].Email))
		moyuByEmail[email] = &moyuUsers[i]
	}

	// Track processed emails to avoid duplicates
	processedEmails := make(map[string]bool)
	processedNames := make(map[string]bool)

	// Also check existing users in target database
	var existingUsers []authModel.User
	if err := targetDB.WithContext(ctx).Select("email", "name").Find(&existingUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch existing users: %w", err)
	}
	for _, u := range existingUsers {
		processedEmails[strings.ToLower(u.Email)] = true
		processedNames[u.Name] = true
	}
	slog.Info("Found existing users in target", "count", len(existingUsers))

	// Process kungal users first (they take priority)
	for _, ku := range kungalUsers {
		email := strings.ToLower(strings.TrimSpace(ku.Email))

		if processedEmails[email] {
			result.SkippedDuplicates++
			continue
		}

		newUser := &authModel.User{
			Name:        strings.TrimSpace(ku.Name),
			Email:       email,
			Password:    nil, // All migrated users must reset password
			Avatar:      ku.Avatar,
			Bio:         ku.Bio,
			Moemoepoint: ku.Moemoepoint,
			Status:      ku.Status,
			IP:          ku.IP,
			CreatedAt:   ku.CreatedAt,
			UpdatedAt:   ku.UpdatedAt,
		}

		var mergedFrom *string

		// Check if this email exists in moyu - merge moemoepoint
		if moyuUser, exists := moyuByEmail[email]; exists {
			newUser.Moemoepoint += moyuUser.Moemoepoint

			// Use moyu bio if kungal bio is empty
			if newUser.Bio == "" && moyuUser.Bio != "" {
				newUser.Bio = moyuUser.Bio
			}

			// Use moyu avatar if kungal avatar is empty
			if newUser.Avatar == "" && moyuUser.Avatar != "" {
				newUser.Avatar = moyuUser.Avatar
			}

			mergedFromStr := "moyu"
			mergedFrom = &mergedFromStr
			result.UsersMerged++
			slog.Debug("Merging user",
				"email", email,
				"kungal_moemoepoint", ku.Moemoepoint,
				"moyu_moemoepoint", moyuUser.Moemoepoint,
				"total", newUser.Moemoepoint)
		}

		// Handle duplicate names by appending suffix
		originalName := newUser.Name
		suffix := 1
		for processedNames[newUser.Name] {
			newUser.Name = fmt.Sprintf("%s_%d", originalName, suffix)
			suffix++
		}
		if newUser.Name != originalName {
			slog.Warn("Renamed duplicate name", "original", originalName, "new", newUser.Name)
		}

		if !dryRun {
			// Use transaction for user + migration record
			err := targetDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				// Create user
				if err := tx.Create(newUser).Error; err != nil {
					return fmt.Errorf("failed to create user: %w", err)
				}

				// Create migration record for kungal
				migration := &authModel.UserMigration{
					UserID:       newUser.ID,
					UserUUID:     newUser.UUID,
					SourceDB:     "kungal",
					SourceUserID: ku.ID,
					SourceEmail:  ku.Email,
					MergedFrom:   mergedFrom,
				}
				if err := tx.Create(migration).Error; err != nil {
					return fmt.Errorf("failed to create migration record: %w", err)
				}

				// If merged from moyu, also create moyu migration record
				if moyuUser, exists := moyuByEmail[email]; exists {
					moyuMigration := &authModel.UserMigration{
						UserID:       newUser.ID,
						UserUUID:     newUser.UUID,
						SourceDB:     "moyu",
						SourceUserID: moyuUser.ID,
						SourceEmail:  moyuUser.Email,
					}
					if err := tx.Create(moyuMigration).Error; err != nil {
						return fmt.Errorf("failed to create moyu migration record: %w", err)
					}
				}

				return nil
			})

			if err != nil {
				slog.Error("failed to migrate user", "email", email, "error", err)
				result.Errors++
				continue
			}
		}

		processedEmails[email] = true
		processedNames[newUser.Name] = true
		result.NewUsersCreated++
	}

	// Process moyu users that are not in kungal
	for _, mu := range moyuUsers {
		email := strings.ToLower(strings.TrimSpace(mu.Email))

		if processedEmails[email] {
			// Already processed via kungal or existing
			continue
		}

		newUser := &authModel.User{
			Name:        strings.TrimSpace(mu.Name),
			Email:       email,
			Password:    nil,
			Avatar:      mu.Avatar,
			Bio:         mu.Bio,
			Moemoepoint: mu.Moemoepoint,
			Status:      mu.Status,
			IP:          mu.IP,
			CreatedAt:   mu.CreatedAt,
			UpdatedAt:   mu.UpdatedAt,
		}

		// Handle duplicate names
		originalName := newUser.Name
		suffix := 1
		for processedNames[newUser.Name] {
			newUser.Name = fmt.Sprintf("%s_%d", originalName, suffix)
			suffix++
		}
		if newUser.Name != originalName {
			slog.Warn("Renamed duplicate name", "original", originalName, "new", newUser.Name)
		}

		if !dryRun {
			err := targetDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(newUser).Error; err != nil {
					return fmt.Errorf("failed to create user: %w", err)
				}

				migration := &authModel.UserMigration{
					UserID:       newUser.ID,
					UserUUID:     newUser.UUID,
					SourceDB:     "moyu",
					SourceUserID: mu.ID,
					SourceEmail:  mu.Email,
				}
				if err := tx.Create(migration).Error; err != nil {
					return fmt.Errorf("failed to create migration record: %w", err)
				}

				return nil
			})

			if err != nil {
				slog.Error("failed to migrate user", "email", email, "error", err)
				result.Errors++
				continue
			}
		}

		processedEmails[email] = true
		processedNames[newUser.Name] = true
		result.NewUsersCreated++
	}

	return result, nil
}

func printResults(r *MigrationResult, dryRun bool) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 50))
	if dryRun {
		fmt.Println("Migration Results (DRY RUN)")
	} else {
		fmt.Println("Migration Results")
	}
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Kungal users total:    %d\n", r.KungalUsersTotal)
	fmt.Printf("Moyu users total:      %d\n", r.MoyuUsersTotal)
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("New users created:     %d\n", r.NewUsersCreated)
	fmt.Printf("Users merged:          %d\n", r.UsersMerged)
	fmt.Printf("Skipped (existing):    %d\n", r.SkippedDuplicates)
	fmt.Printf("Errors:                %d\n", r.Errors)
	fmt.Println(strings.Repeat("=", 50))

	if !dryRun && r.NewUsersCreated > 0 {
		fmt.Println()
		fmt.Println("NOTE: All migrated users have NULL passwords.")
		fmt.Println("They must reset their password via email before logging in.")
	}
}
