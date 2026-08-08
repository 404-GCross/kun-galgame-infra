// Package bgmzhnames projects the Simplified-Chinese names carried by the
// Bangumi infobox into the catalog's alias tables. It started as the character
// wave (refs/proj/151) and now covers three entity families through one
// --lane switch (refs/proj/175 wave A):
//
//	character  catalog_character anchors  → catalog_character_alias
//	person     catalog_person anchors     → catalog_name_alias (primary credit name)
//	label      catalog_label anchors      → catalog_label_alias
//
// All three are zero-parsing-dependency waves: the anchors already exist
// (catalog_external_ref, source=bangumi, link_kind=exact), the staging mirror
// already holds the parsed infobox, and the only work is deciding which of its
// name fields are Chinese and writing them. Bangumi files companies and groups
// in the same `person` table as individuals, with the same infobox shape, so
// the person and label lanes share one staging join and the parse layer is
// identical across all three.
//
// Supply, in document order per entity:
//
//   - the `简体中文名` field's scalar Value — the entity's main Chinese name;
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
//   - lang='zh-Hans', kind=AliasKindTranslation, no latin.
//   - The unique key is (owner, name, lang), so an INSERT … ON CONFLICT
//     DO NOTHING is idempotent by construction: a name the owner already
//     carries in zh-Hans — from a human edit or an earlier run — is absorbed.
//   - is_primary_for_locale is set on at most ONE row per owner, and only
//     when that owner has NO zh-Hans primary yet. An existing primary is
//     never flipped: the wave adds names, it does not re-elect them.
//   - The person and label lanes drop a projected name that is ALREADY the
//     owner's own display name (213 persons / 72 labels): the read faces hide
//     such a row, so writing it would only cost a dead row that claims the
//     locale primary.
//
// TOUCH DISCIPLINE IS PER LANE, and only the character lane has one. A
// character's names are rendered by the works whose roster lists it, so a real
// write bumps those works' updated_at through repository.TouchWorks (the
// step-117/120 changes-feed discipline); a character written once is touched
// once even if it gained several names, and a run that writes nothing touches
// nothing. Persons and labels reach no work read face with their alias sets —
// the same finding personmint and labellogos already recorded — so those lanes
// touch nothing at all. See labelLane / personLane for the full argument.
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

// sourceBangumi is the catalog_source id every row of this wave carries. The
// three lanes are the ONLY writers of kind=AliasKindTranslation rows, which is
// what let wave 195 attribute the whole pre-existing kind=0 corpus to Bangumi;
// stamping it explicitly from here on is what keeps that true without the
// attribution having to be re-derived from writer archaeology again.
const sourceBangumi int16 = 3

// bangumiSourceRef is the addressable sourceBangumi the alias models' nullable
// SourceID needs.
func bangumiSourceRef() *int16 { s := sourceBangumi; return &s }

// maxSamples caps the examples a run collects for logging / test assertions.
const maxSamples = 10

// Opts configures a run. Apply=false is a dry-run forecast. DSN is REQUIRED.
// An empty Lane means LaneCharacter, so an existing invocation keeps its
// meaning.
type Opts struct {
	Apply  bool
	DSN    string
	Lane   Lane
	Limit  int
	Offset int
}

// Sample is one decided name, for dry-run logging and human review. EntityID is
// the anchored entity (character / person / label); OwnerID is the row the
// alias hangs off, which differs from it only on the person lane.
type Sample struct {
	EntityID   int64
	OwnerID    int64
	ExternalID string
	Name       string
	Primary    bool
}

