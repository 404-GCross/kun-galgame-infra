package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Step 22: approving a shared-handle credit_name candidate establishes the
// "same person" fact (create/attach a person, both names survive) and is fully
// reversible. These tests exercise the whole path through the queue service,
// exactly as the admin API does.

func mkCreditCandidate(t *testing.T, a, b int64) {
	t.Helper()
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	require.NoError(t, testDB.Create(&model.CatalogMatchCandidate{
		EntityType: model.EntityTypeCreditName, AID: lo, BID: hi,
		Reason: model.CandidateReasonSharedExternalID, Status: model.CandidateStatusPending,
	}).Error)
}

// giveCredits attaches n distinct credits to a credit name (n distinct works,
// so the credit uniqueness index does not collapse them).
func giveCredits(t *testing.T, creditNameID, roleID int64, n int) {
	t.Helper()
	for range n {
		w := createWork(t, "W")
		createCredit(t, w.ID, creditNameID, roleID, nil)
	}
}

func personIDOfName(t *testing.T, id int64) *int64 {
	t.Helper()
	var n model.CatalogCreditName
	require.NoError(t, testDB.First(&n, id).Error)
	return n.PersonID
}

func orphanCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Where("person_id IS NULL").Count(&n).Error)
	return n
}

func revCount(t *testing.T, entityType int16, entityID int64, action int16) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Model(&model.CatalogRevision{}).
		Where("entity_type = ? AND entity_id = ? AND action = ?", entityType, entityID, action).
		Count(&n).Error)
	return n
}

func candidateStatus(t *testing.T, a, b int64) int16 {
	t.Helper()
	var s int16
	require.NoError(t, testDB.Model(&model.CatalogMatchCandidate{}).
		Where("entity_type = ? AND a_id = ? AND b_id = ?", model.EntityTypeCreditName, a, b).
		Select("status").Scan(&s).Error)
	return s
}

func acceptLink(t *testing.T, a, b int64) *PersonLinkResult {
	t.Helper()
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	out, err := testQueues.DecideCandidate(t.Context(), CandidateDecision{
		EntityType: model.EntityTypeCreditName, AID: lo, BID: hi, Action: "accept", DecidedBy: 9,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Link)
	return out.Link
}

// Double-orphan: a person is minted, both names attach, the display name of
// record is derived from the better-established name, the link is public, and
// the revisions (person created + each name's person_id change) are written.
func TestPersonLinkCreatesPersonFromDoubleOrphan(t *testing.T) {
	cleanTables(t)
	role := seededRoleID(t)

	a := createCreditName(t, nil, "麻枝准")
	b := createCreditName(t, nil, "Jun Maeda")
	giveCredits(t, a.ID, role, 1)
	giveCredits(t, b.ID, role, 3) // b is better established → primary
	mkCreditCandidate(t, a.ID, b.ID)

	before := orphanCount(t)
	link := acceptLink(t, a.ID, b.ID)
	assert.True(t, link.Created)
	assert.NotZero(t, link.PersonID)
	assert.False(t, link.NeedsManual)

	// Both names now point at the new person; orphan count dropped by two.
	require.Equal(t, link.PersonID, *personIDOfName(t, a.ID))
	require.Equal(t, link.PersonID, *personIDOfName(t, b.ID))
	assert.Equal(t, before-2, orphanCount(t))

	// Display name of record + primary derived from the higher-credit name (b).
	var p model.CatalogPerson
	require.NoError(t, testDB.First(&p, link.PersonID).Error)
	assert.Equal(t, "Jun Maeda", p.DisplayName)
	require.NotNil(t, p.PrimaryCreditNameID)
	assert.Equal(t, b.ID, *p.PrimaryCreditNameID)

	// The link is public (step 22 ruling): shared public handle = self-declared
	// association.
	var vis int16
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Where("id = ?", a.ID).
		Select("link_visibility").Scan(&vis).Error)
	assert.Equal(t, model.LinkVisibilityPublic, vis)

	// Revisions: the person's 'created' + a person_id-change on each name.
	assert.Equal(t, int64(1), revCount(t, model.EntityTypePerson, link.PersonID, model.RevisionActionCreated))
	assert.Equal(t, int64(1), revCount(t, model.EntityTypeCreditName, a.ID, model.RevisionActionUpdated))
	assert.Equal(t, int64(1), revCount(t, model.EntityTypeCreditName, b.ID, model.RevisionActionUpdated))

	assert.Equal(t, model.CandidateStatusAccepted, candidateStatus(t, a.ID, b.ID))
}

// One side already has a person → the orphan folds into it; the existing
// person's primary is untouched.
func TestPersonLinkAttachesOrphanToExistingPerson(t *testing.T) {
	cleanTables(t)

	host := createPerson(t, "Host")
	x := createCreditName(t, &host.ID, "Host Name")
	require.NoError(t, testDB.Model(&host).Update("primary_credit_name_id", x.ID).Error)
	y := createCreditName(t, nil, "Orphan Name")
	mkCreditCandidate(t, x.ID, y.ID)

	link := acceptLink(t, x.ID, y.ID)
	assert.False(t, link.Created)
	assert.Equal(t, host.ID, link.PersonID)

	assert.Equal(t, host.ID, *personIDOfName(t, y.ID))
	assert.Equal(t, host.ID, *personIDOfName(t, x.ID))
	// Attaching a name is a roster change on the person and a person_id change
	// on the orphan.
	assert.Equal(t, int64(1), revCount(t, model.EntityTypePerson, host.ID, model.RevisionActionUpdated))
	assert.Equal(t, int64(1), revCount(t, model.EntityTypeCreditName, y.ID, model.RevisionActionUpdated))
	// The host's primary is unchanged.
	var p model.CatalogPerson
	require.NoError(t, testDB.First(&p, host.ID).Error)
	require.NotNil(t, p.PrimaryCreditNameID)
	assert.Equal(t, x.ID, *p.PrimaryCreditNameID)
}

