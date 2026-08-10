package entityintros

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const previewRunes = 40

type insertFn func(ctx context.Context, db *gorm.DB, entityID int64, lang, text string, sourceID int16) (written bool, err error)

func insertCharacterIntro(ctx context.Context, db *gorm.DB, entityID int64, lang, text string, sourceID int16) (bool, error) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "character_id"}, {Name: "lang"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogCharacterIntro{CharacterID: entityID, Lang: lang, Intro: text, SourceID: sourceID})
	return res.RowsAffected > 0, res.Error
}

func insertPersonIntro(ctx context.Context, db *gorm.DB, entityID int64, lang, text string, sourceID int16) (bool, error) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "person_id"}, {Name: "lang"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogPersonIntro{PersonID: entityID, Lang: lang, Intro: text, SourceID: sourceID})
	return res.RowsAffected > 0, res.Error
}

type laneRunner struct {
	db        *gorm.DB
	sourceID  int16
	exist     map[int64]map[string]bool
	stats     *LaneStats
	vndb      bool
	lang      string
	insert    insertFn
	hostWorks map[int64][]int64
	touched   []int64
}

func (r *laneRunner) flushTouch(ctx context.Context) error {
	seen := make(map[int64]struct{}, len(r.touched))
	ids := make([]int64, 0, len(r.touched))
	for _, id := range r.touched {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if err := repository.TouchWorks(ctx, r.db, ids); err != nil {
		return err
	}
	r.stats.Touched = len(ids)
	return nil
}

func (r *laneRunner) markLang(entityID int64, lang string) {
	set := r.exist[entityID]
	if set == nil {
		set = map[string]bool{}
		r.exist[entityID] = set
	}
	set[lang] = true
}

func (r *laneRunner) process(ctx context.Context, cands []candidate, apply bool) {
	for _, c := range cands {
		if ctx.Err() != nil {
			return
		}
		r.enrich(ctx, c, apply)
	}
}

func (r *laneRunner) enrich(ctx context.Context, c candidate, apply bool) {
	text := normalizeText(c.Text)
	if r.vndb {
		var stripped bool
		text, stripped = stripVNDBMarkup(text)
		if stripped {
			r.stats.SpoilerStripped++
		}
	}
	if strings.TrimSpace(text) == "" {
		r.stats.NoText++
		return
	}
	lang := r.lang
	if lang == "" {
		lang = detectLang(text)
	}
	if r.exist[c.EntityID][lang] {
		r.stats.SkipDupLang++
		r.collect(&r.stats.DupSamples, c, lang, text)
		return
	}

	switch lang {
	case langJa:
		r.stats.JaNew++
	case langZhHans:
		r.stats.ZhNew++
	default:
		r.stats.EnNew++
	}
	r.collect(&r.stats.Samples, c, lang, text)
	if !apply {
		r.markLang(c.EntityID, lang)
		return
	}

	written, err := r.insert(ctx, r.db, c.EntityID, lang, text, r.sourceID)
	if err != nil {
		r.stats.Errors++
		slog.Warn("write entity intro", "entity", c.EntityID, "external", c.ExternalID, "lang", lang, "err", err)
		return
	}
	if !written {
		r.stats.Conflict++
		return
	}
	r.markLang(c.EntityID, lang)
	r.touched = append(r.touched, r.hostWorks[c.EntityID]...)
	switch lang {
	case langJa:
		r.stats.JaWritten++
	case langZhHans:
		r.stats.ZhWritten++
	default:
		r.stats.EnWritten++
	}
}

func (r *laneRunner) collect(dst *[]Sample, c candidate, lang, text string) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, Sample{EntityID: c.EntityID, ExternalID: c.ExternalID, Lang: lang, Preview: preview(text)})
}

func preview(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	runes := []rune(s)
	if len(runes) <= previewRunes {
		return s
	}
	return string(runes[:previewRunes]) + "…"
}

