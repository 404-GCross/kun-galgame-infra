package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	authModel "api/internal/platform/auth/model"
	siteModel "api/internal/platform/site/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// Source database models
// ---------------------------------------------------------------------------

type KungalUser struct {
	ID                      uint      `gorm:"column:id"`
	Name                    string    `gorm:"column:name"`
	Email                   string    `gorm:"column:email"`
	Password                string    `gorm:"column:password"`
	Avatar                  string    `gorm:"column:avatar"`
	Bio                     string    `gorm:"column:bio"`
	Role                    int       `gorm:"column:role"`
	Status                  int       `gorm:"column:status"`
	Moemoepoint             int       `gorm:"column:moemoepoint"`
	IP                      string    `gorm:"column:ip"`
	DailyCheckIn            int       `gorm:"column:daily_check_in"`
	DailyImageCount         int       `gorm:"column:daily_image_count"`
	DailyToolsetUploadCount int       `gorm:"column:daily_toolset_upload_count"`
	CreatedAt               time.Time `gorm:"column:created"`
	UpdatedAt               time.Time `gorm:"column:updated"`
}

func (KungalUser) TableName() string { return "user" }

type MoyuUser struct {
	ID              uint      `gorm:"column:id"`
	Name            string    `gorm:"column:name"`
	Email           string    `gorm:"column:email"`
	Password        string    `gorm:"column:password"`
	Avatar          string    `gorm:"column:avatar"`
	Bio             string    `gorm:"column:bio"`
	Role            int       `gorm:"column:role"`
	Status          int       `gorm:"column:status"`
	Moemoepoint     int       `gorm:"column:moemoepoint"`
	IP              string    `gorm:"column:ip"`
	DailyCheckIn    int       `gorm:"column:daily_check_in"`
	DailyImageCount int       `gorm:"column:daily_image_count"`
	DailyUploadSize int       `gorm:"column:daily_upload_size"`
	LastLoginTime   string    `gorm:"column:last_login_time"`
	CreatedAt       time.Time `gorm:"column:created"`
	UpdatedAt       time.Time `gorm:"column:updated"`
}

func (MoyuUser) TableName() string { return "user" }

type MoyuFollowRelation struct {
	ID          uint `gorm:"column:id"`
	FollowerID  uint `gorm:"column:follower_id"`
	FollowingID uint `gorm:"column:following_id"`
}

func (MoyuFollowRelation) TableName() string { return "user_follow_relation" }

// sourceKey identifies a user in a source database
type sourceKey struct {
	db string
	id uint
}

// ---------------------------------------------------------------------------
// Merged user: unified representation before insertion
// ---------------------------------------------------------------------------

type mergedUser struct {
	// Core fields (written to users table)
	Name        string
	Email       string
	Avatar      string
	Bio         string
	Moemoepoint int
	Status      int
	IP          string
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Kungal source (nil if user is moyu-only)
	Kungal *KungalUser
	// Moyu source (nil if user is kungal-only)
	Moyu *MoyuUser
}

// ---------------------------------------------------------------------------
// Migration result
// ---------------------------------------------------------------------------

type MigrationResult struct {
	KungalUsersTotal  int
	MoyuUsersTotal    int
	NewUsersCreated   int
	UsersMerged       int
	SiteDataCreated   int
	FollowsMigrated   int
	FollowsSkipped    int
	RolesAssigned     int
	Errors            int
	SkippedDuplicates int
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	dryRun := flag.Bool("dry-run", false, "Perform a dry run without making changes")
	kungalDSN := flag.String("kungal-dsn", "", "Kungal database DSN (required)")
	moyuDSN := flag.String("moyu-dsn", "", "Moyu database DSN (required)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

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

	targetDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("failed to connect to target database", "error", err)
		os.Exit(1)
	}
	defer targetDB.Close()

	kungalDB, err := connectDB(*kungalDSN, "kungal")
	if err != nil {
		slog.Error("failed to connect to kungal database", "error", err)
		os.Exit(1)
	}

	moyuDB, err := connectDB(*moyuDSN, "moyu")
	if err != nil {
		slog.Error("failed to connect to moyu database", "error", err)
		os.Exit(1)
	}

	result, err := runMigration(context.Background(), targetDB.DB(), kungalDB, moyuDB, *dryRun)
	if err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	printResults(result, *dryRun)
}

