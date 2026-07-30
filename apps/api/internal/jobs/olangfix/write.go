package olangfix

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// writeChunk bounds the VALUES list of one UPDATE statement.
const writeChunk = 1000

// pendingWrite is one decided (work, olang) pair awaiting the batched UPDATE.
type pendingWrite struct {
	workID int64
	olang  string
}

// writer batches the decided rows into chunked UPDATEs. It is a no-op in dry
// runs: the plan is decided identically either way, only flush writes.
type writer struct {
	db      *gorm.DB
	stats   *Stats
	pending []pendingWrite
}

// plan records a decided change. Dry runs skip the buffer entirely — the
// counters and the transition matrix are built by the caller regardless, so a
// dry run reports the same plan without holding it.
func (w *writer) plan(workID int64, olang string, apply bool) {
	if !apply {
		return
	}
	w.pending = append(w.pending, pendingWrite{workID: workID, olang: olang})
}

// flush applies the buffered changes in chunks.
//
// The UPDATE goes through raw SQL rather than the model on purpose: GORM would
// stamp updated_at, and this job must NOT move that watermark (see the package
// doc — olang is a population predicate, and 82k bumped watermarks would flood
// the /v1/catalog/changes feed). `IS DISTINCT FROM` is a concurrency backstop
// only — the plan already excludes unchanged rows — so RowsAffected is the count
// of values that really moved, which is what Written reports.
func (w *writer) flush(ctx context.Context, apply bool) error {
	if !apply || len(w.pending) == 0 {
		return nil
	}
	for start := 0; start < len(w.pending); start += writeChunk {
		end := min(start+writeChunk, len(w.pending))
		batch := w.pending[start:end]

		var sb strings.Builder
		args := make([]any, 0, len(batch)*2)
		for i, p := range batch {
			if i > 0 {
				sb.WriteString(",")
			}
			if i == 0 {
				// Cast the first tuple so Postgres infers the derived columns'
				// types (the catalogsync VALUES-join discipline).
				sb.WriteString("(?::bigint,?::text)")
			} else {
				sb.WriteString("(?,?)")
			}
			args = append(args, p.workID, p.olang)
		}
		res := w.db.WithContext(ctx).Exec(`
			UPDATE catalog_work AS w SET olang = v.ol
			FROM (VALUES `+sb.String()+`) AS v(wid, ol)
			WHERE w.id = v.wid AND w.olang IS DISTINCT FROM v.ol`, args...)
		if res.Error != nil {
			w.stats.Errors++
			return res.Error
		}
		w.stats.Written += int(res.RowsAffected)
	}
	w.pending = nil
	return nil
}
