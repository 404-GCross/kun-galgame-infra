// Package bgmzhnames projects the Simplified-Chinese character names carried by
// the Bangumi infobox into catalog_character_alias (refs/proj/151). It is a
// zero-parsing-dependency wave: the anchors already exist
// (catalog_external_ref entity_type=character, source=bangumi, link_kind=exact),
// the staging mirror already holds the parsed infobox, and the only work is
// deciding which of its name fields are Chinese and writing them.
//
// Supply, in document order per character:
//
//   - the `简体中文名` field's scalar Value — the character's main Chinese name;
//   - the Chinese-DECLARING items of the `别名` array (`中文名`, `第二中文名`,
//     `第三中文名`, `第四中文名`, and their traditional/译名 spellings).
//
// The untagged (`Key: ""`) items of `别名` are deliberately NOT collected. A
// survey of the anchored set found ~2.5k of them, and roughly half are Japanese
// kanji names (渡辺汐里, 加瀬 葵, 鏑木 光久 …) rather than Chinese translations:
// the "has Han, has no kana" heuristic cannot separate those from a genuine
// Chinese rendering, so the lane would file Japanese names as zh-Hans. Only the
// keys that name their own language are safe, and those are collected.
//
// Every collected value must still pass the Chinese test (at least one Han
// character, no kana) — the declaring key is not trusted blindly, and the main
// field's tail is real junk (Latin transcriptions, `？？？` sentinels). Simplified
// and traditional are NOT split: everything lands as zh-Hans, per the step-57
// ruling that an arbitrary character-set split is worth less than a human fix.
//
// Write shape (no new schema, no migration):
//
//   - catalog_character_alias, lang='zh-Hans', kind=AliasKindTranslation, no latin.
//   - The unique key is (character_id, name, lang), so an INSERT … ON CONFLICT
//     DO NOTHING is idempotent by construction: a name the character already
//     carries in zh-Hans — from a human edit or an earlier run — is absorbed.
//   - is_primary_for_locale is set on at most ONE row per character, and only
//     when that character has NO zh-Hans primary yet. An existing primary is
//     never flipped: the wave adds names, it does not re-elect them.
//   - Real writes bump the host works' updated_at through repository.TouchWorks
//     (the step-117/120 changes-feed discipline). A character written once is
//     touched once even if it gained several names, and a run that writes
//     nothing touches nothing.
//
// --dsn is ALWAYS explicit — a bare run cannot touch a live DB. Dry-run is the
// default; --apply writes.
package bgmzhnames

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// LangZhHans is the single language every row of this wave speaks. It is the
// SHORT BCP-47 spelling shared by every catalog name/intro surface — a longer
// or differently-cased tag would make per-language selection mis-fire (the
// 55/56 lesson).
const LangZhHans = "zh-Hans"

// maxSamples caps the examples a run collects for logging / test assertions.
const maxSamples = 10

// Opts configures a run. Apply=false is a dry-run forecast. DSN is REQUIRED.
type Opts struct {
	Apply  bool
	DSN    string
	Limit  int
	Offset int
}

// Sample is one decided name, for dry-run logging and human review.
type Sample struct {
	CharacterID int64
	ExternalID  string
	Name        string
	Primary     bool
}

// Stats reports the run. The DECIDED counters (Candidates / WouldInsert /
// SkippedDup / SkippedGuard) are identical in dry and apply; Inserted /
// PrimarySet / Touched / Conflict / Errors only move in apply.
type Stats struct {
	// Anchored is every live character carrying a bangumi EXACT anchor — the
	// universe this wave looks at.
	Anchored int
	// SkippedGuard counts characters whose infobox_parsed carries no usable
	// Fields ARRAY (missing, JSON null, or a scalar dirty value — the step-81
	// charattrs finding). Nothing is read out of them.
	SkippedGuard int
	// NoSupply counts parseable infoboxes that yielded no Chinese name.
	NoSupply int
	// SkippedNonChinese counts NAME values dropped by the Chinese test
	// (Latin-only transcriptions, sentinels, kana-bearing values).
	SkippedNonChinese int
	// Candidates counts characters with at least one projected Chinese name.
	Candidates int
	// Names is every projected (character, name) pair, deduplicated per character.
	Names int
	// WouldInsert / SkippedDup split Names against what the character already
	// carries in zh-Hans.
	WouldInsert int
	SkippedDup  int
	// Inserted counts rows that actually landed; Conflict counts the ON CONFLICT
	// backstop firing (a row appeared between the preload and the write).
	Inserted   int
	Conflict   int
	PrimarySet int
	Touched    int
	Errors     int
	Samples    []Sample
}

// Run resolves the anchored characters, projects their Chinese names and
// forecasts (dry) or writes (apply) the alias rows.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	sourceID, err := resolveBangumiSource(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := loadAnchored(ctx, db, sourceID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, err
	}
	st := &Stats{Anchored: len(rows)}

	// Project first, so the preloads only pay for characters that have supply.
	plans := make([]plan, 0, len(rows))
	for _, r := range rows {
		fields, ok := parseFields(r.Infobox)
		if !ok {
			st.SkippedGuard++
			continue
		}
		names, rejected := projectNames(fields)
		st.SkippedNonChinese += rejected
		if len(names) == 0 {
			st.NoSupply++
			continue
		}
		st.Candidates++
		st.Names += len(names)
		plans = append(plans, plan{CharacterID: r.EntityID, ExternalID: r.ExternalID, Names: names})
	}
	ids := make([]int64, len(plans))
	for i, p := range plans {
		ids[i] = p.CharacterID
	}
	existing, err := preloadZhAliases(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	hosts, err := preloadHostWorks(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	slog.Info("bgm-zh-names candidates", "anchored", st.Anchored, "candidates", st.Candidates,
		"names", st.Names, "skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)

	w := &writer{db: db, existing: existing, hostWorks: hosts, stats: st, apply: opts.Apply}
	for _, p := range plans {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		w.write(ctx, p)
	}
	if opts.Apply {
		if err := w.flushTouch(ctx); err != nil {
			return nil, fmt.Errorf("touch host works: %w", err)
		}
	}
	slog.Info("bgm-zh-names done", "apply", opts.Apply,
		"anchored", st.Anchored, "skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"skipped_non_chinese", st.SkippedNonChinese, "candidates", st.Candidates, "names", st.Names,
		"would_insert", st.WouldInsert, "skipped_dup", st.SkippedDup,
		"inserted", st.Inserted, "primary_set", st.PrimarySet, "conflict", st.Conflict,
		"touched_works", st.Touched, "errors", st.Errors)
	return st, nil
}

// plan is one character's decided name list, in write order.
type plan struct {
	CharacterID int64
	ExternalID  string
	Names       []string
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
