package main

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Deterministic stratified sample sizes (doc-pinned). title-strict is split
// across its two strata so BOTH the cross-source-corroborated subgroup and the
// bangumi-only subgroup are covered; contradictions are censused in full.
const (
	sampleRosetta      = 150
	sampleTSRosetta    = 150 // title-strict work that also has an eg-rosetta ref
	sampleTSBangumi    = 150 // title-strict, bangumi-only
	defaultSamplesName = "probable-audit-samples.tsv"
)

var tsvHeader = []string{
	"stratum", "rule", "source_id", "external_id", "work_id", "product_work_id",
	"corrob", "year_diff", "also_rosetta", "work_vndb", "ext_vndb",
	"work_title", "ext_title", "work_year", "ext_year", "wiki_url", "ext_url",
	"decision", "notes",
}

func (r probableRef) tsvRow() string {
	yd := yearBucket(r.WorkYear, r.ExtYear)
	return strings.Join([]string{
		r.stratum(), ruleLabel(r.Rule), itoa(int64(r.SourceID)), r.ExternalID,
		itoa(r.WorkID), itoa(r.ProductWorkID), r.Corrob, yd, boolStr(r.AlsoRosetta),
		clean(r.WorkVNDB), clean(r.ExtVNDB), clean(r.WorkTitle), clean(r.ExtTitle),
		itoa(int64(r.WorkYear)), itoa(int64(r.ExtYear)),
		wikiGalgameBase + itoa(r.ProductWorkID), r.extURL(),
		"", "", // decision, notes — the reviewer fills these
	}, "\t")
}

// runExport writes the sample worklist: three deterministic strata plus the
// full contradiction census, one TSV the reviewer fills and feeds to --apply.
func (a *auditor) runExport(out string) error {
	if err := a.loadAll(); err != nil {
		return err
	}

	var rosetta, tsRosetta, tsBangumi, contradictions []probableRef
	for _, r := range a.refs {
		switch r.stratum() {
		case "contradiction":
			contradictions = append(contradictions, r)
		case "rosetta":
			rosetta = append(rosetta, r)
		case "ts-rosetta-corrob":
			tsRosetta = append(tsRosetta, r)
		case "ts-bangumi-only":
			tsBangumi = append(tsBangumi, r)
		}
	}

	var sample []probableRef
	sample = append(sample, deterministicSample(rosetta, sampleRosetta)...)
	sample = append(sample, deterministicSample(tsRosetta, sampleTSRosetta)...)
	sample = append(sample, deterministicSample(tsBangumi, sampleTSBangumi)...)
	sample = append(sample, contradictions...) // census, never sampled
	sort.Slice(sample, func(i, j int) bool { return sample[i].key() < sample[j].key() })

	path := resolveOutPath(out)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	fmt.Fprintln(bw, strings.Join(tsvHeader, "\t"))
	for _, r := range sample {
		fmt.Fprintln(bw, r.tsvRow())
	}
	if err := bw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr,
		"wrote %s: %d sample rows (rosetta %d/%d, ts-rosetta %d/%d, ts-bangumi %d/%d, contradictions %d census)\n",
		path, len(sample),
		min(sampleRosetta, len(rosetta)), len(rosetta),
		min(sampleTSRosetta, len(tsRosetta)), len(tsRosetta),
		min(sampleTSBangumi, len(tsBangumi)), len(tsBangumi),
		len(contradictions))
	return nil
}

// deterministicSample returns the n refs with the smallest FNV hash of their
// key — a reproducible, uniform-ish sample independent of row order. Fewer than
// n rows → all of them.
func deterministicSample(pool []probableRef, n int) []probableRef {
	if len(pool) <= n {
		out := append([]probableRef(nil), pool...)
		sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
		return out
	}
	sort.Slice(pool, func(i, j int) bool {
		hi, hj := hashKey(pool[i].key()), hashKey(pool[j].key())
		if hi != hj {
			return hi < hj
		}
		return pool[i].key() < pool[j].key()
	})
	return append([]probableRef(nil), pool[:n]...)
}

func hashKey(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// resolveOutPath treats a trailing-slash or existing-directory argument as a
// directory to write the default filename into.
func resolveOutPath(out string) string {
	if out == "" {
		return defaultSamplesName
	}
	if strings.HasSuffix(out, string(os.PathSeparator)) {
		_ = os.MkdirAll(out, 0o755)
		return filepath.Join(out, defaultSamplesName)
	}
	if fi, err := os.Stat(out); err == nil && fi.IsDir() {
		return filepath.Join(out, defaultSamplesName)
	}
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
