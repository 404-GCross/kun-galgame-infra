package service

import (
	"context"
	"encoding/json"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// DailySubmissionQuota is the max number of new galgame submissions a single
// user can make in a calendar day. Currently a constant; promote to env if
// per-site tuning is needed.
const DailySubmissionQuota = 5

// SubmissionService owns the user-submission lifecycle: submit / claim /
// patch-draft / delete-draft / list-mine. It composes the existing repos
// plus the new MessageRepository to keep galgame and message writes atomic.
type SubmissionService struct {
	galgameRepo *repository.GalgameRepository
	messageRepo *repository.MessageRepository
}

// NewSubmissionService creates a SubmissionService.
func NewSubmissionService(g *repository.GalgameRepository, m *repository.MessageRepository) *SubmissionService {
	return &SubmissionService{galgameRepo: g, messageRepo: m}
}

// Submit creates a new galgame in status=3 (pending review). vndb_id may be
// empty; when non-empty it must match `v\d+` and be globally unique across
// all statuses. Daily per-user quota is enforced.
//
// Side effects (all in one transaction):
//   - INSERT galgame status=3 user_id=uid
//   - INSERT aliases / tag / official / engine relations
//   - INSERT contributor (uid)
//   - INSERT VNDB link if vndb_id non-empty
//   - INSERT revision 1 (action='created')
//   - INSERT message (type='submitted', actor=uid, target=NULL)
func (s *SubmissionService) Submit(ctx context.Context, uid int, req *dto.SubmitGalgameRequest) (*model.Galgame, error) {
	// VNDB id validation: empty allowed; non-empty must match pattern.
	if req.VNDBID != "" && !vndbIDRegex.MatchString(req.VNDBID) {
		return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
	}

	// Quota: count today's submissions for this user (status IN 3,4 — declined
	// rejects still count to avoid quota-evasion by submit→edit cycles).
	count, err := s.galgameRepo.CountSubmissionsToday(ctx, uid)
	if err != nil {
		return nil, err
	}
	if count >= DailySubmissionQuota {
		return nil, errors.NewWithCode(errors.ErrGalgameQuotaExceeded)
	}

	// Global vndb_id uniqueness (across any status). Empty vndb_id skips the check.
	if req.VNDBID != "" {
		exists, _, err := s.galgameRepo.ExistsByVNDBID(ctx, req.VNDBID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.NewWithCode(errors.ErrGalgameVNDBExists)
		}
	}

	var galgame model.Galgame

	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		galgame = model.Galgame{
			VNDBID:           req.VNDBID,
			NameEnUS:         req.NameEnUS,
			NameJaJP:         req.NameJaJP,
			NameZhCN:         req.NameZhCN,
			NameZhTW:         req.NameZhTW,
			Banner:           req.Banner,
			BannerImageHash:  strToPtr(req.BannerImageHash),
			IntroEnUS:        req.IntroEnUS,
			IntroJaJP:        req.IntroJaJP,
			IntroZhCN:        req.IntroZhCN,
			IntroZhTW:        req.IntroZhTW,
			ContentLimit:     req.ContentLimit,
			OriginalLanguage: req.OriginalLanguage,
			AgeLimit:         req.AgeLimit,
			UserID:           uid,
			SeriesID:         req.SeriesID,
			Status:           model.GalgameStatusPending,
		}
		if galgame.ContentLimit == "" {
			galgame.ContentLimit = "sfw"
		}
		if galgame.AgeLimit == "" {
			galgame.AgeLimit = "r18"
		}
		if galgame.OriginalLanguage == "" {
			galgame.OriginalLanguage = "ja-jp"
		}
		if err := tx.Create(&galgame).Error; err != nil {
			return err
		}

		// Aliases
		if req.Aliases != "" {
			for _, name := range strings.Split(req.Aliases, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if err := tx.Create(&model.GalgameAlias{GalgameID: galgame.ID, Name: name}).Error; err != nil {
					return err
				}
			}
		}

		// Tag / Official / Engine relations
		for _, id := range req.TagIDs {
			if err := tx.Create(&model.GalgameTagRelation{GalgameID: galgame.ID, TagID: id}).Error; err != nil {
				return err
			}
		}
		for _, id := range req.OfficialIDs {
			if err := tx.Create(&model.GalgameOfficialRelation{GalgameID: galgame.ID, OfficialID: id}).Error; err != nil {
				return err
			}
		}
		for _, id := range req.EngineIDs {
			if err := tx.Create(&model.GalgameEngineRelation{GalgameID: galgame.ID, EngineID: id}).Error; err != nil {
				return err
			}
		}

		// Contributor
		if err := tx.Create(&model.GalgameContributor{GalgameID: galgame.ID, UserID: uid}).Error; err != nil {
			return err
		}

		// VNDB link (only if vndb_id present)
		if req.VNDBID != "" {
			if err := tx.Create(&model.GalgameLink{
				GalgameID: galgame.ID, UserID: uid,
				Name: "VNDB", Link: "https://vndb.org/" + req.VNDBID,
			}).Error; err != nil {
				return err
			}
		}

		// Revision 1
		full, err := loadGalgameWithRelations(tx, galgame.ID)
		if err != nil {
			return err
		}
		snapshot := model.TakeSnapshot(full)
		snapJSON, err := snapshot.ToJSON()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameRevision{
			GalgameID: galgame.ID,
			Revision:  1,
			UserID:    uid,
			Action:    "created",
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		// Message: submitted
		payload, _ := json.Marshal(map[string]any{"vndb_id": req.VNDBID})
		uidVal := uid
		return s.messageRepo.Create(ctx, tx, &model.GalgameMessage{
			Type:         model.MessageTypeSubmitted,
			GalgameID:    galgame.ID,
			ActorUserID:  &uidVal,
			TargetUserID: nil,
			Payload:      datatypes.JSON(payload),
		})
	})

	if err != nil {
		return nil, err
	}
	return &galgame, nil
}

