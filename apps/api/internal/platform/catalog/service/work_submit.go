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
	"strings"

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
	// ProductWorkID is the product-side id, and it is OPTIONAL (charter
	// §6.P4-verdict 1).
	//
	//   - SUPPLIED: the product allocated the id first — its existing stub /
	//     patch row already exists and the registry records the id it was told.
	//     This is the 161 STOP-1 posture and forum's stub flows keep using it.
	//   - OMITTED (0): the registry issues the identity and the claim adopts the
	//     minted work's OWN id as product_work_id, so the product can create its
	//     local row at that id afterwards (the response carries it back). This
	//     removes the "who allocates the id" bootstrap problem for a wizard that
	//     has nothing to name the work by yet — forum's local galgame_id_seq is
	//     stale at 6,276 against a max of 63,228, so nextval() collides on the
	//     first call (161 §6.P3-4 STOP-1b).
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
	WorkID int64 `json:"work_id"`
	// ProductWorkID is the id the claim ended up anchored at: the one the caller
	// supplied, or — when it omitted one — the minted work's own id, which the
	// product then creates its local row at.
	ProductWorkID int64  `json:"product_work_id"`
	ClaimState    string `json:"claim_state"`
	EventID       int64  `json:"event_id"`
	// ReleaseID is the curated release row the form date produced; 0 when no
	// date was submitted.
	ReleaseID int64 `json:"release_id,omitempty"`
}

// ClaimExistsError is the idempotency answer: this submission is already on the
// registry. It carries the current state so a retrying wizard renders the truth
// instead of minting a duplicate identity — the failure mode waves 147/148
// spent a day undoing.
//
// MatchedBy names WHICH key recognized the retry, because the two are not
// equally strong and a caller may want to treat them differently: the claim key
// is exact, the anchor is an identity assertion that could also mean "someone
// else already registered this game for your site".
type ClaimExistsError struct {
	WorkID        int64
	Site          string
	ProductWorkID int64
	CurrentState  string
	// MatchedBy is "claim" (site + product_work_id) or "anchor" (an identity
	// ref the submission asserted).
	MatchedBy string
	// Anchor is the matching identity coordinate, set only for MatchedBy=anchor.
	Anchor string
}

func (e *ClaimExistsError) Error() string {
	if e.MatchedBy == ClaimMatchAnchor {
		return fmt.Sprintf("%s is already registered for site %q by work %d (state %q)",
			e.Anchor, e.Site, e.WorkID, e.CurrentState)
	}
	return fmt.Sprintf("%s/%d is already claimed by work %d (state %q)",
		e.Site, e.ProductWorkID, e.WorkID, e.CurrentState)
}

// The two idempotency keys, as wire values.
const (
	ClaimMatchClaim  = "claim"
	ClaimMatchAnchor = "anchor"
)

// ErrSubmitTargetRequired: a submission must name the tenant it is filed for.
// The product id is optional (the registry issues one when it is omitted).
var ErrSubmitTargetRequired = errors.New("site is required to submit")

// ErrSubmitDisplayNameRequired: a registry row with no name is unreadable on
// every face that lists it, and display_name is the one field no other facet
// can be derived from.
var ErrSubmitDisplayNameRequired = errors.New("catalog.work.display_name is required to submit")

// ErrSubmitInvalidDate: a date shape the three nullable release columns cannot
// express.
var ErrSubmitInvalidDate = errors.New("invalid release date")

// findClaimByAnchor is the registry-issued path's idempotency lookup: does this
// SITE already hold a claim on any of the identity coordinates the submission
// asserted? Returns the work and the coordinate that matched.
//
// Scoped to the caller's own tenant on purpose. Another site claiming the same
// VNDB id is not a duplicate — the registry is multi-tenant and one work
// legitimately carries several product claims; only a claim by the SAME site is
// either this submission's own retry or a second entry for a game that site
// already lists, and both want the same answer.
//
// It matches every link_kind, not just the curated candidates this face writes.
// A tighter predicate would recognize a retry but not "your importer already
// anchored this game", and pointing a submitter at the entry that exists is
// strictly better than minting a rival identity for it (the vndb_id squatting
// class). Work-level refs only: a release-level anchor identifies a SKU, and
// two SKUs of one game are not one submission.
func findClaimByAnchor(tx *gorm.DB, site string, anchors []editspec.SubmissionAnchor) (*model.CatalogWork, string, error) {
	keys := make([]string, 0, len(anchors))
	args := []any{model.EntityTypeWork, site}
	for _, a := range anchors {
		keys = append(keys, "(s.key = ? AND r.external_id = ?)")
		args = append(args, a.SourceKey, a.ExternalID)
	}
	var row struct {
		ID            int64
		Site          *string
		ProductWorkID *int64
		ClaimState    *int16
		SourceKey     string
		ExternalID    string
	}
	err := tx.Raw(`
		SELECT w.id, w.site, w.product_work_id, w.claim_state,
		       s.key AS source_key, r.external_id
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_id = w.id AND r.entity_type = ?
		JOIN catalog_source s ON s.id = r.source_id
		WHERE w.deleted_at IS NULL
		  AND w.site = ?
		  AND w.product_work_id IS NOT NULL
		  AND (`+strings.Join(keys, " OR ")+`)
		ORDER BY w.id
		LIMIT 1`, args...).Scan(&row).Error
	if err != nil {
		return nil, "", err
	}
	if row.ID == 0 {
		return nil, "", nil
	}
	return &model.CatalogWork{
		ID: row.ID, Site: row.Site, ProductWorkID: row.ProductWorkID, ClaimState: row.ClaimState,
	}, row.SourceKey + ":" + row.ExternalID, nil
}

