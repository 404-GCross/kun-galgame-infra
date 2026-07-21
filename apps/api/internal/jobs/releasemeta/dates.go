package releasemeta

import (
	"context"
	"fmt"
	"strconv"

	"gorm.io/gorm"
)

// runDlsiteDateLane fills empty release dates from the release's OWN DLsite
// anchor (SKU-level — the highest-precision source, hence first: every release
// it plans is marked in `planned` and never touched by the eg/bgm lanes).
func runDlsiteDateLane(ctx context.Context, db, dlDB *gorm.DB, w *writer, reg registry, opts Opts, maxYear int, planned map[int64]bool) error {
	cands, err := loadDlDateCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load dlsite date candidates: %w", err)
	}
	st := w.stats
	st.DlDateCandidates = len(cands)

	worknos := make([]string, 0, len(cands))
	for _, c := range cands {
		worknos = append(worknos, c.Workno)
	}
	mirror, err := loadDlsiteDates(ctx, dlDB, worknos)
	if err != nil {
		return fmt.Errorf("load dlsite mirror dates: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dt, ok := mirror[c.Workno]
		if !ok {
			st.DlDateMissingMirror++
			continue
		}
		if dt.y == nil { // regist_date IS NULL — DLsite never published a date
			st.DlDateNoRegist++
			continue
		}
		if *dt.y < minYear || *dt.y > maxYear { // 2099-style placeholder
			st.DlDateOutOfRange++
			continue
		}
		y := int16(*dt.y)
		m, d := int16(*dt.m), int16(*dt.d) // always present when y is (real timestamp)
		st.DlDatePlanned++
		planned[c.ReleaseID] = true
		collectDate(&st.DlDateSamples, DateSample{WorkID: c.WorkID, ReleaseID: c.ReleaseID, Ext: c.Workno, Y: y, M: &m, D: &d})
		w.fillDate(ctx, c.ReleaseID, y, &m, &d, opts.Apply, &st.DlDateFilled, &st.DlDateSkippedNonEmpty)
	}
	return nil
}

// runEgDateLane fills the remaining empty stub-release dates from EG
// work-level sellday. A release the dlsite lane already planned is counted
// covered — the value-level dlsite > eg precedence.
func runEgDateLane(ctx context.Context, db, egDB *gorm.DB, w *writer, reg registry, opts Opts, maxYear int, planned map[int64]bool) error {
	cands, err := loadEgDateCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load EG date candidates: %w", err)
	}
	st := w.stats
	st.EgDateCandidates = len(cands)

	idSet := map[int64]bool{}
	for _, c := range cands {
		for _, id := range c.EgIDs {
			if id >= 0 {
				idSet[id] = true
			}
		}
	}
	ids := make([]int64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	mirror, err := loadEgSelldays(ctx, egDB, ids)
	if err != nil {
		return fmt.Errorf("load EG mirror selldays: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if planned[c.ReleaseID] {
			st.EgDateCovered++
			continue
		}
		if len(c.EgIDs) > 1 {
			st.EgDateMultiAnchor += len(c.EgIDs) - 1
		}
		// The representative anchor: the lowest EG id present in the mirror
		// (ids are pre-sorted; dates need no vote-weighting — deterministic).
		sellday, chosen, found := "", int64(-1), false
		for _, id := range c.EgIDs {
			if s, ok := mirror[id]; ok {
				sellday, chosen, found = s, id, true
				break
			}
		}
		if !found {
			st.EgDateMissingMirror++
			continue
		}
		y, m, d, ok := parseFuzzyDate(sellday, maxYear)
		if !ok {
			st.EgDateBadDate++
			continue
		}
		st.EgDatePlanned++
		planned[c.ReleaseID] = true
		collectDate(&st.EgDateSamples, DateSample{WorkID: c.WorkID, ReleaseID: c.ReleaseID, Ext: strconv.FormatInt(chosen, 10), Y: y, M: m, D: d})
		w.fillDate(ctx, c.ReleaseID, y, m, d, opts.Apply, &st.EgDateFilled, &st.EgDateSkippedNonEmpty)
	}
	return nil
}

// runBgmDateLane fills the still-empty stub-release dates from the Bangumi
// subject's free-text date (partial dates legal — the trio is nullable).
func runBgmDateLane(ctx context.Context, db *gorm.DB, w *writer, reg registry, opts Opts, maxYear int, planned map[int64]bool) error {
	cands, err := loadBgmDateCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load bgm date candidates: %w", err)
	}
	st := w.stats
	st.BgmDateCandidates = len(cands)

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if planned[c.ReleaseID] {
			st.BgmDateCovered++
			continue
		}
		if c.Date == "" {
			st.BgmDateNoDate++
			continue
		}
		y, m, d, ok := parseFuzzyDate(c.Date, maxYear)
		if !ok {
			st.BgmDateBadDate++
			continue
		}
		if m == nil || d == nil {
			st.BgmDatePartial++
		}
		st.BgmDatePlanned++
		planned[c.ReleaseID] = true
		collectDate(&st.BgmDateSamples, DateSample{WorkID: c.WorkID, ReleaseID: c.ReleaseID, Ext: strconv.FormatInt(c.SubjectID, 10), Y: y, M: m, D: d})
		w.fillDate(ctx, c.ReleaseID, y, m, d, opts.Apply, &st.BgmDateFilled, &st.BgmDateSkippedNonEmpty)
	}
	return nil
}
