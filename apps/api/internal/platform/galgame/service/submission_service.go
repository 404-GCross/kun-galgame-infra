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
//   - INSERT galgame status=3 user_id=userID
//   - INSERT aliases / tag / official / engine relations
//   - INSERT contributor (userID)
//   - INSERT VNDB link if vndb_id non-empty
//   - INSERT revision 1 (action='created')
//   - INSERT message (type='submitted', actor=userID, target=NULL)
func (s *SubmissionService) Submit(ctx context.Context, userID int, req *dto.SubmitGalgameRequest) (*model.Galgame, error) {
	// VNDB id validation: empty allowed; non-empty must match pattern.
	if req.VNDBID != "" && !vndbIDRegex.MatchString(req.VNDBID) {
		return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
	}

	// Quota: count today's submissions for this user (status IN 3,4 — declined
	// rejects still count to avoid quota-evasion by submit→edit cycles).
	count, err := s.galgameRepo.CountSubmissionsToday(ctx, userID)
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

	var newID int
	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bare insert with system fields only (status=pending). Every
		// editable field is written by the SINGLE ApplySnapshot path —
		// identical to admin Create; no manual relation loops.
		g := model.Galgame{
			VNDBID: req.VNDBID,
			UserID: userID,
			Status: model.GalgameStatusPending,
		}
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		if err := repository.ApplySnapshot(tx, g.ID, userID, buildSubmitSnapshot(req)); err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameContributor{GalgameID: g.ID, UserID: userID}).Error; err != nil {
			return err
		}

		full, err := loadGalgameWithRelations(tx, g.ID)
		if err != nil {
			return err
		}
		snapJSON, err := model.TakeSnapshot(full).ToJSON()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameRevision{
			GalgameID: g.ID,
			Revision:  1,
			UserID:    userID,
			Action:    "created",
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		newID = g.ID
		// Message: submitted
		payload, _ := json.Marshal(map[string]any{"vndb_id": req.VNDBID})
		uidVal := userID
		return s.messageRepo.Create(ctx, tx, &model.GalgameMessage{
			Type:         model.MessageTypeSubmitted,
			GalgameID:    g.ID,
			ActorUserID:  &uidVal,
			TargetUserID: nil,
			Payload:      datatypes.JSON(payload),
		})
	})

	if err != nil {
		return nil, err
	}
	return s.galgameRepo.FindByID(ctx, newID)
}

// buildSubmitSnapshot is buildCreateSnapshot's twin for user submissions
// (POST /galgame/submit). Same single-write-path discipline; vndb_id is
// optional here.
func buildSubmitSnapshot(req *dto.SubmitGalgameRequest) *model.Snapshot {
	s := &model.Snapshot{
		VNDBID:           req.VNDBID,
		ReleaseDate:      strNonEmpty(req.ReleaseDate),
		ReleaseDateTBA:   req.ReleaseDateTBA,
		NameEnUS:         req.NameEnUS,
		NameJaJP:         req.NameJaJP,
		NameZhCN:         req.NameZhCN,
		NameZhTW:         req.NameZhTW,
		Banner:           req.Banner,
		IntroEnUS:        req.IntroEnUS,
		IntroJaJP:        req.IntroJaJP,
		IntroZhCN:        req.IntroZhCN,
		IntroZhTW:        req.IntroZhTW,
		ContentLimit:     orDefault(req.ContentLimit, "sfw"),
		OriginalLanguage: orDefault(req.OriginalLanguage, "ja-jp"),
		AgeLimit:         orDefault(req.AgeLimit, "r18"),
		SeriesID:         req.SeriesID,
		Aliases:          splitCSV(req.Aliases),
		TagIDs:           req.TagIDs,
		OfficialIDs:      req.OfficialIDs,
		EngineIDs:        req.EngineIDs,
		Links:            vndbLink(req.VNDBID),
		Covers:           coverInputsToSnapshot(req.Covers),
		Screenshots:      screenshotInputsToSnapshot(req.Screenshots),
	}
	// Multipart-uploaded banner becomes the pinned cover. Same shape as
	// buildCreateSnapshot; see promoteCoverHashInPlace for the contract.
	if req.PromoteCoverHash != "" {
		s.Covers = promoteCoverHashInPlace(s.Covers, req.PromoteCoverHash)
	}
	return s
}

