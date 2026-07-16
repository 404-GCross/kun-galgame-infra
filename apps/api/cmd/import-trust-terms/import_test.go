package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api/internal/platform/trust/dbtest"
	"api/internal/platform/trust/migrate"
	"api/internal/platform/trust/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres (the trust convention):
// TEST_DATABASE_DSN or a local default; a missing database skips the whole
// package. Schema comes from migrate.Run — the exact production migration.
// The shared trust suite advisory lock serializes this package against the
// migrate/service/handler packages that also truncate trust_term.

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_trust_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	sqlDB, _ := db.DB()
	release := dbtest.AcquireSuiteLock(sqlDB)

	if err := migrate.Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: trust migration failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

// cleanTerms truncates trust_term so each probe starts fresh.
func cleanTerms(t *testing.T) {
	t.Helper()
	if err := testDB.Exec("TRUNCATE trust_term RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate trust_term: %v", err)
	}
}

// writeFixture writes content to a temp file and returns its path. Content is a
// byte slice so fixtures can embed invalid UTF-8.
func writeFixture(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return p
}

// countActive counts active term rows in a scope (site "" = global/NULL).
func countActive(t *testing.T, site string) int64 {
	t.Helper()
	q := testDB.Model(&model.TrustTerm{}).Where("is_deprecated = false")
	if site == "" {
		q = q.Where("site IS NULL")
	} else {
		q = q.Where("site = ?", site)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		t.Fatalf("count active: %v", err)
	}
	return n
}

// Fixture invisible code points (built from rune values so no literal zero-width
// byte sits in the source).
var (
	zwsp = string(rune(0x200B)) // ZERO WIDTH SPACE (Cf, stripped by Normalize)
	fwd  = "ＢＡＤ"                // fullwidth BAD -> "bad" under NFKC
)

// --- pure pipeline (no DB) -------------------------------------------------

// TestProcessFileCounters pins the per-line pipeline classification: blank,
// comment, invalid UTF-8, short-filter, and the full-width / zero-width
// post-Normalize dedup collapsing to one insert.
func TestProcessFileCounters(t *testing.T) {
	// Lines: a real term, a fullwidth variant of it (dup after norm), a
	// zero-width-obfuscated variant (dup after norm), a blank, a comment, a
	// short term (2 runes < 3), a whitespace-only line, and an invalid-UTF-8
	// line — built as bytes so the 0xFF is preserved verbatim.
	var buf bytes.Buffer
	buf.WriteString("hello\n")                      // insert -> "hello"
	buf.WriteString(fwd + "\n")                     // "bad" (novel) -> insert
	buf.WriteString("bad\n")                        // dup-in-batch of "bad"
	buf.WriteString("he" + zwsp + "llo\n")          // dup-in-batch of "hello"
	buf.WriteString("\n")                           // blank
	buf.WriteString("# a comment\n")                // comment
	buf.WriteString("ab\n")                         // short (2 runes)
	buf.WriteString("   \n")                        // whitespace-only -> blank
	buf.Write([]byte{0x66, 0x6f, 0xff, 0x6f, '\n'}) // "fo\xffo" invalid UTF-8

	cfg := importConfig{site: nil, kind: model.TermKindSuspect, minRunes: 3}
	seen := map[string]struct{}{}
	existing := map[string]struct{}{}
	stats, terms, err := processFile("fix.txt", &buf, cfg, seen, existing)
	if err != nil {
		t.Fatalf("processFile: %v", err)
	}

	if stats.read != 9 {
		t.Errorf("read = %d, want 9", stats.read)
	}
	if stats.blankComment != 3 {
		t.Errorf("blank+comment = %d, want 3 (blank, comment, whitespace-only)", stats.blankComment)
	}
	if stats.invalidUTF8 != 1 {
		t.Errorf("invalid-utf8 = %d, want 1", stats.invalidUTF8)
	}
	if stats.shortFiltered != 1 {
		t.Errorf("short-filtered = %d, want 1", stats.shortFiltered)
	}
	if stats.dupInBatch != 2 {
		t.Errorf("dup-in-batch = %d, want 2 (bad, hello collapsed by norm)", stats.dupInBatch)
	}
	if stats.inserted != 2 || len(terms) != 2 {
		t.Fatalf("inserted = %d / terms = %d, want 2 (hello, bad)", stats.inserted, len(terms))
	}
	got := map[string]bool{terms[0].TermNorm: true, terms[1].TermNorm: true}
	if !got["hello"] || !got["bad"] {
		t.Errorf("inserted norms = %v, want {hello, bad}", got)
	}
	// Kind, note, site, deprecation on the built rows.
	for _, tm := range terms {
		if tm.Kind != model.TermKindSuspect {
			t.Errorf("kind = %d, want suspect", tm.Kind)
		}
		if tm.Site != nil {
			t.Errorf("site = %v, want nil (global)", tm.Site)
		}
		if tm.IsDeprecated {
			t.Errorf("is_deprecated = true, want false")
		}
		if tm.Note == nil || *tm.Note != "fix.txt" {
			t.Errorf("note = %v, want fix.txt (default per-file)", tm.Note)
		}
	}
}