func runCharacterLanes(ctx context.Context, db, egDB *gorm.DB, reg registry, opts Opts, st *Stats) error {
	wantBgm := opts.Only == "" || opts.Only == LaneCharBangumi
	wantVndb := opts.Only == "" || opts.Only == LaneCharVNDB
	wantEG := opts.Only == "" || opts.Only == LaneCharEG
	if !wantBgm && !wantVndb && !wantEG {
		return nil
	}
	var bgmCands, vndbCands, egCands []candidate
	var err error
	if wantBgm {
		if bgmCands, err = loadCandidates(ctx, db, LaneCharBangumi, reg, opts.Limit, opts.Offset); err != nil {
			return err
		}
	}
	if wantVndb {
		if vndbCands, err = loadCandidates(ctx, db, LaneCharVNDB, reg, opts.Limit, opts.Offset); err != nil {
			return err
		}
	}
	if wantEG {
		if egCands, st.CharEG.NoSupply, err = loadEGCandidates(ctx, db, egDB, reg, opts.Limit, opts.Offset); err != nil {
			return err
		}
	}
	ids := make([]int64, 0, len(bgmCands)+len(vndbCands)+len(egCands))
	seen := make(map[int64]struct{}, cap(ids))
	for _, cands := range [][]candidate{bgmCands, vndbCands, egCands} {
		for _, c := range cands {
			if _, dup := seen[c.EntityID]; dup {
				continue
			}
			seen[c.EntityID] = struct{}{}
			ids = append(ids, c.EntityID)
		}
	}
	exist, err := preloadExistingLangs(ctx, db, "catalog_character_intro", "character_id", ids)
	if err != nil {
		return fmt.Errorf("preload character intro langs: %w", err)
	}
	hosts, err := preloadHostWorks(ctx, db, ids)
	if err != nil {
		return err
	}
	slog.Info("entity-intros character candidates", "char_bgm", len(bgmCands), "char_vndb", len(vndbCands),
		"char_eg", len(egCands), "char_eg_no_supply", st.CharEG.NoSupply,
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)
	if wantBgm {
		st.CharBangumi.Candidates = len(bgmCands)
		r := &laneRunner{db: db, sourceID: reg.bangumiSource, exist: exist, stats: &st.CharBangumi,
			insert: insertCharacterIntro, hostWorks: hosts}
		r.process(ctx, bgmCands, opts.Apply)
		if opts.Apply {
			if err := r.flushTouch(ctx); err != nil {
				return fmt.Errorf("touch char-bgm host works: %w", err)
			}
		}
	}
	if wantVndb {
		st.CharVNDB.Candidates = len(vndbCands)
		r := &laneRunner{db: db, sourceID: reg.vndbSource, exist: exist, stats: &st.CharVNDB,
			vndb: true, lang: langEn, insert: insertCharacterIntro, hostWorks: hosts}
		r.process(ctx, vndbCands, opts.Apply)
		if opts.Apply {
			if err := r.flushTouch(ctx); err != nil {
				return fmt.Errorf("touch char-vndb host works: %w", err)
			}
		}
	}
	if wantEG {
		st.CharEG.Candidates = len(egCands)
		r := &laneRunner{db: db, sourceID: reg.egSource, exist: exist, stats: &st.CharEG,
			lang: langJa, insert: insertCharacterIntro, hostWorks: hosts}
		r.process(ctx, egCands, opts.Apply)
		if opts.Apply {
			if err := r.flushTouch(ctx); err != nil {
				return fmt.Errorf("touch char-eg host works: %w", err)
			}
		}
	}
	return nil
}

func runPersonLane(ctx context.Context, db *gorm.DB, reg registry, opts Opts, st *Stats) error {
	if opts.Only != "" && opts.Only != LanePersonBangumi {
		return nil
	}
	cands, err := loadCandidates(ctx, db, LanePersonBangumi, reg, opts.Limit, opts.Offset)
	if err != nil {
		return err
	}
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.EntityID
	}
	exist, err := preloadExistingLangs(ctx, db, "catalog_person_intro", "person_id", ids)
	if err != nil {
		return fmt.Errorf("preload person intro langs: %w", err)
	}
	slog.Info("entity-intros person candidates", "person_bgm", len(cands),
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)
	st.PersonBangumi.Candidates = len(cands)
	r := &laneRunner{db: db, sourceID: reg.bangumiSource, exist: exist, stats: &st.PersonBangumi, insert: insertPersonIntro}
	r.process(ctx, cands, opts.Apply)
	return nil
}
