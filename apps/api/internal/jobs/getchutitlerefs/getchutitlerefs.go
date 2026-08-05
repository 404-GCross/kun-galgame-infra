package getchutitlerefs

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/jobs/getchuchars"
	"api/internal/platform/catalog/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const (
	// matchedByConfirmed and matchedByProbable put the EVIDENCE on the row, not
	// just the tier, so an audit can tell why a ref is trusted without
	// re-deriving anything.
	matchedByConfirmed = "rule:title-brand-date+roster"
	matchedByProbable  = "rule:title-brand-date"
)

// Opts configures a run.
type Opts struct {
	DSN       string // catalog — REQUIRED
	GetchuDSN string // crawler staging — REQUIRED
	Apply     bool
	// ProbableToo writes the unconfirmed matches at link_kind=probable as well.
	// Off by default: a probable ref is inert until an adjudication wave looks
	// at it, so minting thousands of them is a decision, not a side effect.
	ProbableToo bool
	Limit       int
	// Audit writes every resolved candidate to this CSV path. The release-level
	// choice is the one claim roster confirmation cannot reach, so it is meant
	// to be read (see audit.go).
	Audit string
}

// Stats reports one run. Every crawled item lands in exactly one match bucket.
type Stats struct {
	Items            int // unanchored crawled products considered
	NoTitleMatch     int
	AmbiguousWork    int // one title+brand, several works → skipped
	AmbiguousRelease int // one work, several same-day releases → skipped
	DateDiffers      int
	Matched          int

	// The three counters below attribute the gain to the mechanism that earned
	// it, so a change in yield can be read rather than guessed at.
	MatchedAfterStrip  int // reached its work only once the edition marker came off
	NarrowedByPlatform int // same-day siblings cut by a contradicting platform/lang
	NarrowedByEdition  int // ...and then resolved by the edition marker itself
	EditionConflict    int // a box was chosen whose own title contradicts the product

	Testable    int // ...of which both sides have a roster
	Confirmed   int // ...and the rosters overlap → exact
	Unconfirmed int // rosters exist but do not overlap → probable
	NoRoster    int // one side has none → probable

	WrittenExact    int
	WrittenProbable int
	Conflict        int
	Errors          int
}

func (s Stats) String() string {
	return fmt.Sprintf(
		"items=%d matched=%d (no_title=%d ambiguous_work=%d ambiguous_release=%d date_differs=%d) | "+
			"via_strip=%d via_platform=%d via_edition=%d edition_conflict=%d | "+
			"testable=%d confirmed=%d unconfirmed=%d no_roster=%d | exact=%d probable=%d conflict=%d errors=%d",
		s.Items, s.Matched, s.NoTitleMatch, s.AmbiguousWork, s.AmbiguousRelease, s.DateDiffers,
		s.MatchedAfterStrip, s.NarrowedByPlatform, s.NarrowedByEdition, s.EditionConflict,
		s.Testable, s.Confirmed, s.Unconfirmed, s.NoRoster,
		s.WrittenExact, s.WrittenProbable, s.Conflict, s.Errors)
}

// Run matches, confirms, and (in apply mode) writes the anchors.
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

	source, err := getchuchars.SourceID(ctx, db)
	if err != nil {
		return nil, err
	}
	var medium int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).
		Scan(&medium).Error; err != nil {
		return nil, err
	}
	if medium == 0 {
		return nil, fmt.Errorf("catalog_medium has no galgame row")
	}

	anchored, err := loadAnchored(ctx, db, source)
	if err != nil {
		return nil, err
	}
	items, err := loadUnanchoredItems(ctx, gdb, anchored)
	if err != nil {
		return nil, err
	}
	rels, err := loadCatalogReleases(ctx, db, medium)
	if err != nil {
		return nil, err
	}

	cands, st := match(items, rels)
	st.Items = len(items)
	st.Matched = len(cands)
	if opts.Limit > 0 && opts.Limit < len(cands) {
		cands = cands[:opts.Limit]
	}
	slog.Info("getchu-title-refs matched", "items", st.Items, "releases", len(rels), "matched", st.Matched)

	confirmed, err := confirmByRoster(ctx, db, gdb, cands, &st)
	if err != nil {
		return nil, err
	}
	slog.Info("getchu-title-refs confirmed", "testable", st.Testable,
		"confirmed", st.Confirmed, "unconfirmed", st.Unconfirmed, "no_roster", st.NoRoster)

	if opts.Audit != "" {
		rows := make([]auditRow, 0, len(cands))
		for _, c := range cands {
			rows = append(rows, auditRow{
				GetchuID: c.GetchuID, GetchuTitle: c.GetchuTitle, Edition: c.Edition,
				WorkID: c.WorkID, ReleaseID: c.ReleaseID, ReleaseTitle: c.ReleaseTitle,
				Platform: c.Platform, Lang: c.Lang, Siblings: c.Siblings,
				Confirmed: confirmed[c.GetchuID],
			})
		}
		if err := writeAudit(opts.Audit, rows); err != nil {
			return &st, fmt.Errorf("write audit: %w", err)
		}
		slog.Info("getchu-title-refs audit written", "path", opts.Audit, "rows", len(rows))
	}

	for _, c := range cands {
		ok := confirmed[c.GetchuID]
		if !ok && !opts.ProbableToo {
			continue
		}
		kind, by := model.LinkKindProbable, matchedByProbable
		if ok {
			kind, by = model.LinkKindExact, matchedByConfirmed
		}
		if !opts.Apply {
			if ok {
				st.WrittenExact++
			} else {
				st.WrittenProbable++
			}
			continue
		}
		res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.CatalogExternalRef{
				EntityType: model.EntityTypeRelease,
				EntityID:   c.ReleaseID,
				SourceID:   source,
				ExternalID: c.GetchuID,
				LinkKind:   kind,
				MatchedBy:  by,
			})
		switch {
		case res.Error != nil:
			st.Errors++
			slog.Warn("write ref", "release", c.ReleaseID, "getchu", c.GetchuID, "err", res.Error)
		case res.RowsAffected == 0:
			st.Conflict++
		case ok:
			st.WrittenExact++
		default:
			st.WrittenProbable++
		}
	}
	slog.Info("getchu-title-refs done", "apply", opts.Apply, "result", st.String())
	return &st, nil
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
