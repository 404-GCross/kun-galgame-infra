package entityintromt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

// langZhHans is the single target language of this job.
const langZhHans = "zh-Hans"

// touchChunk bounds one UPDATE's id list — the character lane can touch tens of
// thousands of entities in a nightly slice, and a single IN list that long is a
// query planner problem, not a correctness one.
const touchChunk = 1000

// decision is the resolved plan for one candidate, computed WITHOUT calling the
// LLM (so dry mode forecasts exactly what apply does).
type decision int

const (
	decInsert   decision = iota // no machine zh row yet → translate + insert
	decRetrans                  // machine row exists, source hash changed → re-translate
	decSkipSame                 // machine row exists, source hash unchanged → idempotent skip
)

// decide computes the plan and the source hash for a candidate. The hash is
// sha256 of the CHOSEN source text verbatim — the re-translate trigger.
func decide(c candidate) (decision, string) {
	hash := hashSource(c.Text)
	if c.MZhID == nil { // no existing machine row
		return decInsert, hash
	}
	if c.MZhSrcHash != nil && *c.MZhSrcHash == hash {
		return decSkipSame, hash
	}
	return decRetrans, hash
}

func hashSource(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// runner carries one lane's dependencies + stats. mu guards stats/samples so
// the concurrent apply path shares the same handle() as the serial one.
type runner struct {
	db    *gorm.DB
	tr    Translator
	lane  laneDef
	stats *LaneStats
	mu    sync.Mutex
	// touched collects entities whose machine intro was actually inserted or
	// re-translated, so the run bumps their entity table's updated_at once at
	// the end and downstream consumers learn the entity is worth re-pulling.
	// Guarded by mu — the apply path runs a worker pool. Unchanged rows,
	// refusals and dry runs contribute nothing, so a second --apply moves no
	// watermark.
	touched []int64
}

func (r *runner) markTouched(entityID int64) {
	r.mu.Lock()
	r.touched = append(r.touched, entityID)
	r.mu.Unlock()
}

// touch bumps updated_at on every entity this run wrote an intro for. Called
// after the worker pool has drained, so no lock is needed here.
func (r *runner) touch(ctx context.Context) error {
	if len(r.touched) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(r.touched))
	ids := make([]int64, 0, len(r.touched))
	for _, id := range r.touched {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for start := 0; start < len(ids); start += touchChunk {
		end := min(start+touchChunk, len(ids))
		if err := r.db.WithContext(ctx).
			Exec(`UPDATE `+r.lane.entityTable+` SET updated_at = now() WHERE id IN (?)`, ids[start:end]).
			Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) inc(n *int) {
	r.mu.Lock()
	*n++
	r.mu.Unlock()
}

// process walks the candidates in entity-id order and, per the decision,
// forecasts (dry) or translates + writes (apply). With workers > 1 in apply
// mode the queue is drained by a pool — per-item independence makes order
// irrelevant; the gateway's per-request latency is the only reason to
// parallelize.
func (r *runner) process(ctx context.Context, cands []candidate, apply bool, delay time.Duration, workers int) {
	if !apply || workers <= 1 {
		for i, c := range cands {
			if ctx.Err() != nil {
				return
			}
			r.handle(ctx, c, apply, delay, i)
		}
		return
	}
	ch := make(chan candidate)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for c := range ch {
				if ctx.Err() != nil {
					continue // drain the queue without doing work
				}
				// idx=1 → the per-worker pacing delay applies before every call.
				r.handle(ctx, c, apply, delay, 1)
			}
		})
	}
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		ch <- c
	}
	close(ch)
	wg.Wait()
}

