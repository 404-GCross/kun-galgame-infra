// expand-bgm-type4-gated gates the UNANCHORED Bangumi type=4 (game) subjects
// through a precision-first three-way OR signal gate (PC-platform galgame genre
// / explicit galgame-classification tag / cross-source title match against the
// erogamespace + dlsite-game + VNDB-ja corpora), with a console/mobile-exclusive
// exclusion (主机/手游排除), and creates a BODYLESS medium=galgame catalog_work +
// an EXACT Bangumi anchor (rule:bgm-type4-gated) + title rows + an imported
// revision per gated subject. A subject whose normalized title collides with an
// EXISTING work title is SKIPPED (a reconcile candidate, not a creation one).
//
//	go run ./cmd/expand-bgm-type4-gated --dsn '…rehearsal…'                 # dry-run survey (default)
//	go run ./cmd/expand-bgm-type4-gated --dsn '…rehearsal…' --apply         # write (×2 = idempotent)
//	go run ./cmd/expand-bgm-type4-gated --dsn '…' --sample-out ./out        # dump random-100 + collisions TSV
//
// The catalog --dsn is REQUIRED and never guessed (the 56a discipline — a bare
// run must not touch a live DB). The erogamespace and DLsite staging DSNs default
// to the same host as --dsn with the dbname swapped (override via --eg-dsn /
// --dlsite-dsn); src_vndb lives in the catalog db itself.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"api/internal/platform/catalog/importer"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; rehearsal locally, live only in the acceptance run)")
	egDSN := flag.String("eg-dsn", "", "erogamespace staging DSN (default: same host as --dsn, dbname=erogamespace)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "dlsite staging DSN (default: same host as --dsn, dbname=dlsite)")
	apply := flag.Bool("apply", false, "write (default: dry-run — survey + samples, nothing written)")
	limit := flag.Int("limit", 0, "cap works actually minted (0 = all); the survey is always full-pool")
	sampleOut := flag.String("sample-out", "", "directory to write random100.tsv + collisions.tsv")
	flag.Parse()

	logger.Init("development")
	if *dsn == "" {
		slog.Error("--dsn is required (refusing to guess the catalog DB — pass the rehearsal copy locally)")
		os.Exit(1)
	}

	catalogDB, err := openDB(*dsn)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	egDB, err := openDB(deriveDSN(*egDSN, *dsn, "erogamespace"))
	if err != nil {
		slog.Error("erogamespace db connect", "error", err)
		os.Exit(1)
	}
	dlsiteDB, err := openDB(deriveDSN(*dlsiteDSN, *dsn, "dlsite"))
	if err != nil {
		slog.Error("dlsite db connect", "error", err)
		os.Exit(1)
	}

	im := importer.New(catalogDB, egDB, importer.Options{DryRun: !*apply, Limit: *limit})
	st, err := im.RunBgmType4Gated(dlsiteDB)
	if err != nil {
		slog.Error("bgm-type4-gated failed", "error", err)
		os.Exit(1)
	}
	slog.Info("bgm-type4-gated summary",
		"pool_total", st.PoolTotal, "excluded_console_mobile", st.ExcludedConsoleMobile, "eligible_pool", st.EligiblePool,
		"sig_p", st.SigP, "sig_t", st.SigT, "sig_x", st.SigX, "gated_total", st.GatedTotal,
		"skipped_title_collision", st.SkippedTitleCollision, "skipped_intra_collision", st.SkippedIntraCollision,
		"to_create", st.ToCreate, "works_created", st.WorksCreated, "titles_created", st.TitlesCreated,
		"anchors_created", st.AnchorsCreated, "revisions_created", st.RevisionsCreated)
	slog.Info("bgm-type4-gated overlap matrix",
		"p_and_t", st.PT, "p_and_x", st.PX, "t_and_x", st.TX, "all_three", st.All3,
		"p_only", st.POnly, "t_only", st.TOnly, "x_only", st.XOnly)

	if *sampleOut != "" {
		if err := writeSamples(*sampleOut, &st); err != nil {
			slog.Error("write samples", "error", err)
			os.Exit(1)
		}
		slog.Info("samples written", "dir", *sampleOut, "random", len(st.RandomSample), "collisions", len(st.CollisionSamples))
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}

func openDB(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

var dbNameRe = regexp.MustCompile(`\bdbname=\S+`)

// deriveDSN returns override when set, else the base DSN with its dbname swapped
// (eg/dlsite always sit on the same server as the catalog db).
func deriveDSN(override, base, dbName string) string {
	if override != "" {
		return override
	}
	if dbNameRe.MatchString(base) {
		return dbNameRe.ReplaceAllString(base, "dbname="+dbName)
	}
	return strings.TrimSpace(base) + " dbname=" + dbName
}

// writeSamples dumps the reviewer's random-100 and the collision samples as TSV.
func writeSamples(dir string, st *importer.BgmGatedStats) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var rb strings.Builder
	rb.WriteString("subject_id\tname\tname_cn\tsignals\n")
	for _, s := range st.RandomSample {
		fmt.Fprintf(&rb, "%d\t%s\t%s\t%s\n", s.SubjectID, tsv(s.Name), tsv(s.NameCN), s.Signals)
	}
	if err := os.WriteFile(filepath.Join(dir, "random100.tsv"), []byte(rb.String()), 0o644); err != nil {
		return err
	}
	var cb strings.Builder
	cb.WriteString("subject_id\tname\tname_cn\tcollided_norm\texisting_work_id\texisting_title\n")
	for _, c := range st.CollisionSamples {
		fmt.Fprintf(&cb, "%d\t%s\t%s\t%s\t%d\t%s\n", c.SubjectID, tsv(c.Name), tsv(c.NameCN), tsv(c.CollidedNorm), c.WorkID, tsv(c.WorkTitle))
	}
	return os.WriteFile(filepath.Join(dir, "collisions.tsv"), []byte(cb.String()), 0o644)
}

// tsv strips tab/newline so a title never breaks the TSV row.
func tsv(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
