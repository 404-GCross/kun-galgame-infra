package srcbangumi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"api/internal/platform/catalog/bangumiwiki"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func EnsureSchema(db *gorm.DB) error {
	if err := db.Exec(`CREATE SCHEMA IF NOT EXISTS src_bangumi`).Error; err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if err := db.AutoMigrate(
		&Subject{}, &Person{}, &Character{},
		&SubjectRelation{}, &SubjectPerson{}, &SubjectCharacter{}, &PersonCharacter{},
		&IngestRun{},
	); err != nil {
		return fmt.Errorf("automigrate src_bangumi: %w", err)
	}
	for _, tc := range []struct{ table, source, column string }{
		{"src_bangumi.subject", "name", "name_norm"},
		{"src_bangumi.subject", "name_cn", "name_cn_norm"},
		{"src_bangumi.person", "name", "name_norm"},
		{"src_bangumi.character", "name", "name_norm"},
	} {
		stmt := `ALTER TABLE ` + tc.table + ` ADD COLUMN IF NOT EXISTS ` + tc.column + ` text
			GENERATED ALWAYS AS (lower(normalize(` + tc.source + `, NFKC))) STORED`
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("create %s.%s: %w", tc.table, tc.column, err)
		}
	}
	return nil
}

type FileReport struct {
	Rows        int64 `json:"rows"`
	ParseErrors int64 `json:"parse_errors"`
	DurationMS  int64 `json:"duration_ms"`
}

type Report struct {
	DumpDir  string
	PerFile  map[string]FileReport
	Duration time.Duration
}

var Files = []string{
	"subject", "person", "character",
	"subject-relations", "subject-persons", "subject-characters", "person-characters",
}

func Run(db *gorm.DB, dumpDir, only string) (*Report, error) {
	started := time.Now()
	report := &Report{DumpDir: dumpDir, PerFile: map[string]FileReport{}}

	for _, name := range Files {
		if only != "" && name != only {
			continue
		}
		path := filepath.Join(dumpDir, name+".jsonlines")
		fileStart := time.Now()
		var fr FileReport
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			fr, err = ingestFile(tx, name, path)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("ingest %s: %w", name, err)
		}
		fr.DurationMS = time.Since(fileStart).Milliseconds()
		report.PerFile[name] = fr
	}
	report.Duration = time.Since(started)

	counts, _ := json.Marshal(report.PerFile)
	parseErrs := map[string]int64{}
	for name, fr := range report.PerFile {
		if fr.ParseErrors > 0 {
			parseErrs[name] = fr.ParseErrors
		}
	}
	perrJSON, _ := json.Marshal(parseErrs)
	run := IngestRun{
		DumpLabel:     filepath.Base(dumpDir),
		ParserVersion: ParserVersion,
		Counts:        counts,
		ParseErrors:   perrJSON,
		DurationMS:    report.Duration.Milliseconds(),
		StartedAt:     started,
	}
	if err := db.Create(&run).Error; err != nil {
		return nil, fmt.Errorf("record ingest_run: %w", err)
	}
	return report, nil
}

func ingestFile(tx *gorm.DB, name, path string) (FileReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileReport{}, err
	}
	defer f.Close()

	table := tableFor(name)
	if err := tx.Exec(`TRUNCATE ` + table).Error; err != nil {
		return FileReport{}, fmt.Errorf("truncate %s: %w", table, err)
	}

	ing := &ingester{tx: tx, now: time.Now()}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		if err := ing.add(name, sc.Bytes()); err != nil {
			return FileReport{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return FileReport{}, err
	}
	if err := ing.flush(); err != nil {
		return FileReport{}, err
	}
	return FileReport{Rows: ing.rows, ParseErrors: ing.parseErrs}, nil
}

func tableFor(name string) string {
	switch name {
	case "subject":
		return "src_bangumi.subject"
	case "person":
		return "src_bangumi.person"
	case "character":
		return "src_bangumi.character"
	case "subject-relations":
		return "src_bangumi.subject_relation"
	case "subject-persons":
		return "src_bangumi.subject_person"
	case "subject-characters":
		return "src_bangumi.subject_character"
	case "person-characters":
		return "src_bangumi.person_character"
	}
	return ""
}

