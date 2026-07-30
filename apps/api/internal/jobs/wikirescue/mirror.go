package wikirescue

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// The MIRROR engine — the shared machinery of the W1-pre nativization steps
// p/q/r/s/t (refs/proj/140,
// refs/plans/10-data-layer-retirement/02-w1pre-bridge-nativization.md).
//
// W0/W2's steps are FILL-MISSING: they project rows that have no catalog-side
// home yet and never overwrite. The W1-pre steps are a different kind of step:
// they MIRROR a wiki facet the read face used to bridge at read time, so the
// catalog table must end up holding EXACTLY what the bridge would have
// projected — no more (a name the wiki editor deleted has to disappear here too)
// and no less. That means a real set-diff: insert / update / DELETE, keyed by the
// target table's own unique key, `IS DISTINCT FROM` on the value columns.
//
// The delete arm is why the OWNERSHIP SCOPE is the load-bearing concept. Each
// step declares the exact row set it owns — (claimed work, source, lang, kind…) —
// and the diff is computed against THAT set only. Rows outside it belong to
// another persistent writer (the bgm/dlsite folksonomy importers, the intromt
// machine-translation lane, the dlsite search-hint lane) and are never read,
// never updated and above all never deleted. One row, one persistent writer, is
// the wave's red line; the scope query is where it is enforced.
//
// Ledger: Insert/Update/Delete/Already are the DECIDED diff and are identical in
// dry and apply — a dry run is a byte-accurate forecast, which is the whole point
// of running it before touching production. Written/Touched are apply-only
// outcomes. A converged re-run reports Insert=Update=Delete=0 with Already equal
// to the whole desired set.

// mirrorExisting is one row already present in a mirror step's ownership scope:
// its primary key (so an update or delete can address it) and the value columns
// the step owns.
type mirrorExisting[V any] struct {
	id  int64
	val V
}

// mirrorDesired is a step's decided row set PLUS the order it decided them in.
//
// The order is not cosmetic. catalog_work_title's display lane reads
// `ORDER BY work_id, kind, id`, i.e. INSERTION order within a work — so the only
// way the flipped face reproduces the bridge's row order (the four name columns
// in pivot order, then the aliases in wiki id order) is for the mirror to INSERT
// in exactly that order. A map alone would hand the whole thing to Go's
// randomized iteration.
type mirrorDesired[K comparable, V any] struct {
	byKey map[K]V
	order []K
}

func newMirrorDesired[K comparable, V any](hint int) *mirrorDesired[K, V] {
	return &mirrorDesired[K, V]{byKey: make(map[K]V, hint), order: make([]K, 0, hint)}
}

// put records a desired row, keeping the FIRST value for a repeated key (the
// bridge's own first-wins append order) and reporting whether it was new. A
// repeated key means the source really does project one row twice — e.g. a body
// carrying the same alias string in two galgame_alias rows — which the target's
// unique key can hold only once.
func (d *mirrorDesired[K, V]) put(k K, v V) bool {
	if _, dup := d.byKey[k]; dup {
		return false
	}
	d.byKey[k] = v
	d.order = append(d.order, k)
	return true
}

// drop removes a decided row (step q's machine-held keys). The order slice keeps
// the key; runMirror skips keys no longer in the map.
func (d *mirrorDesired[K, V]) drop(k K) { delete(d.byKey, k) }

func (d *mirrorDesired[K, V]) len() int { return len(d.byKey) }

// mirrorSpec describes one mirror step's target table to the engine. K is the
// row identity (the target's unique key, minus nothing — two rows with the same
// K are the same row), V the value columns the step owns.
type mirrorSpec[K comparable, V any] struct {
	// table is the target table name (fixed internal SQL, never caller input).
	table string
	// insertCols is the full column list of an INSERT, in the order rowOf emits.
	insertCols []string
	rowOf      func(K, V) []any
	// updateCols are the columns an UPDATE assigns (the value columns plus
	// updated_at when the table has one); updateArgs emits their values.
	updateCols []string
	updateArgs func(V) []any
	// workOf extracts the catalog_work id a row belongs to, for TouchWorks.
	workOf func(K) int64
	// same reports whether an existing value equals the desired one (the
	// `IS DISTINCT FROM` predicate, in Go).
	same func(existing, desired V) bool
	// keepExtra disables the DELETE arm: step t is a one-shot ADOPTION whose rows
	// pass to a persistent importer afterwards, so a row the wiki meta no longer
	// justifies is left to that importer rather than deleted.
	keepExtra bool
}

