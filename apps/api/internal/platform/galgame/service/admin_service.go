package service

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/model"
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
}

// NewAdminService creates an AdminService.
func NewAdminService(g *repository.GalgameRepository, m *repository.MessageRepository) *AdminService {
	return &AdminService{galgameRepo: g, messageRepo: m}
}

// UpdateStatus changes a galgame's status and writes the corresponding
// revision + message in one transaction.
func (s *AdminService) UpdateStatus(ctx context.Context, adminUID, gid, newStatus int, reason string) error {
	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		g, err := s.galgameRepo.FindForUpdate(tx, gid)
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
			// No-op transition. Still allowed to write a message? Skip both
			// to keep the audit log meaningful.
			return nil
		}

		// 1. Update status
		if err := tx.Model(&model.Galgame{}).Where("id = ?", gid).
			Update("status", newStatus).Error; err != nil {
			return err
		}

		// 2. Pick revision action + message type from the transition
		var revAction, msgType string
		switch newStatus {
		case model.GalgameStatusPublished:
			if g.Status == model.GalgameStatusPending {
				revAction = "approved"
				msgType = model.MessageTypeApproved
			} else if g.Status == model.GalgameStatusBanned {
				revAction = "unbanned"
				// no message — unbanned is admin-internal
			} else {
				// Other transitions to 0 (e.g. from 4 declined → 0) are
				// rare; treat as approved for the submitter's benefit.
				revAction = "approved"
				msgType = model.MessageTypeApproved
			}
		case model.GalgameStatusBanned:
			revAction = "banned"
			msgType = model.MessageTypeBanned
		case model.GalgameStatusDeclined:
			revAction = "declined"
			msgType = model.MessageTypeDeclined
		default:
			// Reachable only if DTO validation missed something; defensive.
			revAction = "status_changed"
		}

		// 3. Write revision (snapshot reflects post-update state)
		nextRev, err := repository.NextRevision(tx, gid)
		if err != nil {
			return err
		}
		full, err := loadGalgameWithRelations(tx, gid)
		if err != nil {
			return err
		}
		snapshot := model.TakeSnapshot(full)
		snapJSON, err := snapshot.ToJSON()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameRevision{
			GalgameID: gid,
			Revision:  nextRev,
			UserID:    adminUID,
			Action:    revAction,
			Note:      reason,
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		// 4. Write message — only when msgType is set (transitions that
		// produce a meaningful event for the audience).
		if msgType == "" {
			return nil
		}

		msg := &model.GalgameMessage{
			Type:        msgType,
			GalgameID:   gid,
			ActorUserID: &adminUID,
		}
		// Approve / decline are user-facing; ban is admin-only audit.
		switch msgType {
		case model.MessageTypeApproved:
			submitter := g.UserID
			msg.TargetUserID = &submitter
			payload, _ := json.Marshal(map[string]any{
				"approved_by": adminUID,
				"note":        reason,
			})
			msg.Payload = datatypes.JSON(payload)
		case model.MessageTypeDeclined:
			submitter := g.UserID
			msg.TargetUserID = &submitter
			payload, _ := json.Marshal(map[string]any{
				"declined_by": adminUID,
				"reason":      reason,
			})
			msg.Payload = datatypes.JSON(payload)
		case model.MessageTypeBanned:
			// no target — pure admin audit
			payload, _ := json.Marshal(map[string]any{
				"banned_by": adminUID,
				"reason":    reason,
			})
			msg.Payload = datatypes.JSON(payload)
		}
		return s.messageRepo.Create(ctx, tx, msg)
	})
}