func connectDB(dsn, name string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s database: %w", name, err)
	}
	slog.Info("Connected to database", "name", name)
	return db, nil
}

// ---------------------------------------------------------------------------
// Migration logic
// ---------------------------------------------------------------------------

func runMigration(ctx context.Context, targetDB, kungalDB, moyuDB *gorm.DB, dryRun bool) (*MigrationResult, error) {
	result := &MigrationResult{}

	// ── Pre-flight checks ────────────────────────────────────────────────
	if !targetDB.Migrator().HasTable(&authModel.UserMigration{}) {
		return nil, fmt.Errorf("user_migrations table not found — run 'make migrate' first")
	}

	kungalSiteID, err := findSiteID(ctx, targetDB, "www.kungal.com")
	if err != nil {
		return nil, fmt.Errorf("kungal site not found — run 'make migrate' first: %w", err)
	}
	moyuSiteID, err := findSiteID(ctx, targetDB, "www.moyu.moe")
	if err != nil {
		return nil, fmt.Errorf("moyu site not found — run 'make migrate' first: %w", err)
	}
	slog.Info("Site IDs resolved", "kungal", kungalSiteID, "moyu", moyuSiteID)

	// ── Step 1: Fetch source data ────────────────────────────────────────
	var kungalUsers []KungalUser
	if err := kungalDB.WithContext(ctx).Find(&kungalUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch kungal users: %w", err)
	}
	result.KungalUsersTotal = len(kungalUsers)
	slog.Info("Fetched kungal users", "count", len(kungalUsers))

	var moyuUsers []MoyuUser
	if err := moyuDB.WithContext(ctx).Find(&moyuUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch moyu users: %w", err)
	}
	result.MoyuUsersTotal = len(moyuUsers)
	slog.Info("Fetched moyu users", "count", len(moyuUsers))

	// ── Step 2: Merge into unified list by email ─────────────────────────
	// Build email → merged user map. Kungal takes priority for name/email/avatar/bio.
	mergedByEmail := make(map[string]*mergedUser, len(kungalUsers))

	for i := range kungalUsers {
		ku := &kungalUsers[i]
		email := strings.ToLower(strings.TrimSpace(ku.Email))
		if existing, ok := mergedByEmail[email]; ok {
			// Keep the earlier registration, skip duplicates within kungal
			slog.Warn("Duplicate email within kungal, keeping earlier",
				"email", email,
				"kept_id", existing.Kungal.ID,
				"skipped_id", ku.ID,
			)
			result.SkippedDuplicates++
			continue
		}
		mergedByEmail[email] = &mergedUser{
			Name:        strings.TrimSpace(ku.Name),
			Email:       email,
			Avatar:      ku.Avatar,
			Bio:         ku.Bio,
			Moemoepoint: ku.Moemoepoint,
			Status:      ku.Status,
			IP:          ku.IP,
			CreatedAt:   ku.CreatedAt,
			UpdatedAt:   ku.UpdatedAt,
			Kungal:      ku,
		}
	}

	for i := range moyuUsers {
		mu := &moyuUsers[i]
		email := strings.ToLower(strings.TrimSpace(mu.Email))

		if existing, ok := mergedByEmail[email]; ok {
			if existing.Moyu != nil {
				// Duplicate email within moyu, skip later one
				slog.Warn("Duplicate email within moyu, keeping earlier",
					"email", email,
					"kept_id", existing.Moyu.ID,
					"skipped_id", mu.ID,
				)
				result.SkippedDuplicates++
				continue
			}
			// Cross-db merge: kungal takes priority, moyu supplements
			existing.Moemoepoint += mu.Moemoepoint
			if existing.Bio == "" && mu.Bio != "" {
				existing.Bio = mu.Bio
			}
			if existing.Avatar == "" && mu.Avatar != "" {
				existing.Avatar = mu.Avatar
			}
			if mu.CreatedAt.Before(existing.CreatedAt) {
				existing.CreatedAt = mu.CreatedAt
			}
			existing.Moyu = mu
			result.UsersMerged++
		} else {
			// Moyu-only user
			mergedByEmail[email] = &mergedUser{
				Name:        strings.TrimSpace(mu.Name),
				Email:       email,
				Avatar:      mu.Avatar,
				Bio:         mu.Bio,
				Moemoepoint: mu.Moemoepoint,
				Status:      mu.Status,
				IP:          mu.IP,
				CreatedAt:   mu.CreatedAt,
				UpdatedAt:   mu.UpdatedAt,
				Moyu:        mu,
			}
		}
	}

	// ── Step 3: Sort by created_at (earliest first → smallest ID) ────────
	allMerged := make([]*mergedUser, 0, len(mergedByEmail))
	for _, m := range mergedByEmail {
		allMerged = append(allMerged, m)
	}
	sort.Slice(allMerged, func(i, j int) bool {
		return allMerged[i].CreatedAt.Before(allMerged[j].CreatedAt)
	})
	slog.Info("Merged and sorted users", "total", len(allMerged), "merged_count", result.UsersMerged)

	// Filter out already-existing target users
	existingEmails := make(map[string]bool)
	var existingUsers []authModel.User
	if err := targetDB.WithContext(ctx).Select("email").Find(&existingUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch existing users: %w", err)
	}
	for _, u := range existingUsers {
		existingEmails[strings.ToLower(u.Email)] = true
	}
	slog.Info("Found existing users in target", "count", len(existingUsers))

	// ── Step 4: Insert in chronological order ────────────────────────────
	sourceToNewID := make(map[sourceKey]uint)
	processedNames := make(map[string]bool)

	// Also track names from existing users
	var existingNames []authModel.User
	if err := targetDB.WithContext(ctx).Select("name").Find(&existingNames).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch existing user names: %w", err)
	}
	for _, u := range existingNames {
		processedNames[u.Name] = true
	}

	// Find the starting ID: max existing ID + 1, or 1 if empty
	var maxExistingID uint
	targetDB.WithContext(ctx).Model(&authModel.User{}).Select("COALESCE(MAX(id), 0)").Scan(&maxExistingID)
	nextID := maxExistingID + 1

	totalToProcess := len(allMerged)
	processed := 0
	for _, m := range allMerged {
		if existingEmails[m.Email] {
			result.SkippedDuplicates++
			processed++
			continue
		}

		name := deduplicateName(m.Name, processedNames)

		// Copy legacy passwords for transparent migration
		var kungalPwd, moyuPwd *string
		if ku := m.Kungal; ku != nil && ku.Password != "" {
			kungalPwd = &ku.Password
		}
		if mu := m.Moyu; mu != nil && mu.Password != "" {
			moyuPwd = &mu.Password
		}

		// Assign sequential ID in chronological order
		newUser := &authModel.User{
			ID:             nextID,
			Name:           name,
			Email:          m.Email,
			Password:       nil, // Will be set on first successful legacy login
			KungalPassword: kungalPwd,
			MoyuPassword:   moyuPwd,
			Avatar:         m.Avatar,
			Bio:            m.Bio,
			Moemoepoint:    m.Moemoepoint,
			Status:         m.Status,
			IP:             m.IP,
			CreatedAt:      m.CreatedAt,
			UpdatedAt:      m.UpdatedAt,
		}
		nextID++

		// Count site data for dry-run reporting
		if m.Kungal != nil {
			result.SiteDataCreated++
		}
		if m.Moyu != nil {
			result.SiteDataCreated++
		}

		if !dryRun {
			err := targetDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(newUser).Error; err != nil {
					return fmt.Errorf("create user: %w", err)
				}

				// Kungal site data
				if ku := m.Kungal; ku != nil {
					extra, _ := json.Marshal(map[string]any{
						"daily_toolset_upload_count": ku.DailyToolsetUploadCount,
					})
					if err := tx.Create(&authModel.UserSiteData{
						UserID:          newUser.ID,
						SiteID:          kungalSiteID,
						Role:            ku.Role,
						Status:          ku.Status,
						DailyCheckIn:    ku.DailyCheckIn,
						DailyImageCount: ku.DailyImageCount,
						Extra:           extra,
						CreatedAt:       ku.CreatedAt,
						UpdatedAt:       ku.UpdatedAt,
					}).Error; err != nil {
						return fmt.Errorf("create kungal site data: %w", err)
					}

					// Migration record
					mergedFrom := (*string)(nil)
					if m.Moyu != nil {
						s := "moyu"
						mergedFrom = &s
					}
					if err := tx.Create(&authModel.UserMigration{
						UserID:       newUser.ID,
						UserUUID:     newUser.UUID,
						SourceDB:     "kungal",
						SourceUserID: ku.ID,
						SourceEmail:  ku.Email,
						MergedFrom:   mergedFrom,
					}).Error; err != nil {
						return fmt.Errorf("create kungal migration record: %w", err)
					}
					sourceToNewID[sourceKey{"kungal", ku.ID}] = newUser.ID
				}

				// Moyu site data
				if mu := m.Moyu; mu != nil {
					extra, _ := json.Marshal(map[string]any{
						"daily_upload_size": mu.DailyUploadSize,
						"last_login_time":   mu.LastLoginTime,
					})
					if err := tx.Create(&authModel.UserSiteData{
						UserID:          newUser.ID,
						SiteID:          moyuSiteID,
						Role:            mu.Role,
						Status:          mu.Status,
						DailyCheckIn:    mu.DailyCheckIn,
						DailyImageCount: mu.DailyImageCount,
						Extra:           extra,
						CreatedAt:       mu.CreatedAt,
						UpdatedAt:       mu.UpdatedAt,
					}).Error; err != nil {
						return fmt.Errorf("create moyu site data: %w", err)
					}

					if err := tx.Create(&authModel.UserMigration{
						UserID:       newUser.ID,
						UserUUID:     newUser.UUID,
						SourceDB:     "moyu",
						SourceUserID: mu.ID,
						SourceEmail:  mu.Email,
					}).Error; err != nil {
						return fmt.Errorf("create moyu migration record: %w", err)
					}
					sourceToNewID[sourceKey{"moyu", mu.ID}] = newUser.ID
				}

				return nil
			})
			if err != nil {
				slog.Error("failed to migrate user", "email", m.Email, "error", err)
				result.Errors++
				result.SiteDataCreated-- // Rollback count on error
				if m.Kungal != nil {
					result.SiteDataCreated--
				}
				continue
			}
		}

		processedNames[name] = true
		result.NewUsersCreated++
		processed++
		if processed%1000 == 0 {
			slog.Info("Migration progress", "processed", processed, "total", totalToProcess, "created", result.NewUsersCreated, "errors", result.Errors)
		}
	}
	slog.Info("User insertion complete", "created", result.NewUsersCreated, "skipped", result.SkippedDuplicates, "errors", result.Errors)

	// Reset PostgreSQL auto-increment sequence to max ID
	if !dryRun && result.NewUsersCreated > 0 {
		if err := targetDB.WithContext(ctx).Exec(
			"SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT COALESCE(MAX(id), 1) FROM users))",
		).Error; err != nil {
			slog.Error("failed to reset users ID sequence", "error", err)
		} else {
			slog.Info("Reset users ID sequence", "next_id", nextID)
		}
	}

	// ── Step 5: Migrate social relations (moyu only) ─────────────────────
	var moyuFollows []MoyuFollowRelation
	if err := moyuDB.WithContext(ctx).Find(&moyuFollows).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch moyu follow relations: %w", err)
	}
	slog.Info("Fetched moyu follow relations", "count", len(moyuFollows))

	if !dryRun {
		for _, f := range moyuFollows {
			followerID, ok1 := sourceToNewID[sourceKey{"moyu", f.FollowerID}]
			followingID, ok2 := sourceToNewID[sourceKey{"moyu", f.FollowingID}]
			if !ok1 || !ok2 || followerID == followingID {
				result.FollowsSkipped++
				continue
			}
			if err := targetDB.WithContext(ctx).Create(&authModel.UserFollow{
				FollowerID:  followerID,
				FollowingID: followingID,
			}).Error; err != nil {
				if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "UNIQUE") {
					result.FollowsSkipped++
				} else {
					slog.Error("failed to migrate follow", "error", err)
					result.Errors++
				}
				continue
			}
			result.FollowsMigrated++
		}
	} else {
		result.FollowsMigrated = len(moyuFollows)
	}

	// ── Step 6: Map site-level roles → global roles (user_roles) ─────────
	roleIDs, err := findRoleIDs(ctx, targetDB)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup role IDs: %w", err)
	}
	slog.Info("Role IDs resolved", "admin", roleIDs["admin"], "moderator", roleIDs["moderator"])

	var allSiteData []authModel.UserSiteData
	if err := targetDB.WithContext(ctx).Find(&allSiteData).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch user site data: %w", err)
	}

	// 0=none, 1=moderator, 2=admin
	userMaxLevel := make(map[uint]int)
	for _, sd := range allSiteData {
		level := 0
		if sd.SiteID == kungalSiteID {
			switch sd.Role {
			case 3:
				level = 2
			case 2:
				level = 1
			}
		} else if sd.SiteID == moyuSiteID {
			switch sd.Role {
			case 4:
				level = 2
			case 3:
				level = 1
			}
		}
		if level > userMaxLevel[sd.UserID] {
			userMaxLevel[sd.UserID] = level
		}
	}

	if !dryRun {
		for userID, level := range userMaxLevel {
			if level == 0 {
				continue
			}
			roleName := "moderator"
			if level >= 2 {
				roleName = "admin"
			}
			roleID := roleIDs[roleName]
			if err := targetDB.WithContext(ctx).Exec(
				"INSERT INTO user_roles (user_id, role_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
				userID, roleID,
			).Error; err != nil {
				slog.Error("failed to assign role", "user_id", userID, "role", roleName, "error", err)
				result.Errors++
				continue
			}
			result.RolesAssigned++
		}
	} else {
		for _, level := range userMaxLevel {
			if level > 0 {
				result.RolesAssigned++
			}
		}
	}

	slog.Info("Role assignment complete", "assigned", result.RolesAssigned)

	// ── Step 7: Remap user IDs in source databases ───────────────────────
	// This updates user.id and all foreign keys in business tables so that
	// both source databases use the same IDs as the OAuth target database.
	if !dryRun && result.NewUsersCreated > 0 {
		slog.Info("Remapping user IDs in source databases...")

		kungalMapping := buildMapping(sourceToNewID, "kungal")
		if len(kungalMapping) > 0 {
			if err := remapKungalUserIDs(ctx, kungalDB, kungalMapping); err != nil {
				slog.Error("failed to remap kungal user IDs", "error", err)
				result.Errors++
			} else {
				slog.Info("Kungal user IDs remapped", "count", len(kungalMapping))
			}
		}

		moyuMapping := buildMapping(sourceToNewID, "moyu")
		if len(moyuMapping) > 0 {
			if err := remapMoyuUserIDs(ctx, moyuDB, moyuMapping); err != nil {
				slog.Error("failed to remap moyu user IDs", "error", err)
				result.Errors++
			} else {
				slog.Info("Moyu user IDs remapped", "count", len(moyuMapping))
			}
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func findRoleIDs(ctx context.Context, db *gorm.DB) (map[string]uint, error) {
	var roles []siteModel.Role
	if err := db.WithContext(ctx).Where("name IN ?", []string{"admin", "moderator"}).Find(&roles).Error; err != nil {
		return nil, err
	}
	result := make(map[string]uint, len(roles))
	for _, r := range roles {
		result[r.Name] = r.ID
	}
	if _, ok := result["admin"]; !ok {
		return nil, fmt.Errorf("'admin' role not found — run 'make migrate' first")
	}
	if _, ok := result["moderator"]; !ok {
		return nil, fmt.Errorf("'moderator' role not found — run 'make migrate' first")
	}
	return result, nil
}

func findSiteID(ctx context.Context, db *gorm.DB, domain string) (uint, error) {
	var site siteModel.Site
	if err := db.WithContext(ctx).Where("domain = ?", domain).First(&site).Error; err != nil {
		return 0, err
	}
	return site.ID, nil
}

func deduplicateName(name string, used map[string]bool) string {
	original := name
	suffix := 1
	for used[name] {
		name = fmt.Sprintf("%s_%d", original, suffix)
		suffix++
	}
	return name
}

// buildMapping extracts old_id → new_id pairs for a specific source database
func buildMapping(sourceToNewID map[sourceKey]uint, db string) map[uint]uint {
	m := make(map[uint]uint)
	for k, newID := range sourceToNewID {
		if k.db == db {
			m[k.id] = newID
		}
	}
	return m
}

// remapUserIDsGeneric applies old→new user ID mapping to a list of (table, column) pairs.
// It creates a temp mapping table, then uses UPDATE ... FROM to batch-remap all FKs,
// and finally updates user.id itself.
func remapUserIDsGeneric(ctx context.Context, db *gorm.DB, mapping map[uint]uint, fkColumns []tableColumn) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Collect all affected tables (unique) to disable/re-enable triggers
		tableSet := map[string]bool{`"user"`: true}
		for _, tc := range fkColumns {
			tableSet[fmt.Sprintf(`"%s"`, tc.table)] = true
		}

		// Check which tables actually exist in this database
		var existingTables []string
		tx.Raw("SELECT tablename FROM pg_tables WHERE schemaname = 'public'").Scan(&existingTables)
		existsMap := make(map[string]bool, len(existingTables))
		for _, t := range existingTables {
			existsMap[t] = true
		}

		// Filter to only existing tables
		activeTables := make([]string, 0, len(tableSet))
		for t := range tableSet {
			// Strip quotes for lookup
			name := strings.Trim(t, `"`)
			if existsMap[name] {
				activeTables = append(activeTables, t)
			}
		}

		// Disable FK triggers on all affected tables
		for _, t := range activeTables {
			if err := tx.Exec(fmt.Sprintf("ALTER TABLE %s DISABLE TRIGGER ALL", t)).Error; err != nil {
				return fmt.Errorf("disable triggers on %s: %w", t, err)
			}
		}

		// Re-enable triggers when done (even on error)
		defer func() {
			for _, t := range activeTables {
				_ = tx.Exec(fmt.Sprintf("ALTER TABLE %s ENABLE TRIGGER ALL", t)).Error
			}
		}()

		// Create temp mapping table
		if err := tx.Exec("CREATE TEMP TABLE _id_map (old_id INT PRIMARY KEY, new_id INT NOT NULL) ON COMMIT DROP").Error; err != nil {
			return fmt.Errorf("create temp table: %w", err)
		}

		// Batch insert mapping rows (1000 at a time to avoid query size limits)
		batch := make([]string, 0, 1000)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			sql := "INSERT INTO _id_map (old_id, new_id) VALUES " + strings.Join(batch, ",")
			if err := tx.Exec(sql).Error; err != nil {
				return fmt.Errorf("insert mapping batch: %w", err)
			}
			batch = batch[:0]
			return nil
		}
		for oldID, newID := range mapping {
			batch = append(batch, fmt.Sprintf("(%d,%d)", oldID, newID))
			if len(batch) >= 1000 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}

		// Two-pass remap to avoid unique constraint collisions:
		// Pass 1: shift all affected IDs to a temporary high range (+ offset)
		// Pass 2: set them to the final new IDs
		const offset = 100_000_000

		// Pass 1: old_id → old_id + offset (avoids collision with any real ID)
		for _, tc := range fkColumns {
			if !existsMap[tc.table] {
				continue
			}
			sql := fmt.Sprintf(
				`UPDATE "%s" SET "%s" = "%s"."%s" + %d FROM _id_map WHERE "%s"."%s" = _id_map.old_id`,
				tc.table, tc.column, tc.table, tc.column, offset, tc.table, tc.column,
			)
			if err := tx.Exec(sql).Error; err != nil {
				slog.Error("failed to remap FK (pass 1)", "table", tc.table, "column", tc.column, "error", err)
				return fmt.Errorf("remap pass1 %s.%s: %w", tc.table, tc.column, err)
			}
		}
		// Also shift user.id
		if err := tx.Exec(
			fmt.Sprintf(`UPDATE "user" SET id = id + %d FROM _id_map WHERE "user".id = _id_map.old_id`, offset),
		).Error; err != nil {
			return fmt.Errorf("remap pass1 user.id: %w", err)
		}

		// Pass 2: (old_id + offset) → new_id
		for _, tc := range fkColumns {
			if !existsMap[tc.table] {
				continue
			}
			sql := fmt.Sprintf(
				`UPDATE "%s" SET "%s" = _id_map.new_id FROM _id_map WHERE "%s"."%s" = _id_map.old_id + %d`,
				tc.table, tc.column, tc.table, tc.column, offset,
			)
			if err := tx.Exec(sql).Error; err != nil {
				slog.Error("failed to remap FK (pass 2)", "table", tc.table, "column", tc.column, "error", err)
				return fmt.Errorf("remap pass2 %s.%s: %w", tc.table, tc.column, err)
			}
		}
		// Final: set user.id to new value
		if err := tx.Exec(
			fmt.Sprintf(`UPDATE "user" SET id = _id_map.new_id FROM _id_map WHERE "user".id = _id_map.old_id + %d`, offset),
		).Error; err != nil {
			return fmt.Errorf("remap pass2 user.id: %w", err)
		}

		// Reset the user ID sequence
		if err := tx.Exec(
			`SELECT setval(pg_get_serial_sequence('"user"', 'id'), (SELECT COALESCE(MAX(id), 1) FROM "user"))`,
		).Error; err != nil {
			slog.Warn("failed to reset user ID sequence", "error", err)
		}

		return nil
	})
}

