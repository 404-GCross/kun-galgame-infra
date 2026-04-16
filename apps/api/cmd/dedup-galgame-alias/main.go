package main

import (
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to galgame wiki database", "error", err)
		os.Exit(1)
	}
	db := wikiDB.DB()

	// Count duplicates before dedup
	var totalBefore, uniqueBefore int64
	db.Raw("SELECT COUNT(*) FROM galgame_alias").Scan(&totalBefore)
	db.Raw("SELECT COUNT(DISTINCT (galgame_id, name)) FROM galgame_alias").Scan(&uniqueBefore)
	dupCount := totalBefore - uniqueBefore

	fmt.Printf("Before: total=%d, unique=%d, duplicates=%d\n", totalBefore, uniqueBefore, dupCount)

	if dupCount == 0 {
		fmt.Println("No duplicates found, nothing to do.")
		return
	}

	// Delete duplicates: keep the row with the smallest id for each (galgame_id, name) pair
	result := db.Exec(`
		DELETE FROM galgame_alias
		WHERE id NOT IN (
			SELECT MIN(id)
			FROM galgame_alias
			GROUP BY galgame_id, name
		)
	`)
	if result.Error != nil {
		slog.Error("failed to delete duplicates", "error", result.Error)
		os.Exit(1)
	}

	fmt.Printf("Deleted %d duplicate rows.\n", result.RowsAffected)

	// Add unique constraint to prevent future duplicates
	err = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_alias_unique ON galgame_alias (galgame_id, name)").Error
	if err != nil {
		slog.Error("failed to create unique index", "error", err)
		os.Exit(1)
	}

	fmt.Println("Created unique index idx_galgame_alias_unique(galgame_id, name).")

	// Verify
	var totalAfter int64
	db.Raw("SELECT COUNT(*) FROM galgame_alias").Scan(&totalAfter)
	fmt.Printf("After: total=%d\n", totalAfter)
}
