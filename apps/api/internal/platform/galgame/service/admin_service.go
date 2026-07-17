package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AdminService wraps admin operations with their cross-table side effects:
// status changes write revisions and emit messages to the submitter.
//
// Allowed target statuses on UpdateStatus:
//   - 0 (publish)  — when source is 3 (pending), emits 'approved' message to submitter
//   - 1 (ban)      — emits 'banned' message (no target)
//   - 4 (decline)  — only allowed from source 3, emits 'declined' with reason
//
// 2 and 3 as targets are not allowed (see admin_dto.go).
type AdminService struct {
	galgameRepo *repository.GalgameRepository
	messageRepo *repository.MessageRepository
	// E2a strangler: status transitions are engine direct edits of the
	// perm-gated galgame.game.status field (action=direct, changed_fields
	// says what moved — the old per-transition action vocabulary lives on
	// only in migrated rows' legacy_action).
	edit *editing.Engine
}

// NewAdminService creates an AdminService.
func NewAdminService(g *repository.GalgameRepository, m *repository.MessageRepository) *AdminService {
	return &AdminService{galgameRepo: g, messageRepo: m}
}

// WithEditing wires the editing engine (always set by Mount).
func (s *AdminService) WithEditing(eng *editing.Engine) *AdminService {
	s.edit = eng
	return s
}

// UpdateStatus changes a galgame's status through the engine and emits the
// corresponding submitter message. The engine call owns the column write +
// revision; the message follows only when the transition landed (the old
// single-transaction atomicity narrows to that ordering — accepted, same
// cross-pool posture as everywhere else in E2a).
func (s *AdminService) UpdateStatus(ctx context.Context, adminUserID, gid, newStatus int, reason string) error {
	g, err := s.galgameRepo.FindByID(ctx, gid)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return err
	}

	// decline (→4) is only valid from pending (3)
	if newStatus == model.GalgameStatusDeclined && g.Status != model.GalgameStatusPending {
		return errors.NewWithCode(errors.ErrGalgameDraftStatusInvalid)
	}

	if g.Status == newStatus {
		// No-op transition. Skip both revision and message to keep the
		// audit log meaningful.
		return nil
	}

	// 1. Engine direct edit: status column + revision in one merge. A
	// concurrent identical transition surfaces as ErrNoEffectiveChanges —
	// the same "nothing to do" as the no-op skip above.
	if _, _, err := s.edit.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(gid),
		Patch:      map[string]any{editspec.FieldStatus: float64(newStatus)},
		Note:       reason,
		Actor:      editActor(adminUserID, 2, perm.EditGameStatus),
	}); err != nil {
		if err == editing.ErrNoEffectiveChanges {
			return nil
		}
		return mapEngineWriteError(err)
	}

	// 2. Pick the message type from the transition (the old revision action
	// vocabulary reduced to its message half).
	var msgType string
	switch newStatus {
	case model.GalgameStatusPublished:
		if g.Status == model.GalgameStatusBanned {
			msgType = model.MessageTypeUnbanned
		} else {
			// pending → approved; other transitions to 0 (e.g. declined →
			// 0) are rare; treat as approved for the submitter's benefit.
			msgType = model.MessageTypeApproved
		}
	case model.GalgameStatusBanned:
		msgType = model.MessageTypeBanned
	case model.GalgameStatusDeclined:
		msgType = model.MessageTypeDeclined
	default:
		// Reachable only if DTO validation missed something; defensive —
		// the transition landed, it just produces no message.
	}
	if msgType == "" {
		return nil
	}

	// Every approve / decline / ban / unban message targets the current
	// owner (galgame.user_id). This serves two purposes:
	//   1. The owner gets a notification via /messages/mine
	//   2. /messages/feed (filtered by target_user_id IS NOT NULL) ships
	//      it to kungal/moyu cron so they can sync wiki_status_snapshot.
	// Without a target, banned/unbanned would never reach the downstream
	// cron — making local stats drift permanently out of date.
	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner := g.UserID
		msg := &model.GalgameMessage{
			Type:         msgType,
			GalgameID:    gid,
			ActorUserID:  &adminUserID,
			TargetUserID: &owner,
		}
		var payload []byte
		switch msgType {
		case model.MessageTypeApproved:
			payload, _ = json.Marshal(map[string]any{
				"approved_by": adminUserID,
				"note":        reason,
			})
		case model.MessageTypeDeclined:
			payload, _ = json.Marshal(map[string]any{
				"declined_by": adminUserID,
				"reason":      reason,
			})
		case model.MessageTypeBanned:
			payload, _ = json.Marshal(map[string]any{
				"banned_by": adminUserID,
				"reason":    reason,
			})
		case model.MessageTypeUnbanned:
			payload, _ = json.Marshal(map[string]any{
				"unbanned_by": adminUserID,
				"note":        reason,
			})
		}
		msg.Payload = datatypes.JSON(payload)
		return s.messageRepo.Create(ctx, tx, msg)
	})
}

// BanGalgamesByUser soft-deletes (status→1, banned/hidden) every still-
// visible galgame created by targetUserID. It is the wiki content-side
// companion to the OAuth anonymize action for severe spam: the account is
// scrubbed on the identity side, the content is hidden here.
//
// It reuses UpdateStatus per item rather than a raw bulk UPDATE so each
// galgame gets the same revision + 'banned' message as a manual ban — the
// message propagates to kungal/moyu via /messages/feed, keeping their local
// wiki_status_snapshot mirrors in sync. Rows + prior revisions are preserved
// (soft delete), so a mis-flag can be reverted via the normal status path.
//
// Returns the ids actually banned (for the caller to re-sync search). Only
// status 0 (published) + 3 (pending) are targeted; already declined/banned
// galgame are left as-is.
func (s *AdminService) BanGalgamesByUser(ctx context.Context, adminUserID, targetUserID int, reason string) (banned []int, failed []int, err error) {
	var ids []int
	if err := s.galgameRepo.DB().WithContext(ctx).
		Model(&model.Galgame{}).
		Where("user_id = ? AND status IN ?", targetUserID, []int{
			model.GalgameStatusPublished, model.GalgameStatusPending,
		}).
		Pluck("id", &ids).Error; err != nil {
		return nil, nil, err
	}

	banned = make([]int, 0, len(ids))
	for _, id := range ids {
		if e := s.UpdateStatus(ctx, adminUserID, id, model.GalgameStatusBanned, reason); e != nil {
			// Keep going on per-item failure (e.g. a concurrent status
			// change) — partial cleanup beats aborting the whole batch — but
			// return the failed ids so the caller can surface/retry them.
			slog.Warn("ban galgames by user: item failed", "gid", id, "target_user", targetUserID, "err", e)
			failed = append(failed, id)
			continue
		}
		banned = append(banned, id)
	}
	return banned, failed, nil
}
