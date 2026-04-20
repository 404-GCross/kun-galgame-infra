// cleanup-bogus-vndb-id removes galgame rows whose vndb_id numeric part
// exceeds VNDB's actual max VN id (fetched live from the API).
//
// These are typically garbage strings imported from moyu — users entered
// arbitrary numbers into the vndb_id field when adding entries.
//
// Default: report only (safe). Pass --delete to actually delete.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

const vndbAPI = "https://api.vndb.org/kana"

// Tables that reference galgame.id and must be purged first (FK integrity).
var relatedTables = []string{
	"galgame_tag_relation",
	"galgame_official_relation",
	"galgame_engine_relation",
	"galgame_alias",
	"galgame_contributor",
	"galgame_link",
	"galgame_history",
	"galgame_revision",
	"galgame_pr",
}

// ---------------------------------------------------------------------------
// VNDB: fetch current max VN id
// ---------------------------------------------------------------------------

type vndbRequest struct {
	Fields  string `json:"fields"`
	Sort    string `json:"sort"`
	Reverse bool   `json:"reverse"`
	Results int    `json:"results"`
}

type vndbResponse struct {
	Results []struct {
		ID string `json:"id"`
	} `json:"results"`
}

func fetchVNDBMaxID() (int, error) {
	req := vndbRequest{Fields: "id", Sort: "id", Reverse: true, Results: 1}
	body, err := json.Marshal(req)
	if err != nil {
		return 0, err
	}

	httpReq, err := http.NewRequest("POST", vndbAPI+"/vn", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("VNDB API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result vndbResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, err
	}
	if len(result.Results) == 0 {
		return 0, fmt.Errorf("no results")
	}

	id := result.Results[0].ID
	if !strings.HasPrefix(id, "v") {
		return 0, fmt.Errorf("unexpected ID format: %s", id)
	}
	return strconv.Atoi(id[1:])
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

type bogusRow struct {
	ID       int    `json:"id"`
	VNDBID   string `json:"vndb_id"`
	NameJaJP string `json:"name_ja_jp"`
	NameEnUS string `json:"name_en_us"`
	NameZhCN string `json:"name_zh_cn"`
}

func main() {
	doDelete := flag.Bool("delete", false, "Actually delete bogus rows (default: report only)")
	sampleSize := flag.Int("sample", 30, "How many rows to print as a sample")
	flag.Parse()

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

	// 1. Fetch VNDB max
	vndbMax, err := fetchVNDBMaxID()
	if err != nil {
		slog.Error("failed to fetch VNDB max id", "error", err)
		os.Exit(1)
	}
	fmt.Printf("VNDB max VN id: v%d\n", vndbMax)

	// 2. Find bogus rows
	var rows []bogusRow
	if err := db.Raw(`
		SELECT id, vndb_id, name_ja_jp, name_en_us, name_zh_cn
		FROM galgame
		WHERE vndb_id ~ '^v[0-9]+$'
		  AND CAST(SUBSTRING(vndb_id FROM 2) AS INTEGER) > ?
		ORDER BY CAST(SUBSTRING(vndb_id FROM 2) AS INTEGER) DESC
	`, vndbMax).Scan(&rows).Error; err != nil {
		slog.Error("failed to query bogus rows", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Bogus rows found: %d\n", len(rows))
	if len(rows) == 0 {
		fmt.Println("Nothing to clean up.")
		return
	}

	// 3. Show sample
	fmt.Println("\nSample (most egregious first):")
	limit := min(*sampleSize, len(rows))
	for i := range limit {
		r := rows[i]
		name := firstNonEmpty(r.NameZhCN, r.NameJaJP, r.NameEnUS)
		if name == "" {
			name = "(no name)"
		}
		fmt.Printf("  id=%-6d vndb_id=%-12s %s\n", r.ID, r.VNDBID, name)
	}
	if len(rows) > limit {
		fmt.Printf("  ... and %d more\n", len(rows)-limit)
	}

	// 4. Stop here unless --delete
	if !*doDelete {
		fmt.Println("\n[dry-run] Pass --delete to actually remove these rows + their relations.")
		return
	}

	// 5. Confirm and delete
	ids := make([]int, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}

	fmt.Printf("\n[delete] Removing %d galgame rows and their relations...\n", len(ids))
	if err := deleteAll(db, ids); err != nil {
		slog.Error("delete failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("Done.")
}

func deleteAll(db *gorm.DB, ids []int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// Delete relations first (FK integrity). If any table doesn't exist in
		// this DB, the error surfaces explicitly — safer than silent skip.
		for _, table := range relatedTables {
			res := tx.Exec(
				fmt.Sprintf("DELETE FROM %s WHERE galgame_id IN ?", table),
				ids,
			)
			if res.Error != nil {
				return fmt.Errorf("delete from %s: %w", table, res.Error)
			}
			if res.RowsAffected > 0 {
				fmt.Printf("  %-30s deleted %d rows\n", table, res.RowsAffected)
			}
		}

		// Finally, the galgame rows themselves
		res := tx.Exec("DELETE FROM galgame WHERE id IN ?", ids)
		if res.Error != nil {
			return fmt.Errorf("delete galgame: %w", res.Error)
		}
		fmt.Printf("  %-30s deleted %d rows\n", "galgame", res.RowsAffected)
		return nil
	})
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
