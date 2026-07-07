package service

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LinkService is the second half of the orphan doctrine (doc 10 §10, step 22).
// Step 13 deliberately imported credit names WITHOUT persons; a reviewer
// approving a shared-handle structural candidate is what finally asserts "these
// two names are the same real person". This service creates/attaches the person
// grouping (never destructively — the two names both survive) and can fully
// reverse it (DetachName), because person rows here are pure grouping artifacts.
type LinkService struct {
	db *gorm.DB
}

func NewLinkService(db *gorm.DB) *LinkService { return &LinkService{db: db} }

// PersonLinkResult reports the outcome of approving a person-link candidate.
type PersonLinkResult struct {
	// PersonID is the person both names now share; 0 when NeedsManual.
	PersonID int64 `json:"person_id"`
	// Created is true when a brand-new person row was minted (double-orphan
	// case), false when a name was attached into an already-existing person.
	Created bool `json:"created"`
	// NeedsManual is true when both names already belong to DIFFERENT persons:
	// nothing is written and the caller flags the candidate for manual handling
	// (person merge is future work — doc 10 §10).
	NeedsManual bool `json:"needs_manual"`
}

// linkCreditsTx applies the three-state person-linking rule to a credit_name
// pair inside the caller's transaction (symmetric in a/b):
//
//   - both orphan          → create a person, attach both, revision each
//   - exactly one attached  → attach the orphan into that person
//   - both attached, same   → no-op (idempotent re-approval)
//   - both attached, diff.   → NeedsManual, write nothing
//
// The credit_name→person link defaults to PUBLIC (link_visibility=public, the
// column's zero value): a shared_external_id candidate's evidence is the person
// having attached the same public handle (twitter/pixiv) at two sources — a
// self-declared public association, so public grouping is justified (doc 10
// §10). A future sensitive lane (裏名義 / R18↔all-ages) that defaults hidden
// would carry a visibility parameter; it is intentionally not surfaced here.
func (s *LinkService) linkCreditsTx(tx *gorm.DB, aID, bID int64, actorID *int64) (PersonLinkResult, error) {
	a, err := lockCreditName(tx, aID)
	if err != nil {
		return PersonLinkResult{}, err
	}
	b, err := lockCreditName(tx, bID)
	if err != nil {
		return PersonLinkResult{}, err
	}

	switch {
	case a.PersonID != nil && b.PersonID != nil:
		if *a.PersonID == *b.PersonID {
			return PersonLinkResult{PersonID: *a.PersonID}, nil // already the same person
		}
		return PersonLinkResult{NeedsManual: true}, nil // different persons → manual
	case a.PersonID != nil:
		return s.attachOrphan(tx, *a.PersonID, b, actorID)
	case b.PersonID != nil:
		return s.attachOrphan(tx, *b.PersonID, a, actorID)
	default:
		return s.createAndAttach(tx, a, b, actorID)
	}
}

// createAndAttach mints a person from a double-orphan pair. The display name of
// record is derived deterministically — the better-established name (more
// credits; ties broken toward the lower id, which is `a` since candidate pairs
// are normalized a<b) — and pinned as primary_credit_name_id.
func (s *LinkService) createAndAttach(tx *gorm.DB, a, b *model.CatalogCreditName, actorID *int64) (PersonLinkResult, error) {
	primary := a
	aCredits, err := creditCount(tx, a.ID)
	if err != nil {
		return PersonLinkResult{}, err
	}
	bCredits, err := creditCount(tx, b.ID)
	if err != nil {
		return PersonLinkResult{}, err
	}
	if bCredits > aCredits {
		primary = b
	}

	p := model.CatalogPerson{DisplayName: primary.Name, FieldProvenance: []byte(`{}`)}
	if err := tx.Create(&p).Error; err != nil {
		return PersonLinkResult{}, err
	}
	if err := tx.Model(&model.CatalogCreditName{}).Where("id IN ?", []int64{a.ID, b.ID}).
		Update("person_id", p.ID).Error; err != nil {
		return PersonLinkResult{}, err
	}
	if err := tx.Model(&model.CatalogPerson{}).Where("id = ?", p.ID).
		Update("primary_credit_name_id", primary.ID).Error; err != nil {
		return PersonLinkResult{}, err
	}

	// Person 'created' revision — snapshot AFTER attach so it captures both
	// names — plus a person_id-change revision on each name (doc 10 §9).
	if err := revisionOf(tx, model.EntityTypePerson, p.ID, model.RevisionActionCreated, nil, actorID,
		"person created by linking candidate names"); err != nil {
		return PersonLinkResult{}, err
	}
	for _, n := range []*model.CatalogCreditName{a, b} {
		if err := revisionOfNamePersonChange(tx, n.ID, p.ID, actorID); err != nil {
			return PersonLinkResult{}, err
		}
	}
	return PersonLinkResult{PersonID: p.ID, Created: true}, nil
}

// attachOrphan folds a single orphan name into an existing person: the name
// gets a person_id-change revision, the person a roster-change revision.
func (s *LinkService) attachOrphan(tx *gorm.DB, personID int64, orphan *model.CatalogCreditName, actorID *int64) (PersonLinkResult, error) {
	if err := tx.Model(&model.CatalogCreditName{}).Where("id = ?", orphan.ID).
		Update("person_id", personID).Error; err != nil {
		return PersonLinkResult{}, err
	}
	if err := revisionOfNamePersonChange(tx, orphan.ID, personID, actorID); err != nil {
		return PersonLinkResult{}, err
	}
	if err := revisionOf(tx, model.EntityTypePerson, personID, model.RevisionActionUpdated, nil, actorID,
		"credit name attached by linking candidate"); err != nil {
		return PersonLinkResult{}, err
	}
	return PersonLinkResult{PersonID: personID}, nil
}

