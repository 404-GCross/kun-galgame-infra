package srcvndb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// EnsureSchema creates/updates everything the Silver layer owns: the src_vndb
// schema plus the staging + output tables. Idempotent.
func EnsureSchema(db *gorm.DB) error {
	if err := db.Exec(`CREATE SCHEMA IF NOT EXISTS src_vndb`).Error; err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	// Step-72 chars widening: the pre-72 table staged only a column subset, and
	// AutoMigrate cannot add NOT NULL columns to a populated table. The schema
	// is fully-rebuildable staging, so the upgrade is drop+recreate — the next
	// ingest reloads chars wholesale. One-shot: never fires on a current table.
	m := db.Migrator()
	if m.HasTable(&Char{}) && !m.HasColumn(&Char{}, "bloodt") {
		if err := m.DropTable(&Char{}); err != nil {
			return fmt.Errorf("drop pre-72 chars: %w", err)
		}
	}
	// Step-91 vn widening: same pattern — the pre-91 table staged 5 columns and
	// AutoMigrate cannot add NOT NULL columns to a populated table. Staging is
	// fully rebuildable; the next ingest reloads vn wholesale.
	if m.HasTable(&VN{}) && !m.HasColumn(&VN{}, "c_length") {
		if err := m.DropTable(&VN{}); err != nil {
			return fmt.Errorf("drop pre-91 vn: %w", err)
		}
	}
	if err := db.AutoMigrate(
		&VN{}, &VNRelation{}, &Char{}, &CharName{}, &CharVN{}, &Image{},
		&Staff{}, &StaffAlias{}, &VNStaff{}, &VNSeiyuu{},
		&Trait{}, &TraitParent{}, &CharTrait{}, &Tag{}, &TagParent{}, &TagVN{},
		&Release{}, &ReleaseVN{}, &ReleaseProducer{}, &ReleasePlatform{}, &ReleaseTitle{}, &Producer{},
		&Extlink{}, &ReleaseExtlink{},
		&VNExtlink{}, &ProducerExtlink{}, &StaffExtlink{}, &ProducerRelation{},
		&PortraitBackfill{}, &IngestRun{},
	); err != nil {
		return fmt.Errorf("automigrate src_vndb: %w", err)
	}
	return nil
}

// Files lists the staged dump tables, in load order (entities before their
// edges; the original five first, then the step-72 expansion by family).
var Files = []string{
	"vn", "chars", "chars_names", "chars_vns", "images",
	"vn_relations",
	"staff", "staff_alias", "vn_staff", "vn_seiyuu",
	"traits", "traits_parents", "chars_traits",
	"tags", "tags_parents", "tags_vn",
	"producers", "releases", "releases_vn", "releases_producers", "releases_platforms", "releases_titles",
	"extlinks", "releases_extlinks",
	"vn_extlinks", "producers_extlinks", "staff_extlinks", "producers_relations",
}

// FileReport is one file's ingestion outcome.
type FileReport struct {
	Rows    int64 `json:"rows"`
	Skipped int64 `json:"skipped"` // non-ch image rows dropped
}

// Report aggregates one Run.
type Report struct {
	DumpDir  string
	PerFile  map[string]FileReport
	Duration time.Duration
}

// Run ingests the dump directory's db/ files into src_vndb. Deterministic and
// re-runnable: each file is one transaction that TRUNCATEs its table and
// reloads it wholesale from the VNDB COPY-format export. only restricts the run
// to one file (iteration aid). Columns are mapped by NAME from each file's
// .header, so a column reorder in a future dump does not break the load.
func Run(db *gorm.DB, dumpDir, only string) (*Report, error) {
	started := time.Now()
	report := &Report{DumpDir: dumpDir, PerFile: map[string]FileReport{}}

	for _, name := range Files {
		if only != "" && name != only {
			continue
		}
		var fr FileReport
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			fr, err = ingestFile(tx, dumpDir, name)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("ingest %s: %w", name, err)
		}
		report.PerFile[name] = fr
	}
	report.Duration = time.Since(started)

	counts, _ := json.Marshal(report.PerFile)
	run := IngestRun{
		DumpLabel:  filepath.Base(dumpDir),
		Counts:     string(counts),
		DurationMS: report.Duration.Milliseconds(),
		StartedAt:  started,
	}
	if err := db.Create(&run).Error; err != nil {
		return nil, fmt.Errorf("record ingest_run: %w", err)
	}
	return report, nil
}

// ingestFile replaces one table from one COPY-format file inside the caller's
// transaction.
func ingestFile(tx *gorm.DB, dumpDir, name string) (FileReport, error) {
	newTableLoader, ok := loaders[name]
	if !ok {
		return FileReport{}, fmt.Errorf("unknown dump file %q", name)
	}

	cols, err := readHeader(filepath.Join(dumpDir, name+".header"))
	if err != nil {
		return FileReport{}, err
	}
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[c] = i
	}

	f, err := os.Open(filepath.Join(dumpDir, name))
	if err != nil {
		return FileReport{}, err
	}
	defer f.Close()

	table := "src_vndb." + name
	if err := tx.Exec(`TRUNCATE ` + table + ` RESTART IDENTITY`).Error; err != nil {
		return FileReport{}, fmt.Errorf("truncate %s: %w", table, err)
	}

	ld := newTableLoader(tx, time.Now())
	var report FileReport
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024) // descriptions are large
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		fields := strings.Split(string(line), "\t")
		// get returns (value, present): present is false when the column is
		// absent OR the field is the COPY NULL sentinel (\N). String callers
		// use getStr (present=false → ""); numeric/bool callers use the
		// getInt*/getBool* helpers below.
		get := func(col string) (string, bool) {
			i, ok := idx[col]
			if !ok || i >= len(fields) {
				return "", false
			}
			v, isNull := copyUnescape(fields[i])
			if isNull {
				return "", false
			}
			return v, true
		}
		skipped, err := ld.add(get)
		if err != nil {
			return FileReport{}, err
		}
		if skipped {
			report.Skipped++
		} else {
			report.Rows++
		}
	}
	if err := sc.Err(); err != nil {
		return FileReport{}, err
	}
	if err := ld.flush(); err != nil {
		return FileReport{}, err
	}
	return report, nil
}

