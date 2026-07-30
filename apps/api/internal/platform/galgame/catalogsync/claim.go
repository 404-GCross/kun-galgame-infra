package catalogsync

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
	galgamemodel "api/internal/platform/galgame/model"
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
	claimed, err := repository.LoadClaimedWorkIDs(r.catalog, siteGalgame, r.writePathIDs...)
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

		// olang is the work's TRUE original language, mapped from the wiki's
		// product-locale spelling (144). It used to be a flat "ja" here, which is
		// how the whole registry ended up unable to answer "is this a JP/CN
		// title?" — and turned the public calendar's ja+zh family gate into a
		// no-op. Note this only decides a work MINTED by this claim: adopting an
		// existing unclaimed row deliberately keeps that row's own olang, which
		// its minting lane resolved from the upstream source directly (VNDB for
		// the anchored bulk) and is therefore at least as good as this mapping.
		olang, _ := model.MapWikiOLang(g.OriginalLanguage)
		workID, created, err := r.works.ClaimWork(ctx, service.ClaimWorkParams{
			MediumID:       mediumGalgame,
			Site:           siteGalgame,
			ProductWorkID:  g.ID,
			DisplayName:    displayName(g),
			OLang:          olang,
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
		claimed[g.ID] = workID // so the claim-state pass below covers it too
	}

	// Project the claim VISIBILITY state (R7) for every claimed work. Runs
	// last so it also covers the works this pass just minted.
	n, err := r.syncClaimStates(claimed)
	if err != nil {
		return stats, fmt.Errorf("sync claim_state: %w", err)
	}
	stats.ClaimStateWritten = n

	return stats, nil
}

// claimStateOf projects a wiki publication status onto the catalog-owned claim
// visibility vocabulary (R7). This is the ONLY place the two vocabularies meet:
// the wiki value itself never reaches the catalog contract.
//
//	published (0)              → live   — publicly visible on the wiki
//	vndb draft (2) / pending (3) → draft — exists, not published yet
//	banned (1) / declined (4)   → hidden — withdrawn; consumers show nothing
//
// An unknown future status is the CONSERVATIVE hidden rather than live: a
// status we do not understand must not be published on downstream sites.
func claimStateOf(wikiStatus int16) int16 {
	switch wikiStatus {
	case galgamemodel.GalgameStatusPublished:
		return model.ClaimStateLive
	case galgamemodel.GalgameStatusVNDBDraft, galgamemodel.GalgameStatusPending:
		return model.ClaimStateDraft
	default:
		return model.ClaimStateHidden
	}
}

// syncClaimStates writes catalog_work.claim_state for the given galgame →
// work id pairs, touching only the cells whose value differs (so a converged
// re-run writes nothing and the daily job stays free). Dry-run counts the
// cells that WOULD change.
//
// Idempotent re-projection by construction: the value is a pure function of
// the wiki row's current status, so running this over the whole population IS
// the backfill — there is no separate one-shot migration step to remember.
//
// Chunked like writeBackWorkIDs (its mirror image: that one drives the wiki
// pool from catalog ids, this one drives the catalog pool from wiki statuses),
// because the two pools may be different connections and the join therefore
// has to happen through a VALUES list.
func (r *Reconciler) syncClaimStates(claimed map[int64]int64) (int, error) {
	if len(claimed) == 0 {
		return 0, nil
	}
	type pair struct{ workID, state int64 }
	pairs := make([]pair, 0, len(claimed))
	for i := range r.games {
		g := &r.games[i]
		workID, ok := claimed[g.ID]
		if !ok || workID == 0 { // unclaimed, or a dry-run placeholder id
			continue
		}
		pairs = append(pairs, pair{workID: workID, state: int64(claimStateOf(g.Status))})
	}
	if len(pairs) == 0 {
		return 0, nil
	}

	const chunk = 1000
	changed := 0
	for start := 0; start < len(pairs); start += chunk {
		end := min(start+chunk, len(pairs))
		batch := pairs[start:end]
		var sb strings.Builder
		args := make([]any, 0, len(batch)*2)
		for i, p := range batch {
			if i > 0 {
				sb.WriteString(",")
			}
			if i == 0 {
				// Cast the first tuple so Postgres infers the derived columns'
				// types; claim_state is smallint, hence the narrower cast.
				sb.WriteString("(?::bigint,?::smallint)")
			} else {
				sb.WriteString("(?,?)")
			}
			args = append(args, p.workID, p.state)
		}
		values := sb.String()
		// IS DISTINCT FROM, not <>: the pre-projection value is NULL, which a
		// plain inequality would never match (and every row would look
		// converged forever).
		if r.dryRun {
			var n int64
			q := `SELECT count(*) FROM catalog_work AS w
			      JOIN (VALUES ` + values + `) AS v(wid, st) ON w.id = v.wid
			      WHERE w.claim_state IS DISTINCT FROM v.st`
			if err := r.catalog.Raw(q, args...).Scan(&n).Error; err != nil {
				return changed, err
			}
			changed += int(n)
			continue
		}
		q := `UPDATE catalog_work AS w SET claim_state = v.st
		      FROM (VALUES ` + values + `) AS v(wid, st)
		      WHERE w.id = v.wid AND w.claim_state IS DISTINCT FROM v.st`
		res := r.catalog.Exec(q, args...)
		if res.Error != nil {
			return changed, res.Error
		}
		changed += int(res.RowsAffected)
	}
	return changed, nil
}

// writeBackWorkIDs sets wiki galgame.catalog_work_id from the given galgame_id →
// catalog_work id pairs, touching only cells that are NULL or differ (so a
// re-run writes nothing once converged). Returns rows actually changed; in
// dry-run it counts the rows that WOULD change without writing. Chunked so the
// VALUES list stays bounded on the full ~62k-work backfill. The claims this
// mirrors are made HERE and nowhere else — by the nightly reconcile over the
// whole population, and (wave 146) by the write-path lane over the single
// galgame a publication just landed. Both run this same function, so the
// cross-face pointer has one writer either way.
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

// displayName picks the wiki main name: Japanese first, then the other locales
// as fallbacks so a work is never nameless. Deliberately NOT keyed on the work's
// olang — the display name is the catalog's denormalized fast-path and its
// preference order is frozen; changing it would rename existing rows on the next
// reconcile, which is a separate decision from getting olang right.
func displayName(g *wikiGame) string {
	for _, n := range []string{g.NameJaJP, g.NameZhCN, g.NameEnUS, g.NameZhTW} {
		if n != "" {
			return n
		}
	}
	return ""
}
