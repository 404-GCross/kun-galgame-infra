package wikizh

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// snapshotTable is the wave-168 rescue capture. It is read INSTEAD of the
// galgame family on purpose: that family is mid-retirement and this job must
// keep working after the DROP.
const snapshotTable = "src_wiki.intro_snapshot"

// SnapshotMissingError says the rescue capture has not been taken. It is a
// precondition, not a failure — reported so the operator runs the rescue rather
// than wondering why a pass found nothing.
type SnapshotMissingError struct{}

func (SnapshotMissingError) Error() string {
	return snapshotTable + " does not exist — run the wave-168 rescue snapshot first (refs/proj/168 §6)"
}

// LoadCandidates assembles the adjudication set for one bucket.
//
// Only PUBLISHED works are considered: the draft sea is five times larger and
// nobody can read it, the same reasoning that scoped waves 166 and 168's
// machine lane.
//
// A work whose catalog already holds a HUMAN (provenance=0) Chinese row is
// excluded from both buckets — there is nothing to restore over, and this job
// must never touch first-party text somebody curated later.
func LoadCandidates(ctx context.Context, db *gorm.DB, bucket Bucket, limit int) ([]Candidate, error) {
	var exists bool
	if err := db.WithContext(ctx).Raw(
		`SELECT to_regclass(?) IS NOT NULL`, snapshotTable).Scan(&exists).Error; err != nil {
		return nil, err
	}
	if !exists {
		return nil, SnapshotMissingError{}
	}

	var machineGate string
	switch bucket {
	case BucketUsable:
		machineGate = "catalog_zh_mt = ''"
	case BucketCompare:
		machineGate = "catalog_zh_mt <> ''"
	default:
		return nil, fmt.Errorf("unknown bucket %q (want %q or %q)", bucket, BucketUsable, BucketCompare)
	}

	q := `
		SELECT work_id,
		       btrim(wiki_zh_cn) AS wiki_zh,
		       catalog_zh_mt AS machine_zh,
		       -- the judge compares against the ORIGINAL. Prefer catalog's ja
		       -- (it is the text the machine lane translated), then the wiki's
		       -- own ja, then either side's English.
		       CASE
		         WHEN btrim(catalog_ja) <> '' THEN catalog_ja
		         WHEN btrim(wiki_ja)    <> '' THEN wiki_ja
		         WHEN btrim(catalog_en) <> '' THEN catalog_en
		         ELSE wiki_en
		       END AS source,
		       CASE
		         WHEN btrim(catalog_ja) <> '' OR btrim(wiki_ja) <> '' THEN 'ja'
		         ELSE 'en'
		       END AS source_lang
		FROM ` + snapshotTable + `
		WHERE published
		  AND btrim(wiki_zh_cn) <> ''
		  AND catalog_zh_source = ''
		  AND ` + machineGate + `
		ORDER BY work_id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	var rows []Candidate
	if err := db.WithContext(ctx).Raw(q).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Bucket = bucket
		rows[i].Lang = "zh-Hans"
	}
	return rows, nil
}

// UserPacket renders one candidate as the user message the judge sees. The key
// is echoed back by the model, which is how a chunked reply is reassembled.
func UserPacket(c Candidate) string {
	b := &stringsBuilder{}
	b.line("### key: " + c.Key())
	b.line("原文语言: " + c.SourceLang)
	b.line("原文简介:")
	b.line(truncate(c.Source, 3000))
	if c.Bucket == BucketCompare {
		b.line("")
		b.line("A(用户手写中文):")
		b.line(truncate(c.WikiZh, 3000))
		b.line("")
		b.line("B(机器翻译中文):")
		b.line(truncate(c.MachineZh, 3000))
	} else {
		b.line("")
		b.line("用户手写中文简介:")
		b.line(truncate(c.WikiZh, 3000))
	}
	return b.String()
}