// DetachName reverses a link one name at a time (doc 10 §10 reversibility): it
// nulls the name's person_id and, when that leaves the person with zero names,
// DELETES the person. The delete is safe because credits reference
// credit_name.id, never person.id (invariant 1), and an auto-linked person
// carries no external refs or usage rows of its own — nothing is orphaned. A
// surviving person whose primary name was the one detached has its primary (and
// display name) re-derived from the remaining names.
func (s *LinkService) DetachName(ctx context.Context, creditNameID int64, actorID *int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		n, err := lockCreditName(tx, creditNameID)
		if err != nil {
			return err
		}
		if n.PersonID == nil {
			return fmt.Errorf("%w: credit name %d is not attached to a person", ErrProposalState, creditNameID)
		}
		personID := *n.PersonID

		if err := tx.Model(&model.CatalogCreditName{}).Where("id = ?", creditNameID).
			Update("person_id", nil).Error; err != nil {
			return err
		}
		if err := revisionOfNameDetach(tx, creditNameID, actorID); err != nil {
			return err
		}

		var remaining int64
		if err := tx.Model(&model.CatalogCreditName{}).Where("person_id = ?", personID).
			Count(&remaining).Error; err != nil {
			return err
		}
		if remaining == 0 {
			// Tombstone the empty person, then hard-delete it (see doc comment).
			if err := revisionOf(tx, model.EntityTypePerson, personID, model.RevisionActionDeleted, nil, actorID,
				"empty person removed after detach"); err != nil {
				return err
			}
			return tx.Unscoped().Delete(&model.CatalogPerson{}, personID).Error
		}
		return s.repointPrimaryIfDetached(tx, personID, creditNameID, actorID)
	})
}

// repointPrimaryIfDetached keeps the surviving person's primary_credit_name_id
// (and denormalized display name) pointing at one of its OWN names after a
// detach. When the detached name was not the primary, only a roster-change
// revision is written.
func (s *LinkService) repointPrimaryIfDetached(tx *gorm.DB, personID, detachedID int64, actorID *int64) error {
	var p model.CatalogPerson
	if err := tx.First(&p, personID).Error; err != nil {
		return err
	}
	if p.PrimaryCreditNameID != nil && *p.PrimaryCreditNameID == detachedID {
		primary, err := primaryOfPerson(tx, personID)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.CatalogPerson{}).Where("id = ?", personID).
			Updates(map[string]any{"primary_credit_name_id": primary.ID, "display_name": primary.Name}).Error; err != nil {
			return err
		}
	}
	return revisionOf(tx, model.EntityTypePerson, personID, model.RevisionActionUpdated, nil, actorID,
		"credit name detached")
}

// --- helpers ---------------------------------------------------------------

// lockCreditName loads a credit_name FOR UPDATE (serializes concurrent links
// and detaches on the same name).
func lockCreditName(tx *gorm.DB, id int64) (*model.CatalogCreditName, error) {
	var n model.CatalogCreditName
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&n, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("%w: credit name %d", ErrNotFound, id)
		}
		return nil, err
	}
	return &n, nil
}

// primaryOfPerson picks the best-established remaining name of a person (most
// credits, ties toward the lower id) — the same rule createAndAttach uses.
func primaryOfPerson(tx *gorm.DB, personID int64) (*model.CatalogCreditName, error) {
	var names []model.CatalogCreditName
	if err := tx.Where("person_id = ?", personID).Order("id").Find(&names).Error; err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: person %d has no names", ErrNotFound, personID)
	}
	best := &names[0]
	bestCredits, err := creditCount(tx, best.ID)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(names); i++ {
		c, err := creditCount(tx, names[i].ID)
		if err != nil {
			return nil, err
		}
		if c > bestCredits {
			best, bestCredits = &names[i], c
		}
	}
	return best, nil
}

func creditCount(tx *gorm.DB, creditNameID int64) (int64, error) {
	var n int64
	err := tx.Model(&model.CatalogCredit{}).Where("credit_name_id = ?", creditNameID).Count(&n).Error
	return n, err
}

// revisionOf snapshots an entity and appends a revision in one call.
func revisionOf(tx *gorm.DB, entityType int16, entityID int64, action int16, changed map[string]any, actorID *int64, note string) error {
	snap, err := takeSnapshot(tx, entityType, entityID)
	if err != nil {
		return err
	}
	changedFields, err := marshalChangedFields(changed)
	if err != nil {
		return err
	}
	return writeRevision(tx, entityType, entityID, action, snap, changedFields, actorID, note)
}

func revisionOfNamePersonChange(tx *gorm.DB, creditNameID, personID int64, actorID *int64) error {
	return revisionOf(tx, model.EntityTypeCreditName, creditNameID, model.RevisionActionUpdated,
		map[string]any{"person_id": personID}, actorID, "attached to person")
}

func revisionOfNameDetach(tx *gorm.DB, creditNameID int64, actorID *int64) error {
	return revisionOf(tx, model.EntityTypeCreditName, creditNameID, model.RevisionActionUpdated,
		map[string]any{"person_id": nil}, actorID, "detached from person")
}
