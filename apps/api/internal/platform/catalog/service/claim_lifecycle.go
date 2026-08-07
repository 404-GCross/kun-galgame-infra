// claim_lifecycle.go — the registry's claim lifecycle: eight SEMANTIC actions
// over catalog_work.claim_state, each of which appends one immutable
// catalog_claim_event row in the same transaction (03 定案 §3, wave 155 W2).
//
// Why actions and not a PATCH of the column: the legal moves are a small graph
// and the interesting facts are WHO moved it and WHY. A field patch expresses
// neither, and would let any caller write any state — which is how the wiki's
// status column became a vocabulary nobody could reason about. Here an illegal
// move is a 409 that names the current state, the moderation metadata rides the
// event row (no new columns), and the audit log is a byproduct of the write
// rather than something a caller must remember to emit.
//
// The event table is also the HANDOVER LEDGER (wave 155 ruling 1): the nightly
// wiki claim projector skips any work with an event row, so the first action
// through this service is the moment that work's lifecycle authority moves from
// galgame.status to the registry. Nothing else marks the handover, and nothing
// needs to.
package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// ClaimAction is the semantic vocabulary. These strings are wire values (path
// segments) and are eternal.
type ClaimAction string

const (
	// Owner actions — the product site that holds (or is taking) the claim.
	ClaimActionClaim    ClaimAction = "claim"
	ClaimActionSubmit   ClaimAction = "submit"
	ClaimActionPublish  ClaimAction = "publish"
	ClaimActionWithdraw ClaimAction = "withdraw"
	// Review actions — curator authority (catalog.review).
	ClaimActionApprove ClaimAction = "approve"
	ClaimActionDecline ClaimAction = "decline"
	ClaimActionBan     ClaimAction = "ban"
	ClaimActionUnban   ClaimAction = "unban"
)

// ClaimActions is the closed vocabulary, in workflow order. Pinned by test.
var ClaimActions = []ClaimAction{
	ClaimActionClaim, ClaimActionSubmit, ClaimActionPublish, ClaimActionWithdraw,
	ClaimActionApprove, ClaimActionDecline, ClaimActionBan, ClaimActionUnban,
}

// ReviewActions are the four a curator performs. The owning site may do the
// other four on its own claims.
var ReviewActions = map[ClaimAction]bool{
	ClaimActionApprove: true, ClaimActionDecline: true,
	ClaimActionBan: true, ClaimActionUnban: true,
}

// transitions is the whole state machine, expressed on the PUBLIC claim
// vocabulary (model.ClaimStateKey) rather than the raw column, so it reads the
// way the contract does and a NULL column (= live) needs no special case.
//
// `to` is empty for unban alone: its target is derived from the event log
// (03 §3 / wave 155 ruling 4 — the prior state is already recorded, so a
// prior_state column would be a second copy of it that could disagree).
var transitions = map[ClaimAction]struct {
	from []string
	to   string
}{
	ClaimActionClaim:    {from: []string{model.ClaimStateKeyNone}, to: model.ClaimStateKeyDraft},
	ClaimActionSubmit:   {from: []string{model.ClaimStateKeyDraft, model.ClaimStateKeyDeclined}, to: model.ClaimStateKeyPending},
	ClaimActionPublish:  {from: []string{model.ClaimStateKeyDraft}, to: model.ClaimStateKeyLive},
	ClaimActionWithdraw: {from: []string{model.ClaimStateKeyLive, model.ClaimStateKeyPending}, to: model.ClaimStateKeyDraft},
	ClaimActionApprove:  {from: []string{model.ClaimStateKeyPending}, to: model.ClaimStateKeyLive},
	ClaimActionDecline:  {from: []string{model.ClaimStateKeyPending}, to: model.ClaimStateKeyDeclined},
	ClaimActionBan: {from: []string{
		model.ClaimStateKeyLive, model.ClaimStateKeyDraft,
		model.ClaimStateKeyPending, model.ClaimStateKeyDeclined,
	}, to: model.ClaimStateKeyHidden},
	ClaimActionUnban: {from: []string{model.ClaimStateKeyHidden}},
}

// claimStateValue maps a public key back onto the stored column. Only the five
// column-backed keys appear here — `none` is the absence of a claim, which no
// action can transition INTO (a claim is never un-anchored; that is a merge).
var claimStateValue = map[string]int16{
	model.ClaimStateKeyLive:     model.ClaimStateLive,
	model.ClaimStateKeyDraft:    model.ClaimStateDraft,
	model.ClaimStateKeyPending:  model.ClaimStatePending,
	model.ClaimStateKeyDeclined: model.ClaimStateDeclined,
	model.ClaimStateKeyHidden:   model.ClaimStateHidden,
}

