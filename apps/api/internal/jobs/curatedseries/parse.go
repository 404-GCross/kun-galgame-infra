package curatedseries

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The two frozen artifacts of wave 180 (refs/proj/180-artifacts). They are the
// ONLY surviving copy of the wiki's series mapping: the galgame table family
// was dropped in wave 161, so nothing can regenerate them.
const (
	seriesFile  = "wiki-series.tsv"
	membersFile = "wiki-series-members.tsv"
)

// seriesRow is one wiki series: id, name, description.
//
// Descriptions are hand-written user prose and DO carry embedded newlines, so
// the file is record-oriented rather than line-oriented: a record begins at a
// line whose first tab-separated field is a bare integer and which carries the
// full three fields; every other line continues the description above it. No
// continuation line contains a tab, which is what makes the rule unambiguous.
type seriesRow struct {
	ID          int64
	Name        string
	Description string
}

// memberRow is one work's membership: series_id, galgame_id, catalog_work_id.
// The wiki status and the series name repeated on every row are context for a
// human reading the artifact; the seeder reads neither.
type memberRow struct {
	SeriesID int64
	WorkID   int64
}

func parseSeries(dir string) ([]seriesRow, error) {
	f, err := os.Open(filepath.Join(dir, seriesFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		out []seriesRow
		cur *seriesRow
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		fields := strings.Split(line, "\t")
		if id, ok := recordStart(fields); ok {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &seriesRow{ID: id, Name: fields[1], Description: strings.Join(fields[2:], "\t")}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("%s: content before the first record: %q", seriesFile, line)
		}
		cur.Description += "\n" + line
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out, nil
}

// recordStart reports whether these fields open a new series record.
func recordStart(fields []string) (int64, bool) {
	if len(fields) < 3 {
		return 0, false
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func parseMembers(dir string) ([]memberRow, error) {
	f, err := os.Open(filepath.Join(dir, membersFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []memberRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSuffix(sc.Text(), "\r")
		if text == "" {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) < 4 {
			return nil, fmt.Errorf("%s:%d: want 5 fields, got %d", membersFile, line, len(fields))
		}
		seriesID, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: series_id: %w", membersFile, line, err)
		}
		workID, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: catalog_work_id: %w", membersFile, line, err)
		}
		if workID <= 0 {
			return nil, fmt.Errorf("%s:%d: catalog_work_id must be positive, got %d", membersFile, line, workID)
		}
		out = append(out, memberRow{SeriesID: seriesID, WorkID: workID})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
