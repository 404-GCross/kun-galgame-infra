package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	apierrors "api/pkg/errors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All tests in this file assume getRepos() has been called to set up
// testSubmissionSvc / testAdminSvc / testMessageRepo. They share the same
// test DB as the rest of the package and clean tables between cases.

func TestSubmit_WithVNDB(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g, err := testSubmissionSvc.Submit(ctx, 42, &dto.SubmitGalgameRequest{
		VNDBID:   "v999",
		NameZhCN: "测试投稿",
	})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Equal(t, model.GalgameStatusPending, g.Status)
	assert.Equal(t, 42, g.UserID)
	assert.Equal(t, "v999", g.VNDBID)

	// Revision 1, action=created
	var rev model.GalgameRevision
	require.NoError(t, testDB.Where("galgame_id = ?", g.ID).First(&rev).Error)
	assert.Equal(t, 1, rev.Revision)
	assert.Equal(t, "created", rev.Action)

	// Message: type=submitted, actor=42, target=NULL
	var msg model.GalgameMessage
	require.NoError(t, testDB.Where("galgame_id = ?", g.ID).First(&msg).Error)
	assert.Equal(t, model.MessageTypeSubmitted, msg.Type)
	require.NotNil(t, msg.ActorUserID)
	assert.Equal(t, 42, *msg.ActorUserID)
	assert.Nil(t, msg.TargetUserID)

	// Contributor written
	var contribCount int64
	testDB.Model(&model.GalgameContributor{}).
		Where("galgame_id = ? AND user_id = 42", g.ID).Count(&contribCount)
	assert.Equal(t, int64(1), contribCount)

	// VNDB link auto-created
	var linkCount int64
	testDB.Model(&model.GalgameLink{}).
		Where("galgame_id = ? AND name = 'VNDB'", g.ID).Count(&linkCount)
	assert.Equal(t, int64(1), linkCount)
}

func TestSubmit_NoVNDB(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g, err := testSubmissionSvc.Submit(ctx, 1, &dto.SubmitGalgameRequest{
		VNDBID:   "",
		NameZhCN: "国产作品",
	})
	require.NoError(t, err)
	assert.Equal(t, "", g.VNDBID)

	// No VNDB link when vndb_id empty
	var linkCount int64
	testDB.Model(&model.GalgameLink{}).
		Where("galgame_id = ?", g.ID).Count(&linkCount)
	assert.Equal(t, int64(0), linkCount)

	// Two empty-vndb submissions must coexist (partial unique index)
	_, err = testSubmissionSvc.Submit(ctx, 2, &dto.SubmitGalgameRequest{
		VNDBID:   "",
		NameZhCN: "另一个原创",
	})
	require.NoError(t, err, "partial unique must allow multiple empty vndb_id")
}

func TestSubmit_DuplicateVNDB(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	_, err := testSubmissionSvc.Submit(ctx, 1, &dto.SubmitGalgameRequest{
		VNDBID:   "v123",
		NameZhCN: "first",
	})
	require.NoError(t, err)

	_, err = testSubmissionSvc.Submit(ctx, 2, &dto.SubmitGalgameRequest{
		VNDBID:   "v123",
		NameZhCN: "second",
	})
	require.Error(t, err)
	assert.True(t, apierrors.Is(err, apierrors.ErrGalgameVNDBExists))
}

func TestSubmit_QuotaEnforced(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	userID := 99

	for i := 0; i < DailySubmissionQuota; i++ {
		_, err := testSubmissionSvc.Submit(ctx, userID, &dto.SubmitGalgameRequest{
			NameZhCN: "fill",
		})
		require.NoError(t, err, "submission %d should succeed", i+1)
	}

	_, err := testSubmissionSvc.Submit(ctx, userID, &dto.SubmitGalgameRequest{
		NameZhCN: "overflow",
	})
	require.Error(t, err)
	assert.True(t, apierrors.Is(err, apierrors.ErrGalgameQuotaExceeded))
}

func TestClaim_Status2To0(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Seed a VNDB draft (status=2, system user_id=1)
	draft := &model.Galgame{
		VNDBID: "v555",
		Status: model.GalgameStatusVNDBDraft,
		UserID: 1,
	}
	require.NoError(t, testDB.Create(draft).Error)

	claimed, err := testSubmissionSvc.Claim(ctx, 42, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, model.GalgameStatusPublished, claimed.Status)
	assert.Equal(t, 42, claimed.UserID)

	// Claimer added as contributor
	var c int64
	testDB.Model(&model.GalgameContributor{}).
		Where("galgame_id = ? AND user_id = 42", draft.ID).Count(&c)
	assert.Equal(t, int64(1), c)

	// Message: type=claimed
	var msg model.GalgameMessage
	require.NoError(t, testDB.Where("galgame_id = ? AND type = ?", draft.ID, model.MessageTypeClaimed).
		First(&msg).Error)
	require.NotNil(t, msg.ActorUserID)
	assert.Equal(t, 42, *msg.ActorUserID)
}

