// work_submit.go — the SUBMISSION mint (wave 162, 161 §6.P3-verdict STOP-1).
//
// Before this endpoint there was no way to bring a work into existence in a
// submittable state. `POST /works/claim` mints one born LIVE with no claim
// event, so a submission would be publicly claimed before anyone reviewed it
// and the handover ledger would never record its birth; `claim-actions/claim`
// only moves an EXISTING unclaimed row. Chaining the two writes a history that
// never happened (NULL → live → draft → pending) with a genuinely public window
// in the middle. So: one endpoint, one transaction, one event.
//
// It reuses ClaimLifecycleService rather than living beside it because the
// event row is the same row the eight actions write, and there must be exactly
// one writer of catalog_claim_event (appendClaimEvent below) — the ledger 155
// ruling 1 made load-bearing for lifecycle authority.
package service

import (
	"context"
	"errors"
	"fmt"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// SubmitWorkParams is one submission.
type SubmitWorkParams struct {
	// Site is the caller's tenant — enforced against the client's catalog_site
	// binding by the face, and the claim this mint creates.
	Site string
	// ProductWorkID is the product-side id. The PRODUCT owns that id space
	// (161 STOP-1 ruling): the registry never allocates one, it records the one
	// the submitting site allocated.
	ProductWorkID int64
	ActorUID      int64
	// Fields is the submission subset of the catalog.work matrix, keyed exactly
	// like an edit patch (editspec.SubmissionFieldKeys).
	Fields map[string]any
	// Released is the optional form date. It becomes ONE curated
	// catalog_release row (03 §6-1), never a work column — the work-level date
	// triple is an authorized cut.
	Released ReleaseDate
}

// ReleaseDate is the fuzzy submitted date. Zero means "not given", and the
// nullable tail IS the precision: {2019,0,0} is "sometime in 2019", an entirely
// zero value is TBA. No separate precision enum exists, by design.
type ReleaseDate struct {
	Y int16
	M int16
	D int16
}

func (d ReleaseDate) given() bool { return d.Y != 0 || d.M != 0 || d.D != 0 }

// validate rejects a shape the three nullable columns cannot express: a month
// without a year, or a day without a month, would store a date whose precision
// no reader could interpret.
func (d ReleaseDate) validate() error {
	if !d.given() {
		return nil
	}
	switch {
	case d.Y < 1970 || d.Y > 2200:
		return fmt.Errorf("%w: released.y must be between 1970 and 2200", ErrSubmitInvalidDate)
	case d.M < 0 || d.M > 12:
		return fmt.Errorf("%w: released.m must be between 1 and 12", ErrSubmitInvalidDate)
	case d.D < 0 || d.D > 31:
		return fmt.Errorf("%w: released.d must be between 1 and 31", ErrSubmitInvalidDate)
	case d.D > 0 && d.M == 0:
		return fmt.Errorf("%w: released.d requires released.m", ErrSubmitInvalidDate)
	}
	return nil
}

// SubmitWorkResult is what the mint produced.
type SubmitWorkResult struct {
	WorkID     int64  `json:"work_id"`
	ClaimState string `json:"claim_state"`
	EventID    int64  `json:"event_id"`
	// ReleaseID is the curated release row the form date produced; 0 when no
	// date was submitted.
	ReleaseID int64 `json:"release_id,omitempty"`
}

// ClaimExistsError is the idempotency answer: this (site, product_work_id) is
// already on the registry. It carries the current state so a retrying wizard
// renders the truth instead of minting a duplicate identity — the failure mode
// waves 147/148 spent a day undoing.
type ClaimExistsError struct {
	WorkID        int64
	Site          string
	ProductWorkID int64
	CurrentState  string
}

func (e *ClaimExistsError) Error() string {
	return fmt.Sprintf("%s/%d is already claimed by work %d (state %q)",
		e.Site, e.ProductWorkID, e.WorkID, e.CurrentState)
}

// ErrSubmitTargetRequired: a submission must name the tenant and the product
// id it is being filed for.
var ErrSubmitTargetRequired = errors.New("site and product_work_id are required to submit")

// ErrSubmitDisplayNameRequired: a registry row with no name is unreadable on
// every face that lists it, and display_name is the one field no other facet
// can be derived from.
var ErrSubmitDisplayNameRequired = errors.New("catalog.work.display_name is required to submit")

// ErrSubmitInvalidDate: a date shape the three nullable release columns cannot
// express.
var ErrSubmitInvalidDate = errors.New("invalid release date")

// SubmitWork mints a work in the PENDING claim state and records its birth.
//
// Everything is one transaction: the registry row, its content facets, the
// optional curated release, the machine revision and the claim event. A partial
// submission — a row with no name, or a claim with no event — is not a state
// this registry is willing to have.
func (s *ClaimLifecycleService) SubmitWork(ctx context.Context, p SubmitWorkParams) (*SubmitWorkResult, error) {
	if p.Site == "" || p.ProductWorkID <= 0 {
		return nil, ErrSubmitTargetRequired
	}
	if name, ok := p.Fields[editspec.FieldWorkDisplayName].(string); !ok || name == "" {
		return nil, ErrSubmitDisplayNameRequired
	}
	if err := p.Released.validate(); err != nil {
		return nil, err
	}

	var out SubmitWorkResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Idempotency on the SAME key the claim unique index enforces
		// (medium, site, product_work_id): a wizard that retries after a
		// timeout gets the existing work back as a 409, never a second row.
		existing, err := repository.FindClaimed(tx, galgameMediumID, p.Site, p.ProductWorkID)
		if err != nil {
			return err
		}
		if existing != nil {
			return &ClaimExistsError{
				WorkID: existing.ID, Site: p.Site, ProductWorkID: p.ProductWorkID,
				CurrentState: model.ClaimStateKey(existing.Site, existing.ProductWorkID, existing.ClaimState),
			}
		}

		pending := model.ClaimStatePending
		w := model.CatalogWork{
			MediumID:      galgameMediumID,
			Site:          &p.Site,
			ProductWorkID: &p.ProductWorkID,
			ClaimState:    &pending,
			// OLang is overwritten below when the payload carries one; the
			// column has no DB default on purpose, so the mint must supply it.
			OLang: model.OLangDefault,
			// Status is the REGISTRY row's status (live / stub / merged), not a
			// lifecycle: a claimed row is live by the same rule ClaimWork
			// applies. Visibility of the SUBMISSION is claim_state=pending,
			// which is what a product face filters on.
			Status:          model.WorkStatusLive,
			Extra:           []byte(`{}`),
			FieldProvenance: []byte(`{}`),
		}
		if err := tx.Create(&w).Error; err != nil {
			return err
		}

		// The content, through the editing face's own field table — including
		// the mirror gate, which refuses the mirrored facets while the claiming
		// site is still the one the duty chain owns (409, same message the edit
		// face gives).
		if err := editspec.ApplyWorkFields(ctx, tx, w.ID, p.Fields); err != nil {
			return err
		}

		if p.Released.given() {
			rel := model.CatalogRelease{
				WorkID: w.ID, Kind: model.ReleaseKindDefault, Extra: []byte(`{}`),
			}
			rel.ReleasedY = &p.Released.Y
			if p.Released.M > 0 {
				rel.ReleasedM = &p.Released.M
			}
			if p.Released.D > 0 {
				rel.ReleasedD = &p.Released.D
			}
			if err := tx.Create(&rel).Error; err != nil {
				return err
			}
			out.ReleaseID = rel.ID
		}

		// The registry's own entity-snapshot log, exactly as ClaimWork writes
		// it on its mint path — a machine record of the row's birth, distinct
		// from the engine's per-field edit log (03 §9-3: two layers, never
		// merged).
		snap, err := takeSnapshot(tx, model.EntityTypeWork, w.ID)
		if err != nil {
			return err
		}
		actor := p.ActorUID
		if err := writeRevision(tx, model.EntityTypeWork, w.ID, model.RevisionActionCreated, snap, nil, &actor,
			fmt.Sprintf("submitted by %s/%d", p.Site, p.ProductWorkID)); err != nil {
			return err
		}

		// from_state NULL — the birth of a claim, the one transition with no
		// prior state, exactly as the `claim` action records it. The difference
		// is only where it lands: a submission is born pending, awaiting the
		// review actions the state machine already defines out of pending.
		eventID, err := appendClaimEvent(tx, claimEventRow{
			WorkID: w.ID, To: model.ClaimStatePending,
			ActorUID: p.ActorUID, Site: p.Site,
		})
		if err != nil {
			return err
		}
		out.WorkID, out.ClaimState, out.EventID = w.ID, model.ClaimStateKeyPending, eventID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
