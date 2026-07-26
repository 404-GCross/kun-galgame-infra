// Package workseries imports dlsite work series into the catalog_series /
// catalog_series_member entity pair (step 94, refs/proj/94 option B):
//
//   - anchors: game-domain dlsite RELEASE anchors (entity_type=6, source_id=
//     dlsite, link_kind=0) on galgame-medium works — workno → work_id.
//   - mirror: works.product_json series_id/series_name (--dlsite-dsn, the
//     separate mirror DB, the workplaytime/workratings second-pool precedent).
//   - materialization gate: only series with >=2 DISTINCT anchored works land
//     (a single-member series has no read value; it stays in the mirror).
//
// Refresh discipline (the step-93 edge rule — the mirror is the truth):
// series rows upsert with change detection (renames land in place); member
// rows insert-if-absent + stale delete; a series whose anchored membership
// drops below 2 is deleted entirely (members cascade via explicit delete).
// Dry-run default; single catalog DSN + mirror DSN, both explicit.
package workseries

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// seriesInfo accumulates one source series: its display name and the set of
// anchored catalog works that are members.
type seriesInfo struct {
	name    string
	members map[int64]struct{}
}

// Opts configures a run.
type Opts struct {
	Apply     bool
	DSN       string // catalog DB — REQUIRED
	DlsiteDSN string // dlsite mirror DB — REQUIRED
}

// Stats reports a run. Planned counters are identical in dry and apply.
type Stats struct {
	AnchoredWorks  int // distinct galgame works carrying a dlsite release anchor
	SeriesEligible int // series with >=2 distinct anchored works
	MembersWanted  int // memberships in eligible series

	SeriesCreated int
	SeriesRenamed int
	SeriesDeleted int // dropped below the gate / vanished from the mirror
	MembersAdded  int
	MembersStale  int // deleted

	Errors int
}