func TestClaim_RejectsNonDraft(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// status=0 (published, not 2) — must reject
	g := &model.Galgame{Status: model.GalgameStatusPublished, UserID: 1, VNDBID: "v1"}
	require.NoError(t, testDB.Create(g).Error)

	_, err := testSubmissionSvc.Claim(ctx, 42, g.ID)
	require.Error(t, err)
	assert.True(t, apierrors.Is(err, apierrors.ErrGalgameClaimNotDraft))
}

func TestClaim_Concurrent(t *testing.T) {
	cleanTables(t)
	getRepos()

	draft := &model.Galgame{
		VNDBID: "v888",
		Status: model.GalgameStatusVNDBDraft,
		UserID: 1,
	}
	require.NoError(t, testDB.Create(draft).Error)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var winErr, loseErr error
	winCount := 0
	loseCount := 0

	// Two concurrent claimers — exactly one should win.
	for _, userID := range []int{42, 43} {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()
			_, err := testSubmissionSvc.Claim(context.Background(), userID, draft.ID)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winCount++
				_ = winErr
			} else {
				loseCount++
				loseErr = err
			}
		}(userID)
	}
	wg.Wait()

	assert.Equal(t, 1, winCount, "exactly one claimer should win")
	assert.Equal(t, 1, loseCount, "the other must get an error")
	require.Error(t, loseErr)
	assert.True(t, apierrors.Is(loseErr, apierrors.ErrGalgameClaimNotDraft))
}

func TestPatchDraft_DeclinedToPending(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Seed a declined draft owned by userID=7
	g := &model.Galgame{
		Status:   model.GalgameStatusDeclined,
		UserID:   7,
		NameZhCN: "old name",
	}
	require.NoError(t, testDB.Create(g).Error)

	newName := "updated name"
	patched, err := testSubmissionSvc.PatchDraft(ctx, 7, g.ID, &dto.UpdateGalgameRequest{
		NameZhCN: &newName,
	})
	require.NoError(t, err)
	assert.Equal(t, model.GalgameStatusPending, patched.Status, "declined → pending auto-flip")
	assert.Equal(t, "updated name", patched.NameZhCN)

	// edited_pending message written
	var msg model.GalgameMessage
	require.NoError(t, testDB.Where("galgame_id = ? AND type = ?", g.ID, model.MessageTypeEditedPending).
		First(&msg).Error)
}

func TestPatchDraft_NonOwnerRejected(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g := &model.Galgame{Status: model.GalgameStatusPending, UserID: 7}
	require.NoError(t, testDB.Create(g).Error)

	name := "evil"
	_, err := testSubmissionSvc.PatchDraft(ctx, 999, g.ID, &dto.UpdateGalgameRequest{
		NameZhCN: &name,
	})
	require.Error(t, err)
	assert.True(t, apierrors.Is(err, apierrors.ErrGalgameSubmitterOnly))
}

func TestPatchDraft_PublishedRejected(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Owner trying to patch a published entry — must use PUT /galgame/:gid not PATCH.
	g := &model.Galgame{Status: model.GalgameStatusPublished, UserID: 7}
	require.NoError(t, testDB.Create(g).Error)

	name := "edit"
	_, err := testSubmissionSvc.PatchDraft(ctx, 7, g.ID, &dto.UpdateGalgameRequest{
		NameZhCN: &name,
	})
	require.Error(t, err)
	assert.True(t, apierrors.Is(err, apierrors.ErrGalgameDraftStatusInvalid))
}

func TestAdmin_Approve_WritesApprovedMessage(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Submitter user_id=11 submits → status=3
	g := &model.Galgame{Status: model.GalgameStatusPending, UserID: 11, NameZhCN: "x"}
	require.NoError(t, testDB.Create(g).Error)

	// Admin user_id=1 approves
	err := testAdminSvc.UpdateStatus(ctx, 1, g.ID, model.GalgameStatusPublished, "looks good")
	require.NoError(t, err)

	var updated model.Galgame
	require.NoError(t, testDB.First(&updated, g.ID).Error)
	assert.Equal(t, model.GalgameStatusPublished, updated.Status)

	var msg model.GalgameMessage
	require.NoError(t, testDB.Where("galgame_id = ? AND type = ?", g.ID, model.MessageTypeApproved).
		First(&msg).Error)
	require.NotNil(t, msg.TargetUserID)
	assert.Equal(t, 11, *msg.TargetUserID, "target must be the submitter, not the admin")

	// Revision action=approved with note
	var rev model.GalgameRevision
	require.NoError(t, testDB.Where("galgame_id = ? AND action = ?", g.ID, "approved").
		First(&rev).Error)
	assert.Equal(t, "looks good", rev.Note)
}

func TestAdmin_Decline_RequiresPendingSource(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Published entry (status=0) — admin tries to decline → must reject
	g := &model.Galgame{Status: model.GalgameStatusPublished, UserID: 11}
	require.NoError(t, testDB.Create(g).Error)

	err := testAdminSvc.UpdateStatus(ctx, 1, g.ID, model.GalgameStatusDeclined, "no")
	require.Error(t, err)
	assert.True(t, apierrors.Is(err, apierrors.ErrGalgameDraftStatusInvalid))
}