type tableColumn struct {
	table  string
	column string
}

func remapKungalUserIDs(ctx context.Context, db *gorm.DB, mapping map[uint]uint) error {
	fks := []tableColumn{
		// Chat
		{"chat_room", "last_message_sender_id"},
		{"chat_room_participant", "user_id"},
		{"chat_room_admin", "user_id"},
		{"chat_message", "sender_id"},
		{"chat_message", "receiver_id"},
		{"chat_message_read_by", "user_id"},
		{"chat_message_reaction", "user_id"},
		// Doc
		{"doc_article", "author_id"},
		// Galgame
		{"galgame", "user_id"},
		{"galgame_rating", "user_id"},
		{"galgame_rating_like", "user_id"},
		{"galgame_rating_comment", "user_id"},
		{"galgame_rating_comment", "target_user_id"},
		{"galgame_comment", "user_id"},
		{"galgame_comment", "target_user_id"},
		{"galgame_comment_like", "user_id"},
		{"galgame_contributor", "user_id"},
		{"galgame_like", "user_id"},
		{"galgame_favorite", "user_id"},
		{"galgame_history", "user_id"},
		{"galgame_link", "user_id"},
		{"galgame_pr", "user_id"},
		{"galgame_resource", "user_id"},
		{"galgame_resource_like", "user_id"},
		{"galgame_toolset", "user_id"},
		{"galgame_toolset_contributor", "user_id"},
		{"galgame_toolset_practicality", "user_id"},
		{"galgame_toolset_resource", "user_id"},
		{"galgame_toolset_comment", "user_id"},
		{"galgame_website", "user_id"},
		{"galgame_website_comment", "user_id"},
		{"galgame_website_like", "user_id"},
		{"galgame_website_favorite", "user_id"},
		// Message
		{"message", "sender_id"},
		{"message", "receiver_id"},
		{"system_message", "user_id"},
		// Topic
		{"topic", "user_id"},
		{"topic_comment", "user_id"},
		{"topic_comment", "target_user_id"},
		{"topic_comment_like", "user_id"},
		{"topic_poll", "user_id"},
		{"topic_poll_vote", "user_id"},
		{"topic_reply", "user_id"},
		{"topic_reply_like", "user_id"},
		{"topic_reply_dislike", "user_id"},
		{"topic_upvote", "user_id"},
		{"topic_like", "user_id"},
		{"topic_dislike", "user_id"},
		{"topic_favorite", "user_id"},
		// Admin
		{"todo", "user_id"},
		{"update_log", "user_id"},
		{"unmoe", "user_id"},
		// Social
		{"user_friend", "user_id"},
		{"user_friend", "friend_id"},
		{"user_follow", "follower_id"},
		{"user_follow", "followed_id"},
		// OAuth
		{"oauth_account", "user_id"},
	}
	return remapUserIDsGeneric(ctx, db, mapping, fks)
}

