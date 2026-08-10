// Package userplaytime folds the per-user playtime reports into the public
// per-source estimate — the one place where private data becomes a published
// number.
//
// It writes catalog_work_playtime rows under source `nextmoe` (id 19) and
// nothing else. That table is the SAME one the vndb and erogamescape lanes
// write, so the read faces need no new shape: `playtimes[]` simply grows a
// third entry, and a consumer that already renders "VNDB: 32h" renders
// "NextMoe: 30h" beside it for free.
//
// Three rules define what gets published, and each exists because of a way the
// number could otherwise lie:
//
//   - Only `finished` reports count. "How long to finish" and "how long I have
//     played so far" are different quantities; averaging them together answers
//     neither.
//   - One vote per USER, not per report. A user running two managers is one
//     person's evidence, so their clients are folded with MAX first (the same
//     fold the read face applies — see model.CatalogUserPlaytime).
//   - At least PlaytimeMinReporters distinct users, or the row is DELETED
//     rather than left behind. A stale median that two people defined is worse
//     than no median, and a work whose reporters drop below the threshold must
//     stop publishing rather than freeze at its last value.
//
// The statistic is the MEDIAN, via percentile_disc — a real observed value
// rather than an interpolated one, matching the source-native semantics the
// other two lanes carry (both publish community medians).
package userplaytime

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Stats is one run's outcome.
type Stats struct {
	// Eligible is how many works cleared the reporter threshold.
	Eligible int
	// Written / Unchanged split the eligible works by whether the upsert
	// actually moved anything — an unchanged re-run is the healthy steady
	// state, not a no-op to be surprised by.
	Written   int
	Unchanged int
	// Deleted is rows retired because their work fell below the threshold
	// (reports withdrawn, works merged away).
	Deleted int
	Errors  int
}

// Opts configures a run.
type Opts struct {
	// DB is the catalog connection. Set DSN instead to have Run open one.
	DB *gorm.DB
	// DSN opens the catalog database when DB is nil (the cmd's path).
	DSN string
	// Apply is the safety catch: false reports what WOULD change and writes
	// nothing, which is how every one of these jobs is run the first time.
	Apply bool
	// MinReporters overrides the published threshold; 0 uses the model's.
	MinReporters int
	// ExcludeClients are OAuth client ids whose reports do not count toward
	// the public number. This is the lever the design put in place of trying
	// to detect bad data statistically: a client that reports garbage is
	// identifiable, so it is excluded by name and everyone else's data stands.
	ExcludeClients []string
}

// candidate is one work's aggregated evidence.
type candidate struct {
	WorkID    int64
	Median    int
	Reporters int
}

// Run aggregates and (when Apply) publishes.
func Run(ctx context.Context, o Opts) (*Stats, error) {
	st := &Stats{}
	min := o.MinReporters
	if min <= 0 {
		min = model.PlaytimeMinReporters
	}
	if o.DB == nil {
		db, err := database.OpenJob(o.DSN)
		if err != nil {
			return nil, fmt.Errorf("open catalog: %w", err)
		}
		defer func() {
			if sqlDB, err := db.DB(); err == nil {
				sqlDB.Close()
			}
		}()
		o.DB = db
	}

	sourceID, err := nextmoeSourceID(ctx, o.DB)
	if err != nil {
		return nil, err
	}

	cands, err := aggregate(ctx, o.DB, min, o.ExcludeClients)
	if err != nil {
		return nil, err
	}
	st.Eligible = len(cands)

	keep := make(map[int64]bool, len(cands))
	for _, c := range cands {
		keep[c.WorkID] = true
		if !o.Apply {
			continue
		}
		changed, err := upsert(ctx, o.DB, c, sourceID)
		switch {
		case err != nil:
			slog.Warn("playtime aggregate upsert", "work", c.WorkID, "err", err)
			st.Errors++
		case changed:
			st.Written++
		default:
			st.Unchanged++
		}
	}

	stale, err := staleRows(ctx, o.DB, sourceID, keep)
	if err != nil {
		return nil, err
	}
	st.Deleted = len(stale)
	if o.Apply && len(stale) > 0 {
		if err := o.DB.WithContext(ctx).Exec(
			`DELETE FROM catalog_work_playtime WHERE source_id = ? AND work_id IN ?`,
			sourceID, stale).Error; err != nil {
			return nil, fmt.Errorf("delete stale playtime rows: %w", err)
		}
	}
	return st, nil
}

