package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/importer"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const otherStaffRoleID = 2

func main() {
	dsn := flag.String("dsn", "", "postgres DSN (required, always explicit)")
	apply := flag.Bool("apply", false, "write the moves (default: dry-run plan)")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "refine-staff-notes: --dsn is required")
		os.Exit(2)
	}

	db, err := database.OpenJobWith(*dsn, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		fatal(err)
	}

	roleName := map[int64]string{}
	{
		var rows []struct {
			ID     int64
			NameCN string
		}
		if err := db.Raw(`SELECT id, name_cn FROM catalog_role`).Scan(&rows).Error; err != nil {
			fatal(err)
		}
		for _, r := range rows {
			roleName[r.ID] = r.NameCN
		}
	}

	var distinct []struct{ Note string }
	if err := db.Raw(`SELECT DISTINCT lower(btrim(note)) AS note
		FROM catalog_credit WHERE role_id = ? AND btrim(note) <> ''`, otherStaffRoleID).
		Scan(&distinct).Error; err != nil {
		fatal(err)
	}
	resolved := map[string][]int64{}
	notes := make([]string, 0, len(distinct))
	for _, d := range distinct {
		roles := importer.RefineVNDBStaffRoles(otherStaffRoleID, d.Note)
		if len(roles) == 1 && roles[0] == otherStaffRoleID {
			continue
		}
		resolved[d.Note] = roles
		notes = append(notes, d.Note)
	}
	sort.Strings(notes)

	var totalMove, totalInsert, totalSkip int64
	for _, note := range notes {
		roles := resolved[note]
		primary, extras := roles[0], roles[1:]

		var inserted int64
		for _, extra := range extras {
			if *apply {
				res := db.Exec(`
					INSERT INTO catalog_credit
						(work_id, credit_name_id, label_id, role_id, character_id,
						 release_id, spoiler, note, source_id, created_at, updated_at)
					SELECT work_id, credit_name_id, label_id, ?, character_id,
					       release_id, spoiler, note, source_id, now(), now()
					FROM catalog_credit c
					WHERE c.role_id = ? AND lower(btrim(c.note)) = ?
					ON CONFLICT DO NOTHING`,
					extra, otherStaffRoleID, note)
				if res.Error != nil {
					fatal(res.Error)
				}
				inserted += res.RowsAffected
			} else {
				var n int64
				if err := db.Raw(`
					SELECT count(*) FROM catalog_credit c
					WHERE c.role_id = ? AND lower(btrim(c.note)) = ?
					  AND NOT EXISTS (
					    SELECT 1 FROM catalog_credit t
					    WHERE t.work_id = c.work_id AND t.credit_name_id = c.credit_name_id
					      AND t.role_id = ? AND COALESCE(t.character_id,0) = COALESCE(c.character_id,0))`,
					otherStaffRoleID, note, extra).Scan(&n).Error; err != nil {
					fatal(err)
				}
				inserted += n
			}
		}

		var movable, conflicted int64
		if err := db.Raw(`
			SELECT count(*) FILTER (WHERE NOT EXISTS (
			         SELECT 1 FROM catalog_credit t
			         WHERE t.work_id = c.work_id AND t.credit_name_id = c.credit_name_id
			           AND t.role_id = ? AND COALESCE(t.character_id,0) = COALESCE(c.character_id,0))),
			       count(*) FILTER (WHERE EXISTS (
			         SELECT 1 FROM catalog_credit t
			         WHERE t.work_id = c.work_id AND t.credit_name_id = c.credit_name_id
			           AND t.role_id = ? AND COALESCE(t.character_id,0) = COALESCE(c.character_id,0)))
			FROM catalog_credit c
			WHERE c.role_id = ? AND lower(btrim(c.note)) = ?`,
			primary, primary, otherStaffRoleID, note).Row().Scan(&movable, &conflicted); err != nil {
			fatal(err)
		}
		if movable == 0 && conflicted == 0 && inserted == 0 {
			continue
		}

		if *apply {
			res := db.Exec(`
				UPDATE catalog_credit c
				SET role_id = ?, updated_at = now()
				WHERE c.role_id = ? AND lower(btrim(c.note)) = ?
				  AND NOT EXISTS (
				    SELECT 1 FROM catalog_credit t
				    WHERE t.work_id = c.work_id AND t.credit_name_id = c.credit_name_id
				      AND t.role_id = ? AND COALESCE(t.character_id,0) = COALESCE(c.character_id,0))`,
				primary, otherStaffRoleID, note, primary)
			if res.Error != nil {
				fatal(res.Error)
			}
			movable = res.RowsAffected
		}

		names := make([]string, 0, len(roles))
		for _, r := range roles {
			names = append(names, roleName[r])
		}
		fmt.Printf("%-32q -> %-16s move=%-6d insert=%-5d skip_conflict=%d\n",
			note, strings.Join(names, "+"), movable, inserted, conflicted)
		totalMove += movable
		totalInsert += inserted
		totalSkip += conflicted
	}

	mode := "DRY-RUN (plan only)"
	if *apply {
		mode = "APPLIED"
	}
	fmt.Printf("\n%s: %d edges moved, %d composite copies inserted, %d left in 其他 (target credit already exists)\n",
		mode, totalMove, totalInsert, totalSkip)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "refine-staff-notes:", err)
	os.Exit(1)
}
