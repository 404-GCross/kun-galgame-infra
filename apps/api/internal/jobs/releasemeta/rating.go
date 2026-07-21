package releasemeta

import (
	"context"
	"fmt"
	"strconv"

	"api/internal/platform/catalog/model"

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
func runRatingLane(ctx context.Context, db, dlDB, wikiDB *gorm.DB, w *writer, reg registry, opts Opts) error {
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
	var wikiIDs []int64
	for _, c := range cands {
		if wn, ok := dlAnchors[c.WorkID]; ok {
			worknoSet[wn] = true
		}
		if isWikiClaimed(c) {
			wikiIDs = append(wikiIDs, *c.ProductWorkID)
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
	wikiAges, err := loadWikiAgeLimits(ctx, wikiDB, wikiIDs)
	if err != nil {
		return fmt.Errorf("load wiki age limits: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rating, source, ext, ok := decideRating(c, dlAnchors, dlAges, wikiAges, bgmNSFW, st)
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
	wikiAges map[int64]string, bgmNSFW map[int64]bool, st *Stats) (int16, string, string, bool) {

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

	// ② claimed works: the wiki's editorial age_limit.
	if isWikiClaimed(c) {
		ext := strconv.FormatInt(*c.ProductWorkID, 10)
		switch age, ok := wikiAges[*c.ProductWorkID]; {
		case !ok: // claimed but no wiki row — fall through to ③
		case age == "r18":
			st.RatingWikiR18++
			return model.ContentRatingR18, "wiki", ext, true
		case age == "all":
			st.RatingWikiAllAges++
			return model.ContentRatingAllAges, "wiki", ext, true
		default:
			st.RatingWikiUnmapped++ // unexpected value — no verdict, fall through
		}
	}

	// ③ Bangumi nsfw=true fallback (false is never a verdict).
	if bgmNSFW[c.WorkID] {
		st.RatingBgmR18++
		return model.ContentRatingR18, "bangumi", "", true
	}
	return 0, "", "", false
}

// isWikiClaimed reports whether the work is claimed by the galgame wiki (the
// only claiming site live today) with a product row to look up.
func isWikiClaimed(c ratingCandidate) bool {
	return c.Site != nil && *c.Site == "galgame_wiki" && c.ProductWorkID != nil
}