// nextmoeSourceID resolves the lane's registry id. A missing row is a hard
// failure rather than an insert: the seed owns the registry, and a job that
// mints its own source id would create a second lane nobody else knows about.
func nextmoeSourceID(ctx context.Context, db *gorm.DB) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_source WHERE key = 'nextmoe'`).Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("catalog_source has no 'nextmoe' row — run the seed first")
	}
	return id, nil
}

// aggregate is the whole statistic in one statement.
//
// per_user folds a user's clients with MAX (one person, one measurement); the
// outer query takes the median across users and counts them. The floor/ceiling
// live here rather than on the write path on purpose: the report is the user's
// own data and is stored verbatim, while its right to vote on a public number
// is this job's decision to make and to change later.
func aggregate(ctx context.Context, db *gorm.DB, min int, excludeClients []string) ([]candidate, error) {
	// An empty exclusion list must not become `client_id NOT IN ()`, which is
	// a syntax error; the sentinel keeps one statement instead of two.
	if len(excludeClients) == 0 {
		excludeClients = []string{""}
	}
	var out []candidate
	err := db.WithContext(ctx).Raw(`
		WITH per_user AS (
		    SELECT work_id, actor_uid, MAX(minutes) AS minutes
		      FROM catalog_user_playtime
		     WHERE status = ?
		       AND minutes >= ? AND minutes <= ?
		       AND client_id NOT IN ?
		     GROUP BY work_id, actor_uid
		)
		SELECT work_id,
		       percentile_disc(0.5) WITHIN GROUP (ORDER BY minutes)::int AS median,
		       COUNT(*)::int AS reporters
		  FROM per_user
		 GROUP BY work_id
		HAVING COUNT(*) >= ?
		 ORDER BY work_id`,
		model.PlaytimeStatusFinished,
		model.PlaytimeMinutesMin, model.PlaytimeMinutesMax,
		excludeClients, min).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("aggregate playtime: %w", err)
	}
	return out, nil
}

// upsert writes one work's estimate, reporting whether anything moved. The
// change detection is in the WHERE of the DO UPDATE, so an unchanged re-run
// touches no rows and bumps no updated_at — the same upsert discipline the
// other playtime lanes follow.
func upsert(ctx context.Context, db *gorm.DB, c candidate, sourceID int16) (bool, error) {
	res := db.WithContext(ctx).Exec(`
		INSERT INTO catalog_work_playtime (work_id, source_id, minutes, vote_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, now(), now())
		ON CONFLICT (work_id, source_id) DO UPDATE
		   SET minutes = EXCLUDED.minutes, vote_count = EXCLUDED.vote_count, updated_at = now()
		 WHERE catalog_work_playtime.minutes IS DISTINCT FROM EXCLUDED.minutes
		    OR catalog_work_playtime.vote_count IS DISTINCT FROM EXCLUDED.vote_count`,
		c.WorkID, sourceID, c.Median, c.Reporters)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// staleRows finds published rows whose work no longer clears the threshold.
// Retiring them is what keeps the public face honest as reports change: a
// median nobody supports any more must disappear, not linger.
func staleRows(ctx context.Context, db *gorm.DB, sourceID int16, keep map[int64]bool) ([]int64, error) {
	var published []int64
	if err := db.WithContext(ctx).Raw(
		`SELECT work_id FROM catalog_work_playtime WHERE source_id = ?`, sourceID).
		Scan(&published).Error; err != nil {
		return nil, err
	}
	var stale []int64
	for _, id := range published {
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	return stale, nil
}
