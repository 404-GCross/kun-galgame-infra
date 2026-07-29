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
// exist map is SHARED between the character lanes: an earlier lane's decision
// marks the language present, so a later lane's fill-missing skip stays correct
// within one run.
type laneRunner struct {
	db       *gorm.DB
	sourceID int16
	exist    map[int64]map[string]bool // entity_id → intro langs already present (any source)
	stats    *LaneStats
	vndb     bool   // vndb lane: strip markup before the language decision
	lang     string // fixed lane language ("" = detect from the text)
	insert   insertFn
	// hostWorks/touched are the changes-feed wiring of the three CHARACTER
	// lanes; person-bgm leaves hostWorks nil, so it appends nothing and touches
	// nothing. Only REAL writes contribute — dry-runs, dup-lang skips and ON
	// CONFLICT no-ops leave every watermark where it was (the step-117
	// discipline).
	hostWorks map[int64][]int64
	touched   []int64
}

// flushTouch bumps catalog_work.updated_at once for every host work this lane
// actually wrote an intro into, and records how many distinct works that was.
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

// markLang records that the entity now has (or is planned to have) an intro in
// this language, so the fill-missing rule keeps holding across lanes.
func (r *laneRunner) markLang(entityID int64, lang string) {
	set := r.exist[entityID]
	if set == nil {
		set = map[string]bool{}
		r.exist[entityID] = set
	}
	set[lang] = true
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
		// A dry run marks the PLANNED language too, so the forecast matches what
		// an apply would do: char-bgm and char-eg can both offer ja for the same
		// character, and without this both lanes would count the same write.
		r.markLang(c.EntityID, lang)
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

// runCharacterLanes runs char-bgm, then char-vndb, then char-eg over ONE shared
// exist map (preloaded across every lane's candidate ids in a single query).
//
// The order is the fill-missing precedence: char-bgm and char-eg both speak ja,
// so a character supplied by both takes the Bangumi text and the EG lane skips
// it as an already-present language. char-vndb writes en and never collides.
//
// All three lanes fill the SAME table, so all three bump the works that host
// the characters they wrote (refs/proj/122 — step 120 wired only char-eg, and
// the weekly Bangumi refresh is the lane that keeps adding intros). The host
// map is preloaded ONCE over the union of the candidate ids, the same set the
// exist map uses, so a run pays for it once rather than per lane. A work that
// hosts characters written by two lanes is bumped by each of them: updated_at
// is a watermark, so repeating it within one run is harmless, and each lane's
// Touched then stays an honest fact about its own writes.
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
	// The union of the lanes' candidates, deduplicated: a character anchored in
	// several sources is a candidate of several lanes, and the host preload
	// appends per returned row — an id repeated across two preload CHUNKS would
	// otherwise stack its works twice.
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

// runPersonLane runs person-bgm. Empty today (person anchors don't exist yet —
// identity resolution deferred); re-runs auto-expand once anchors land.
//
// This lane deliberately gets NO touch wiring (refs/proj/122 §1). A person is
// not on the work read face: step 118 kept person_id on the credit-NAME facet
// only (see service/merge_execute.go — "person_id lives on the name face only
// — no work changes"), so a new person intro changes no work's rendered page,
// and catalog_work_character carries no person edge to derive hosts from. On
// top of that the anchor set is empty, so wiring one would mean writing a
// false changes-feed signal against a set that cannot even exercise it.
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