// TransitionRule exposes one action's legal source states (and its fixed
// target, empty for unban) so a face can reject an unknown action before it
// opens a transaction, and render the allowed moves in a doc or an error.
func TransitionRule(a ClaimAction) (from []string, known bool) {
	rule, ok := transitions[a]
	return rule.from, ok
}

// ClaimTransitionError is an illegal move: the action exists, the work exists,
// but not from where it currently stands. Carries the current state so the
// caller can render it without a second read (409).
type ClaimTransitionError struct {
	Action  ClaimAction
	Current string
	Allowed []string
}

func (e *ClaimTransitionError) Error() string {
	return fmt.Sprintf("cannot %s a claim in state %q (allowed from: %s)",
		e.Action, e.Current, strings.Join(e.Allowed, ", "))
}

// ClaimOwnershipError is a caller acting on another site's claim.
type ClaimOwnershipError struct {
	WorkID     int64
	OwningSite string
}

func (e *ClaimOwnershipError) Error() string {
	return fmt.Sprintf("work %d is claimed by site %q", e.WorkID, e.OwningSite)
}

// ClaimNotOwnedError is a caller moving a claim that ALREADY BELONGS TO SOMEBODY
// ELSE. It is the PERSONAL half of the ownership question — ClaimOwnershipError
// above is the tenant half — and it exists because the two faces know different
// things: an S2S backend asserts a uid it authenticated itself and is believed,
// while a user token IS the uid, so the registry can and must check it against
// the owner it holds. Only a request that asks for the check
// (ClaimActionParams.RequireOwner) can meet this error.
//
// An UNOWNED claim is deliberately NOT this error: see ownedActions below.
type ClaimNotOwnedError struct {
	WorkID   int64
	ActorUID int64
	OwnerUID int64
}

func (e *ClaimNotOwnedError) Error() string {
	return fmt.Sprintf("work %d belongs to user %d, not user %d", e.WorkID, e.OwnerUID, e.ActorUID)
}

// ownedActions are the three owner actions that move an EXISTING claim, and
// therefore the three RequireOwner is checked against. `claim` is absent by
// construction: it is the birth of a claim on an UNANCHORED work, so there is
// no owner yet to compare the caller to — the same reason the tenancy check
// exempts it.
//
// The rule these three meet is TAKE-IT-IF-IT-IS-FREE, not refuse-unless-owned:
//
//   - a foreign owner refuses (ClaimNotOwnedError);
//   - NO owner ALLOWS, and stamps the caller as the owner in the same UPDATE.
//
// The second half is not a leniency, it is the product's main gesture. The bulk
// of the registry is machine-imported mirror stock sitting in `draft` with a
// NULL owner (prod, 2026-08: 53,486 such kungal drafts, against zero NULL-owner
// rows in pending/declined), and the forum wizard's "claim this game" is
// exactly a person calling `publish` on one of them. Refusing an ownerless row
// would have 403'd the entire feature across that whole stock while looking
// like a security check. So the first human to move a free claim becomes its
// owner, on the same write-once terms as the `claim` action and the submission
// mint — and from that moment the first bullet fences everyone else out.
//
// Concurrency needs nothing extra: the FOR UPDATE above serializes two
// claimants, and the loser meets the transition rule (the state it wanted to
// move from is gone) rather than a half-applied stamp.
var ownedActions = map[ClaimAction]bool{
	ClaimActionSubmit: true, ClaimActionPublish: true, ClaimActionWithdraw: true,
}

// ErrClaimReasonRequired: decline must say why (the reason reaches the
// submitter through the event feed, and a decline nobody can act on is the
// wiki's worst moderation habit).
var ErrClaimReasonRequired = errors.New("a reason is required")

// ErrClaimTargetRequired: claim must name the product-side work it anchors.
var ErrClaimTargetRequired = errors.New("site and product_work_id are required to claim")

// ClaimActionParams is one action request.
type ClaimActionParams struct {
	WorkID int64
	Action ClaimAction
	// Site is the caller's tenant. On the S2S face it is the client's bound
	// site and is enforced against the work's owner; the staff face leaves it
	// empty (a curator acts across tenants).
	Site string
	// ProductWorkID is the anchor `claim` writes. Ignored by every other action.
	ProductWorkID *int64
	ActorUID      int64
	Reason        string
	// RequireOwner asks the service to settle ownership against ActorUID for the
	// three owner actions (wave 179): another person's claim is refused, a FREE
	// claim is adopted (see ownedActions). It is set by the USER-token face
	// alone, where the uid is the token's rather than an assertion. The S2S and
	// staff faces leave it false — a product backend asserting a uid it
	// authenticated is trusted with its own tenant's claims exactly as before,
	// and in particular does not stamp an owner by moving a claim.
	RequireOwner bool
}