// mirrorLedger accumulates one STEP's diff across however many target tables it
// mirrors (step t writes two), so the reported Touched is the distinct union of
// the arms' work sets rather than the last arm's.
type mirrorLedger struct {
	planned, insert, update, deleted, already, written int
	touched                                            map[int64]struct{}
}

func newMirrorLedger() *mirrorLedger {
	return &mirrorLedger{touched: map[int64]struct{}{}}
}

// into folds the ledger into the step's reported Stats.
func (l *mirrorLedger) into(st *Stats) {
	st.Planned = l.planned
	st.Insert = l.insert
	st.Update = l.update
	st.Delete = l.deleted
	st.Already = l.already
	st.Written = l.written
	st.Touched = len(l.touched)
}

// runMirror computes and (apply) applies one mirror arm's set-diff, accumulating
// into l. desired and existing are both keyed by the target's unique key;
// existing must already be restricted to the step's ownership scope.
func runMirror[K comparable, V any](
	ctx context.Context, r *Runner, l *mirrorLedger,
	spec mirrorSpec[K, V], desired *mirrorDesired[K, V], existing map[K]mirrorExisting[V],
) error {
	l.planned += desired.len()

	var inserts [][]any
	var updates [][]any // updateCols values, then the row id
	var deletes []int64
	armTouched := make(map[int64]struct{})
	mark := func(workID int64) {
		armTouched[workID] = struct{}{}
		l.touched[workID] = struct{}{}
	}

	// The decided order, work by work: rows land in the order the source projects
	// them (see mirrorDesired), and the works themselves walk ascending so two runs
	// produce comparable logs.
	keys := make([]K, 0, desired.len())
	for _, k := range desired.order {
		if _, live := desired.byKey[k]; live {
			keys = append(keys, k)
		}
	}
	sortKeysByWork(keys, spec.workOf)

	for _, k := range keys {
		v := desired.byKey[k]
		ex, ok := existing[k]
		switch {
		case !ok:
			inserts = append(inserts, spec.rowOf(k, v))
			mark(spec.workOf(k))
			l.insert++
		case !spec.same(ex.val, v):
			updates = append(updates, append(spec.updateArgs(v), ex.id))
			mark(spec.workOf(k))
			l.update++
		default:
			l.already++
		}
	}
	if !spec.keepExtra {
		for k, ex := range existing {
			if _, ok := desired.byKey[k]; ok {
				continue
			}
			deletes = append(deletes, ex.id)
			mark(spec.workOf(k))
			l.deleted++
		}
		sort.Slice(deletes, func(i, j int) bool { return deletes[i] < deletes[j] })
	}

	if !r.opts.Apply {
		return nil
	}
	return r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, arm := range []func() (int, error){
			func() (int, error) { return mirrorInsert(tx, spec.table, spec.insertCols, inserts) },
			func() (int, error) { return mirrorUpdate(tx, spec.table, spec.updateCols, updates) },
			func() (int, error) { return mirrorDelete(tx, spec.table, deletes) },
		} {
			n, err := arm()
			if err != nil {
				return err
			}
			l.written += n
		}
		if len(armTouched) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(armTouched))
		for id := range armTouched {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err := repository.TouchWorks(ctx, tx, ids); err != nil {
			return fmt.Errorf("touch works: %w", err)
		}
		return nil
	})
}

// sortKeysByWork orders a key slice by work id so the emitted batches walk the
// works in ascending order (the plan is then diffable between runs).
func sortKeysByWork[K comparable](keys []K, workOf func(K) int64) {
	sort.SliceStable(keys, func(i, j int) bool { return workOf(keys[i]) < workOf(keys[j]) })
}

