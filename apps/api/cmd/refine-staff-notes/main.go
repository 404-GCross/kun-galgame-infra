// refine-staff-notes is the backfill half of the staff-note refinement wave:
// it moves catalog_credit edges out of the 其他 bucket (role 2) onto the real
// role their VNDB note names, using the SAME note→role table the VNDB credits
// importer now applies at plan time (importer/staffnotes.go — one table, two
// writers, no drift). Only the role_id moves; note, source and ids stay, so
// provenance is intact and the move is reversible by note.
//
// A row whose target credit already exists (same work, name, character —
// typically Bangumi credited the classified role first) is SKIPPED, not
// deleted: the doc-10 unique index would reject the move, the read faces
// already fold the duplicate away, and a re-import plans the refined role so
// the skipped row is never re-minted either way.
//
// Discipline (QA-track charter): the DSN is ALWAYS explicit via --dsn; dry-run
// is the DEFAULT and prints the per-note move/skip plan; --apply writes.
//
//	go run ./cmd/refine-staff-notes --dsn '<dsn>'            # dry-run plan
//	go run ./cmd/refine-staff-notes --dsn '<dsn>' --apply    # move the edges
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"api/internal/platform/catalog/importer"

	"gorm.io/driver/postgres"
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

	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		fatal(err)
	}

	table := importer.StaffNoteRoleTable()
	notes := make([]string, 0, len(table))
	for note := range table {
		notes = append(notes, note)
	}
	sort.Strings(notes) // deterministic order: same-target notes resolve conflicts identically across runs

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

	var totalMove, totalSkip int64
	for _, note := range notes {
		target := table[note]
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
			target, target, otherStaffRoleID, note).Row().Scan(&movable, &conflicted); err != nil {
			fatal(err)
		}
		if movable == 0 && conflicted == 0 {
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
				target, otherStaffRoleID, note, target)
			if res.Error != nil {
				fatal(res.Error)
			}
			movable = res.RowsAffected
		}

		fmt.Printf("%-26q -> %3d %-8s move=%-6d skip_conflict=%d\n",
			note, target, roleName[target], movable, conflicted)
		totalMove += movable
		totalSkip += conflicted
	}

	mode := "DRY-RUN (plan only)"
	if *apply {
		mode = "APPLIED"
	}
	fmt.Printf("\n%s: %d edges moved, %d left in 其他 (target credit already exists)\n",
		mode, totalMove, totalSkip)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "refine-staff-notes:", err)
	os.Exit(1)
}
