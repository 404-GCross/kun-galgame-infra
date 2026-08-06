package intromt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// langZhHans is the single target language of this pilot (bodyless ja→zh-Hans).
const langZhHans = "zh-Hans"

// decision is the resolved plan for one candidate, computed WITHOUT calling the
// LLM (so dry mode forecasts exactly what apply does).
type decision int

const (
	decInsert   decision = iota // no zh row yet → translate + insert a machine row
	decRetrans                  // machine row exists, source hash changed → re-translate
	decSkipSame                 // machine row exists, source hash unchanged → idempotent skip
)

// decide computes the plan and the source hash for a candidate.
func decide(c candidate) (decision, string) {
	hash := hashCandidate(c.JaText, c.Gloss)
	if c.MZhID == nil { // no existing machine row
		return decInsert, hash
	}
	if c.MZhSrcHash != nil && *c.MZhSrcHash == hash {
		return decSkipSame, hash
	}
	return decRetrans, hash
}

// hashCandidate computes src_hash — the re-translate trigger. CONTRACT
// (wave 175, glossary-injected MT):
//
//   - EMPTY glossary → sha256(source text), bit-for-bit what every machine row
//     written before glossary injection carries. This is load-bearing: ~137k
//     machine rows already exist, and a candidate with no glossary data must
//     keep hashing to exactly the same value or the next run re-translates the
//     whole corpus for no gain.
//   - NON-EMPTY glossary → sha256(source text + "\x00" + glossary.Canonical()).
//     The NUL separator cannot occur in either part, so no (text, glossary)
//     pair can collide with another. Effect, by design: an entity that HAS
//     glossary data re-translates exactly ONCE the first time the new binary
//     sees it — the injected terms genuinely changed the prompt, so the old
//     translation is stale — and afterwards only when its source text OR its
//     glossary changes (a newly ingested zh alias, a merged label, a new
//     roster member).
//
// The glossary's canonical form is order-sensitive, which is why the loader's
// priority order and cap are deterministic (glossary.go): a wobbling order
// would re-translate the corpus on every run.
func hashCandidate(text string, gloss Glossary) string {
	if len(gloss) == 0 {
		return hashSource(text)
	}
	return hashSource(text + "\x00" + gloss.Canonical())
}

func hashSource(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// runner carries per-run dependencies + stats. mu guards stats/samples so the
// concurrent apply path can share the same handle() as the serial one.
type runner struct {
	db    *gorm.DB
	tr    Translator
	stats *Stats
	mu    sync.Mutex
	// touched collects works whose machine intro was actually inserted or
	// re-translated, so the run bumps their catalog_work.updated_at once at the
	// end and the public changes feed learns the work is worth re-pulling.
	// Guarded by mu — the apply path runs a worker pool. Unchanged rows,
	// refusals and dry-runs contribute nothing, so a second --apply moves no
	// watermark.
	touched []int64
}

// markTouched records a work whose intro row this run really rewrote.
func (r *runner) markTouched(workID int64) {
	r.mu.Lock()
	r.touched = append(r.touched, workID)
	r.mu.Unlock()
}

// touch bumps updated_at on every work this run wrote an intro for. Called
// after the worker pool has drained, so no lock is needed here.
func (r *runner) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, r.db, r.touched)
}

func (r *runner) inc(n *int) {
	r.mu.Lock()
	*n++
	r.mu.Unlock()
}

// process walks the candidates in popularity order and, per the decision,
// forecasts (dry) or translates + writes (apply). With workers > 1 in apply
// mode the queue is drained by a pool — per-item independence makes order
// irrelevant; upstream throughput is the only reason to parallelize (the
// gateway's per-request latency dominates, not our rate).
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

	// Rate-limit real gateway calls (idle infra — no need to hammer it); the
	// mock passes delay=0.
	if delay > 0 && idx > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}

	zh, mtModel, err := r.tr.Translate(ctx, c.JaText, c.Gloss)
	if err != nil {
		r.inc(&r.stats.Errors)
		slog.Warn("translate failed", "work", c.WorkID, "err", err)
		return
	}
	if zh == "" {
		r.inc(&r.stats.Errors)
		slog.Warn("translate returned empty — refusing to write an empty machine row", "work", c.WorkID)
		return
	}

	rows, err := r.upsert(ctx, c, zh, hash, mtModel)
	if err != nil {
		r.inc(&r.stats.Errors)
		slog.Warn("write machine intro", "work", c.WorkID, "err", err)
		return
	}
	if rows == 0 {
		// The DO UPDATE guard fired: a source row (provenance=0) sits at the
		// key. Should be impossible (candidate query excludes zh-source works),
		// so it means a source row landed mid-run — NEVER overwrite it.
		r.inc(&r.stats.Refused)
		slog.Warn("refused to overwrite a source intro row", "work", c.WorkID, "source_id", c.JaSourceID)
		return
	}
	r.markTouched(c.WorkID)
	if dec == decRetrans {
		r.inc(&r.stats.Retranslated)
	} else {
		r.inc(&r.stats.Inserted)
	}
	r.finishSample(sample, zh, mtModel)
}

// upsert writes the machine zh-Hans row, keyed on the SAME (work_id, lang,
// source_id) unique index as source rows — a machine row reuses the source ja
// row's source_id and is told apart by provenance=1. The DO UPDATE is guarded
// `WHERE provenance = 1` so it can NEVER overwrite a source row (provenance=0):
// if one ever sits at the key, RowsAffected is 0 and the caller refuses. Returns
// rows affected (1 = written/updated, 0 = guard fired).
func (r *runner) upsert(ctx context.Context, c candidate, zh, hash, mtModel string) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		INSERT INTO catalog_work_intro
			(work_id, lang, intro, source_id, provenance, src_hash, mt_model, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, now(), now())
		ON CONFLICT (work_id, lang, source_id) DO UPDATE
			SET intro = EXCLUDED.intro,
				src_hash = EXCLUDED.src_hash,
				mt_model = EXCLUDED.mt_model,
				updated_at = now()
			WHERE catalog_work_intro.provenance = 1`,
		c.WorkID, langZhHans, zh, c.JaSourceID, hash, mtModel)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
