package catalogsync

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

	// Backfill the wiki cross-face pointer (galgame.catalog_work_id, step 34 T1)
	// for the already-claimed set FIRST — before the claim loop that may mint new
	// works. A claim conflict aborts the loop, but the already-claimed pointers
	// (the bulk) must still land: the claims are maintained here, not on the
	// galgame write path, so this reconcile is their only writer. Idempotent —
	// a converged re-run changes nothing. Newly-minted works are backfilled
	// individually as each ClaimWork succeeds below.
	stats.WorkIDCovered = len(claimed)
	backfilled, err := r.writeBackWorkIDs(claimed)
	if err != nil {
		return stats, fmt.Errorf("write back catalog_work_id: %w", err)
	}
	stats.WorkIDBackfilled = backfilled

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

		workID, created, err := r.works.ClaimWork(ctx, service.ClaimWorkParams{
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
		// Backfill this freshly-minted work's pointer immediately (cheap — only
		// the handful of newly-claimed games reach here; the bulk was done above).
		n, err := r.writeBackWorkIDs(map[int64]int64{g.ID: workID})
		if err != nil {
			return stats, fmt.Errorf("write back catalog_work_id: %w", err)
		}
		stats.WorkIDBackfilled += n
		stats.WorkIDCovered++
		if created {
			stats.ClaimedNew++
		} else {
			stats.AlreadyClaimed++
		}
	}

	return stats, nil
}

// writeBackWorkIDs sets wiki galgame.catalog_work_id from the given galgame_id →
// catalog_work id pairs, touching only cells that are NULL or differ (so a
// re-run writes nothing once converged). Returns rows actually changed; in
// dry-run it counts the rows that WOULD change without writing. Chunked so the
// VALUES list stays bounded on the full ~62k-work backfill. The claims this
// mirrors are owned by reconcile-galgame-works, so this is the wiki's single
// source for the cross-face pointer (there is no live catalog claim on the
// galgame write path).
func (r *Reconciler) writeBackWorkIDs(pairs map[int64]int64) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(pairs))
	for id := range pairs {
		ids = append(ids, id)
	}
	const chunk = 1000
	changed := 0
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		batch := ids[start:end]
		// Build a VALUES list of (galgame_id, work_id) pairs; the first tuple is
		// cast so Postgres infers bigint for the derived columns.
		var sb strings.Builder
		args := make([]any, 0, len(batch)*2)
		for i, id := range batch {
			if i > 0 {
				sb.WriteString(",")
			}
			if i == 0 {
				sb.WriteString("(?::bigint,?::bigint)")
			} else {
				sb.WriteString("(?,?)")
			}
			args = append(args, id, pairs[id])
		}
		values := sb.String()
		if r.dryRun {
			var n int64
			q := `SELECT count(*) FROM galgame AS g
			      JOIN (VALUES ` + values + `) AS v(gid, wid) ON g.id = v.gid
			      WHERE g.catalog_work_id IS NULL OR g.catalog_work_id <> v.wid`
			if err := r.wiki.Raw(q, args...).Scan(&n).Error; err != nil {
				return changed, err
			}
			changed += int(n)
			continue
		}
		q := `UPDATE galgame AS g SET catalog_work_id = v.wid
		      FROM (VALUES ` + values + `) AS v(gid, wid)
		      WHERE g.id = v.gid AND (g.catalog_work_id IS NULL OR g.catalog_work_id <> v.wid)`
		res := r.wiki.Exec(q, args...)
		if res.Error != nil {
			return changed, res.Error
		}
		changed += int(res.RowsAffected)
	}
	return changed, nil
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
