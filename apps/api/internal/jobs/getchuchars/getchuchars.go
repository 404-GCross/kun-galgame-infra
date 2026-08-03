package getchuchars

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/jobs/charattrs"
	"api/internal/platform/catalog/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	entityTypeRelease = int16(6)
	linkKindExact     = int16(0)
	// langJa — the profile prose is Japanese; Getchu has no other language.
	langJa = "ja"
)

// Opts configures a run. Both DSNs are explicit and never defaulted: this job
// reads a staging database and writes the live catalog, and a bare invocation
// must not be able to guess either.
type Opts struct {
	DSN       string // catalog
	GetchuDSN string // the crawler's staging database
	Apply     bool
	Limit     int
}

// Stats reports one run.
type Stats struct {
	Match MatchStats

	IntroWritten int
	IntroExists  int // the character already has a ja intro (fill-missing)
	IntroNoText  int
	AttrsWritten int // characters that gained at least one attribute
	AttrFields   int // individual columns filled
	AttrSkipped  int // every field this row could offer was already set
	Conflict     int
	Errors       int
}

func (s Stats) String() string {
	return fmt.Sprintf(
		"input=%d matched=%d (name=%d alias=%d reading=%d) no_work=%d no_name=%d ambiguous=%d collided=%d | "+
			"intro_written=%d intro_exists=%d intro_no_text=%d | attr_rows=%d attr_fields=%d attr_skipped=%d | conflict=%d errors=%d",
		s.Match.Input, s.Match.Matched, s.Match.ByName, s.Match.ByAlias, s.Match.ByReading,
		s.Match.NoWork, s.Match.NoNameInWork, s.Match.Ambiguous, s.Match.Collided,
		s.IntroWritten, s.IntroExists, s.IntroNoText,
		s.AttrsWritten, s.AttrFields, s.AttrSkipped, s.Conflict, s.Errors)
}

// Run matches and (in apply mode) writes.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" || opts.GetchuDSN == "" {
		return nil, fmt.Errorf("--dsn and --getchu-dsn are both REQUIRED; refusing to guess either")
	}
	db, err := open(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog: %w", err)
	}
	defer closeDB(db)
	gdb, err := open(opts.GetchuDSN)
	if err != nil {
		return nil, fmt.Errorf("connect getchu staging: %w", err)
	}
	defer closeDB(gdb)

	var getchuSource int16
	if err := db.WithContext(ctx).
		Raw(`SELECT id FROM catalog_source WHERE key = 'getchu'`).Scan(&getchuSource).Error; err != nil {
		return nil, err
	}
	if getchuSource == 0 {
		return nil, fmt.Errorf("catalog_source has no getchu row — seed it first (refs/proj/167)")
	}

	roster, err := loadRoster(ctx, db, getchuSource)
	if err != nil {
		return nil, err
	}
	chars, err := loadGetchuChars(ctx, gdb)
	if err != nil {
		return nil, err
	}
	cands, ms := match(chars, buildIndex(roster))
	if opts.Limit > 0 && opts.Limit < len(cands) {
		cands = cands[:opts.Limit]
	}
	st := &Stats{Match: ms}
	slog.Info("getchu-chars matched", "result", ms, "candidates", len(cands), "apply", opts.Apply)

	for _, c := range cands {
		writeIntro(ctx, db, getchuSource, c, opts.Apply, st)
		writeAttrs(ctx, db, c, opts.Apply, st)
	}
	slog.Info("getchu-chars done", "apply", opts.Apply, "result", st.String())
	return st, nil
}