// Both sides already belong to DIFFERENT persons → needs_manual, nothing
// written (person merge is future work).
func TestPersonLinkDifferentPersonsNeedsManual(t *testing.T) {
	cleanTables(t)

	p1 := createPerson(t, "P1")
	p2 := createPerson(t, "P2")
	x := createCreditName(t, &p1.ID, "X")
	y := createCreditName(t, &p2.ID, "Y")
	mkCreditCandidate(t, x.ID, y.ID)

	link := acceptLink(t, x.ID, y.ID)
	assert.True(t, link.NeedsManual)
	assert.Zero(t, link.PersonID)

	// Nothing moved.
	assert.Equal(t, p1.ID, *personIDOfName(t, x.ID))
	assert.Equal(t, p2.ID, *personIDOfName(t, y.ID))
	// The candidate is flagged, not accepted.
	assert.Equal(t, model.CandidateStatusNeedsManual, candidateStatus(t, x.ID, y.ID))
}

// Detach reverses the link one name at a time; emptying an auto-linked person
// deletes it. A full detach-then-reapprove round trip proves reversibility and
// re-appliability (a fresh person is minted the second time).
func TestDetachReversesLinkAndDeletesEmptyPerson(t *testing.T) {
	cleanTables(t)
	role := seededRoleID(t)
	ctx := t.Context()
	actor := int64(9)

	a := createCreditName(t, nil, "A")
	b := createCreditName(t, nil, "B")
	giveCredits(t, a.ID, role, 2) // a is primary
	giveCredits(t, b.ID, role, 1)
	mkCreditCandidate(t, a.ID, b.ID)

	link := acceptLink(t, a.ID, b.ID)
	firstPerson := link.PersonID

	// Detach the primary name first: the person survives (b remains) and its
	// primary/display re-derive from the remaining name.
	require.NoError(t, testQueues.DetachName(ctx, a.ID, &actor))
	assert.Nil(t, personIDOfName(t, a.ID))
	var p model.CatalogPerson
	require.NoError(t, testDB.First(&p, firstPerson).Error)
	require.NotNil(t, p.PrimaryCreditNameID)
	assert.Equal(t, b.ID, *p.PrimaryCreditNameID, "primary re-pointed to the surviving name")
	assert.Equal(t, "B", p.DisplayName)

	// Detach the last name: the person is emptied and removed (tombstoned).
	require.NoError(t, testQueues.DetachName(ctx, b.ID, &actor))
	assert.Nil(t, personIDOfName(t, b.ID))
	var gone int64
	require.NoError(t, testDB.Unscoped().Model(&model.CatalogPerson{}).Where("id = ?", firstPerson).Count(&gone).Error)
	assert.Zero(t, gone, "empty person hard-deleted")
	assert.Equal(t, int64(1), revCount(t, model.EntityTypePerson, firstPerson, model.RevisionActionDeleted))

	// Re-approve (the pair could be re-proposed): a brand-new person is minted.
	require.NoError(t, testDB.Model(&model.CatalogMatchCandidate{}).
		Where("entity_type = ? AND a_id = ? AND b_id = ?", model.EntityTypeCreditName, a.ID, b.ID).
		Update("status", model.CandidateStatusPending).Error)
	link2 := acceptLink(t, a.ID, b.ID)
	assert.True(t, link2.Created)
	assert.NotEqual(t, firstPerson, link2.PersonID, "reversibility yields a fresh person, not the deleted one")
	assert.Equal(t, link2.PersonID, *personIDOfName(t, a.ID))
	assert.Equal(t, link2.PersonID, *personIDOfName(t, b.ID))

	// Detaching an unattached name is a clean error, not a silent no-op.
	orphan := createCreditName(t, nil, "loose")
	require.ErrorIs(t, testQueues.DetachName(ctx, orphan.ID, &actor), ErrProposalState)
}

// The linking path leaves the step-21 negative-knowledge guarantee intact: a
// rejected candidate stays rejected and is never linked.
func TestPersonLinkLeavesRejectedCandidateAlone(t *testing.T) {
	cleanTables(t)

	a := createCreditName(t, nil, "A")
	b := createCreditName(t, nil, "B")
	mkCreditCandidate(t, a.ID, b.ID)

	_, err := testQueues.DecideCandidate(t.Context(), CandidateDecision{
		EntityType: model.EntityTypeCreditName, AID: a.ID, BID: b.ID, Action: "reject", DecidedBy: 9,
	})
	require.NoError(t, err)
	assert.Equal(t, model.CandidateStatusRejected, candidateStatus(t, a.ID, b.ID))

	// A rejected candidate cannot be linked afterwards.
	_, err = testQueues.DecideCandidate(t.Context(), CandidateDecision{
		EntityType: model.EntityTypeCreditName, AID: a.ID, BID: b.ID, Action: "accept", DecidedBy: 9,
	})
	require.ErrorIs(t, err, ErrProposalState)
	assert.Nil(t, personIDOfName(t, a.ID))
	assert.Nil(t, personIDOfName(t, b.ID))
	// No person materialized.
	var persons int64
	require.NoError(t, testDB.Model(&model.CatalogPerson{}).Count(&persons).Error)
	assert.Zero(t, persons)
}
