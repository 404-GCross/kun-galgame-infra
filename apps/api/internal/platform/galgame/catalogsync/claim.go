package catalogsync

import (
	"context"
	"strconv"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
)

// runClaim registers every preloaded wiki galgame as a claimed catalog_work
// via the ClaimWork service (never a raw insert — idempotency, second-identity
// prevention, and the created revision all come for free). Anchors are graded:
//   - vndb_id → exact (rule:wiki-vndb-id): our curated tier-0-treated column;
//   - audit-ok bid → exact (rule:wiki-bid-typed): the bid resolves to a
//     Bangumi subject that exists AND is type=4 (a game). Live-checked against
//     the Silver layer, not step 10's static list;
//   - a wrong-type / missing bid contributes NO anchor (it is an editorial
//     work-item, not an identity).
func (r *Reconciler) runClaim(ctx context.Context) (ClaimStats, error) {
	var stats ClaimStats

	// Read-prefetch which product works are already claimed, so a re-run skips
	// the ClaimWork transaction entirely (the second full pass writes nothing)
	// and dry-run can predict claimed_new without touching the DB.
	claimed, err := repository.LoadClaimedWorkIDs(r.catalog, siteGalgame)
	if err != nil {
		return stats, err
	}
	type4 := r.type4IDs()

	// Negative-knowledge gate for the exact anchors: an anchor a human has
	// rejected for the resolved work is dropped and counted. Fires only on the
	// adopt path (a fresh work carries no prior rejection); an already-claimed
	// game never reaches ClaimWork, so this is counted in apply only.
	rejectedAnchor := func(workID int64, sourceID int16, externalID string) bool {
		if r.rejected.Has(model.EntityTypeWork, workID, sourceID, externalID) {
			stats.SkippedRejected++
			return true
		}
		return false
	}

	for i := range r.games {
		g := &r.games[i]
		stats.Processed++

		anchors := make([]service.ExternalAnchor, 0, 2)
		if g.VNDBID != "" {
			stats.VNDBExact++
			anchors = append(anchors, service.ExternalAnchor{
				SourceID: sourceVNDB, ExternalID: g.VNDBID, MatchedBy: ruleWikiVNDB,
			})
		}
		if g.BID != nil {
			if _, ok := type4[*g.BID]; ok {
				stats.BIDExact++
				anchors = append(anchors, service.ExternalAnchor{
					SourceID: sourceBangumi, ExternalID: strconv.FormatInt(*g.BID, 10),
					MatchedBy: ruleWikiBID,
				})
			} else {
				stats.BIDSkippedBad++ // wrong-type or missing — never an anchor
			}
		}

		if _, ok := claimed[g.ID]; ok {
			stats.AlreadyClaimed++
			continue
		}
		if r.dryRun {
			stats.ClaimedNew++ // would create
			continue
		}

		_, created, err := r.works.ClaimWork(ctx, service.ClaimWorkParams{
			MediumID:       mediumGalgame,
			Site:           siteGalgame,
			ProductWorkID:  g.ID,
			DisplayName:    displayName(g),
			OLang:          "ja",
			ContentRating:  0, // all_ages is never inferred (doc 17 §6); wiki has no rating we map here
			Anchors:        anchors,
			RejectedAnchor: rejectedAnchor,
		})
		if err != nil {
			return stats, err
		}
		if created {
			stats.ClaimedNew++
		} else {
			stats.AlreadyClaimed++
		}
	}
	return stats, nil
}

// displayName picks the wiki main name for olang=ja: Japanese first, then the
// other locales as fallbacks so a work is never nameless.
func displayName(g *wikiGame) string {
	for _, n := range []string{g.NameJaJP, g.NameZhCN, g.NameEnUS, g.NameZhTW} {
		if n != "" {
			return n
		}
	}
	return ""
}