// ClaimActionResult is what happened, in the vocabulary the wire speaks.
type ClaimActionResult struct {
	WorkID  int64   `json:"work_id"`
	From    *string `json:"from_state"`
	To      string  `json:"to_state"`
	EventID int64   `json:"event_id"`
}

// ClaimLifecycleService owns the action transaction and the event feed.
type ClaimLifecycleService struct {
	db *gorm.DB
}

func NewClaimLifecycleService(db *gorm.DB) *ClaimLifecycleService {
	return &ClaimLifecycleService{db: db}
}

// Act performs one action. Everything — the row lock, the state write, the
// event append and the changes-feed touch — happens in ONE transaction, so a
// consumer of the event feed can never observe a transition whose state is not
// yet visible on the work (the failure mode that made the wiki's message table
// and its status column drift apart).
func (s *ClaimLifecycleService) Act(ctx context.Context, p ClaimActionParams) (*ClaimActionResult, error) {
	rule, known := transitions[p.Action]
	if !known {
		return nil, fmt.Errorf("unknown claim action %q", p.Action)
	}
	if p.Action == ClaimActionDecline && strings.TrimSpace(p.Reason) == "" {
		return nil, ErrClaimReasonRequired
	}
	if p.Action == ClaimActionClaim && (p.Site == "" || p.ProductWorkID == nil) {
		return nil, ErrClaimTargetRequired
	}

	var out ClaimActionResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var work struct {
			ID            int64
			Site          *string
			ProductWorkID *int64
			ClaimState    *int16
			OwnerUserID   *int64
		}
		// FOR UPDATE: two concurrent actions on one claim must serialize, or the
		// event log would record a transition from a state that never was.
		if err := tx.Raw(`SELECT id, site, product_work_id, claim_state, owner_user_id FROM catalog_work
		                  WHERE id = ? AND deleted_at IS NULL FOR UPDATE`, p.WorkID).
			Scan(&work).Error; err != nil {
			return err
		}
		if work.ID == 0 {
			return gorm.ErrRecordNotFound
		}

		current := model.ClaimStateKey(work.Site, work.ProductWorkID, work.ClaimState)
		if !slices.Contains(rule.from, current) {
			return &ClaimTransitionError{Action: p.Action, Current: current, Allowed: rule.from}
		}
		// Tenancy: only the owning site moves its own claim. `claim` is exempt —
		// there is no owner yet, which is the point of the action.
		if p.Site != "" && p.Action != ClaimActionClaim && work.Site != nil && *work.Site != p.Site {
			return &ClaimOwnershipError{WorkID: p.WorkID, OwningSite: *work.Site}
		}
		// Personal ownership, when the face asked for it: take it if it is free,
		// refuse it if it is somebody else's (see ownedActions). Inside the FOR
		// UPDATE so it cannot race the stamping a concurrent action performs —
		// whatever owner this transaction sees is the one that will still be
		// there when it commits.
		takeOwnership := false
		if p.RequireOwner && ownedActions[p.Action] {
			switch {
			case work.OwnerUserID != nil && *work.OwnerUserID != p.ActorUID:
				return &ClaimNotOwnedError{
					WorkID: p.WorkID, ActorUID: p.ActorUID, OwnerUID: *work.OwnerUserID,
				}
			case work.OwnerUserID == nil && p.ActorUID > 0:
				// A free claim, and a real person moving it: they become its
				// owner. The uid is checked for >0 only to keep the invariant
				// "no row is ever owned by user 0" — the face that sets
				// RequireOwner resolves a positive uid before it gets here.
				takeOwnership = true
			}
		}

		target := rule.to
		if p.Action == ClaimActionUnban {
			prior, err := s.priorState(tx, p.WorkID)
			if err != nil {
				return err
			}
			target = prior
		}
		updates := map[string]any{"claim_state": claimStateValue[target]}
		// The first human to move a free claim adopts it, in the SAME statement
		// that moves the state — so there is no window in which the row is
		// published but still ownerless. Write-once holds by construction: this
		// branch is only reachable while OwnerUserID is nil.
		if takeOwnership {
			updates["owner_user_id"] = p.ActorUID
		}
		eventSite := p.Site
		if work.Site != nil && *work.Site != "" {
			eventSite = *work.Site
		}
		if p.Action == ClaimActionClaim {
			updates["site"] = p.Site
			updates["product_work_id"] = *p.ProductWorkID
			eventSite = p.Site
			// Ownership is WRITE-ONCE (wave 178), and this is its second and last
			// writer beside the submission mint: `claim` is the birth of a claim
			// (the transition with no prior state), so the claimant is the entry's
			// creator in exactly the sense the forum's SetCreatorIfUnset means it.
			// A row that already carries an owner keeps it — a later re-claim never
			// re-attributes someone else's entry — and a machine claim (uid 0)
			// stamps nothing rather than stamping 0. The FOR UPDATE above makes the
			// read-then-write safe against a concurrent claimant.
			if work.OwnerUserID == nil && p.ActorUID > 0 {
				updates["owner_user_id"] = p.ActorUID
			}
		}
		// A model Update also bumps updated_at, which IS the changes-feed touch
		// (repository.TouchWorks does exactly that and nothing more) — so the
		// action is visible to /v1/catalog/changes without a second statement.
		res := tx.Model(&model.CatalogWork{}).Where("id = ?", p.WorkID).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		row := claimEventRow{
			WorkID:   p.WorkID,
			To:       claimStateValue[target],
			ActorUID: p.ActorUID,
			Site:     eventSite,
			Reason:   p.Reason,
		}
		// from_state is NULL only for the birth of a claim — the one transition
		// with no prior state to record (model/claimevent.go).
		if p.Action != ClaimActionClaim {
			prior := claimStateValue[current]
			row.From = &prior
		}
		eventID, err := appendClaimEvent(tx, row)
		if err != nil {
			return err
		}

		out = ClaimActionResult{WorkID: p.WorkID, To: target, EventID: eventID}
		if p.Action != ClaimActionClaim {
			from := current
			out.From = &from
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// claimEventRow is one append to the transition ledger. From is nil for the
// birth of a claim; Reason is trimmed and dropped when empty.
type claimEventRow struct {
	WorkID   int64
	From     *int16
	To       int16
	ActorUID int64
	Site     string
	Reason   string
}

// appendClaimEvent is the ONLY writer of catalog_claim_event (wave 162): the
// eight semantic actions and the submission mint both go through here. A second
// writer would be a second definition of "this work's lifecycle authority has
// moved to the registry" (155 ruling 1), and the projector that reads this
// table as a handover ledger cannot tell two definitions apart.
func appendClaimEvent(tx *gorm.DB, row claimEventRow) (int64, error) {
	event := model.CatalogClaimEvent{
		WorkID:    row.WorkID,
		FromState: row.From,
		ToState:   row.To,
		ActorUID:  row.ActorUID,
		Site:      row.Site,
	}
	if reason := strings.TrimSpace(row.Reason); reason != "" {
		event.Reason = &reason
	}
	if err := tx.Create(&event).Error; err != nil {
		return 0, err
	}
	return event.ID, nil
}

// priorState answers "what was this claim before it was hidden": the from_state
// of its most recent transition INTO hidden. Falls back to live when there is
// none (a work hidden before the event log existed, or hidden from a state that
// was itself the birth event) and refuses to return hidden, which would make
// unban a no-op.
func (s *ClaimLifecycleService) priorState(tx *gorm.DB, workID int64) (string, error) {
	var from *int16
	if err := tx.Raw(`SELECT from_state FROM catalog_claim_event
	                  WHERE work_id = ? AND to_state = ? ORDER BY id DESC LIMIT 1`,
		workID, model.ClaimStateHidden).Scan(&from).Error; err != nil {
		return "", err
	}
	if from == nil {
		return model.ClaimStateKeyLive, nil
	}
	site, product := "unban", int64(0)
	key := model.ClaimStateKey(&site, &product, from)
	if key == model.ClaimStateKeyHidden || key == model.ClaimStateKeyNone {
		return model.ClaimStateKeyLive, nil
	}
	return key, nil
}

// ClaimEventItem is one feed row: the event plus the CURRENT identity of the
// work it belongs to, so a consumer can route it without a second call.
type ClaimEventItem struct {
	ID            int64     `json:"id"`
	WorkID        int64     `json:"work_id"`
	FromState     *string   `json:"from_state"`
	ToState       string    `json:"to_state"`
	ActorUID      int64     `json:"actor_uid"`
	Reason        *string   `json:"reason"`
	Site          string    `json:"site"`
	ProductWorkID *int64    `json:"product_work_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// EventsSince is the S2S cursor feed: ascending by the monotonic id, `since` is
// exclusive. Ascending and id-keyed for the same reason the two wiki feeds it
// replaces are: a consumer stores one integer and can never skip a row, no
// matter how many rows share a timestamp.
//
// actorUID (wave 157, 0 = no filter) narrows the same feed to one user's own
// transitions. It is the cheap half of the per-user need: a product rendering
// "what happened to my submissions" reads this, and a product rendering "my
// submissions" reads ClaimsByActor.
func (s *ClaimLifecycleService) EventsSince(ctx context.Context, since int64, limit int, site string, actorUID int64) ([]ClaimEventItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows []struct {
		ID            int64
		WorkID        int64
		FromState     *int16
		ToState       int16
		ActorUID      int64
		Reason        *string
		Site          string
		CreatedAt     time.Time
		WorkSite      *string
		ProductWorkID *int64
	}
	q := s.db.WithContext(ctx).Table("catalog_claim_event AS e").
		Select(`e.id, e.work_id, e.from_state, e.to_state, e.actor_uid, e.reason, e.site,
		        e.created_at, w.site AS work_site, w.product_work_id`).
		Joins("LEFT JOIN catalog_work w ON w.id = e.work_id").
		Where("e.id > ?", since)
	if site != "" {
		q = q.Where("e.site = ?", site)
	}
	if actorUID > 0 {
		q = q.Where("e.actor_uid = ?", actorUID)
	}
	if err := q.Order("e.id ASC").Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ClaimEventItem, 0, len(rows))
	for _, r := range rows {
		item := ClaimEventItem{
			ID: r.ID, WorkID: r.WorkID, ActorUID: r.ActorUID, Reason: r.Reason,
			Site: r.Site, ProductWorkID: r.ProductWorkID, CreatedAt: r.CreatedAt,
			ToState: stateKeyOf(r.ToState),
		}
		if r.FromState != nil {
			from := stateKeyOf(*r.FromState)
			item.FromState = &from
		}
		out = append(out, item)
	}
	return out, nil
}

// PendingClaims is the staff review queue: claims awaiting a decision, oldest
// first (a submission queue is worked front to back).
func (s *ClaimLifecycleService) PendingClaims(ctx context.Context, site string, limit int) ([]PendingClaimItem, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.db.WithContext(ctx).Table("catalog_work AS w").
		Where("w.deleted_at IS NULL AND w.claim_state = ?", model.ClaimStatePending)
	if site != "" {
		q = q.Where("w.site = ?", site)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []PendingClaimItem
	if err := q.Session(&gorm.Session{}).
		Select(`w.id AS work_id, w.display_name, w.site, w.product_work_id,
		        (SELECT max(e.id) FROM catalog_claim_event e
		          WHERE e.work_id = w.id AND e.to_state = ?) AS submitted_event_id`,
			model.ClaimStatePending).
		Order("submitted_event_id ASC NULLS LAST, w.id ASC").
		Limit(limit).Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// PendingClaimItem is one row of the review queue.
type PendingClaimItem struct {
	WorkID           int64   `json:"work_id"`
	DisplayName      string  `json:"display_name"`
	Site             *string `json:"site"`
	ProductWorkID    *int64  `json:"product_work_id"`
	SubmittedEventID *int64  `json:"submitted_event_id"`
}

// stateKeyOf renders a stored claim_state on the public vocabulary. The work is
// claimed by construction wherever an event exists, so the site/product
// arguments are satisfied with placeholders — only the column matters here.
func stateKeyOf(state int16) string {
	site, product := "e", int64(0)
	return model.ClaimStateKey(&site, &product, &state)
}

// ClaimIdentity is a catalog work's product-side identity: the tenant that
// claimed it and the id that tenant knows it by.
type ClaimIdentity struct {
	Site          string
	ProductWorkID int64
}

// ClaimIdentities projects the product-side identity of the given catalog
// works. It is the revision feed's enrichment (wave 180): a product replaying
// the editing log keys its own tables by its own id, and the claim columns are
// the only place that mapping lives. Unclaimed works are simply absent, and the
// caller matches Site itself — one catalog work is claimed by at most one
// product, so a revision recorded under a different site must resolve to
// nothing rather than to another tenant's id.
func (s *ClaimLifecycleService) ClaimIdentities(ctx context.Context, workIDs []int64) (map[int64]ClaimIdentity, error) {
	out := map[int64]ClaimIdentity{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID            int64
		Site          string
		ProductWorkID int64
	}
	if err := s.db.WithContext(ctx).Model(&model.CatalogWork{}).
		Select("id, site, product_work_id").
		Where("id IN ? AND site IS NOT NULL AND product_work_id IS NOT NULL", workIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = ClaimIdentity{Site: r.Site, ProductWorkID: r.ProductWorkID}
	}
	return out, nil
}