func TestAdmin_Unban_WritesUnbannedMessage(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g := &model.Galgame{Status: model.GalgameStatusBanned, UserID: 11, NameZhCN: "redeemed"}
	require.NoError(t, testDB.Create(g).Error)

	err := testAdminSvc.UpdateStatus(ctx, 1, g.ID, model.GalgameStatusPublished, "reinstated")
	require.NoError(t, err)

	// Must write 'unbanned' message targeting the owner — critical for the
	// kungal/moyu cron to learn this transition.
	var msg model.GalgameMessage
	require.NoError(t, testDB.Where("galgame_id = ? AND type = ?", g.ID, model.MessageTypeUnbanned).
		First(&msg).Error)
	require.NotNil(t, msg.TargetUserID)
	assert.Equal(t, 11, *msg.TargetUserID)
}

func TestListMine_AttachesDeclineReason(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Seed: user 5 has 2 submissions — one pending, one declined.
	pending := &model.Galgame{Status: model.GalgameStatusPending, UserID: 5, NameZhCN: "pending"}
	require.NoError(t, testDB.Create(pending).Error)
	declined := &model.Galgame{Status: model.GalgameStatusDeclined, UserID: 5, NameZhCN: "declined"}
	require.NoError(t, testDB.Create(declined).Error)

	// Write the decline message that ListMine should pick up.
	payload, _ := json.Marshal(map[string]any{"reason": "VNDB ID 错"})
	adminUserID := 1
	target := 5
	require.NoError(t, testDB.Create(&model.GalgameMessage{
		Type:         model.MessageTypeDeclined,
		GalgameID:    declined.ID,
		ActorUserID:  &adminUserID,
		TargetUserID: &target,
		Payload:      payload,
	}).Error)

	items, total, err := testSubmissionSvc.ListMine(ctx, 5, &dto.ListMineRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)

	// Find the declined one — its decline_reason should be populated.
	var declinedItem, pendingItem *dto.MineGalgame
	for i := range items {
		switch items[i].ID {
		case declined.ID:
			declinedItem = &items[i]
		case pending.ID:
			pendingItem = &items[i]
		}
	}
	require.NotNil(t, declinedItem)
	require.NotNil(t, pendingItem)
	assert.Equal(t, "VNDB ID 错", declinedItem.DeclineReason)
	assert.Equal(t, "", pendingItem.DeclineReason, "pending entries have no decline reason")
}

func TestListMine_OnlyReturnsCallersOwn(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// User 1 submission
	a := &model.Galgame{Status: model.GalgameStatusPending, UserID: 1, NameZhCN: "A"}
	require.NoError(t, testDB.Create(a).Error)
	// User 2 submission
	b := &model.Galgame{Status: model.GalgameStatusPending, UserID: 2, NameZhCN: "B"}
	require.NoError(t, testDB.Create(b).Error)

	items, total, err := testSubmissionSvc.ListMine(ctx, 1, &dto.ListMineRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, a.ID, items[0].ID)
	assert.NotEqual(t, b.ID, items[0].ID)
}

func TestMessageRepo_ListMineCrossUserIsolation(t *testing.T) {
	cleanTables(t)
	getRepos()

	// Two messages, one for user 1, one for user 2.
	one := 1
	two := 2
	require.NoError(t, testDB.Create(&model.GalgameMessage{
		Type:         model.MessageTypeApproved,
		GalgameID:    1,
		TargetUserID: &one,
	}).Error)
	require.NoError(t, testDB.Create(&model.GalgameMessage{
		Type:         model.MessageTypeApproved,
		GalgameID:    2,
		TargetUserID: &two,
	}).Error)

	msgs, total, err := testMessageRepo.ListMine(context.Background(), 1, 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, msgs, 1)
	require.NotNil(t, msgs[0].TargetUserID)
	assert.Equal(t, 1, *msgs[0].TargetUserID, "user 1 must not see user 2's messages")
}

func TestDeleteDraft_RemovesGalgameAndRelations(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g, err := testSubmissionSvc.Submit(ctx, 50, &dto.SubmitGalgameRequest{
		VNDBID:   "v77",
		NameZhCN: "to be deleted",
		Aliases:  "别名",
		TagIDs:   []int{createTestTag(t, "tg", "content")},
	})
	require.NoError(t, err)

	require.NoError(t, testSubmissionSvc.DeleteDraft(ctx, 50, g.ID))

	// Galgame gone
	var cnt int64
	testDB.Model(&model.Galgame{}).Where("id = ?", g.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt)

	// Relations gone
	testDB.Model(&model.GalgameAlias{}).Where("galgame_id = ?", g.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt)
	testDB.Model(&model.GalgameRevision{}).Where("galgame_id = ?", g.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt)
	testDB.Model(&model.GalgameTagRelation{}).Where("galgame_id = ?", g.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt)

	// Messages stay (intentional ghost; downstream cron skips nil galgame)
	testDB.Model(&model.GalgameMessage{}).Where("galgame_id = ?", g.ID).Count(&cnt)
	assert.GreaterOrEqual(t, cnt, int64(1), "submitted message persists as ghost")
}
