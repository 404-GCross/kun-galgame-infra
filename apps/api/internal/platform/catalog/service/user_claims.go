// user_claims.go — "the claims this user has worked on", the per-user read
// face the registry was missing (wave 157 P3, doc 156 §3.1 categories ① and ⑤).
//
// Why it is an AGGREGATE over catalog_claim_event rather than a column filter:
// a claim belongs to a SITE, never to a user (catalog_work carries site +
// product_work_id and nothing else about people — 03 定案 §9-3 keeps it that
// way on purpose, because the registry is an identity layer and its rows
// outlive any account). The only place a user appears is as the ACTOR of a
// transition. So "my submissions" is, precisely, "the works whose lifecycle I
// moved", and this file says exactly that in SQL instead of adding a
// denormalized owner column the registry would then have to keep true forever.
//
// The shape follows the wiki ListMine it replaces (identity + current state +
// the latest event's summary, most recent activity first), because that is
// what its consumers render: a card per submission, a state chip, and the
// decline reason inline so a rejected submitter needs no second call.
package service

import (
	"context"
	"time"

	"api/internal/platform/catalog/model"
)

// UserClaimItem is one work the user has acted on.
type UserClaimItem struct {
	WorkID        int64  `json:"work_id"`
	DisplayName   string `json:"display_name"`
	Site          string `json:"site"`
	ProductWorkID *int64 `json:"product_work_id"`
	// ClaimState is the work's CURRENT state on the public claim vocabulary —
	// the same projection every other face renders, so a consumer branches on
	// one word list everywhere.
	ClaimState string `json:"claim_state"`
	// The work's LATEST transition — by anyone, not just this user. That is
	// deliberate and it is the whole reason the summary is here: what a
	// submitter needs to see on their own submission is the moderator's verdict
	// and note, which is an event they did not cause. LastReason is where a
	// decline note surfaces, the one enrichment the wiki list it replaces had to
	// make a second query for.
	LastEventID   int64     `json:"last_event_id"`
	LastFromState *string   `json:"last_from_state"`
	LastToState   string    `json:"last_to_state"`
	LastReason    *string   `json:"last_reason"`
	LastActorUID  int64     `json:"last_actor_uid"`
	LastEventAt   time.Time `json:"last_event_at"`
	// FirstActedAt is when this user first touched the claim — "submitted on"
	// for the common case, and enough for a consumer to count today's
	// submissions off the first page without a dedicated stats endpoint.
	FirstActedAt time.Time `json:"first_acted_at"`
	// ActedCount is how many transitions this user caused on this work.
	ActedCount int `json:"acted_count"`
}

// UserClaimQuery is one page request.
type UserClaimQuery struct {
	ActorUID int64
	// Site restricts to one tenant's claims (empty = every site the user acted
	// on). ClaimStates is the public vocabulary filter — `pending,declined` is
	// the wiki "my submissions" default, `live` is "published by me".
	Site        string
	ClaimStates []string
	// Before is the exclusive cursor: return works whose latest event id is
	// smaller. 0 = the first page. It cursors on that id rather than on a
	// timestamp or an offset because it is unique per work (a work's latest
	// event belongs to that work alone), so paging can neither skip nor
	// duplicate a row while the list is being written to.
	Before int64
	Limit  int
}

// ClaimsByActor returns one page plus the total number of matching works. The
// total is deliberately part of this face: it is what makes a dedicated
// per-user stats endpoint unnecessary — "how many works has this user
// published" is this call with claim_states=[live] and limit=1.
func (s *ClaimLifecycleService) ClaimsByActor(ctx context.Context, q UserClaimQuery) ([]UserClaimItem, int64, error) {
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	stateGate, stateArgs := claimStateWhere(q.ClaimStates)
	if stateGate != "" {
		stateGate = " AND " + stateGate
	}

	// The work is joined (not left-joined) and soft-deleted rows drop out: this
	// is a product's list of its own submissions, and a work that no longer
	// exists is not one. The event log keeping its history is a separate
	// promise, served by the feed.
	const from = `
		FROM (
		    SELECT e.work_id,
		           min(e.created_at) AS first_acted_at,
		           count(*)          AS acted_count
		    FROM catalog_claim_event e
		    WHERE e.actor_uid = ? AND (? = '' OR e.site = ?)
		    GROUP BY e.work_id
		) m
		JOIN catalog_work w ON w.id = m.work_id AND w.deleted_at IS NULL`

	baseArgs := func() []any {
		args := []any{q.ActorUID, q.Site, q.Site}
		return append(args, stateArgs...)
	}

	var total int64
	if err := s.db.WithContext(ctx).
		Raw(`SELECT count(*)`+from+` WHERE true`+stateGate, baseArgs()...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	// The summary row comes from a LATERAL "newest event of this work" rather
	// than from the aggregate: an aggregate can produce max(id) but not the
	// from/to/reason that belong to that one row, and picking them with separate
	// aggregates is how a summary ends up describing an event that never
	// happened.
	var rows []struct {
		WorkID        int64
		DisplayName   string
		Site          *string
		ProductWorkID *int64
		ClaimState    *int16
		LastEventID   int64
		FromState     *int16
		ToState       int16
		Reason        *string
		LastActorUID  int64
		LastEventAt   time.Time
		FirstActedAt  time.Time
		ActedCount    int
	}
	args := append(baseArgs(), q.Before, q.Before, limit)
	if err := s.db.WithContext(ctx).Raw(`
		SELECT w.id AS work_id, w.display_name, w.site, w.product_work_id, w.claim_state,
		       m.first_acted_at, m.acted_count,
		       le.id AS last_event_id, le.from_state, le.to_state, le.reason,
		       le.actor_uid AS last_actor_uid, le.created_at AS last_event_at`+
		from+`
		JOIN LATERAL (
		    SELECT le.id, le.from_state, le.to_state, le.reason, le.actor_uid, le.created_at
		    FROM catalog_claim_event le
		    WHERE le.work_id = m.work_id
		    ORDER BY le.id DESC
		    LIMIT 1
		) le ON true
		WHERE true`+stateGate+`
		  AND (? <= 0 OR le.id < ?)
		ORDER BY le.id DESC
		LIMIT ?`, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	out := make([]UserClaimItem, 0, len(rows))
	for _, r := range rows {
		item := UserClaimItem{
			WorkID: r.WorkID, DisplayName: r.DisplayName,
			ProductWorkID: r.ProductWorkID,
			ClaimState:    model.ClaimStateKey(r.Site, r.ProductWorkID, r.ClaimState),
			LastEventID:   r.LastEventID, LastToState: stateKeyOf(r.ToState),
			LastReason: r.Reason, LastActorUID: r.LastActorUID, LastEventAt: r.LastEventAt,
			FirstActedAt: r.FirstActedAt, ActedCount: r.ActedCount,
		}
		if r.Site != nil {
			item.Site = *r.Site
		}
		if r.FromState != nil {
			from := stateKeyOf(*r.FromState)
			item.LastFromState = &from
		}
		out = append(out, item)
	}
	return out, total, nil
}
