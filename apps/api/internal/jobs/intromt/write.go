package intromt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

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

// decide computes the plan and the source hash for a candidate. The hash is
// sha256 of the CHOSEN ja text verbatim — the re-translate trigger.
func decide(c candidate) (decision, string) {
	hash := hashSource(c.JaText)
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

// runner carries per-run dependencies + stats (serial, plain ints).
type runner struct {
	db    *gorm.DB
	tr    Translator
	stats *Stats
}

// process walks the candidates in popularity order and, per the decision,
// forecasts (dry) or translates + writes (apply).
func (r *runner) process(ctx context.Context, cands []candidate, apply bool, delay time.Duration) {
	for i, c := range cands {
		if ctx.Err() != nil {
			return
		}
		r.handle(ctx, c, apply, delay, i)
	}
}

// handle resolves one candidate. In apply mode it calls the translator only for
// insert/re-translate (never for a skip) and writes through the guarded upsert.
func (r *runner) handle(ctx context.Context, c candidate, apply bool, delay time.Duration, idx int) {
	dec, hash := decide(c)
	switch dec {
	case decSkipSame:
		r.stats.SkipUnchanged++
		return
	case decRetrans:
		r.stats.WouldRetranslate++
	case decInsert:
		r.stats.WouldInsert++
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

	zh, mtModel, err := r.tr.Translate(ctx, c.JaText)
	if err != nil {
		r.stats.Errors++
		slog.Warn("translate failed", "work", c.WorkID, "err", err)
		return
	}
	if zh == "" {
		r.stats.Errors++
		slog.Warn("translate returned empty — refusing to write an empty machine row", "work", c.WorkID)
		return
	}

	rows, err := r.upsert(ctx, c, zh, hash, mtModel)
	if err != nil {
		r.stats.Errors++
		slog.Warn("write machine intro", "work", c.WorkID, "err", err)
		return
	}
	if rows == 0 {
		// The DO UPDATE guard fired: a source row (provenance=0) sits at the
		// key. Should be impossible (candidate query excludes zh-source works),
		// so it means a source row landed mid-run — NEVER overwrite it.
		r.stats.Refused++
		slog.Warn("refused to overwrite a source intro row", "work", c.WorkID, "source_id", c.JaSourceID)
		return
	}
	if dec == decRetrans {
		r.stats.Retranslated++
	} else {
		r.stats.Inserted++
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