// mirrorInsert runs a chunked multi-row INSERT with NO conflict clause. A
// conflict here is a BUG, not a race: the scope query and the desired set are
// keyed identically, so a collision means the scope missed a row that the unique
// key covers — exactly the "another writer owns this" case that must fail loudly
// instead of silently skipping (DO NOTHING) or stealing the row (DO UPDATE).
func mirrorInsert(db *gorm.DB, table string, cols []string, rows [][]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	chunk := max(paramChunk/len(cols), 1)
	written := 0
	for start := 0; start < len(rows); start += chunk {
		end := min(start+chunk, len(rows))
		batch := rows[start:end]
		var sb strings.Builder
		fmt.Fprintf(&sb, "INSERT INTO %s (%s) VALUES ", table, strings.Join(cols, ", "))
		args := make([]any, 0, len(batch)*len(cols))
		for i, rw := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			for j := range rw {
				if j > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString("?")
			}
			sb.WriteString(")")
			args = append(args, rw...)
		}
		res := db.Exec(sb.String(), args...)
		if res.Error != nil {
			return written, fmt.Errorf("mirror insert into %s: %w", table, res.Error)
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}

// mirrorUpdate rewrites the changed rows by primary key, ONE statement per row.
// The last element of every row is the target id.
//
// Per-row and not batched on purpose. A batched `UPDATE … FROM (VALUES …)` would
// have to spell out a cast for every column, because a bare parameter inside a
// VALUES list has no type context and lands as text — a silent mis-cast on a
// numeric or smallint column is exactly the failure class this wave cannot
// afford. The update arm is also the SMALL arm by construction: the first apply
// runs against a near-empty ownership scope (inserts), and a daily follow-up run
// only touches the works whose wiki body actually changed.
func mirrorUpdate(db *gorm.DB, table string, cols []string, rows [][]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	sets := make([]string, len(cols))
	for i, c := range cols {
		sets[i] = c + " = ?"
	}
	stmt := "UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	written := 0
	for _, rw := range rows {
		res := db.Exec(stmt, rw...)
		if res.Error != nil {
			return written, fmt.Errorf("mirror update %s: %w", table, res.Error)
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}

// mirrorDelete removes the rows the wiki side no longer justifies, by primary
// key, chunked.
func mirrorDelete(db *gorm.DB, table string, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	written := 0
	for start := 0; start < len(ids); start += paramChunk {
		end := min(start+paramChunk, len(ids))
		res := db.Exec("DELETE FROM "+table+" WHERE id IN ?", ids[start:end])
		if res.Error != nil {
			return written, fmt.Errorf("mirror delete from %s: %w", table, res.Error)
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}

// claimSpan is the CLAIMED population every mirror step runs over: galgame body
// id → catalog_work id, taken from the CATALOG side.
//
// This is deliberately NOT the W0 anchorMap (galgame.catalog_work_id): the read
// face partitions on catalog_work.site='galgame_wiki' AND product_work_id
// (service.partitionClaimSubjects), and a mirror whose population disagreed with
// the face it replaces would materialize rows the face never showed — or miss
// rows it did. Live rows only (deleted_at IS NULL), same as every read path.
//
// The span is a MAP, not a query argument. A mirror step covers the whole claimed
// population (60k+ works), so the wiki-side reads take their table WHOLE and let
// this map do the filtering, and the catalog-side scope reads join
// claimedScopeJoin instead. Passing 60k bind parameters would work and be slower,
// but the real objection is legibility: it turns every logged statement into a
// 400 kB id list.
type claimSpan struct {
	// byGalgame maps galgame.id → catalog_work.id.
	byGalgame map[int64]int64
}

func (r *Runner) claimedSpan(ctx context.Context) (claimSpan, error) {
	var rows []struct {
		WorkID    int64 `gorm:"column:id"`
		GalgameID int64 `gorm:"column:product_work_id"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT id, product_work_id FROM catalog_work
		 WHERE site = ? AND product_work_id IS NOT NULL AND deleted_at IS NULL
		 ORDER BY id`, siteGalgameWiki).Scan(&rows).Error; err != nil {
		return claimSpan{}, fmt.Errorf("load claimed span: %w", err)
	}
	span := claimSpan{byGalgame: make(map[int64]int64, len(rows))}
	for _, x := range rows {
		span.byGalgame[x.GalgameID] = x.WorkID
	}
	return span, nil
}

// claimedScopeJoin restricts a catalog facet table (aliased `t`) to the claimed
// population — the ownership scope's outer predicate, spelled once so every
// step's scope query provably means the same thing as claimedSpan. One bound
// argument: the site key.
const claimedScopeJoin = `JOIN catalog_work w ON w.id = t.work_id
	 AND w.site = ? AND w.product_work_id IS NOT NULL AND w.deleted_at IS NULL`