// handle resolves one candidate. In apply mode it calls the translator only for
// insert/re-translate (never for a skip) and writes through the guarded upsert.
func (r *runner) handle(ctx context.Context, c candidate, apply bool, delay time.Duration, idx int) {
	dec, hash := decide(c)
	switch dec {
	case decSkipSame:
		r.inc(&r.stats.SkipUnchanged)
		return
	case decRetrans:
		r.inc(&r.stats.WouldRetranslate)
	case decInsert:
		r.inc(&r.stats.WouldInsert)
	}

	sample := r.beginSample(c, dec)
	if !apply {
		r.finishSample(sample, "", "")
		return
	}

	// Rate-limit real gateway calls; the mock passes delay=0.
	if delay > 0 && idx > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}

	zh, mtModel, err := r.tr.Translate(ctx, c.Text, SourceLang(c.SrcLang))
	if err != nil {
		r.inc(&r.stats.Errors)
		slog.Warn("translate failed", "lane", r.lane.key, "entity", c.EntityID, "err", err)
		return
	}
	if zh == "" {
		r.inc(&r.stats.Errors)
		slog.Warn("translate returned empty — refusing to write an empty machine row", "lane", r.lane.key, "entity", c.EntityID)
		return
	}

	rows, err := r.upsert(ctx, c, zh, hash, mtModel)
	if err != nil {
		r.inc(&r.stats.Errors)
		slog.Warn("write machine intro", "lane", r.lane.key, "entity", c.EntityID, "err", err)
		return
	}
	if rows == 0 {
		// The DO UPDATE guard fired: a source row (provenance=0) sits at the
		// key. Should be impossible (the candidate query excludes entities with
		// a zh source row), so it means one landed mid-run — NEVER overwrite it.
		r.inc(&r.stats.Refused)
		slog.Warn("refused to overwrite a source intro row", "lane", r.lane.key, "entity", c.EntityID, "source_id", c.SourceID)
		return
	}
	r.markTouched(c.EntityID)
	if dec == decRetrans {
		r.inc(&r.stats.Retranslated)
	} else {
		r.inc(&r.stats.Inserted)
	}
	r.finishSample(sample, zh, mtModel)
}

// upsert writes the machine zh-Hans row, keyed on the SAME (<entity>_id, lang,
// source_id) unique index as source rows — a machine row reuses the chosen
// source row's source_id and is told apart by provenance=1. The DO UPDATE is
// guarded `WHERE provenance = 1` so it can NEVER overwrite a source row: if one
// sits at the key, RowsAffected is 0 and the caller refuses. The table and id
// column come from the package-internal lane table, never from input.
func (r *runner) upsert(ctx context.Context, c candidate, zh, hash, mtModel string) (int64, error) {
	t, id := r.lane.introTable, r.lane.idCol
	res := r.db.WithContext(ctx).Exec(`
		INSERT INTO `+t+`
			(`+id+`, lang, intro, source_id, provenance, src_hash, mt_model, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, now(), now())
		ON CONFLICT (`+id+`, lang, source_id) DO UPDATE
			SET intro = EXCLUDED.intro,
				src_hash = EXCLUDED.src_hash,
				mt_model = EXCLUDED.mt_model,
				updated_at = now()
			WHERE `+t+`.provenance = 1`,
		c.EntityID, langZhHans, zh, c.SourceID, hash, mtModel)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// beginSample starts a capped Sample (source text captured now; zh filled on
// apply). Returns the sample's INDEX, -1 when the cap is reached — an index
// stays valid across append reallocations, which a slice-element pointer would
// not under the concurrent pool.
func (r *runner) beginSample(c candidate, dec decision) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stats.Samples) >= maxSamples {
		return -1
	}
	r.stats.Samples = append(r.stats.Samples, Sample{
		Lane: r.lane.key, EntityID: c.EntityID, Decision: decisionName(dec),
		SrcLang: c.SrcLang, Src: c.Text,
	})
	return len(r.stats.Samples) - 1
}

func (r *runner) finishSample(i int, zh, mtModel string) {
	if i < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Samples[i].Zh = zh
	r.stats.Samples[i].MTModel = mtModel
}

func decisionName(d decision) string {
	switch d {
	case decInsert:
		return "insert"
	case decRetrans:
		return "retranslate"
	default:
		return "skip_unchanged"
	}
}