type ingester struct {
	tx        *gorm.DB
	now       time.Time
	rows      int64
	parseErrs int64

	subjects   []Subject
	persons    []Person
	characters []Character
	subRels    []SubjectRelation
	subPersons []SubjectPerson
	subChars   []SubjectCharacter
	perChars   []PersonCharacter
}

const (
	wideBatch   = 1000
	narrowBatch = 5000
)

func (g *ingester) add(name string, line []byte) error {
	g.rows++
	switch name {
	case "subject":
		row, isErr, err := decodeSubject(line, g.now)
		if err != nil {
			return err
		}
		if isErr {
			g.parseErrs++
		}
		g.subjects = append(g.subjects, row)
		if len(g.subjects) >= wideBatch {
			return g.flushSlice(&g.subjects, false)
		}
	case "person":
		row, isErr, err := decodePerson(line, g.now)
		if err != nil {
			return err
		}
		if isErr {
			g.parseErrs++
		}
		g.persons = append(g.persons, row)
		if len(g.persons) >= wideBatch {
			return g.flushSlice(&g.persons, false)
		}
	case "character":
		row, isErr, err := decodeCharacter(line, g.now)
		if err != nil {
			return err
		}
		if isErr {
			g.parseErrs++
		}
		g.characters = append(g.characters, row)
		if len(g.characters) >= wideBatch {
			return g.flushSlice(&g.characters, false)
		}
	case "subject-relations":
		var row SubjectRelation
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("decode subject-relations: %w", err)
		}
		g.subRels = append(g.subRels, row)
		if len(g.subRels) >= narrowBatch {
			return g.flushSlice(&g.subRels, true)
		}
	case "subject-persons":
		var row SubjectPerson
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("decode subject-persons: %w", err)
		}
		g.subPersons = append(g.subPersons, row)
		if len(g.subPersons) >= narrowBatch {
			return g.flushSlice(&g.subPersons, false)
		}
	case "subject-characters":
		var row SubjectCharacter
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("decode subject-characters: %w", err)
		}
		g.subChars = append(g.subChars, row)
		if len(g.subChars) >= narrowBatch {
			return g.flushSlice(&g.subChars, false)
		}
	case "person-characters":
		var row PersonCharacter
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("decode person-characters: %w", err)
		}
		g.perChars = append(g.perChars, row)
		if len(g.perChars) >= narrowBatch {
			return g.flushSlice(&g.perChars, false)
		}
	default:
		return fmt.Errorf("unknown dump file %q", name)
	}
	return nil
}

func flushTyped[T any](g *ingester, batch *[]T, doNothing bool) error {
	if len(*batch) == 0 {
		return nil
	}
	q := g.tx
	if doNothing {
		q = q.Clauses(clause.OnConflict{DoNothing: true})
	}
	if err := q.Create(batch).Error; err != nil {
		return err
	}
	*batch = (*batch)[:0]
	return nil
}

func (g *ingester) flushSlice(batch any, doNothing bool) error {
	switch b := batch.(type) {
	case *[]Subject:
		return flushTyped(g, b, doNothing)
	case *[]Person:
		return flushTyped(g, b, doNothing)
	case *[]Character:
		return flushTyped(g, b, doNothing)
	case *[]SubjectRelation:
		return flushTyped(g, b, doNothing)
	case *[]SubjectPerson:
		return flushTyped(g, b, doNothing)
	case *[]SubjectCharacter:
		return flushTyped(g, b, doNothing)
	case *[]PersonCharacter:
		return flushTyped(g, b, doNothing)
	}
	return fmt.Errorf("unknown batch type %T", batch)
}

func (g *ingester) flush() error {
	if err := flushTyped(g, &g.subjects, false); err != nil {
		return err
	}
	if err := flushTyped(g, &g.persons, false); err != nil {
		return err
	}
	if err := flushTyped(g, &g.characters, false); err != nil {
		return err
	}
	if err := flushTyped(g, &g.subRels, true); err != nil {
		return err
	}
	if err := flushTyped(g, &g.subPersons, false); err != nil {
		return err
	}
	if err := flushTyped(g, &g.subChars, false); err != nil {
		return err
	}
	return flushTyped(g, &g.perChars, false)
}

