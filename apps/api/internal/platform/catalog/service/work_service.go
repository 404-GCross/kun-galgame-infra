package service

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// WorkService owns work registration and claiming (doc 17 R2).
type WorkService struct {
	db      *gorm.DB
	resolve *ResolveService
}

func NewWorkService(db *gorm.DB, resolve *ResolveService) *WorkService {
	return &WorkService{db: db, resolve: resolve}
}

// ExternalAnchor is one external identity the claiming product knows for the
// work (a VNDB id, a Bangumi subject id, ...). Anchors ground the claim
// lookup and become exact refs on a newly created work.
type ExternalAnchor struct {
	SourceID   int16
	ExternalID string
	// MatchedBy is the traceability tag written into external_ref
	// ('rule:<id>' / 'import:<job>' / 'human:<uid>').
	MatchedBy string
}

// ClaimWorkParams describes the product-side work doing the claiming.
type ClaimWorkParams struct {
	MediumID      int16
	Site          string
	ProductWorkID int64
	DisplayName   string
	// OLang defaults to "ja" when empty (the column carries no DB default
	// on purpose).
	OLang         string
	ContentRating int16
	Anchors       []ExternalAnchor
	ActorID       *int64
	// RejectedAnchor, when set, is consulted with the RESOLVED work id before an
	// anchor is written or adopted through: a true result means the pairing is
	// recorded negative knowledge (catalog_match_rejection) and the anchor is
	// dropped. It gates CROSS-SOURCE identity claims (a curated bid/vndb anchor);
	// a work born fresh here can carry no prior rejection, so this only ever
	// fires on the adopt path (an existing work a human has since rejected). nil
	// disables the check — the default for importers writing self-identity
	// anchors, whose existence IS the identity and is never rejectable.
	RejectedAnchor func(workID int64, sourceID int16, externalID string) bool
}

// ClaimWork implements the claim semantics pinned on catalog_work: find an
// existing registry row by external anchors FIRST and claim it — a claim
// must never mint a second identity. Idempotent: the same (medium, site,
// product_work_id) always returns the same work.
func (s *WorkService) ClaimWork(ctx context.Context, params ClaimWorkParams) (int64, bool, error) {
	if params.OLang == "" {
		params.OLang = "ja"
	}
	var workID int64
	var created bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// (1) Idempotency: this product work already claimed a row.
		existing, err := repository.FindClaimed(tx, params.MediumID, params.Site, params.ProductWorkID)
		if err != nil {
			return err
		}
		if existing != nil {
			workID = existing.ID
			return nil
		}

		// (2) Anchor lookup: an exact ref hit (followed through redirects)
		// is the existing identity to claim. An anchor that resolves to a work
		// a human has rejected for that pairing is recorded so step (3) never
		// re-writes it onto a fresh work (which would resurrect the identity or
		// collide with the surviving exact ref).
		rejectedInAdopt := make(map[int]bool)
		for ai, anchor := range params.Anchors {
			id, found, err := repository.FindWorkIDByExactRef(tx, anchor.SourceID, anchor.ExternalID)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			canonical, err := repository.ResolveTx(tx, model.EntityTypeWork, id)
			if err != nil {
				return err
			}
			var w model.CatalogWork
			if err := tx.First(&w, canonical).Error; err != nil {
				return err
			}
			if params.RejectedAnchor != nil && params.RejectedAnchor(w.ID, anchor.SourceID, anchor.ExternalID) {
				rejectedInAdopt[ai] = true
				continue // never adopt a work through a human-rejected anchor
			}
			if w.Site != nil {
				if *w.Site == params.Site && w.ProductWorkID != nil && *w.ProductWorkID == params.ProductWorkID {
					workID = w.ID // already ours (raced sibling call)
					return nil
				}
				if w.ProductWorkID != nil {
					return fmt.Errorf("%w: work %d is claimed by %s/%d", ErrClaimConflict, w.ID, *w.Site, *w.ProductWorkID)
				}
				return fmt.Errorf("%w: work %d is claimed by %s", ErrClaimConflict, w.ID, *w.Site)
			}
			updates := map[string]any{
				"site":            params.Site,
				"product_work_id": params.ProductWorkID,
				"status":          model.WorkStatusLive,
			}
			if w.DisplayName == "" && params.DisplayName != "" {
				updates["display_name"] = params.DisplayName
			}
			if err := tx.Model(&w).Updates(updates).Error; err != nil {
				return err
			}
			workID = w.ID
			return nil
		}

		// (3) No anchor hit → new registry row. Unclaimed-hygiene does not
		// apply (the row is born claimed, hence live).
		w := model.CatalogWork{
			MediumID:        params.MediumID,
			Site:            &params.Site,
			ProductWorkID:   &params.ProductWorkID,
			OLang:           params.OLang,
			DisplayName:     params.DisplayName,
			ContentRating:   params.ContentRating,
			Status:          model.WorkStatusLive,
			Extra:           []byte(`{}`),
			FieldProvenance: []byte(`{}`),
		}
		if err := tx.Create(&w).Error; err != nil {
			return err
		}
		for ai, anchor := range params.Anchors {
			if rejectedInAdopt[ai] {
				continue // an anchor rejected in (2) is never re-written here
			}
			ref := model.CatalogExternalRef{
				EntityType: model.EntityTypeWork,
				EntityID:   w.ID,
				SourceID:   anchor.SourceID,
				ExternalID: anchor.ExternalID,
				LinkKind:   model.LinkKindExact,
				MatchedBy:  anchor.MatchedBy,
			}
			if err := tx.Create(&ref).Error; err != nil {
				return err
			}
		}
		snap, err := takeSnapshot(tx, model.EntityTypeWork, w.ID)
		if err != nil {
			return err
		}
		if err := writeRevision(tx, model.EntityTypeWork, w.ID, model.RevisionActionCreated, snap, nil, params.ActorID,
			fmt.Sprintf("claimed by %s/%d", params.Site, params.ProductWorkID)); err != nil {
			return err
		}
		workID = w.ID
		created = true
		return nil
	})
	return workID, created, err
}
