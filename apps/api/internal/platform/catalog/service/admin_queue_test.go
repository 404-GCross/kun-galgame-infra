package service

import (
	"fmt"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Step-05 carry-over ①: merge endpoints must be live entities.
func TestMergeRejectsDeadEndpoints(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	alive := createPerson(t, "alive")
	dead := createPerson(t, "dead")
	require.NoError(t, testDB.Delete(&model.CatalogPerson{}, dead.ID).Error) // soft delete

	_, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, dead.ID, alive.ID, 7, "")
	require.ErrorIs(t, err, ErrEntityNotLive)
	_, err = testMerge.ProposeMerge(ctx, model.EntityTypePerson, alive.ID, dead.ID, 7, "")
	require.ErrorIs(t, err, ErrEntityNotLive)

	// Execution-time check: an endpoint dying during the cooling-off window
	// invalidates the approved proposal.
	other := createPerson(t, "other")
	p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, other.ID, alive.ID, 7, "")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testDB.Delete(&model.CatalogPerson{}, other.ID).Error)
	require.ErrorIs(t, testMerge.ExecuteMerge(ctx, p.ID, nil), ErrEntityNotLive)
}

// Step-05 carry-over ②: the probable confirmation bucket must include the
// rows a merge demoted from exact (matched_by untouched, verified_at empty).
func TestProbableBucketIncludesMergeDemotedRows(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createPerson(t, "target")
	source := createPerson(t, "source")
	// Same source (bangumi=3), different external ids, both exact → the
	// merge demotes both to probable.
	addExternalRef(t, model.EntityTypePerson, target.ID, 3, "bgm-1", model.LinkKindExact)
	addExternalRef(t, model.EntityTypePerson, source.ID, 3, "bgm-2", model.LinkKindExact)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, source.ID, target.ID, 7, "")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	items, total, err := testQueues.ListProbableRefs(ctx, RefFilters{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "both demoted exacts enter the confirmation bucket")
	for _, item := range items {
		assert.Equal(t, "import:test", item.MatchedBy, "demotion keeps the original matched_by")
		assert.Nil(t, item.VerifiedAt)
		assert.Equal(t, target.ID, item.EntityID)
		assert.Equal(t, target.DisplayName, item.Entity.DisplayName, "entity brief attached")
	}
}

