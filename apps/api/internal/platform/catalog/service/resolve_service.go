// Package service is the catalog domain-service core: resolve, merge
// lifecycle + execution, unmerge, work claiming, revision writing and the
// usage delete-guard. No HTTP here — handlers arrive in a later step; the
// services are consumed by tests, jobs and ingestion pipelines.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"api/internal/platform/catalog/repository"
)

// Sentinel errors — inspected by callers (and by the future HTTP layer).
var (
	// ErrRedirectChain fires when a redirect's target is itself redirected —
	// the flatten invariant (doc 10 invariant 3) is broken and reads must
	// not silently follow chains.
	ErrRedirectChain = errors.New("catalog: redirect chain detected (flatten invariant broken)")
	// ErrSameEntity guards merge endpoints resolving to one entity.
	ErrSameEntity = errors.New("catalog: source and target resolve to the same entity")
	// ErrProposalState fires on an illegal proposal state transition.
	ErrProposalState = errors.New("catalog: proposal is not in the required state")
	// ErrCoolingOff fires when executing a merge before execute_after.
	ErrCoolingOff = errors.New("catalog: cooling-off window has not elapsed")
	// ErrDuplicateOpenProposal fires when an open proposal for the pair exists.
	ErrDuplicateOpenProposal = errors.New("catalog: an open proposal for this pair already exists")
	// ErrClaimConflict fires when a work is already claimed by another product.
	ErrClaimConflict = errors.New("catalog: work already claimed by another product work")
	// ErrHasUsage blocks hard deletion of referenced entities (invariant 8).
	ErrHasUsage = errors.New("catalog: entity is referenced by site usage and cannot be hard-deleted")
	// ErrNotFound is the generic missing-row error of the service layer.
	ErrNotFound = errors.New("catalog: not found")
)

// ResolveService follows redirects (doc 10 §5.2/§5.3). Reads are one index
// lookup — write-time flattening guarantees chains of length one, and the
// runtime sentinel below turns a broken invariant into a loud error instead
// of a silently followed chain.
type ResolveService struct {
	redirects *repository.RedirectRepository
}

func NewResolveService(redirects *repository.RedirectRepository) *ResolveService {
	return &ResolveService{redirects: redirects}
}

// Resolve returns the canonical id for (entityType, id) and whether a
// redirect was followed.
func (s *ResolveService) Resolve(ctx context.Context, entityType int16, id int64) (int64, bool, error) {
	row, err := s.redirects.Get(ctx, entityType, id)
	if err != nil {
		return 0, false, err
	}
	if row == nil {
		return id, false, nil
	}
	// Flatten sentinel: the target must itself be canonical.
	chained, err := s.redirects.Get(ctx, entityType, row.CurrentID)
	if err != nil {
		return 0, false, err
	}
	if chained != nil {
		slog.Error("catalog redirect chain detected",
			"entity_type", entityType, "old_id", id,
			"current_id", row.CurrentID, "chained_to", chained.CurrentID)
		return 0, false, fmt.Errorf("%w: %d -> %d -> %d", ErrRedirectChain, id, row.CurrentID, chained.CurrentID)
	}
	return row.CurrentID, true, nil
}

// ResolveBatch maps old ids to canonical ids (ids without redirects map to
// themselves) — the render-path bulk API.
func (s *ResolveService) ResolveBatch(ctx context.Context, entityType int16, ids []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(ids))
	for _, id := range ids {
		out[id] = id
	}
	if len(ids) == 0 {
		return out, nil
	}
	redirected, err := s.redirects.GetBatch(ctx, entityType, ids)
	if err != nil {
		return nil, err
	}
	maps.Copy(out, redirected)
	return out, nil
}

// RedirectsSince exposes the keyset redirect feed for product-site cleanup
// crons. The feed keys on merged_at, which the merge path always sets —
// manually inserted redirect rows (not a supported path) would break the
// cursor's monotonicity assumption.
func (s *ResolveService) RedirectsSince(ctx context.Context, entityType *int16, cursor repository.RedirectCursor, limit int) ([]RedirectFeedItem, repository.RedirectCursor, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.redirects.Since(ctx, entityType, cursor, limit)
	if err != nil {
		return nil, cursor, err
	}
	items := make([]RedirectFeedItem, len(rows))
	next := cursor
	for i, row := range rows {
		items[i] = RedirectFeedItem{
			EntityType: row.EntityType,
			OldID:      row.OldID,
			CurrentID:  row.CurrentID,
		}
		if row.MergedAt != nil {
			items[i].MergedAt = *row.MergedAt
			next = repository.RedirectCursor{MergedAt: *row.MergedAt, EntityType: row.EntityType, OldID: row.OldID}
		}
	}
	return items, next, nil
}

// RedirectFeedItem is one redirect edge as served by the feed.
type RedirectFeedItem struct {
	EntityType int16     `json:"entity_type"`
	OldID      int64     `json:"old_id"`
	CurrentID  int64     `json:"current_id"`
	MergedAt   time.Time `json:"merged_at"`
}
