package editbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Bridge is the old-wire read adapter: it serves model.GalgameRevision /
// model.GalgamePR values — the exact shapes the galgame handlers return
// verbatim today — reconstructed from the edit_* tables. Reads and writes both
// live entirely on the engine side (the old tables freeze at migration).
type Bridge struct {
	db  *gorm.DB // engine pool (catalog DB, charter ruling 2)
	eng *editing.Engine
}

// New builds a Bridge over the engine pool and the engine itself (needed for
// effective-patch computation when reconstructing open proposals).
func New(db *gorm.DB, eng *editing.Engine) *Bridge {
	return &Bridge{db: db, eng: eng}
}

func (b *Bridge) revisions(ctx context.Context) *gorm.DB {
	return b.db.WithContext(ctx).Model(&revisionRow{}).
		Where("entity_family = ? AND entity_type = ?", "galgame", editspec.TypeGame)
}

func (b *Bridge) proposals(ctx context.Context) *gorm.DB {
	return b.db.WithContext(ctx).Model(&proposalRow{}).
		Where("entity_family = ? AND entity_type = ?", "galgame", editspec.TypeGame)
}

// ---- revisions --------------------------------------------------------------

// ListRevisions mirrors the old repository List: seq (= revision number)
// descending, offset pagination, include_minor filter. Minor flags exist only
// on migrated rows (legacy_meta) — new-era rows are never minor.
func (b *Bridge) ListRevisions(ctx context.Context, galgameID, page, limit int, includeMinor bool) ([]model.GalgameRevision, int64, error) {
	q := b.revisions(ctx).Where("entity_id = ?", galgameID)
	if !includeMinor {
		q = q.Where(`NOT COALESCE((legacy_meta->>'is_minor')::boolean, false)`)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []revisionRow
	if err := q.Order("seq DESC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items, err := b.wireRevisions(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetRevision returns one revision by its per-galgame number (old contract:
// rev <= 0 means "latest"). Missing → gorm.ErrRecordNotFound, matching the old
// repository so the service's error mapping is untouched.
func (b *Bridge) GetRevision(ctx context.Context, galgameID, rev int) (*model.GalgameRevision, error) {
	q := b.revisions(ctx).Where("entity_id = ?", galgameID)
	var row revisionRow
	var err error
	if rev > 0 {
		err = q.Where("seq = ?", rev).First(&row).Error
	} else {
		err = q.Order("seq DESC").First(&row).Error
	}
	if err != nil {
		return nil, err
	}
	items, err := b.wireRevisions(ctx, []revisionRow{row})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

// Feed mirrors the old ListRecentMerged: "an edit landed" events (old action
// 'merged'; new-era engine action merged = a reviewer merged a proposal),
// cursored and ordered by the WIRE id — COALESCE(legacy_id, id) — so
// downstream kungal/moyu since_id cursors survive the migration. Snapshot is
// never selected (feed rows are narrow on purpose, like the old repo).
func (b *Bridge) Feed(ctx context.Context, sinceID int64, limit int) ([]model.GalgameRevision, error) {
	q := b.revisions(ctx).
		Where("(legacy_action = 'merged' OR (legacy_action IS NULL AND action = ?))", editing.ActionMerged)
	if sinceID > 0 {
		q = q.Where("COALESCE(legacy_id, id) > ?", sinceID)
	}
	var rows []revisionRow
	if err := q.Select("id", "entity_id", "seq", "action", "actor_uid", "legacy_action", "legacy_id", "created_at").
		Order("COALESCE(legacy_id, id) ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.GalgameRevision, len(rows))
	for i := range rows {
		r := &rows[i]
		out[i] = model.GalgameRevision{
			ID:        int(r.wireID()),
			GalgameID: int(r.EntityID),
			Revision:  r.Seq,
			UserID:    int(r.ActorUID),
			Action:    actionString(r),
			Created:   model.Timestamp(r.CreatedAt),
		}
	}
	return out, nil
}

// wireRevisions converts engine rows to the old wire shape, batching the
// proposal-note lookup for new-era rows (migrated rows carry their note in
// legacy_meta and never join).
func (b *Bridge) wireRevisions(ctx context.Context, rows []revisionRow) ([]model.GalgameRevision, error) {
	var proposalIDs []int64
	for i := range rows {
		if rows[i].LegacyID == nil && rows[i].ProposalID != nil {
			proposalIDs = append(proposalIDs, *rows[i].ProposalID)
		}
	}
	notes := map[int64]string{}
	if len(proposalIDs) > 0 {
		var props []proposalRow
		if err := b.db.WithContext(ctx).Model(&proposalRow{}).
			Select("id", "note").Where("id IN ?", proposalIDs).Find(&props).Error; err != nil {
			return nil, err
		}
		for i := range props {
			notes[props[i].ID] = wireNoteFromProposal(props[i].Note)
		}
	}
	out := make([]model.GalgameRevision, len(rows))
	for i := range rows {
		wire, err := wireRevision(&rows[i], notes)
		if err != nil {
			return nil, err
		}
		out[i] = *wire
	}
	return out, nil
}

func wireRevision(r *revisionRow, proposalNotes map[int64]string) (*model.GalgameRevision, error) {
	meta := decodeRevisionMeta(r.LegacyMeta)
	snapshot, err := RekeySnapshotNewToOld(r.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("editbridge: revision %d/%d snapshot: %w", r.EntityID, r.Seq, err)
	}
	note := meta.Note
	if r.LegacyID == nil && r.ProposalID != nil {
		note = proposalNotes[*r.ProposalID]
	}
	out := &model.GalgameRevision{
		ID:         int(r.wireID()),
		GalgameID:  int(r.EntityID),
		Revision:   r.Seq,
		UserID:     int(r.ActorUID),
		Action:     actionString(r),
		Note:       note,
		Snapshot:   snapshot,
		IsMinor:    meta.IsMinor,
		RevertedTo: meta.RevertedTo,
		Created:    model.Timestamp(r.CreatedAt),
	}
	// Tri-state changed_fields: a legacy-NULL source row keeps OMITTING the
	// field on the wire (the stored derivation only serves engine-side diff).
	if !meta.NullChangedFields {
		keys, err := decodeStringList(r.ChangedFields)
		if err != nil {
			return nil, err
		}
		rekeyed, err := json.Marshal(RekeyKeysNewToOld(keys))
		if err != nil {
			return nil, err
		}
		out.ChangedFields = datatypes.JSON(rekeyed)
	}
	return out, nil
}

// actionString renders the old-wire action: honest provenance for migrated
// rows (legacy_action), the engine vocabulary mapped back for new-era rows
// (direct edits show as the old "updated").
func actionString(r *revisionRow) string {
	if r.LegacyAction != nil {
		return *r.LegacyAction
	}
	switch r.Action {
	case editing.ActionCreated:
		return "created"
	case editing.ActionMerged:
		return "merged"
	case editing.ActionReverted:
		return "reverted"
	default: // editing.ActionDirect
		return "updated"
	}
}

func decodeStringList(raw datatypes.JSON) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("editbridge: decode changed_fields: %w", err)
	}
	return keys, nil
}

// ---- proposals (old PRs) ----------------------------------------------------

// ListPRs mirrors the old repository: created DESC, offset pagination.
// Migrated rows are merged-only (D2 dropped pending/declined); new-era open
// proposals appear with status 0.
func (b *Bridge) ListPRs(ctx context.Context, galgameID, page, limit int) ([]model.GalgamePR, int64, error) {
	q := b.proposals(ctx).Where("entity_id = ?", galgameID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []proposalRow
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.GalgamePR, len(rows))
	for i := range rows {
		pr, err := b.wirePR(ctx, &rows[i])
		if err != nil {
			return nil, 0, err
		}
		items[i] = *pr
	}
	return items, total, nil
}

// GetPR resolves a PR by its OLD WIRE id, scoped to the galgame (route
// contract). Legacy ids win; new-era ids cannot collide with them (sequence
// bump). Missing → gorm.ErrRecordNotFound.
func (b *Bridge) GetPR(ctx context.Context, galgameID, wireID int) (*model.GalgamePR, error) {
	row, err := b.proposalByWireID(ctx, galgameID, wireID)
	if err != nil {
		return nil, err
	}
	return b.wirePR(ctx, row)
}

// ResolveProposalID maps an old wire PR id to the ENGINE proposal id the
// write paths (merge/decline) need.
func (b *Bridge) ResolveProposalID(ctx context.Context, galgameID, wireID int) (int64, error) {
	row, err := b.proposalByWireID(ctx, galgameID, wireID)
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (b *Bridge) proposalByWireID(ctx context.Context, galgameID, wireID int) (*proposalRow, error) {
	var row proposalRow
	err := b.proposals(ctx).Where("entity_id = ?", galgameID).
		Where("(legacy_pr_id = ? OR (legacy_pr_id IS NULL AND id = ?))", wireID, wireID).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// RevisionWireID returns the old-wire id of the revision a proposal produced
// (nil when the proposal never merged).
func (b *Bridge) RevisionWireID(ctx context.Context, proposalID int64) (*int, error) {
	var rows []revisionRow
	if err := b.db.WithContext(ctx).Model(&revisionRow{}).
		Select("id", "legacy_id").Where("proposal_id = ?", proposalID).
		Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	id := int(rows[0].wireID())
	return &id, nil
}

func (b *Bridge) wirePR(ctx context.Context, p *proposalRow) (*model.GalgamePR, error) {
	out := &model.GalgamePR{
		ID:           int(p.wireID()),
		GalgameID:    int(p.EntityID),
		UserID:       int(p.ProposerUID),
		Status:       wirePRStatus(p.Status),
		BaseRevision: p.BaseRevisionSeq,
		Created:      model.Timestamp(p.CreatedAt),
		Updated:      model.Timestamp(p.UpdatedAt),
	}
	if meta, ok := decodeProposalMeta(p.LegacyMeta); ok {
		// Migrated row: exact old fields, snapshot byte-verbatim.
		out.Title, out.Message = meta.Title, meta.Message
		out.Snapshot = datatypes.JSON(meta.Snapshot)
		out.BaseRevision = meta.BaseRevision
	} else {
		out.Title, out.Message = DecodeNote(p.Note)
		snapshot, err := b.reconstructPRSnapshot(ctx, p)
		if err != nil {
			return nil, err
		}
		out.Snapshot = snapshot
	}
	if p.Status != editing.StatusOpen {
		if p.DecidedByUID != nil {
			by := int(*p.DecidedByUID)
			out.CompletedBy = &by
		}
		if p.DecidedAt != nil {
			t := model.Timestamp(*p.DecidedAt)
			out.CompletedTime = &t
		}
		revID, err := b.RevisionWireID(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out.RevisionID = revID
	}
	return out, nil
}

// wirePRStatus maps engine proposal status to the old 0/1/2 vocabulary.
// withdrawn (an S2S-face-only outcome; the old wire cannot produce it) reads
// as declined — the closest old-wire truth.
func wirePRStatus(s int16) int {
	switch s {
	case editing.StatusMerged:
		return 1
	case editing.StatusDeclined, editing.StatusWithdrawn:
		return 2
	default:
		return 0
	}
}

// reconstructPRSnapshot rebuilds the old wire's FULL proposed snapshot for a
// new-era proposal: the base revision's snapshot overlaid with the effective
// patch (original ⊕ amendments), re-keyed to the old vocabulary. A missing
// base revision degrades to a patch-only document (comment: post-migration
// every galgame has revision 1, so this is a defensive path only).
func (b *Bridge) reconstructPRSnapshot(ctx context.Context, p *proposalRow) (datatypes.JSON, error) {
	base := map[string]json.RawMessage{}
	var baseRow revisionRow
	err := b.revisions(ctx).Where("entity_id = ? AND seq = ?", p.EntityID, p.BaseRevisionSeq).
		First(&baseRow).Error
	if err == nil {
		if err := json.Unmarshal(baseRow.Snapshot, &base); err != nil {
			return nil, fmt.Errorf("editbridge: decode base snapshot: %w", err)
		}
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	_, _, eff, err := b.eng.GetProposal(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for key, value := range eff {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		base[key] = raw
	}
	merged, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	return RekeySnapshotNewToOld(merged)
}

// ---- profile stats ----------------------------------------------------------

// UserEditStats aggregates one user's galgame edit activity from the engine
// tables — the E2a home of the old profile counters (galgame_revision /
// galgame_pr are frozen). Historical attribution survives the migration
// (actor_uid / proposer_uid are carried over); the pending/declined PRs that
// D2 discarded are honestly absent from the counts.
type UserEditStats struct {
	RevisionCount int
	PRSubmitted   int
	PRMerged      int
	PRDeclined    int
	PRPending     int
}

// UserEditStats counts revisions authored by uid and proposals filed by uid
// (galgame.game rows only). Withdrawn proposals count as declined on the old
// wire (the old status vocabulary has no withdrawn state).
func (b *Bridge) UserEditStats(ctx context.Context, uid int) (*UserEditStats, error) {
	out := &UserEditStats{}
	count := func(q *gorm.DB) (int, error) {
		var n int64
		if err := q.Count(&n).Error; err != nil {
			return 0, err
		}
		return int(n), nil
	}
	var err error
	if out.RevisionCount, err = count(b.revisions(ctx).Where("actor_uid = ?", uid)); err != nil {
		return nil, err
	}
	if out.PRSubmitted, err = count(b.proposals(ctx).Where("proposer_uid = ?", uid)); err != nil {
		return nil, err
	}
	if out.PRMerged, err = count(b.proposals(ctx).Where("proposer_uid = ? AND status = ?", uid, editing.StatusMerged)); err != nil {
		return nil, err
	}
	if out.PRDeclined, err = count(b.proposals(ctx).Where("proposer_uid = ? AND status IN ?", uid,
		[]int16{editing.StatusDeclined, editing.StatusWithdrawn})); err != nil {
		return nil, err
	}
	if out.PRPending, err = count(b.proposals(ctx).Where("proposer_uid = ? AND status = ?", uid, editing.StatusOpen)); err != nil {
		return nil, err
	}
	return out, nil
}
