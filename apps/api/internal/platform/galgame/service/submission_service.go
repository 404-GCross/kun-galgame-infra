package service

import (
	"context"
	"encoding/json"
	"strings"

	"api/internal/platform/authz"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
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
	// E2a strangler: submission revisions land in the engine log; the
	// legacy galgame_revision table is frozen.
	edit *editing.Engine
	// claimCatalog is the post-commit catalog claim for a trusted publisher's
	// direct publish (the one submission path born at status=0). A pending
	// submission is deliberately NOT claimed — see ClaimHookFunc and the
	// write-path lane's policy in catalogsync.loadGames.
	claimCatalog ClaimHookFunc
}

// NewSubmissionService creates a SubmissionService.
func NewSubmissionService(g *repository.GalgameRepository, m *repository.MessageRepository) *SubmissionService {
	return &SubmissionService{galgameRepo: g, messageRepo: m}
}

// WithEditing wires the editing engine (always set by Mount; see
// GalgameService.WithEditing for the posture).
func (s *SubmissionService) WithEditing(eng *editing.Engine) *SubmissionService {
	s.edit = eng
	return s
}

// WithClaimHook wires the catalog write-path claim fired after a direct publish
// commits (wave 146). Returns the service for fluent chaining; nil explicitly
// disables it. See ClaimHookFunc.
func (s *SubmissionService) WithClaimHook(h ClaimHookFunc) *SubmissionService {
	s.claimCatalog = h
	return s
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
func (s *SubmissionService) Submit(ctx context.Context, userID int, roles []string, req *dto.SubmitGalgameRequest) (*model.Galgame, error) {
	// VNDB id validation: empty allowed; non-empty must match pattern.
	if req.VNDBID != "" && !vndbIDRegex.MatchString(req.VNDBID) {
		return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
	}

	// Trusted publishers (creator / moderator / admin) publish directly to
	// status=0, bypassing the review queue AND the submission quota; everyone
	// else creates a pending(3) draft. Empty vndb_id is allowed for all here, so
	// a trusted publisher can publish a doujin/indie title with no VNDB entry.
	// See docs/auth/01-creator-role-design.md.
	publishDirect := perm.Resolver.Can(roles, perm.PublishDirect)

	if !publishDirect {
		// Quota: count today's submissions for this user (status IN 3,4 —
		// declined rejects still count, to avoid quota-evasion by submit→edit
		// cycles).
		count, err := s.galgameRepo.CountSubmissionsToday(ctx, userID)
		if err != nil {
			return nil, err
		}
		if count >= DailySubmissionQuota {
			return nil, errors.NewWithCode(errors.ErrGalgameQuotaExceeded)
		}
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

	status := model.GalgameStatusPending
	if publishDirect {
		status = model.GalgameStatusPublished
	}

	var newID int
	var createdKeys []string
	err := s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bare insert with system fields only. Every editable field is written
		// by the SINGLE ApplySnapshot path — identical to admin Create; no
		// manual relation loops. status = published(0) for trusted publishers,
		// else pending(3) for the review queue.
		g := model.Galgame{
			VNDBID: req.VNDBID,
			UserID: userID,
			Status: status,
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
		// created → every populated field is "new" (delta vs empty); the
		// engine birth record below stores the re-keyed set.
		createdKeys = model.KeysOf(model.ChangedKeys(&model.Snapshot{}, model.TakeSnapshot(full)))

		newID = g.ID
		if publishDirect {
			// Published straight to status=0 by a trusted publisher — no
			// review-queue message (the queue is driven by status=3).
			return nil
		}
		// Message: submitted (enters the admin review queue via status=3).
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

	// Catalog claim for the DIRECT PUBLISH only, before the birth record (same
	// ordering rationale as GalgameService.Create). A pending(3) draft gets no
	// identity here: it may still be withdrawn, and the nightly reconcile
	// claims it once it has survived to 03:20.
	if publishDirect && s.claimCatalog != nil {
		s.claimCatalog(ctx, int64(newID))
	}

	// Engine birth record after the product commit (same posture and
	// residual-window note as GalgameService.Create).
	newKeys, err := editspec.RekeyKeysOldToNew(createdKeys)
	if err != nil {
		return nil, err
	}
	if _, err := s.edit.RecordCreated(ctx, editspec.TypeGame, int64(newID), editActor(userID, 2), newKeys); err != nil {
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
		ReleaseDate:      model.NormalizeReleaseDateInput(req.ReleaseDate),
		ReleaseDateTBA:   req.ReleaseDateTBA,
		ReleasePrecision: string(model.DeriveInputPrecision(req.ReleaseDate, req.ReleaseDateTBA)),
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

// Claim converts a VNDB draft (status=2) into a published galgame (status=0)
// owned by the claimer. Returns 20006 if target is not in status=2.
//
// E2a ordering (deliberately self-healing): the product transaction flips
// OWNERSHIP + contributor first while the row is still a draft; the engine
// direct edit then lands status 2→0 and mints the revision (action=direct,
// changed_fields=[galgame.game.status] — the old machine wrote an empty
// changed set with action=claimed). If the engine step fails the row stays
// status=2 and a re-claim simply overwrites ownership again. The message is
// emitted only after the status actually landed.
func (s *SubmissionService) Claim(ctx context.Context, userID, gid int) (*model.Galgame, error) {
	// Fast-path check for the honest error on non-drafts (unlocked read — the
	// engine below is the real arbiter).
	g, err := s.galgameRepo.FindByID(ctx, gid)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, err
	}
	if g.Status != model.GalgameStatusVNDBDraft {
		return nil, errors.NewWithCode(errors.ErrGalgameClaimNotDraft)
	}

	// Engine direct edit FIRST: status 2→0 (writes the column + the revision)
	// and doubles as the concurrency arbiter — of two racing claimers exactly
	// one lands the status change. The loser surfaces either as "no effective
	// changes" (status already 0) or as the (entity, seq) unique-index
	// collision on the revision log; both mean "no longer a claimable draft".
	if _, _, err := s.edit.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(gid),
		Patch:      map[string]any{editspec.FieldStatus: float64(model.GalgameStatusPublished)},
		Actor:      editActor(userID, 2, perm.EditGameStatus),
	}); err != nil {
		if err == editing.ErrNoEffectiveChanges ||
			strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			return nil, errors.NewWithCode(errors.ErrGalgameClaimNotDraft)
		}
		return nil, mapEngineWriteError(err)
	}

	// Ownership + contributor for the WINNER only. Residual window: if this
	// fails after the engine landed, the entry is published but unclaimed
	// (owner stays the import identity) — narrow, and preferable to the old
	// race where a losing claimer could overwrite the winner's ownership.
	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Galgame{}).Where("id = ?", gid).
			Update("user_id", userID).Error; err != nil {
			return err
		}
		var cnt int64
		if err := tx.Model(&model.GalgameContributor{}).
			Where("galgame_id = ? AND user_id = ?", gid, userID).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: gid, UserID: userID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Message: claimed
	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
//
// E2a: the content overlay and the declined→pending revive land as ONE engine
// direct edit (the revive rides the patch as galgame.game.status=3), so the
// revision records exactly what this edit did. The submitter may still fix
// vndb_id on their own draft (pre-publication — the squatting lesson applies
// to published entries), so the vndb permission key is granted here.
func (s *SubmissionService) PatchDraft(ctx context.Context, userID, gid int, req *dto.UpdateGalgameRequest) (*model.Galgame, error) {
	g, err := s.galgameRepo.FindByID(ctx, gid)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, err
	}
	if g.UserID != userID {
		return nil, errors.NewWithCode(errors.ErrGalgameSubmitterOnly)
	}
	if g.Status != model.GalgameStatusPending && g.Status != model.GalgameStatusDeclined {
		return nil, errors.NewWithCode(errors.ErrGalgameDraftStatusInvalid)
	}

	// Validate a vndb_id change the same way Submit/Create do. (The partial
	// unique index backstops the TOCTOU between this check and the apply.)
	if req.VNDBID != nil {
		if v := *req.VNDBID; v != "" {
			if !vndbIDRegex.MatchString(v) {
				return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
			}
			exists, existingID, err := s.galgameRepo.ExistsByVNDBID(ctx, v)
			if err != nil {
				return nil, err
			}
			if exists && existingID != gid {
				return nil, errors.NewWithCode(errors.ErrGalgameVNDBExists)
			}
		}
	}

	// Same snapshot-overlay model as the published-galgame Update.
	full, err := loadGalgameWithRelations(s.galgameRepo.DB().WithContext(ctx), gid)
	if err != nil {
		return nil, err
	}
	cur := model.TakeSnapshot(full)
	next := overlayUpdate(cur, req, vndbManagedTagIDs(full.Tag), vndbManagedOfficialIDs(full.Official))
	changed := model.ChangedKeys(cur, next)

	// A declined draft re-enters the review queue even if the content is
	// byte-identical (re-submission intent). A pending draft with no change
	// is a true no-op.
	reviving := g.Status == model.GalgameStatusDeclined
	if len(changed) == 0 && !reviving {
		return g, nil
	}

	patch, err := rekeyedPatch(next, changed)
	if err != nil {
		return nil, err
	}
	perms := []authz.Permission{perm.EditGameVNDBID}
	if reviving {
		patch[editspec.FieldStatus] = float64(model.GalgameStatusPending)
		perms = append(perms, perm.EditGameStatus)
	}
	if _, _, err := s.edit.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(gid),
		Patch:      patch,
		Actor:      editActor(userID, 2, perms...),
	}); err != nil && err != editing.ErrNoEffectiveChanges {
		return nil, mapEngineWriteError(err)
	}

	// Message: edited_pending. Admin queue picks it up via JOIN on status=3.
	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		// EVERY table with an FK → galgame (all ON DELETE NO ACTION) must be
		// cleared before the parent delete, or `DELETE galgame` is blocked.
		// galgame_cover / galgame_screenshot were missing here: a submission
		// with an uploaded cover (the common case — most submitters add one)
		// hit a fk_galgame_cover violation on withdraw and surfaced as the
		// generic "操作失败". history / pr can't exist on a never-merged draft
		// today, but are kept for completeness so this stays the full FK set.
		tables := []any{
			&model.GalgameAlias{},
			&model.GalgameTagRelation{},
			&model.GalgameOfficialRelation{},
			&model.GalgameEngineRelation{},
			&model.GalgameLink{},
			&model.GalgameContributor{},
			&model.GalgameCover{},
			&model.GalgameScreenshot{},
			&model.GalgameHistory{},
			&model.GalgamePR{},
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