func readHeader(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read header %s: %w", path, err)
	}
	return strings.Split(strings.TrimRight(string(b), "\r\n"), "\t"), nil
}

// --- generic per-file loader ------------------------------------------------

// getter fetches one column of the current row by header name.
type getter = func(col string) (string, bool)

// tableLoader accumulates one file's rows and flushes them in batches.
type tableLoader interface {
	// add decodes one row; skipped=true means the row was deliberately dropped
	// (e.g. non-ch images).
	add(get getter) (skipped bool, err error)
	flush() error
}

// loader is the one generic tableLoader: decode maps a row onto the table's
// struct (returning ok=false to skip the row).
type loader[T any] struct {
	tx     *gorm.DB
	decode func(get getter) (T, bool)
	batch  []T
}

func newLoader[T any](tx *gorm.DB, decode func(get getter) (T, bool)) *loader[T] {
	return &loader[T]{tx: tx, decode: decode}
}

func (l *loader[T]) add(get getter) (bool, error) {
	row, ok := l.decode(get)
	if !ok {
		return true, nil
	}
	l.batch = append(l.batch, row)
	if len(l.batch) >= batchSize {
		return false, l.flush()
	}
	return false, nil
}

func (l *loader[T]) flush() error {
	if len(l.batch) == 0 {
		return nil
	}
	if err := l.tx.CreateInBatches(l.batch, batchSize).Error; err != nil {
		return err
	}
	l.batch = l.batch[:0]
	return nil
}

const batchSize = 2000

// loaders maps each dump file name to its loader constructor. The decode
// functions live in the loaders_*.go files, grouped by family.
var loaders = map[string]func(tx *gorm.DB, now time.Time) tableLoader{
	"vn":                  newVNLoader,
	"vn_relations":        newVNRelationLoader,
	"chars":               newCharLoader,
	"chars_names":         newCharNameLoader,
	"chars_vns":           newCharVNLoader,
	"images":              newImageLoader,
	"staff":               newStaffLoader,
	"staff_alias":         newStaffAliasLoader,
	"vn_staff":            newVNStaffLoader,
	"vn_seiyuu":           newVNSeiyuuLoader,
	"traits":              newTraitLoader,
	"traits_parents":      newTraitParentLoader,
	"chars_traits":        newCharTraitLoader,
	"tags":                newTagLoader,
	"tags_parents":        newTagParentLoader,
	"tags_vn":             newTagVNLoader,
	"producers":           newProducerLoader,
	"releases":            newReleaseLoader,
	"releases_vn":         newReleaseVNLoader,
	"releases_producers":  newReleaseProducerLoader,
	"releases_platforms":  newReleasePlatformLoader,
	"releases_titles":     newReleaseTitleLoader,
	"extlinks":            newExtlinkLoader,
	"releases_extlinks":   newReleaseExtlinkLoader,
	"vn_extlinks":         newVNExtlinkLoader,
	"producers_extlinks":  newProducerExtlinkLoader,
	"staff_extlinks":      newStaffExtlinkLoader,
	"producers_relations": newProducerRelationLoader,
}

// --- field helpers ----------------------------------------------------------

func getStr(get getter, col string) string {
	v, _ := get(col)
	return v
}

func getInt16(get getter, col string) int16 {
	return int16(getInt64(get, col))
}

func getInt(get getter, col string) int {
	return int(getInt64(get, col))
}

func getInt64(get getter, col string) int64 {
	v, ok := get(col)
	if !ok || v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// getBool decodes the dump's t/f booleans (anything but "t" — including NULL —
// is false).
func getBool(get getter, col string) bool {
	v, _ := get(col)
	return v == "t"
}

// Nullable variants: NULL (\N) stays nil — used where the dump distinguishes
// NULL from a meaningful zero value (see the models' NULL DISCIPLINE note).

func getInt16Ptr(get getter, col string) *int16 {
	v, ok := get(col)
	if !ok {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 16)
	if err != nil {
		return nil
	}
	p := int16(n)
	return &p
}

func getIntPtr(get getter, col string) *int {
	v, ok := get(col)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	return &n
}

func getFloat64Ptr(get getter, col string) *float64 {
	v, ok := get(col)
	if !ok {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &f
}

func getBoolPtr(get getter, col string) *bool {
	v, ok := get(col)
	if !ok {
		return nil
	}
	b := v == "t"
	return &b
}

// copyUnescape decodes one PostgreSQL COPY text-format field. Returns (value,
// isNull); "\N" is the NULL sentinel. Real tabs/newlines/backslashes inside a
// value arrive escaped (\t, \n, \r, \\), so each dump row is exactly one
// physical line — line-based reading is correct.
func copyUnescape(s string) (string, bool) {
	if s == `\N` {
		return "", true
	}
	if !strings.Contains(s, `\`) {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), false
}