// Run executes the import.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" || opts.DlsiteDSN == "" {
		return nil, fmt.Errorf("both --dsn and --dlsite-dsn are required")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer closeGorm(db)
	dl, err := openGorm(opts.DlsiteDSN)
	if err != nil {
		return nil, fmt.Errorf("connect dlsite mirror: %w", err)
	}
	defer closeGorm(dl)

	var dlsiteSrc int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&dlsiteSrc).Error; err != nil {
		return nil, fmt.Errorf("resolve dlsite source: %w", err)
	}
	if dlsiteSrc == 0 {
		return nil, fmt.Errorf("registry not seeded (dlsite source missing)")
	}

	// ── anchors: workno → work_id (release-level, galgame medium) ───────────
	var anchors []struct {
		ExternalID string `gorm:"column:external_id"`
		WorkID     int64  `gorm:"column:work_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT r.external_id, rel.work_id
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id
		JOIN catalog_work w ON w.id = rel.work_id AND w.deleted_at IS NULL
		JOIN catalog_medium m ON m.id = w.medium_id AND m.key = 'galgame'
		WHERE r.entity_type = 6 AND r.source_id = ? AND r.link_kind = 0`, dlsiteSrc).
		Scan(&anchors).Error; err != nil {
		return nil, fmt.Errorf("load dlsite anchors: %w", err)
	}
	workByWorkno := make(map[string]int64, len(anchors))
	distinctWorks := map[int64]struct{}{}
	for _, a := range anchors {
		workByWorkno[a.ExternalID] = a.WorkID
		distinctWorks[a.WorkID] = struct{}{}
	}
	st := &Stats{AnchoredWorks: len(distinctWorks)}

	// ── mirror: series per anchored workno ──────────────────────────────────
	worknos := make([]string, 0, len(workByWorkno))
	for wn := range workByWorkno {
		worknos = append(worknos, wn)
	}
	sort.Strings(worknos)
	series := map[string]*seriesInfo{} // external series id → info
	for _, chunk := range chunkStr(worknos, 10000) {
		var rows []struct {
			Workno     string `gorm:"column:workno"`
			SeriesID   string `gorm:"column:series_id"`
			SeriesName string `gorm:"column:series_name"`
		}
		if err := dl.WithContext(ctx).Raw(`
			SELECT workno, product_json->>'series_id' AS series_id,
			       coalesce(product_json->>'series_name','') AS series_name
			FROM works
			WHERE workno IN ? AND coalesce(product_json->>'series_id','') <> ''`, chunk).
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("load mirror series: %w", err)
		}
		for _, r := range rows {
			si := series[r.SeriesID]
			if si == nil {
				si = &seriesInfo{members: map[int64]struct{}{}}
				series[r.SeriesID] = si
			}
			if si.name == "" && strings.TrimSpace(r.SeriesName) != "" {
				si.name = strings.TrimSpace(r.SeriesName)
			}
			si.members[workByWorkno[r.Workno]] = struct{}{}
		}
	}

	// Materialization gate: >=2 distinct anchored works.
	want := map[string]*seriesInfo{}
	for sid, si := range series {
		if len(si.members) >= 2 {
			if si.name == "" {
				si.name = sid // no name in the mirror — the external id is the fallback
			}
			want[sid] = si
			st.MembersWanted += len(si.members)
		}
	}
	st.SeriesEligible = len(want)

	// ── existing state ───────────────────────────────────────────────────────
	var existing []struct {
		ID          int64  `gorm:"column:id"`
		ExternalID  string `gorm:"column:external_id"`
		DisplayName string `gorm:"column:display_name"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT id, external_id, display_name FROM catalog_series
		WHERE source_id = ?`, dlsiteSrc).Scan(&existing).Error; err != nil {
		return nil, fmt.Errorf("load existing series: %w", err)
	}
	existingByExt := make(map[string]struct {
		id   int64
		name string
	}, len(existing))
	for _, e := range existing {
		existingByExt[e.ExternalID] = struct {
			id   int64
			name string
		}{e.ID, e.DisplayName}
	}

	// Plan series create/rename/delete.
	for sid, si := range want {
		e, ok := existingByExt[sid]
		switch {
		case !ok:
			st.SeriesCreated++
		case e.name != si.name:
			st.SeriesRenamed++
		}
	}
	for sid := range existingByExt {
		if _, ok := want[sid]; !ok {
			st.SeriesDeleted++
		}
	}

	if !opts.Apply {
		// Member add/stale planning needs series ids; for dry we approximate
		// via the existing rows only (new series contribute all members as
		// adds).
		planMembers(ctx, db, want, existingByExt, st)
		logDone(st, opts.Apply)
		return st, nil
	}

	// ── apply: series rows ───────────────────────────────────────────────────
	idByExt := make(map[string]int64, len(want))
	for sid, si := range want {
		e, ok := existingByExt[sid]
		if !ok {
			var id int64
			if err := db.WithContext(ctx).Raw(`INSERT INTO catalog_series (display_name, source_id, external_id)
				VALUES (?, ?, ?) RETURNING id`, si.name, dlsiteSrc, sid).Scan(&id).Error; err != nil {
				st.Errors++
				slog.Warn("series insert", "ext", sid, "err", err)
				continue
			}
			idByExt[sid] = id
			continue
		}
		idByExt[sid] = e.id
		if e.name != si.name {
			if err := db.WithContext(ctx).Exec(`UPDATE catalog_series SET display_name = ?, updated_at = now()
				WHERE id = ?`, si.name, e.id).Error; err != nil {
				st.Errors++
				slog.Warn("series rename", "ext", sid, "err", err)
			}
		}
	}
	// Delete series that fell out of the want set (members first).
	for sid, e := range existingByExt {
		if _, ok := want[sid]; ok {
			continue
		}
		if err := db.WithContext(ctx).Exec(`DELETE FROM catalog_series_member WHERE series_id = ?`, e.id).Error; err != nil {
			st.Errors++
			slog.Warn("series member cascade", "ext", sid, "err", err)
			continue
		}
		if err := db.WithContext(ctx).Exec(`DELETE FROM catalog_series WHERE id = ?`, e.id).Error; err != nil {
			st.Errors++
			slog.Warn("series delete", "ext", sid, "err", err)
		}
	}

	// ── apply: membership (insert-if-absent + stale delete per series) ──────
	for sid, si := range want {
		seriesID, ok := idByExt[sid]
		if !ok {
			continue // insert failed above; counted
		}
		var existingMembers []int64
		if err := db.WithContext(ctx).Raw(`SELECT work_id FROM catalog_series_member
			WHERE series_id = ?`, seriesID).Scan(&existingMembers).Error; err != nil {
			st.Errors++
			slog.Warn("load members", "ext", sid, "err", err)
			continue
		}
		have := make(map[int64]struct{}, len(existingMembers))
		for _, w := range existingMembers {
			have[w] = struct{}{}
		}
		for w := range si.members {
			if _, ok := have[w]; ok {
				continue
			}
			res := db.WithContext(ctx).Exec(`INSERT INTO catalog_series_member (series_id, work_id)
				VALUES (?, ?) ON CONFLICT (series_id, work_id) DO NOTHING`, seriesID, w)
			if res.Error != nil {
				st.Errors++
				slog.Warn("member insert", "ext", sid, "work", w, "err", res.Error)
				continue
			}
			if res.RowsAffected == 1 {
				st.MembersAdded++
			}
		}
		for _, w := range existingMembers {
			if _, ok := si.members[w]; ok {
				continue
			}
			if err := db.WithContext(ctx).Exec(`DELETE FROM catalog_series_member
				WHERE series_id = ? AND work_id = ?`, seriesID, w).Error; err != nil {
				st.Errors++
				slog.Warn("member stale delete", "ext", sid, "work", w, "err", err)
				continue
			}
			st.MembersStale++
		}
	}
	logDone(st, opts.Apply)
	return st, nil
}

// planMembers computes the dry-run member add/stale counts against the current
// DB state: a NEW series contributes all its members as adds; an existing one
// diffs against its current member rows (stale members of series that fell out
// of the want set are covered by SeriesDeleted, same as the apply path).
func planMembers(ctx context.Context, db *gorm.DB, want map[string]*seriesInfo, existingByExt map[string]struct {
	id   int64
	name string
}, st *Stats) {
	for sid, si := range want {
		e, ok := existingByExt[sid]
		if !ok {
			st.MembersAdded += len(si.members)
			continue
		}
		var existingMembers []int64
		if err := db.WithContext(ctx).Raw(`SELECT work_id FROM catalog_series_member
			WHERE series_id = ?`, e.id).Scan(&existingMembers).Error; err != nil {
			st.Errors++
			continue
		}
		have := make(map[int64]struct{}, len(existingMembers))
		for _, w := range existingMembers {
			have[w] = struct{}{}
		}
		for w := range si.members {
			if _, ok := have[w]; !ok {
				st.MembersAdded++
			}
		}
		for _, w := range existingMembers {
			if _, ok := si.members[w]; !ok {
				st.MembersStale++
			}
		}
	}
}

func logDone(st *Stats, apply bool) {
	slog.Info("workseries done", "apply", apply,
		"anchored_works", st.AnchoredWorks, "series_eligible", st.SeriesEligible, "members_wanted", st.MembersWanted,
		"series_created", st.SeriesCreated, "series_renamed", st.SeriesRenamed, "series_deleted", st.SeriesDeleted,
		"members_added", st.MembersAdded, "members_stale", st.MembersStale, "errors", st.Errors)
}

func chunkStr(in []string, size int) [][]string {
	var out [][]string
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
