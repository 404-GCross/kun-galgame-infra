package entityintros

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// previewRunes is how many runes of a text a Sample carries.
const previewRunes = 40

// insertFn writes one intro row with its ON CONFLICT DO NOTHING backstop and
// reports whether a row was actually inserted (false = the backstop fired).
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

// laneRunner carries one lane's dependencies + stats (serial, plain ints). The
// exist map is SHARED between the two character lanes: an earlier lane's write
// marks the language present, so the later lane's fill-missing skip stays
// correct within one run.
type laneRunner struct {
	db       *gorm.DB
	sourceID int16
	exist    map[int64]map[string]bool // entity_id → intro langs already present (any source)
	stats    *LaneStats
	vndb     bool // vndb lane: strip markup, lang=en fixed
	insert   insertFn
}

// process walks the candidates and applies the fill-missing-language rule:
// one staging text → one row in its (detected or lane-fixed) language, written
// ONLY when the entity has no intro row in that language yet.
func (r *laneRunner) process(ctx context.Context, cands []candidate, apply bool) {
	for _, c := range cands {
		if ctx.Err() != nil {
			return
		}
		r.enrich(ctx, c, apply)
	}
}

// enrich decides and (in apply mode) writes one candidate's intro row.
func (r *laneRunner) enrich(ctx context.Context, c candidate, apply bool) {
	text := normalizeText(c.Text)
	lang := ""
	if r.vndb {
		var stripped bool
		text, stripped = stripVNDBMarkup(text)
		if stripped {
			r.stats.SpoilerStripped++
		}
		lang = langEn
	}
	if strings.TrimSpace(text) == "" {
		r.stats.NoText++
		return
	}
	if !r.vndb {
		lang = detectLang(text)
	}
	if r.exist[c.EntityID][lang] {
		r.stats.SkipDupLang++
		r.collect(&r.stats.DupSamples, c, lang, text)
		return
	}

	// Decided plan — identical in dry and apply.
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
		return
	}

	written, err := r.insert(ctx, r.db, c.EntityID, lang, text, r.sourceID)
	if err != nil {
		r.stats.Errors++
		slog.Warn("write entity intro", "entity", c.EntityID, "external", c.ExternalID, "lang", lang, "err", err)
		return
	}
	if !written { // concurrent writer / backstop — row already there
		r.stats.Conflict++
		return
	}
	// Mark the lang present so a later lane (or a same-run duplicate) skips via
	// the primary rule.
	set := r.exist[c.EntityID]
	if set == nil {
		set = map[string]bool{}
		r.exist[c.EntityID] = set
	}
	set[lang] = true
	switch lang {
	case langJa:
		r.stats.JaWritten++
	case langZhHans:
		r.stats.ZhWritten++
	default:
		r.stats.EnWritten++
	}
}

// collect appends a capped Sample for logging / test assertions.
func (r *laneRunner) collect(dst *[]Sample, c candidate, lang, text string) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, Sample{EntityID: c.EntityID, ExternalID: c.ExternalID, Lang: lang, Preview: preview(text)})
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

// runCharacterLanes runs char-bgm then char-vndb over ONE shared exist map
// (preloaded across both lanes' candidate ids in a single query).
func runCharacterLanes(ctx context.Context, db *gorm.DB, reg registry, opts Opts, st *Stats) error {
	wantBgm := opts.Only == "" || opts.Only == LaneCharBangumi
	wantVndb := opts.Only == "" || opts.Only == LaneCharVNDB
	if !wantBgm && !wantVndb {
		return nil
	}
	var bgmCands, vndbCands []candidate
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
	ids := make([]int64, 0, len(bgmCands)+len(vndbCands))
	for _, c := range bgmCands {
		ids = append(ids, c.EntityID)
	}
	for _, c := range vndbCands {
		ids = append(ids, c.EntityID)
	}
	exist, err := preloadExistingLangs(ctx, db, "catalog_character_intro", "character_id", ids)
	if err != nil {
		return fmt.Errorf("preload character intro langs: %w", err)
	}
	slog.Info("entity-intros character candidates", "char_bgm", len(bgmCands), "char_vndb", len(vndbCands),
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)
	if wantBgm {
		st.CharBangumi.Candidates = len(bgmCands)
		r := &laneRunner{db: db, sourceID: reg.bangumiSource, exist: exist, stats: &st.CharBangumi, insert: insertCharacterIntro}
		r.process(ctx, bgmCands, opts.Apply)
	}
	if wantVndb {
		st.CharVNDB.Candidates = len(vndbCands)
		r := &laneRunner{db: db, sourceID: reg.vndbSource, exist: exist, stats: &st.CharVNDB, vndb: true, insert: insertCharacterIntro}
		r.process(ctx, vndbCands, opts.Apply)
	}
	return nil
}

// runPersonLane runs person-bgm. Empty today (person anchors don't exist yet —
// identity resolution deferred); re-runs auto-expand once anchors land.
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
