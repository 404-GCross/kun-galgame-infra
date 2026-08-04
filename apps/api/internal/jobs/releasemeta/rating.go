package releasemeta

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// The age-rating lane: every live work still at content_rating=0 asks its
// sources for a verdict in PRIORITY order — ① DLsite age_category (via the
// work's release anchors) ② a claimed work's wiki galgame.age_limit ③ Bangumi
// subject.nsfw=true as fallback. The FIRST source with a verdict wins:
// ownership, not strictest-across-sources arbitration (v1 ruling, spec-pinned).
// A verdict of all_ages (0) is explicit and final for that work — it writes
// nothing (0 over 0) but SUPPRESSES the lower-priority lanes, so e.g. a DLsite
// '1' (all ages) is never escalated by a wiki 'r18'.
//
// Verdict mapping (surveyed values → doc-14 tiers):
//
//	source   value        content_rating
//	dlsite   '3' adult    2 (r18)
//	dlsite   '2' r15      1 (sensitive)
//	dlsite   '1' general  0 (all_ages, explicit — no write)
//	wiki     'r18'        2 (r18)
//	wiki     'all'        0 (all_ages, explicit — no write)
//	bangumi  nsfw=true    2 (r18)
//	bangumi  nsfw=false   NO VERDICT (never an all-ages statement, doc 17 §6)
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

	// Source maps: catalog-side joins run unwindowed (single query each, the
	// windowed candidate loop just looks up); the cross-DB mirror reads are
	// restricted to the referenced keys.
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

	// CURATED OVERRIDE (03 定案 §0 line 2): works whose content_rating a human
	// edited through the engine are off limits. The lane's fill-empty guard is
	// NOT enough on its own — an editor who ruled a work all_ages leaves the
	// column at 0, which is exactly the state this job treats as "unset", so
	// without this check the one verdict a human made explicitly is the one the
	// importer would overwrite. Preloaded in one query for the whole window.
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
			continue // explicit all-ages: stays 0, nothing to write
		}
		st.RatingPlanned++
		w.fillRating(ctx, c.WorkID, rating, opts.Apply)
	}
	return nil
}

// decideRating walks the priority chain and returns the first verdict.
func decideRating(c ratingCandidate, dlAnchors map[int64]string, dlAges map[string]string,
	bgmNSFW map[int64]bool, st *Stats) (int16, string, string, bool) {

	// ① DLsite age_category via the work's release anchors.
	if wn, ok := dlAnchors[c.WorkID]; ok {
		switch dlAges[wn] { // "" covers both a missing mirror row and NULL age
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

	// ③ Bangumi nsfw=true fallback (false is never a verdict).
	if bgmNSFW[c.WorkID] {
		st.RatingBgmR18++
		return model.ContentRatingR18, "bangumi", "", true
	}
	return 0, "", "", false
}
