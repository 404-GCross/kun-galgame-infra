package bgmsummaries

import (
	"context"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// previewRunes is how many runes of a summary a Sample carries.
const previewRunes = 40

// runner carries per-run dependencies + stats (serial, plain ints).
type runner struct {
	db       *gorm.DB
	sourceID int16
	exist    map[int64]map[string]bool // work_id → intro langs already present (any source)
	stats    *Stats
}

// process walks the candidates and applies the fill-missing-language rule:
// one summary → one row in its detected language, written ONLY when the work
// has no intro row in that language yet.
func (r *runner) process(ctx context.Context, cands []candidate, apply bool) {
	for _, c := range cands {
		if ctx.Err() != nil {
			return
		}
		r.enrich(ctx, c, apply)
	}
}

// enrich decides and (in apply mode) writes one candidate's intro row.
func (r *runner) enrich(ctx context.Context, c candidate, apply bool) {
	if !isBodyless(c.Site) { // XOR guard (§8.D) — never materialise a claimed work
		r.stats.Refused++
		return
	}
	text := normalizeSummary(c.Summary)
	if strings.TrimSpace(text) == "" {
		r.stats.NoSummary++
		return
	}
	lang := detectLang(text)
	if r.exist[c.WorkID][lang] {
		r.stats.SkipDupLang++
		r.collect(&r.stats.DupSamples, c, lang, text)
		return
	}

	// Decided plan — identical in dry and apply.
	if lang == langJa {
		r.stats.JaFill++
		r.collect(&r.stats.JaSamples, c, lang, text)
	} else {
		r.stats.ZhNew++
		r.collect(&r.stats.ZhSamples, c, lang, text)
	}
	if !apply {
		return
	}

	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "lang"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkIntro{
		WorkID: c.WorkID, Lang: lang, Intro: text, SourceID: r.sourceID,
	})
	if res.Error != nil {
		r.stats.Errors++
		slog.Warn("write intro", "work", c.WorkID, "subject", c.SubjectID, "lang", lang, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // concurrent writer / backstop — row already there
		r.stats.Conflict++
		return
	}
	// Mark the lang present so a later same-run duplicate (can't happen under
	// DISTINCT ON, but stays honest) skips via the primary rule.
	set := r.exist[c.WorkID]
	if set == nil {
		set = map[string]bool{}
		r.exist[c.WorkID] = set
	}
	set[lang] = true
	if lang == langJa {
		r.stats.JaWritten++
	} else {
		r.stats.ZhWritten++
	}
}

// collect appends a capped Sample for logging / test assertions.
func (r *runner) collect(dst *[]Sample, c candidate, lang, text string) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, Sample{WorkID: c.WorkID, SubjectID: c.SubjectID, Lang: lang, Preview: preview(text)})
}

// preview flattens newlines and truncates to previewRunes for log lines.
func preview(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	runes := []rune(s)
	if len(runes) <= previewRunes {
		return s
	}
	return string(runes[:previewRunes]) + "…"
}
