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
		}
		// FOR UPDATE: two concurrent actions on one claim must serialize, or the
		// event log would record a transition from a state that never was.
		if err := tx.Raw(`SELECT id, site, product_work_id, claim_state FROM catalog_work
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

		target := rule.to
		if p.Action == ClaimActionUnban {
			prior, err := s.priorState(tx, p.WorkID)
			if err != nil {
				return err
			}
			target = prior
		}
		updates := map[string]any{"claim_state": claimStateValue[target]}
		eventSite := p.Site
		if work.Site != nil && *work.Site != "" {
			eventSite = *work.Site
		}
		if p.Action == ClaimActionClaim {
			updates["site"] = p.Site
			updates["product_work_id"] = *p.ProductWorkID
			eventSite = p.Site
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

		event := model.CatalogClaimEvent{
			WorkID:   p.WorkID,
			ToState:  claimStateValue[target],
			ActorUID: p.ActorUID,
			Site:     eventSite,
		}
		// from_state is NULL only for the birth of a claim — the one transition
		// with no prior state to record (model/claimevent.go).
		if p.Action != ClaimActionClaim {
			prior := claimStateValue[current]
			event.FromState = &prior
		}
		if reason := strings.TrimSpace(p.Reason); reason != "" {
			event.Reason = &reason
		}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}

		out = ClaimActionResult{WorkID: p.WorkID, To: target, EventID: event.ID}
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
func (s *ClaimLifecycleService) EventsSince(ctx context.Context, since int64, limit int, site string) ([]ClaimEventItem, error) {
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