// Claim atomically converts a VNDB draft (status=2) into a published galgame
// (status=0) owned by the claimer. Returns 20006 if target is not in status=2.
func (s *SubmissionService) Claim(ctx context.Context, uid, gid int) (*model.Galgame, error) {
	err := s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		g, err := s.galgameRepo.FindForUpdate(tx, gid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.NewWithCode(errors.ErrGalgameNotFound)
			}
			return err
		}
		if g.Status != model.GalgameStatusVNDBDraft {
			return errors.NewWithCode(errors.ErrGalgameClaimNotDraft)
		}

		// Flip status & ownership
		if err := tx.Model(&model.Galgame{}).Where("id = ?", gid).
			Updates(map[string]any{
				"status":  model.GalgameStatusPublished,
				"user_id": uid,
			}).Error; err != nil {
			return err
		}

		// Ensure claimer is a contributor (don't duplicate if already)
		var cnt int64
		tx.Model(&model.GalgameContributor{}).
			Where("galgame_id = ? AND user_id = ?", gid, uid).Count(&cnt)
		if cnt == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: gid, UserID: uid}).Error; err != nil {
				return err
			}
		}

		// Revision: action=claimed
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
			UserID:    uid,
			Action:    "claimed",
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		// Message: claimed
		payload, _ := json.Marshal(map[string]any{"from_status": int(model.GalgameStatusVNDBDraft)})
		uidVal := uid
		return s.messageRepo.Create(ctx, tx, &model.GalgameMessage{
			Type:         model.MessageTypeClaimed,
			GalgameID:    gid,
			ActorUserID:  &uidVal,
			TargetUserID: nil,
			Payload:      datatypes.JSON(payload),
		})
	})

	if err != nil {
		return nil, err
	}
	return s.galgameRepo.FindByID(ctx, gid)
}