// Stats reports the run. The DECIDED counters (Candidates / WouldInsert /
// SkippedDup / SkippedGuard) are identical in dry and apply; Inserted /
// PrimarySet / Touched / Conflict / Errors only move in apply.
type Stats struct {
	// Anchored is every live entity carrying a bangumi EXACT anchor — the
	// universe this lane looks at.
	Anchored int
	// SkippedNoOwner counts anchored entities with no row to hang an alias off.
	// Person lane only: a person whose primary_credit_name_id is NULL. Choosing
	// one of its other credit names would be an identity judgement.
	SkippedNoOwner int
	// SkippedGuard counts entities whose infobox_parsed carries no usable
	// Fields ARRAY (missing, JSON null, or a scalar dirty value — the step-81
	// charattrs finding). Nothing is read out of them.
	SkippedGuard int
	// NoSupply counts parseable infoboxes that yielded no Chinese name.
	NoSupply int
	// SkippedNonChinese counts NAME values dropped by the Chinese test
	// (Latin-only transcriptions, sentinels, kana-bearing values).
	SkippedNonChinese int
	// SkippedSameAsOwner counts names identical to the owner's own display name
	// (person / label lanes only — see laneSpec.dropOwnerName).
	SkippedSameAsOwner int
	// Candidates counts entities with at least one projected Chinese name.
	Candidates int
	// Names is every projected (owner, name) pair, deduplicated per entity.
	Names int
	// WouldInsert / SkippedDup split Names against what the owner already
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

// Run resolves the lane's anchored entities, projects their Chinese names and
// forecasts (dry) or writes (apply) the alias rows.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	spec, err := laneFor(opts.Lane)
	if err != nil {
		return nil, err
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
	rows, err := spec.load(ctx, db, sourceID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, err
	}
	st := &Stats{Anchored: len(rows)}

	// Project first, so the preloads only pay for entities that have supply.
	plans := make([]plan, 0, len(rows))
	for _, r := range rows {
		if r.OwnerID == nil {
			st.SkippedNoOwner++
			continue
		}
		fields, ok := parseFields(r.Infobox)
		if !ok {
			st.SkippedGuard++
			continue
		}
		names, rejected := projectNames(fields)
		st.SkippedNonChinese += rejected
		if spec.dropOwnerName {
			names = dropName(names, r.OwnerName, &st.SkippedSameAsOwner)
		}
		if len(names) == 0 {
			st.NoSupply++
			continue
		}
		st.Candidates++
		st.Names += len(names)
		plans = append(plans, plan{
			EntityID: r.EntityID, OwnerID: *r.OwnerID, ExternalID: r.ExternalID, Names: names,
		})
	}
	owners := make([]int64, len(plans))
	for i, p := range plans {
		owners[i] = p.OwnerID
	}
	existing, err := spec.preload(ctx, db, owners)
	if err != nil {
		return nil, err
	}
	hosts := map[int64][]int64{}
	if spec.hostWorks != nil {
		if hosts, err = spec.hostWorks(ctx, db, owners); err != nil {
			return nil, err
		}
	}
	lane := opts.Lane
	if lane == "" {
		lane = LaneCharacter
	}
	slog.Info("bgm-zh-names candidates", "lane", lane, "anchored", st.Anchored, "candidates", st.Candidates,
		"names", st.Names, "skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)

	w := &writer{db: db, spec: spec, existing: existing, hostWorks: hosts, stats: st, apply: opts.Apply}
	for _, p := range plans {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		w.write(ctx, p)
	}
	if opts.Apply && spec.hostWorks != nil {
		if err := w.flushTouch(ctx); err != nil {
			return nil, fmt.Errorf("touch host works: %w", err)
		}
	}
	slog.Info("bgm-zh-names done", "lane", lane, "apply", opts.Apply,
		"anchored", st.Anchored, "skipped_no_owner", st.SkippedNoOwner,
		"skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"skipped_non_chinese", st.SkippedNonChinese, "skipped_same_as_owner", st.SkippedSameAsOwner,
		"candidates", st.Candidates, "names", st.Names,
		"would_insert", st.WouldInsert, "skipped_dup", st.SkippedDup,
		"inserted", st.Inserted, "primary_set", st.PrimarySet, "conflict", st.Conflict,
		"touched_works", st.Touched, "errors", st.Errors)
	return st, nil
}

// plan is one entity's decided name list, in write order.
type plan struct {
	EntityID   int64
	OwnerID    int64
	ExternalID string
	Names      []string
}

// dropName removes the owner's own display name from a projected list,
// counting what it removed. An empty owner name removes nothing.
func dropName(names []string, owner string, counter *int) []string {
	if owner == "" {
		return names
	}
	out := names[:0]
	for _, n := range names {
		if n == owner {
			*counter++
			continue
		}
		out = append(out, n)
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
