package bgmsummaries

import (
	"context"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const previewRunes = 40

type runner struct {
	db       *gorm.DB
	sourceID int16
	exist    map[int64]map[string]bool
	stats    *Stats
	touched  []int64
}

func (r *runner) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, r.db, r.touched)
}

func (r *runner) process(ctx context.Context, cands []candidate, apply bool) {
	for _, c := range cands {
		if ctx.Err() != nil {
			return
		}
		r.enrich(ctx, c, apply)
	}
}

func (r *runner) enrich(ctx context.Context, c candidate, apply bool) {
	text := normalizeSummary(c.Summary)
	if strings.TrimSpace(text) == "" {
		r.stats.NoSummary++
		return
	}
	lang, ok := detectLang(text)
	if !ok {
		r.stats.NoLang++
		r.collect(&r.stats.NoLangSamples, c, "", text)
		return
	}
	if r.exist[c.WorkID][lang] {
		r.stats.SkipDupLang++
		r.collect(&r.stats.DupSamples, c, lang, text)
		return
	}

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
	if res.RowsAffected == 0 {
		r.stats.Conflict++
		return
	}
	set := r.exist[c.WorkID]
	if set == nil {
		set = map[string]bool{}
		r.exist[c.WorkID] = set
	}
	set[lang] = true
	r.touched = append(r.touched, c.WorkID)
	if lang == langJa {
		r.stats.JaWritten++
	} else {
		r.stats.ZhWritten++
	}
	if c.Site != nil && *c.Site != "" {
		r.stats.ClaimedWritten++
	}
}

func (r *runner) collect(dst *[]Sample, c candidate, lang, text string) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, Sample{WorkID: c.WorkID, SubjectID: c.SubjectID, Lang: lang, Preview: preview(text)})
}

func preview(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	runes := []rune(s)
	if len(runes) <= previewRunes {
		return s
	}
	return string(runes[:previewRunes]) + "…"
}