// TestProcessFileDedupAcrossFiles pins that the shared seen set makes a term
// repeated in a SECOND file an in-batch duplicate (all files share the one
// -site scope).
func TestProcessFileDedupAcrossFiles(t *testing.T) {
	cfg := importConfig{minRunes: 3}
	seen := map[string]struct{}{}
	existing := map[string]struct{}{}

	a := bytes.NewBufferString("alpha\nbeta\n")
	sa, ta, _ := processFile("a.txt", a, cfg, seen, existing)
	if sa.inserted != 2 || len(ta) != 2 {
		t.Fatalf("file a inserted = %d, want 2", sa.inserted)
	}
	b := bytes.NewBufferString("beta\ngamma\n") // beta repeats file a
	sb, tb, _ := processFile("b.txt", b, cfg, seen, existing)
	if sb.dupInBatch != 1 {
		t.Errorf("file b dup-in-batch = %d, want 1 (beta)", sb.dupInBatch)
	}
	if sb.inserted != 1 || len(tb) != 1 || tb[0].TermNorm != "gamma" {
		t.Fatalf("file b inserted = %d (%v), want 1 (gamma)", sb.inserted, tb)
	}
	// Each file's note is its own filename.
	if ta[0].Note == nil || *ta[0].Note != "a.txt" || tb[0].Note == nil || *tb[0].Note != "b.txt" {
		t.Errorf("per-file notes wrong: a=%v b=%v", ta[0].Note, tb[0].Note)
	}
}

// TestProcessFileExistingSkip pins that a term already in the DB set is counted
// dup-existing and never re-inserted.
func TestProcessFileExistingSkip(t *testing.T) {
	cfg := importConfig{minRunes: 3}
	seen := map[string]struct{}{}
	existing := map[string]struct{}{"known": {}}

	buf := bytes.NewBufferString("known\nfresh\n")
	stats, terms, _ := processFile("f.txt", buf, cfg, seen, existing)
	if stats.dupExisting != 1 {
		t.Errorf("dup-existing = %d, want 1", stats.dupExisting)
	}
	if stats.inserted != 1 || len(terms) != 1 || terms[0].TermNorm != "fresh" {
		t.Fatalf("inserted = %d (%v), want 1 (fresh)", stats.inserted, terms)
	}
}

// TestProcessFileFixedNote pins that a set -note overrides the per-file default.
func TestProcessFileFixedNote(t *testing.T) {
	cfg := importConfig{minRunes: 3, note: "konsheng MIT"}
	_, terms, _ := processFile("porn.txt", bytes.NewBufferString("word one\n"), cfg, map[string]struct{}{}, map[string]struct{}{})
	if len(terms) != 1 || terms[0].Note == nil || *terms[0].Note != "konsheng MIT" {
		t.Fatalf("note = %v, want fixed 'konsheng MIT'", terms[0].Note)
	}
}

// --- DB-backed: dry-run / apply / idempotency ------------------------------

// TestRunDryRunWritesNothing pins that a dry-run reports a would-insert count
// but writes zero rows.
func TestRunDryRunWritesNothing(t *testing.T) {
	cleanTerms(t)
	fx := writeFixture(t, "terms.txt", []byte("alpha\nbeta\ngamma\n"))

	var out bytes.Buffer
	res, err := run(testDB, &out, importConfig{minRunes: 3, apply: false}, []string{fx})
	if err != nil {
		t.Fatalf("run dry: %v", err)
	}
	if res.total.inserted != 3 {
		t.Errorf("would-insert = %d, want 3", res.total.inserted)
	}
	if n := countActive(t, ""); n != 0 {
		t.Fatalf("dry-run wrote %d rows, want 0", n)
	}
	if !strings.Contains(out.String(), "would-insert") {
		t.Errorf("dry-run output missing 'would-insert':\n%s", out.String())
	}
	if !strings.Contains(out.String(), "samples (raw -> norm)") {
		t.Errorf("dry-run output missing samples preview:\n%s", out.String())
	}
}

