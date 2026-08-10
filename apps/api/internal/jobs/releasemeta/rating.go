package releasemeta

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

func runRatingLane(ctx context.Context, db, dlDB *gorm.DB, w *writer, reg registry, opts Opts) error {
	cands, err := loadRatingCandidates(ctx, db, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load rating candidates: %w", err)
	}
	st := w.stats
	st.RatingCandidates = len(cands)
	if len(cands) == 0 {
		return nil
	}

	dlAnchors, err := loadRatingDlsiteAnchors(ctx, db, reg)
	if err != nil {
		return fmt.Errorf("load rating dlsite anchors: %w", err)
	}
	bgmNSFW, err := loadRatingBgmNSFW(ctx, db, reg)
	if err != nil {
		return fmt.Errorf("load rating bgm nsfw: %w", err)
	}

	worknoSet := map[string]bool{}
	for _, c := range cands {
		if wn, ok := dlAnchors[c.WorkID]; ok {
			worknoSet[wn] = true
		}
	}
	worknos := make([]string, 0, len(worknoSet))
	for wn := range worknoSet {
		worknos = append(worknos, wn)
	}
	dlAges, err := loadDlsiteAges(ctx, dlDB, worknos)
	if err != nil {
		return fmt.Errorf("load dlsite mirror ages: %w", err)
	}

	ratingWorkIDs := make([]int64, 0, len(cands))
	for _, c := range cands {
		ratingWorkIDs = append(ratingWorkIDs, c.WorkID)
	}
	edited, err := editing.EditedEntities(ctx, db, editspec.TypeWork, editspec.FieldWorkContentRating, ratingWorkIDs)
	if err != nil {
		return fmt.Errorf("load curated content_rating overrides: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if edited[c.WorkID] {
			st.RatingCuratedOverride++
			continue
		}
		rating, source, ext, ok := decideRating(c, dlAnchors, dlAges, bgmNSFW, st)
		if !ok {
			st.RatingNoVerdict++
			continue
		}
		collectRating(&st.RatingSamples, RatingSample{WorkID: c.WorkID, Source: source, Ext: ext, Rating: rating})
		if rating == model.ContentRatingAllAges {
			continue
		}
		st.RatingPlanned++
		w.fillRating(ctx, c.WorkID, rating, opts.Apply)
	}
	return nil
}

func decideRating(c ratingCandidate, dlAnchors map[int64]string, dlAges map[string]string,
	bgmNSFW map[int64]bool, st *Stats) (int16, string, string, bool) {

	if wn, ok := dlAnchors[c.WorkID]; ok {
		switch dlAges[wn] {
		case "3":
			st.RatingDlR18++
			return model.ContentRatingR18, "dlsite", wn, true
		case "2":
			st.RatingDlSensitive++
			return model.ContentRatingSensitive, "dlsite", wn, true
		case "1":
			st.RatingDlAllAges++
			return model.ContentRatingAllAges, "dlsite", wn, true
		}
	}

	// ② was the wiki's editorial age_limit, read from galgame.age_limit for
	// claimed works. Wave 149 dropped that table, so the lane is gone rather
	// than merely unfilled. It had in fact stopped producing verdicts earlier:
	// its claim test pinned site == "galgame_wiki", the literal wave 161
	// renamed, so it silently matched nothing from then on. Removing it is
	// behaviour-neutral; the numbering below keeps ③ so the priority ladder
	// still reads against the spec.

	if bgmNSFW[c.WorkID] {
		st.RatingBgmR18++
		return model.ContentRatingR18, "bangumi", "", true
	}
	return 0, "", "", false
}
