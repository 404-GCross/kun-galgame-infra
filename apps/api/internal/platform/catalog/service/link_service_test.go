package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestPersonLinkCreatesPersonFromDoubleOrphan(t *testing.T) {
	cleanTables(t)
	role := seededRoleID(t)

	a := createCreditName(t, nil, "麻枝准")
	b := createCreditName(t, nil, "Jun Maeda")
	giveCredits(t, a.ID, role, 1)
	giveCredits(t, b.ID, role, 3)
	mkCreditCandidate(t, a.ID, b.ID)

	before := orphanCount(t)
	link := acceptLink(t, a.ID, b.ID)
	assert.True(t, link.Created)
	assert.NotZero(t, link.PersonID)
	assert.False(t, link.NeedsManual)

	require.Equal(t, link.PersonID, *personIDOfName(t, a.ID))
	require.Equal(t, link.PersonID, *personIDOfName(t, b.ID))
	assert.Equal(t, before-2, orphanCount(t))

	var p model.CatalogPerson
	require.NoError(t, testDB.First(&p, link.PersonID).Error)
	assert.Equal(t, "Jun Maeda", p.DisplayName)
	require.NotNil(t, p.PrimaryCreditNameID)
	assert.Equal(t, b.ID, *p.PrimaryCreditNameID)

	var vis int16
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Where("id = ?", a.ID).
		Select("link_visibility").Scan(&vis).Error)
	assert.Equal(t, model.LinkVisibilityPublic, vis)

	assert.Equal(t, int64(1), revCount(t, model.EntityTypePerson, link.PersonID, model.RevisionActionCreated))
	assert.Equal(t, int64(1), revCount(t, model.EntityTypeCreditName, a.ID, model.RevisionActionUpdated))
	assert.Equal(t, int64(1), revCount(t, model.EntityTypeCreditName, b.ID, model.RevisionActionUpdated))

	assert.Equal(t, model.CandidateStatusAccepted, candidateStatus(t, a.ID, b.ID))
}

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
	assert.Equal(t, int64(1), revCount(t, model.EntityTypePerson, host.ID, model.RevisionActionUpdated))
	assert.Equal(t, int64(1), revCount(t, model.EntityTypeCreditName, y.ID, model.RevisionActionUpdated))
	var p model.CatalogPerson
	require.NoError(t, testDB.First(&p, host.ID).Error)
	require.NotNil(t, p.PrimaryCreditNameID)
	assert.Equal(t, x.ID, *p.PrimaryCreditNameID)
}

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

	assert.Equal(t, p1.ID, *personIDOfName(t, x.ID))
	assert.Equal(t, p2.ID, *personIDOfName(t, y.ID))
	assert.Equal(t, model.CandidateStatusNeedsManual, candidateStatus(t, x.ID, y.ID))
}

func TestDetachReversesLinkAndDeletesEmptyPerson(t *testing.T) {
	cleanTables(t)
	role := seededRoleID(t)
	ctx := t.Context()
	actor := int64(9)

	a := createCreditName(t, nil, "A")
	b := createCreditName(t, nil, "B")
	giveCredits(t, a.ID, role, 2)
	giveCredits(t, b.ID, role, 1)
	mkCreditCandidate(t, a.ID, b.ID)

	link := acceptLink(t, a.ID, b.ID)
	firstPerson := link.PersonID

	require.NoError(t, testQueues.DetachName(ctx, a.ID, &actor))
	assert.Nil(t, personIDOfName(t, a.ID))
	var p model.CatalogPerson
	require.NoError(t, testDB.First(&p, firstPerson).Error)
	require.NotNil(t, p.PrimaryCreditNameID)
	assert.Equal(t, b.ID, *p.PrimaryCreditNameID, "primary re-pointed to the surviving name")
	assert.Equal(t, "B", p.DisplayName)

	require.NoError(t, testQueues.DetachName(ctx, b.ID, &actor))
	assert.Nil(t, personIDOfName(t, b.ID))
	var gone int64
	require.NoError(t, testDB.Unscoped().Model(&model.CatalogPerson{}).Where("id = ?", firstPerson).Count(&gone).Error)
	assert.Zero(t, gone, "empty person hard-deleted")
	assert.Equal(t, int64(1), revCount(t, model.EntityTypePerson, firstPerson, model.RevisionActionDeleted))

	require.NoError(t, testDB.Model(&model.CatalogMatchCandidate{}).
		Where("entity_type = ? AND a_id = ? AND b_id = ?", model.EntityTypeCreditName, a.ID, b.ID).
		Update("status", model.CandidateStatusPending).Error)
	link2 := acceptLink(t, a.ID, b.ID)
	assert.True(t, link2.Created)
	assert.NotEqual(t, firstPerson, link2.PersonID, "reversibility yields a fresh person, not the deleted one")
	assert.Equal(t, link2.PersonID, *personIDOfName(t, a.ID))
	assert.Equal(t, link2.PersonID, *personIDOfName(t, b.ID))

	orphan := createCreditName(t, nil, "loose")
	require.ErrorIs(t, testQueues.DetachName(ctx, orphan.ID, &actor), ErrProposalState)
}

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

	_, err = testQueues.DecideCandidate(t.Context(), CandidateDecision{
		EntityType: model.EntityTypeCreditName, AID: a.ID, BID: b.ID, Action: "accept", DecidedBy: 9,
	})
	require.ErrorIs(t, err, ErrProposalState)
	assert.Nil(t, personIDOfName(t, a.ID))
	assert.Nil(t, personIDOfName(t, b.ID))
	var persons int64
	require.NoError(t, testDB.Model(&model.CatalogPerson{}).Count(&persons).Error)
	assert.Zero(t, persons)
}