// writeIntro fills a MISSING ja intro. Fill-missing across all sources, not
// just this one: wave 120 already gave many of these characters an EG or
// Bangumi intro, and the read face should not surface two Japanese intros for
// one character.
func writeIntro(ctx context.Context, db *gorm.DB, source int16, c Candidate, apply bool, st *Stats) {
	text := strings.TrimSpace(c.Profile)
	if text == "" {
		st.IntroNoText++
		return
	}
	var n int64
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_character_intro WHERE character_id = ? AND lang = ?`,
		c.CharacterID, langJa).Scan(&n).Error; err != nil {
		st.Errors++
		return
	}
	if n > 0 {
		st.IntroExists++
		return
	}
	if !apply {
		st.IntroWritten++ // the forecast count; nothing is written
		return
	}
	res := db.WithContext(ctx).Exec(`
		INSERT INTO catalog_character_intro (character_id, lang, intro, source_id)
		VALUES (?,?,?,?) ON CONFLICT DO NOTHING`, c.CharacterID, langJa, text, source)
	switch {
	case res.Error != nil:
		st.Errors++
		slog.Warn("write character intro", "character", c.CharacterID, "err", res.Error)
	case res.RowsAffected == 0:
		st.Conflict++
	default:
		st.IntroWritten++
	}
}

// writeAttrs fills the typed columns wave 81 built, using THAT wave's parsers
// (charattrs.Parse*) rather than a private copy — one set of rules for the
// sentinels, the ranges and the blood vocabulary.
//
// Fill-missing per FIELD, never per row: a character with a height but no blood
// type gains only the blood type, and an existing value is never overwritten.
func writeAttrs(ctx context.Context, db *gorm.DB, c Candidate, apply bool, st *Stats) {
	if len(c.Attrs) == 0 {
		return
	}
	var raw map[string]string
	if err := json.Unmarshal(c.Attrs, &raw); err != nil {
		st.Errors++
		return
	}

	set := map[string]any{}
	if v, _ := charattrs.ParseHeightCM(raw["身長"]); v != nil {
		set["height_cm"] = *v
	}
	if v, _ := charattrs.ParseWeightKG(raw["体重"]); v != nil {
		set["weight_kg"] = *v
	}
	if v := charattrs.ParseBloodType(raw["血液型"]); v != nil {
		set["blood_type"] = *v
	}
	if m, d := charattrs.ParseBirthdayMD(raw["誕生日"]); m != nil || d != nil {
		if m != nil {
			set["birthday_month"] = *m
		}
		if d != nil {
			set["birthday_day"] = *d
		}
	}
	if b, w, h, cup := charattrs.ParseBWH(raw["スリーサイズ"]); b != nil || w != nil || h != nil || cup != nil {
		if b != nil {
			set["bust_cm"] = *b
		}
		if w != nil {
			set["waist_cm"] = *w
		}
		if h != nil {
			set["hip_cm"] = *h
		}
		if cup != nil {
			set["cup"] = *cup
		}
	}
	if len(set) == 0 {
		return
	}

	// coalesce(col, ?) is the never-overwrite: an existing value keeps itself,
	// a NULL takes the new one. Doing it in the UPDATE rather than by reading
	// first also makes it safe against a concurrent writer — there is no window
	// between the read and the write for one to slip through.
	assigns := make([]string, 0, len(set))
	args := make([]any, 0, len(set)+1)
	for col, v := range set {
		assigns = append(assigns, fmt.Sprintf("%s = coalesce(%s, ?)", col, col))
		args = append(args, v)
	}
	if !apply {
		st.AttrsWritten++
		st.AttrFields += len(set)
		return
	}
	args = append(args, c.CharacterID)
	res := db.WithContext(ctx).Exec(
		"UPDATE catalog_character SET "+strings.Join(assigns, ", ")+" WHERE id = ?", args...)
	if res.Error != nil {
		st.Errors++
		slog.Warn("write character attrs", "character", c.CharacterID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		st.AttrSkipped++
		return
	}
	st.AttrsWritten++
	st.AttrFields += len(set)
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// The intro write is raw SQL rather than a GORM Create because the table's
// identity PK uses `default:(-)` and the fill-missing check has already run;
// this reference keeps the model discoverable from here.
var _ = model.CatalogCharacterIntro{}
