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
	if err := db.AutoMigrate(&Char{}, &CharName{}, &CharVN{}, &Image{}, &PortraitBackfill{}, &IngestRun{}); err != nil {
		return fmt.Errorf("automigrate src_vndb: %w", err)
	}
	return nil
}

// Files lists the four staged dump tables, in load order.
var Files = []string{"chars", "chars_names", "chars_vns", "images"}

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

	table := tableFor(name)
	if err := tx.Exec(`TRUNCATE ` + table + ` RESTART IDENTITY`).Error; err != nil {
		return FileReport{}, fmt.Errorf("truncate %s: %w", table, err)
	}

	ing := &ingester{tx: tx, name: name, idx: idx, now: time.Now()}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024) // char descriptions are large
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := ing.add(line); err != nil {
			return FileReport{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return FileReport{}, err
	}
	if err := ing.flush(); err != nil {
		return FileReport{}, err
	}
	return FileReport{Rows: ing.rows, Skipped: ing.skipped}, nil
}

func readHeader(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read header %s: %w", path, err)
	}
	return strings.Split(strings.TrimRight(string(b), "\r\n"), "\t"), nil
}

func tableFor(name string) string { return "src_vndb." + name }

// ingester accumulates typed batches and flushes them per table.
type ingester struct {
	tx      *gorm.DB
	name    string
	idx     map[string]int
	now     time.Time
	rows    int64
	skipped int64

	chars     []Char
	charNames []CharName
	charVNs   []CharVN
	images    []Image
}

const batchSize = 2000

func (g *ingester) add(line []byte) error {
	fields := strings.Split(string(line), "\t")
	// get returns (value, present): present is false when the column is absent
	// OR the field is the COPY NULL sentinel (\N). Callers that want a string
	// use getStr (present=false → ""); numeric callers use getInt16.
	get := func(col string) (string, bool) {
		i, ok := g.idx[col]
		if !ok || i >= len(fields) {
			return "", false
		}
		v, isNull := copyUnescape(fields[i])
		if isNull {
			return "", false
		}
		return v, true
	}

	switch g.name {
	case "chars":
		id, _ := get("id")
		image, _ := get("image")
		sex, _ := get("sex")
		gender, _ := get("gender")
		main, _ := get("main")
		g.chars = append(g.chars, Char{
			ID: id, Image: image, Sex: sex, Gender: gender, Main: main,
			MainSpoil: getInt16(get, "main_spoil"), Description: getStr(get, "description"),
			IngestedAt: g.now,
		})
		g.rows++
		if len(g.chars) >= batchSize {
			return g.flushChars()
		}
	case "chars_names":
		id, _ := get("id")
		lang, _ := get("lang")
		g.charNames = append(g.charNames, CharName{
			ID: id, Lang: lang, Name: getStr(get, "name"), Latin: getStr(get, "latin"),
		})
		g.rows++
		if len(g.charNames) >= batchSize {
			return g.flushCharNames()
		}
	case "chars_vns":
		id, _ := get("id")
		vid, _ := get("vid")
		g.charVNs = append(g.charVNs, CharVN{
			ID: id, VID: vid, RID: getStr(get, "rid"),
			Role: getStr(get, "role"), Spoil: getInt16(get, "spoil"),
		})
		g.rows++
		if len(g.charVNs) >= batchSize {
			return g.flushCharVNs()
		}
	case "images":
		id, _ := get("id")
		if !strings.HasPrefix(id, "ch") { // character portraits only (drop sf/cv)
			g.skipped++
			return nil
		}
		g.images = append(g.images, Image{
			ID: id, Width: int(getInt16(get, "width")), Height: int(getInt16(get, "height")),
			VoteCount:      int(getInt16(get, "c_votecount")),
			SexualAvg:      getInt16(get, "c_sexual_avg"),
			SexualStddev:   getInt16(get, "c_sexual_stddev"),
			ViolenceAvg:    getInt16(get, "c_violence_avg"),
			ViolenceStddev: getInt16(get, "c_violence_stddev"),
			Weight:         getInt16(get, "c_weight"),
		})
		g.rows++
		if len(g.images) >= batchSize {
			return g.flushImages()
		}
	default:
		return fmt.Errorf("unknown dump file %q", g.name)
	}
	return nil
}

func (g *ingester) flush() error {
	if err := g.flushChars(); err != nil {
		return err
	}
	if err := g.flushCharNames(); err != nil {
		return err
	}
	if err := g.flushCharVNs(); err != nil {
		return err
	}
	return g.flushImages()
}

func (g *ingester) flushChars() error     { return flushBatch(g.tx, &g.chars) }
func (g *ingester) flushCharNames() error { return flushBatch(g.tx, &g.charNames) }
func (g *ingester) flushCharVNs() error   { return flushBatch(g.tx, &g.charVNs) }
func (g *ingester) flushImages() error    { return flushBatch(g.tx, &g.images) }

func flushBatch[T any](tx *gorm.DB, batch *[]T) error {
	if len(*batch) == 0 {
		return nil
	}
	if err := tx.CreateInBatches(*batch, batchSize).Error; err != nil {
		return err
	}
	*batch = (*batch)[:0]
	return nil
}

func getStr(get func(string) (string, bool), col string) string {
	v, _ := get(col)
	return v
}

func getInt16(get func(string) (string, bool), col string) int16 {
	v, ok := get(col)
	if !ok || v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return int16(n)
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