// TestRunApplyWritesCorrectly pins that -apply inserts the rows with the right
// site (NULL), kind, note (filename default), and is_deprecated=false.
func TestRunApplyWritesCorrectly(t *testing.T) {
	cleanTerms(t)
	// A fullwidth + zero-width variant collapse to one; a short + comment drop.
	content := []byte("keyword\n" + fwd + "\n" + "he" + zwsp + "llo\n" + "hello\n" + "ab\n" + "# note\n")
	fx := writeFixture(t, "lexicon.txt", content)

	var out bytes.Buffer
	res, err := run(testDB, &out, importConfig{minRunes: 3, kind: model.TermKindBanned, apply: true}, []string{fx})
	if err != nil {
		t.Fatalf("run apply: %v", err)
	}
	// keyword, bad (fullwidth), hello (fullwidth+zw collapse to 1) = 3 inserts.
	if res.total.inserted != 3 {
		t.Fatalf("inserted = %d, want 3", res.total.inserted)
	}
	if n := countActive(t, ""); n != 3 {
		t.Fatalf("db active rows = %d, want 3", n)
	}

	var rows []model.TrustTerm
	if err := testDB.Where("is_deprecated = false").Order("term_norm").Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	norms := []string{}
	for _, r := range rows {
		norms = append(norms, r.TermNorm)
		if r.Site != nil {
			t.Errorf("row %q site = %v, want NULL (global)", r.TermNorm, r.Site)
		}
		if r.Kind != model.TermKindBanned {
			t.Errorf("row %q kind = %d, want banned", r.TermNorm, r.Kind)
		}
		if r.IsDeprecated {
			t.Errorf("row %q is_deprecated = true, want false", r.TermNorm)
		}
		if r.Note == nil || *r.Note != "lexicon.txt" {
			t.Errorf("row %q note = %v, want lexicon.txt", r.TermNorm, r.Note)
		}
	}
	want := []string{"bad", "hello", "keyword"}
	if strings.Join(norms, ",") != strings.Join(want, ",") {
		t.Fatalf("stored norms = %v, want %v", norms, want)
	}
	if !strings.Contains(out.String(), "inserted") {
		t.Errorf("apply output missing 'inserted':\n%s", out.String())
	}
}

// TestRunApplyIdempotent pins the core idempotency contract: a second -apply run
// of the same files finds every term already active → inserts 0.
func TestRunApplyIdempotent(t *testing.T) {
	cleanTerms(t)
	fx := writeFixture(t, "terms.txt", []byte("alpha\nbeta\ngamma\n"))

	var out1 bytes.Buffer
	res1, err := run(testDB, &out1, importConfig{minRunes: 3, apply: true}, []string{fx})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if res1.total.inserted != 3 {
		t.Fatalf("first run inserted = %d, want 3", res1.total.inserted)
	}

	var out2 bytes.Buffer
	res2, err := run(testDB, &out2, importConfig{minRunes: 3, apply: true}, []string{fx})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if res2.total.inserted != 0 {
		t.Errorf("second run inserted = %d, want 0 (idempotent)", res2.total.inserted)
	}
	if res2.total.dupExisting != 3 {
		t.Errorf("second run dup-existing = %d, want 3", res2.total.dupExisting)
	}
	if n := countActive(t, ""); n != 3 {
		t.Fatalf("db active rows after re-run = %d, want 3 (no duplicates)", n)
	}
}

// TestRunSiteScoped pins that -site stores a non-NULL scope and that an
// existing GLOBAL term with the same norm does NOT shadow the per-site insert
// (different keyspace).
func TestRunSiteScoped(t *testing.T) {
	cleanTerms(t)
	// Seed a global term "shared".
	if err := testDB.Create(&model.TrustTerm{TermNorm: "shared", Kind: model.TermKindSuspect}).Error; err != nil {
		t.Fatalf("seed global: %v", err)
	}
	fx := writeFixture(t, "kungal.txt", []byte("shared\nkungalonly\n"))

	site := "kungal"
	res, err := run(testDB, new(bytes.Buffer), importConfig{site: &site, minRunes: 3, apply: true}, []string{fx})
	if err != nil {
		t.Fatalf("run site: %v", err)
	}
	// Global "shared" is a different scope, so the per-site "shared" is fresh.
	if res.total.inserted != 2 {
		t.Fatalf("inserted = %d, want 2 (per-site shared + kungalonly)", res.total.inserted)
	}
	if n := countActive(t, "kungal"); n != 2 {
		t.Fatalf("kungal active rows = %d, want 2", n)
	}
	if n := countActive(t, ""); n != 1 {
		t.Fatalf("global active rows = %d, want 1 (untouched)", n)
	}
}