// SubmitWork mints a work in the PENDING claim state and records its birth.
//
// Everything is one transaction: the registry row, its content facets, the
// optional curated release, the machine revision and the claim event. A partial
// submission — a row with no name, or a claim with no event — is not a state
// this registry is willing to have.
//
// IDEMPOTENCY has two keys, because the identity key depends on who allocated
// the product id:
//
//   - product_work_id SUPPLIED → the claim unique key (medium, site,
//     product_work_id). Exact, and the same key the DB index enforces.
//   - product_work_id OMITTED → the identity anchors the submission asserted
//     through its links (a VNDB / Bangumi URL). A retry carries the same URLs,
//     so it recognizes itself; and a second person submitting a game the site
//     already registered is recognized by the same rule, which is the answer
//     that page needs anyway.
//   - OMITTED AND no identity anchor → NOTHING can tie a retry to the first
//     mint, and the registry will not invent a key the product did not give it.
//     Such a submission double-mints on retry. That is a documented contract
//     (see the endpoint summary), not a silent behaviour: a caller that needs
//     the guarantee either allocates its own id first or includes the anchor.
func (s *ClaimLifecycleService) SubmitWork(ctx context.Context, p SubmitWorkParams) (*SubmitWorkResult, error) {
	if p.Site == "" {
		return nil, ErrSubmitTargetRequired
	}
	if p.ProductWorkID < 0 {
		return nil, ErrSubmitTargetRequired
	}
	if name, ok := p.Fields[editspec.FieldWorkDisplayName].(string); !ok || name == "" {
		return nil, ErrSubmitDisplayNameRequired
	}
	if err := p.Released.validate(); err != nil {
		return nil, err
	}
	// Anchors come off the payload BEFORE the transaction: they are a pure
	// function of the links value, and the registry-issued path needs them as
	// its idempotency key rather than as content.
	var anchors []editspec.SubmissionAnchor
	if p.ProductWorkID == 0 {
		anchors = editspec.SubmissionAnchorsOf(p.Fields[editspec.FieldWorkLinks])
	}

	var out SubmitWorkResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if p.ProductWorkID > 0 {
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
					MatchedBy:    ClaimMatchClaim,
				}
			}
		} else if len(anchors) > 0 {
			existing, anchor, err := findClaimByAnchor(tx, p.Site, anchors)
			if err != nil {
				return err
			}
			if existing != nil {
				productID := int64(0)
				if existing.ProductWorkID != nil {
					productID = *existing.ProductWorkID
				}
				return &ClaimExistsError{
					WorkID: existing.ID, Site: p.Site, ProductWorkID: productID,
					CurrentState: model.ClaimStateKey(existing.Site, existing.ProductWorkID, existing.ClaimState),
					MatchedBy:    ClaimMatchAnchor, Anchor: anchor,
				}
			}
		}

		pending := model.ClaimStatePending
		w := model.CatalogWork{
			MediumID:   galgameMediumID,
			Site:       &p.Site,
			ClaimState: &pending,
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
		// The submitter is stamped as the row's owner at birth (wave 178): a
		// submission is the one moment where "who created this entry" is known
		// beyond doubt, and writing it here is what lets the editing engine
		// derive ownership later without any product-side assertion. A machine
		// submission (no actor) leaves it nil rather than stamping 0.
		if p.ActorUID != 0 {
			w.OwnerUserID = &p.ActorUID
		}
		if err := tx.Create(&w).Error; err != nil {
			return err
		}
		// The claim is anchored IMMEDIATELY after the insert, before anything
		// else runs: `claimed_by` — and therefore every projection of this row —
		// is (site, product_work_id) TOGETHER, so a row that exists with a site
		// and no product id is a state no reader has a name for. When the caller
		// supplied no id, the registry issues the identity by adopting the work's
		// own id: unique by construction (it IS a primary key), so the claim
		// unique index is satisfied without a second allocator to keep in step.
		productWorkID := p.ProductWorkID
		if productWorkID == 0 {
			productWorkID = w.ID
		}
		if err := tx.Model(&w).Update("product_work_id", productWorkID).Error; err != nil {
			return err
		}

		// The content, through the editing face's own field table — the same
		// validators and the same writes a later edit of these fields will use.
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
			fmt.Sprintf("submitted by %s/%d", p.Site, productWorkID)); err != nil {
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
		out.WorkID, out.ProductWorkID = w.ID, productWorkID
		out.ClaimState, out.EventID = model.ClaimStateKeyPending, eventID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