// Candidate decisions: accept opens a proposal, reject keeps the row
// forever, defer stays decidable — and candidate-reject never touches
// catalog_match_rejection (T0.4 semantics split).
func TestDecideCandidatePaths(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	mk := func(a, b int64) {
		require.NoError(t, testDB.Create(&model.CatalogMatchCandidate{
			EntityType: model.EntityTypePerson, AID: min(a, b), BID: max(a, b),
			Reason: model.CandidateReasonNameNormEqual, Status: model.CandidateStatusPending,
		}).Error)
	}
	p1, p2 := createPerson(t, "P1"), createPerson(t, "P2")
	p3, p4 := createPerson(t, "P3"), createPerson(t, "P4")
	p5, p6 := createPerson(t, "P5"), createPerson(t, "P6")
	mk(p1.ID, p2.ID)
	mk(p3.ID, p4.ID)
	mk(p5.ID, p6.ID)

	// accept → candidate accepted + proposal opened with the given direction.
	outcome, err := testQueues.DecideCandidate(ctx, CandidateDecision{
		EntityType: model.EntityTypePerson, AID: p1.ID, BID: p2.ID,
		Action: "accept", SourceID: p2.ID, TargetID: p1.ID, Note: "same person", DecidedBy: 9,
	})
	require.NoError(t, err)
	require.NotNil(t, outcome.Proposal)
	assert.Equal(t, p2.ID, outcome.Proposal.SourceEntityID)
	assert.Equal(t, p1.ID, outcome.Proposal.TargetEntityID)
	var cand model.CatalogMatchCandidate
	require.NoError(t, testDB.Where("a_id = ? AND b_id = ?", p1.ID, p2.ID).First(&cand).Error)
	assert.Equal(t, model.CandidateStatusAccepted, cand.Status)
	require.NotNil(t, cand.DecidedBy)
	assert.Equal(t, int64(9), *cand.DecidedBy)

	// A decided candidate cannot be re-decided.
	_, err = testQueues.DecideCandidate(ctx, CandidateDecision{
		EntityType: model.EntityTypePerson, AID: p1.ID, BID: p2.ID, Action: "reject", DecidedBy: 9,
	})
	require.ErrorIs(t, err, ErrProposalState)

	// reject → row kept with status rejected; the rejection table stays
	// EMPTY (candidate verdicts are not external-ref negative knowledge).
	_, err = testQueues.DecideCandidate(ctx, CandidateDecision{
		EntityType: model.EntityTypePerson, AID: p3.ID, BID: p4.ID, Action: "reject", DecidedBy: 9,
	})
	require.NoError(t, err)
	var rejCount, candCount int64
	testDB.Model(&model.CatalogMatchRejection{}).Count(&rejCount)
	assert.Zero(t, rejCount, "candidate reject must NOT write catalog_match_rejection")
	testDB.Model(&model.CatalogMatchCandidate{}).Where("status = ?", model.CandidateStatusRejected).Count(&candCount)
	assert.Equal(t, int64(1), candCount)

	// defer → still decidable afterwards.
	_, err = testQueues.DecideCandidate(ctx, CandidateDecision{
		EntityType: model.EntityTypePerson, AID: p5.ID, BID: p6.ID, Action: "defer", DecidedBy: 9,
	})
	require.NoError(t, err)
	_, err = testQueues.DecideCandidate(ctx, CandidateDecision{
		EntityType: model.EntityTypePerson, AID: p5.ID, BID: p6.ID,
		Action: "accept", SourceID: p6.ID, TargetID: p5.ID, DecidedBy: 9,
	})
	require.NoError(t, err)

	// Listing attaches entity briefs.
	items, total, err := testQueues.ListCandidates(ctx, CandidateFilters{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.NotEmpty(t, items)
	assert.NotEmpty(t, items[0].A.DisplayName)
}

// Ref confirmation: probable → exact; losing the exact slot is a conflict;
// rejection deletes the ref and records mandatory-reason negative knowledge.
func TestConfirmAndRejectRefs(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	holder := createPerson(t, "holder")
	contender := createPerson(t, "contender")
	addExternalRef(t, model.EntityTypePerson, holder.ID, 3, "bgm-9", model.LinkKindExact)
	addExternalRef(t, model.EntityTypePerson, contender.ID, 3, "bgm-9", model.LinkKindProbable)
	addExternalRef(t, model.EntityTypePerson, contender.ID, 2, "s77", model.LinkKindProbable)

	// Confirming the contender's probable for an already-exact identity →
	// conflict, and the error NAMES the holder (the lookup must run outside
	// the aborted transaction — regression guard for the holder=0 bug).
	err := testQueues.ConfirmRef(ctx, RefKey{
		EntityType: model.EntityTypePerson, EntityID: contender.ID, SourceID: 3, ExternalID: "bgm-9",
	}, 9)
	require.ErrorIs(t, err, ErrExactTaken)
	assert.Contains(t, err.Error(), fmt.Sprintf("held by entity %d", holder.ID))

	// A free identity promotes cleanly with verifier bookkeeping.
	require.NoError(t, testQueues.ConfirmRef(ctx, RefKey{
		EntityType: model.EntityTypePerson, EntityID: contender.ID, SourceID: 2, ExternalID: "s77",
	}, 9))
	var ref model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_id = ? AND source_id = ?", contender.ID, 2).First(&ref).Error)
	assert.Equal(t, model.LinkKindExact, ref.LinkKind)
	require.NotNil(t, ref.VerifiedBy)
	assert.Equal(t, int64(9), *ref.VerifiedBy)
	assert.NotNil(t, ref.VerifiedAt)

	// Rejection requires a reason...
	err = testQueues.RejectRef(ctx, RefKey{
		EntityType: model.EntityTypePerson, EntityID: contender.ID, SourceID: 3, ExternalID: "bgm-9",
	}, "", 9)
	require.Error(t, err)
	// ...and with one, deletes the ref + records the negative knowledge.
	require.NoError(t, testQueues.RejectRef(ctx, RefKey{
		EntityType: model.EntityTypePerson, EntityID: contender.ID, SourceID: 3, ExternalID: "bgm-9",
	}, "homonym, different person", 9))
	var refCount int64
	testDB.Model(&model.CatalogExternalRef{}).Where("entity_id = ? AND source_id = ?", contender.ID, 3).Count(&refCount)
	assert.Zero(t, refCount)
	var rej model.CatalogMatchRejection
	require.NoError(t, testDB.Where("entity_id = ? AND source_id = ?", contender.ID, 3).First(&rej).Error)
	assert.Equal(t, "homonym, different person", rej.Reason)
	require.NotNil(t, rej.RejectedBy)
	assert.Equal(t, int64(9), *rej.RejectedBy)
}