// PatchDraft updates a pending or declined galgame on behalf of its submitter.
// If status was 4 (declined), flips back to 3 to re-enter the review queue.
// Returns 20007/20008 for ownership/status violations.
func (s *SubmissionService) PatchDraft(ctx context.Context, uid, gid int, req *dto.UpdateGalgameRequest) (*model.Galgame, error) {
	updates := buildUpdates(req)

	err := s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		g, err := s.galgameRepo.FindForUpdate(tx, gid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.NewWithCode(errors.ErrGalgameNotFound)
			}
			return err
		}
		if g.UserID != uid {
			return errors.NewWithCode(errors.ErrGalgameSubmitterOnly)
		}
		if g.Status != model.GalgameStatusPending && g.Status != model.GalgameStatusDeclined {
			return errors.NewWithCode(errors.ErrGalgameDraftStatusInvalid)
		}

		// Auto-revive declined drafts to pending so admin sees them in the queue again.
		if g.Status == model.GalgameStatusDeclined {
			updates["status"] = model.GalgameStatusPending
		}

		if len(updates) > 0 {
			if err := tx.Model(&model.Galgame{}).Where("id = ?", gid).Updates(updates).Error; err != nil {
				return err
			}
		}

		// Revision
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
			UserID:    uid,
			Action:    "edited_pending",
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		// Message: edited_pending. Admin queue picks it up via JOIN on status=3.
		payload, _ := json.Marshal(map[string]any{"field_count": len(updates)})
		uidVal := uid
		return s.messageRepo.Create(ctx, tx, &model.GalgameMessage{
			Type:         model.MessageTypeEditedPending,
			GalgameID:    gid,
			ActorUserID:  &uidVal,
			TargetUserID: nil,
			Payload:      datatypes.JSON(payload),
		})
	})

	if err != nil {
		return nil, err
	}
	return s.galgameRepo.FindByID(ctx, gid)
}

// DeleteDraft hard-deletes a pending or declined galgame submitted by uid.
// CASCADE clears revisions, contributors, aliases, relations.
// Messages keep their galgame_id pointer (no FK), so downstream consumers
// see them as a "ghost" — that's intentional, no message is rewritten on delete.
func (s *SubmissionService) DeleteDraft(ctx context.Context, uid, gid int) error {
	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		g, err := s.galgameRepo.FindForUpdate(tx, gid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.NewWithCode(errors.ErrGalgameNotFound)
			}
			return err
		}
		if g.UserID != uid {
			return errors.NewWithCode(errors.ErrGalgameSubmitterOnly)
		}
		if g.Status != model.GalgameStatusPending && g.Status != model.GalgameStatusDeclined {
			return errors.NewWithCode(errors.ErrGalgameDraftStatusInvalid)
		}

		// Wipe relations explicitly (no FK CASCADE configured for some of these).
		tables := []any{
			&model.GalgameAlias{},
			&model.GalgameTagRelation{},
			&model.GalgameOfficialRelation{},
			&model.GalgameEngineRelation{},
			&model.GalgameLink{},
			&model.GalgameContributor{},
			&model.GalgameRevision{},
		}
		for _, m := range tables {
			if err := tx.Where("galgame_id = ?", gid).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&model.Galgame{}, gid).Error
	})
}

// ListMine returns the user's own galgame submissions filtered by statuses.
// Default statuses (when input empty): [3, 4].
func (s *SubmissionService) ListMine(ctx context.Context, uid int, req *dto.ListMineRequest) ([]model.Galgame, int64, error) {
	statuses := parseStatusCSV(req.Status, []int{
		model.GalgameStatusPending,
		model.GalgameStatusDeclined,
	})
	page := req.Page
	if page < 1 {
		page = 1
	}
	limit := req.Limit
	if limit < 1 {
		limit = 20
	}
	return s.galgameRepo.ListMine(ctx, uid, statuses, page, limit)
}

// parseStatusCSV parses "0,1,2,3,4" into a deduped int slice. Falls back to
// defaults if input is empty. Silently drops invalid entries.
func parseStatusCSV(s string, defaults []int) []int {
	if s == "" {
		return defaults
	}
	seen := make(map[int]bool, 5)
	out := make([]int, 0, 5)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v := 0
		ok := true
		for _, c := range part {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			v = v*10 + int(c-'0')
		}
		if !ok || v < 0 || v > 4 {
			continue
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return defaults
	}
	return out
}
