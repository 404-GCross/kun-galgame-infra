package srcvndb

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type VotesReport struct {
	VNs     int
	Votes   int64
	Skipped int64
}

// RunVotes stages the votes dump, which VNDB publishes separately from the
// database dump. Only the per-vn bucket counts survive: the user id and date on
// every line are read and dropped, never written anywhere.
func RunVotes(db *gorm.DB, path string) (*VotesReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open votes dump: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("decompress votes dump %s: %w", path, err)
	}
	defer gz.Close()

	agg := newVotesAgg()
	if err := agg.consume(gz); err != nil {
		return nil, fmt.Errorf("read votes dump %s: %w", path, err)
	}
	rows := agg.rows()
	if len(rows) == 0 {
		return nil, fmt.Errorf("votes dump %s aggregated to nothing (%d lines unparsed) — upstream format change", path, agg.skipped)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`TRUNCATE src_vndb.vn_vote_stats`).Error; err != nil {
			return fmt.Errorf("truncate src_vndb.vn_vote_stats: %w", err)
		}
		return tx.CreateInBatches(rows, batchSize).Error
	}); err != nil {
		return nil, err
	}
	return &VotesReport{VNs: len(rows), Votes: agg.counted, Skipped: agg.skipped}, nil
}

const voteBucketCount = 10

type votesAgg struct {
	byVN    map[int64]*[voteBucketCount]int
	counted int64
	skipped int64
}

func newVotesAgg() *votesAgg {
	return &votesAgg{byVN: map[int64]*[voteBucketCount]int{}}
}

func (a *votesAgg) consume(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		a.addLine(sc.Text())
	}
	return sc.Err()
}

// A line is "<vn> <user> <vote> <date>": the vn id is a bare integer (the "v"
// prefix the staged tables key on is ours to add) and the vote is on VNDB's
// stored 10-100 scale, ten points per displayed point.
func (a *votesAgg) addLine(line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	if len(fields) < 3 {
		a.skipped++
		return
	}
	vn, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || vn <= 0 {
		a.skipped++
		return
	}
	vote, err := strconv.Atoi(fields[2])
	if err != nil || vote < 10 {
		a.skipped++
		return
	}
	bucket := vote / 10
	if bucket > voteBucketCount {
		bucket = voteBucketCount
	}
	b := a.byVN[vn]
	if b == nil {
		b = &[voteBucketCount]int{}
		a.byVN[vn] = b
	}
	b[bucket-1]++
	a.counted++
}

func (a *votesAgg) rows() []VNVoteStats {
	ids := make([]int64, 0, len(a.byVN))
	for vn := range a.byVN {
		ids = append(ids, vn)
	}
	slices.Sort(ids)

	out := make([]VNVoteStats, 0, len(ids))
	for _, vn := range ids {
		dist := map[string]int{}
		total := 0
		for i, n := range a.byVN[vn] {
			if n == 0 {
				continue
			}
			dist[strconv.Itoa(i+1)] = n
			total += n
		}
		raw, _ := json.Marshal(dist)
		out = append(out, VNVoteStats{
			ID: "v" + strconv.FormatInt(vn, 10), Distribution: datatypes.JSON(raw), Total: total,
		})
	}
	return out
}