// Claim atomically converts a VNDB draft (status=2) into a published galgame
// (status=0) owned by the claimer. Returns 20006 if target is not in status=2.
func (s *SubmissionService) Claim(ctx context.Context, userID, gid int) (*model.Galgame, error) {
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
				"user_id": userID,
			}).Error; err != nil {
			return err
		}

		// Ensure claimer is a contributor (don't duplicate if already)
		var cnt int64
		tx.Model(&model.GalgameContributor{}).
			Where("galgame_id = ? AND user_id = ?", gid, userID).Count(&cnt)
		if cnt == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: gid, UserID: userID}).Error; err != nil {
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
			UserID:    userID,
			Action:    "claimed",
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		// Message: claimed
		payload, _ := json.Marshal(map[string]any{"from_status": int(model.GalgameStatusVNDBDraft)})
		uidVal := userID
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
func (s *SubmissionService) PatchDraft(ctx context.Context, userID, gid int, req *dto.UpdateGalgameRequest) (*model.Galgame, error) {
	err := s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		g, err := s.galgameRepo.FindForUpdate(tx, gid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.NewWithCode(errors.ErrGalgameNotFound)
			}
			return err
		}
		if g.UserID != userID {
			return errors.NewWithCode(errors.ErrGalgameSubmitterOnly)
		}
		if g.Status != model.GalgameStatusPending && g.Status != model.GalgameStatusDeclined {
			return errors.NewWithCode(errors.ErrGalgameDraftStatusInvalid)
		}

		// Validate a vndb_id change the same way Submit/Create do — PatchDraft
		// previously skipped both checks, so a malformed id persisted silently
		// and a duplicate surfaced as a raw 500 instead of 20004. (The partial
		// unique index backstops the TOCTOU between this check and commit.)
		if req.VNDBID != nil {
			if v := *req.VNDBID; v != "" {
				if !vndbIDRegex.MatchString(v) {
					return errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
				}
				exists, existingID, err := s.galgameRepo.ExistsByVNDBID(ctx, v)
				if err != nil {
					return err
				}
				if exists && existingID != gid {
					return errors.NewWithCode(errors.ErrGalgameVNDBExists)
				}
			}
		}

		// Same snapshot-overlay model as the published-galgame Update:
		// overlay onto the current canonical snapshot, write via the one
		// ApplySnapshot path (so tag/official/engine edits persist and
		// the recorded snapshot == DB == intent).
		full, err := loadGalgameWithRelations(tx, gid)
		if err != nil {
			return err
		}
		cur := model.TakeSnapshot(full)
		next := overlayUpdate(cur, req)
		changed := model.ChangedKeys(cur, next)

		// A declined draft re-enters the review queue even if the
		// content is byte-identical (re-submission intent). A pending
		// draft with no change is a true no-op (commit empty tx).
		reviving := g.Status == model.GalgameStatusDeclined
		if len(changed) == 0 && !reviving {
			return nil
		}

		if len(changed) > 0 {
			if err := repository.ApplySnapshot(tx, gid, userID, next); err != nil {
				return err
			}
		}
		// status is not a snapshot field; flip declined→pending here.
		if reviving {
			if err := tx.Model(&model.Galgame{}).Where("id = ?", gid).
				Update("status", model.GalgameStatusPending).Error; err != nil {
				return err
			}
		}

		nextRev, err := repository.NextRevision(tx, gid)
		if err != nil {
			return err
		}
		fullAfter, err := loadGalgameWithRelations(tx, gid)
		if err != nil {
			return err
		}
		snapJSON, err := model.TakeSnapshot(fullAfter).ToJSON()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameRevision{
			GalgameID: gid,
			Revision:  nextRev,
			UserID:    userID,
			Action:    "edited_pending",
			Snapshot:  snapJSON,
		}).Error; err != nil {
			return err
		}

		// Message: edited_pending. Admin queue picks it up via JOIN on status=3.
		payload, _ := json.Marshal(map[string]any{"field_count": len(changed)})
		uidVal := userID
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

// DeleteDraft hard-deletes a pending or declined galgame submitted by userID.
// CASCADE clears revisions, contributors, aliases, relations.
// Messages keep their galgame_id pointer (no FK), so downstream consumers
// see them as a "ghost" — that's intentional, no message is rewritten on delete.
func (s *SubmissionService) DeleteDraft(ctx context.Context, userID, gid int) error {
	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		g, err := s.galgameRepo.FindForUpdate(tx, gid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.NewWithCode(errors.ErrGalgameNotFound)
			}
			return err
		}
		if g.UserID != userID {
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
//
// For status=4 (declined) entries, the most recent decline reason is
// attached to each item so the UI can render "Your submission was
// rejected because: ..." without an extra trip to /messages/mine.
func (s *SubmissionService) ListMine(ctx context.Context, userID int, req *dto.ListMineRequest) ([]dto.MineGalgame, int64, error) {
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

	items, total, err := s.galgameRepo.ListMine(ctx, userID, statuses, page, limit)
	if err != nil {
		return nil, 0, err
	}

	// Collect ids of status=4 entries so we only fetch decline reasons for
	// rows that need them. status=3 rows skip the lookup entirely.
	declinedIDs := make([]int, 0, len(items))
	for _, g := range items {
		if g.Status == model.GalgameStatusDeclined {
			declinedIDs = append(declinedIDs, g.ID)
		}
	}

	reasons := make(map[int]string, len(declinedIDs))
	if len(declinedIDs) > 0 {
		latest, err := s.messageRepo.LatestDeclinedByGalgameIDs(ctx, userID, declinedIDs)
		if err == nil {
			for gid, msg := range latest {
				reasons[gid] = extractDeclineReason(msg.Payload)
			}
		}
		// On error, fall through with empty reasons — better to show the
		// list without reasons than 500 the whole page.
	}

	out := make([]dto.MineGalgame, len(items))
	for i, g := range items {
		// g.EffectiveBannerHash already populated by repo (ListMine preloads
		// Cover + runs model.PopulateEffectiveBanner). Pass through here so
		// /galgame/mine cards can render the pinned cover instead of an
		// EffectiveBannerHash is the sole image-service banner reference
		// after PR5 (banner_image_hash column retired).
		out[i] = dto.MineGalgame{
			ID:                  g.ID,
			VNDBID:              g.VNDBID,
			NameEnUS:            g.NameEnUS,
			NameJaJP:            g.NameJaJP,
			NameZhCN:            g.NameZhCN,
			NameZhTW:            g.NameZhTW,
			Banner:              g.Banner,
			EffectiveBannerHash: g.EffectiveBannerHash,
			ContentLimit:        g.ContentLimit,
			Status:              g.Status,
			Created:             g.Created.Time().UTC().Format("2006-01-02T15:04:05Z"),
			Updated:             g.Updated.Time().UTC().Format("2006-01-02T15:04:05Z"),
			DeclineReason:       reasons[g.ID],
		}
	}
	return out, total, nil
}

// extractDeclineReason pulls payload.reason out of a 'declined' message's
// JSONB payload. Returns "" on parse error or missing field — UI handles
// empty gracefully.
func extractDeclineReason(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Reason
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