func remapMoyuUserIDs(ctx context.Context, db *gorm.DB, mapping map[uint]uint) error {
	fks := []tableColumn{
		// Chat
		{"chat_member", "user_id"},
		{"chat_message", "sender_id"},
		{"chat_message", "deleted_by_id"},
		{"chat_message_seen", "user_id"},
		{"chat_message_reaction", "user_id"},
		// Patch
		{"patch", "user_id"},
		{"patch_resource", "user_id"},
		{"patch_comment", "user_id"},
		// Admin
		{"admin_log", "user_id"},
		// Social
		{"user_follow_relation", "follower_id"},
		{"user_follow_relation", "following_id"},
		{"user_message", "sender_id"},
		{"user_message", "recipient_id"},
		// Relations
		{"user_patch_favorite_relation", "user_id"},
		{"user_patch_contribute_relation", "user_id"},
		{"user_patch_comment_like_relation", "user_id"},
		{"user_patch_resource_like_relation", "user_id"},
		// OAuth (if table exists)
		{"oauth_account", "user_id"},
	}
	return remapUserIDsGeneric(ctx, db, mapping, fks)
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
	fmt.Printf("Site data created:     %d\n", r.SiteDataCreated)
	fmt.Printf("Follows migrated:      %d\n", r.FollowsMigrated)
	fmt.Printf("Follows skipped:       %d\n", r.FollowsSkipped)
	fmt.Printf("Roles assigned:        %d\n", r.RolesAssigned)
	fmt.Printf("Skipped (existing):    %d\n", r.SkippedDuplicates)
	fmt.Printf("Errors:                %d\n", r.Errors)
	fmt.Println(strings.Repeat("=", 50))

	if !dryRun && r.NewUsersCreated > 0 {
		fmt.Println()
		fmt.Println("NOTE: All migrated users have NULL passwords.")
		fmt.Println("They must reset their password via email before logging in.")
		fmt.Println()
		fmt.Println("NOTE: User IDs are assigned in chronological order (earliest registration → smallest ID).")
	}
}