func parseInfobox(raw string) (datatypes.JSON, string) {
	box, err := bangumiwiki.Parse(raw)
	if err != nil {
		return nil, err.Error()
	}
	b, merr := json.Marshal(box)
	if merr != nil {
		return nil, "marshal parsed infobox: " + merr.Error()
	}
	return datatypes.JSON(b), ""
}

func jsonOrNull(raw json.RawMessage) datatypes.JSON {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return datatypes.JSON(raw)
}

type subjectLine struct {
	ID           int64           `json:"id"`
	Type         int             `json:"type"`
	Name         string          `json:"name"`
	NameCN       string          `json:"name_cn"`
	Infobox      string          `json:"infobox"`
	Platform     int             `json:"platform"`
	Summary      string          `json:"summary"`
	NSFW         bool            `json:"nsfw"`
	Date         string          `json:"date"`
	Series       bool            `json:"series"`
	Score        float64         `json:"score"`
	Rank         int             `json:"rank"`
	Tags         json.RawMessage `json:"tags"`
	MetaTags     json.RawMessage `json:"meta_tags"`
	ScoreDetails json.RawMessage `json:"score_details"`
	Favorite     json.RawMessage `json:"favorite"`
}

func decodeSubject(line []byte, now time.Time) (Subject, bool, error) {
	var l subjectLine
	if err := json.Unmarshal(line, &l); err != nil {
		return Subject{}, false, fmt.Errorf("decode subject: %w", err)
	}
	parsed, parseErr := parseInfobox(l.Infobox)
	return Subject{
		ID: l.ID, Type: l.Type, Name: l.Name, NameCN: l.NameCN,
		InfoboxRaw: l.Infobox, InfoboxParsed: parsed, ParseError: parseErr,
		Platform: l.Platform, Summary: l.Summary, NSFW: l.NSFW,
		Date: l.Date, Series: l.Series, Score: l.Score, Rank: l.Rank,
		Tags: jsonOrNull(l.Tags), MetaTags: jsonOrNull(l.MetaTags),
		ScoreDetails: jsonOrNull(l.ScoreDetails), Favorite: jsonOrNull(l.Favorite),
		ParserVersion: ParserVersion, IngestedAt: now,
	}, parseErr != "", nil
}

type personLine struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Type     int             `json:"type"`
	Career   json.RawMessage `json:"career"`
	Infobox  string          `json:"infobox"`
	Summary  string          `json:"summary"`
	Comments int             `json:"comments"`
	Collects int             `json:"collects"`
}

func decodePerson(line []byte, now time.Time) (Person, bool, error) {
	var l personLine
	if err := json.Unmarshal(line, &l); err != nil {
		return Person{}, false, fmt.Errorf("decode person: %w", err)
	}
	parsed, parseErr := parseInfobox(l.Infobox)
	return Person{
		ID: l.ID, Name: l.Name, Type: l.Type, Career: jsonOrNull(l.Career),
		InfoboxRaw: l.Infobox, InfoboxParsed: parsed, ParseError: parseErr,
		Summary: l.Summary, Comments: l.Comments, Collects: l.Collects,
		ParserVersion: ParserVersion, IngestedAt: now,
	}, parseErr != "", nil
}

type characterLine struct {
	ID       int64  `json:"id"`
	Role     int    `json:"role"`
	Name     string `json:"name"`
	Infobox  string `json:"infobox"`
	Summary  string `json:"summary"`
	Comments int    `json:"comments"`
	Collects int    `json:"collects"`
}

func decodeCharacter(line []byte, now time.Time) (Character, bool, error) {
	var l characterLine
	if err := json.Unmarshal(line, &l); err != nil {
		return Character{}, false, fmt.Errorf("decode character: %w", err)
	}
	parsed, parseErr := parseInfobox(l.Infobox)
	return Character{
		ID: l.ID, Role: l.Role, Name: l.Name,
		InfoboxRaw: l.Infobox, InfoboxParsed: parsed, ParseError: parseErr,
		Summary: l.Summary, Comments: l.Comments, Collects: l.Collects,
		ParserVersion: ParserVersion, IngestedAt: now,
	}, parseErr != "", nil
}
