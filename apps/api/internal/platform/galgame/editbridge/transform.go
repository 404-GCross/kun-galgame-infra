package editbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// The legacy transform (D2 彻底做法, 03 号裁定 3): move every
// galgame_revision into the unified edit_revision log (re-seq'd — old numbers
// are verified contiguous, so seq == the old revision number), and every
// MERGED galgame_pr into edit_proposal. Pending/declined PRs are dropped (the
// caller pg_dumps both tables first). Idempotent: rows upsert keyed on the
// source-row PK (legacy_id / legacy_pr_id), so re-runs repair and DELTA runs
// pick up only source rows written since the previous pass.

// legacyActionMap maps the 11-value old action vocabulary onto the engine's
// permanent 4-value one (03 号裁定 3). legacy_action keeps the original.
var legacyActionMap = map[string]int16{
	"created":        editing.ActionCreated,
	"updated":        editing.ActionMerged,
	"merged":         editing.ActionMerged,
	"reverted":       editing.ActionReverted,
	"claimed":        editing.ActionDirect,
	"approved":       editing.ActionDirect,
	"banned":         editing.ActionDirect,
	"unbanned":       editing.ActionDirect,
	"declined":       editing.ActionDirect,
	"edited_pending": editing.ActionDirect,
	"status_changed": editing.ActionDirect,
}

// TransformOpts tunes the batch size of the revision scan.
type TransformOpts struct {
	Batch int // rows per keyset page (default 500)
}

// TransformSummary reports what a pass did.
type TransformSummary struct {
	RevisionsProcessed int64
	RevisionsMigrated  int64 // net-new edit_revision legacy rows this pass
	ProposalsProcessed int64
	ProposalsMigrated  int64
	DroppedPending     int64
	DroppedDeclined    int64
}

func (s TransformSummary) String() string {
	return fmt.Sprintf("revisions processed=%d net-new=%d; merged PRs processed=%d net-new=%d; dropped pending=%d declined=%d",
		s.RevisionsProcessed, s.RevisionsMigrated, s.ProposalsProcessed, s.ProposalsMigrated, s.DroppedPending, s.DroppedDeclined)
}

// Transform runs one full pass: revisions, then merged PRs, then the identity
// sequence bump that keeps new engine ids above every legacy wire id.
func Transform(galgameDB, catalogDB *gorm.DB, opts TransformOpts) (*TransformSummary, error) {
	if opts.Batch <= 0 {
		opts.Batch = 500
	}
	sum := &TransformSummary{}
	before, err := legacyRevisionCount(catalogDB)
	if err != nil {
		return nil, err
	}
	if err := transformRevisions(galgameDB, catalogDB, opts.Batch, sum); err != nil {
		return nil, err
	}
	after, err := legacyRevisionCount(catalogDB)
	if err != nil {
		return nil, err
	}
	sum.RevisionsMigrated = after - before

	beforeP, err := legacyProposalCount(catalogDB)
	if err != nil {
		return nil, err
	}
	if err := transformMergedPRs(galgameDB, catalogDB, sum); err != nil {
		return nil, err
	}
	afterP, err := legacyProposalCount(catalogDB)
	if err != nil {
		return nil, err
	}
	sum.ProposalsMigrated = afterP - beforeP

	if err := galgameDB.Model(&model.GalgamePR{}).Where("status = 0").Count(&sum.DroppedPending).Error; err != nil {
		return nil, err
	}
	if err := galgameDB.Model(&model.GalgamePR{}).Where("status = 2").Count(&sum.DroppedDeclined).Error; err != nil {
		return nil, err
	}

	// Sequence floors come from the SOURCE tables, not the migrated rows:
	// dropped pending/declined PR ids must stay unreachable too, or an old
	// wire id could resolve to an unrelated new proposal.
	for table, source := range map[string]string{
		"edit_revision": "galgame_revision",
		"edit_proposal": "galgame_pr",
	} {
		var floor int64
		if err := galgameDB.Raw("SELECT COALESCE(MAX(id), 0) FROM " + source).Scan(&floor).Error; err != nil {
			return nil, err
		}
		if err := bumpSequence(catalogDB, table, floor); err != nil {
			return nil, err
		}
	}
	return sum, nil
}

func legacyRevisionCount(catalogDB *gorm.DB) (int64, error) {
	var n int64
	err := catalogDB.Model(&revisionRow{}).Where("legacy_id IS NOT NULL").Count(&n).Error
	return n, err
}

func legacyProposalCount(catalogDB *gorm.DB) (int64, error) {
	var n int64
	err := catalogDB.Model(&proposalRow{}).Where("legacy_pr_id IS NOT NULL").Count(&n).Error
	return n, err
}

// transformRevisions walks galgame_revision in (galgame_id, revision) keyset
// order, carrying the previous row's snapshot for the legacy-NULL
// changed_fields derivation (numbers are contiguous, so the previous row in
// stream IS revision-1 of the same gid).
func transformRevisions(galgameDB, catalogDB *gorm.DB, batch int, sum *TransformSummary) error {
	lastGid, lastRev := 0, 0
	var prevGid int
	var prevSnapshot []byte
	for {
		var rows []model.GalgameRevision
		if err := galgameDB.
			Where("(galgame_id, revision) > (?, ?)", lastGid, lastRev).
			Order("galgame_id ASC, revision ASC").Limit(batch).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for i := range rows {
			r := &rows[i]
			var prev []byte
			if prevGid == r.GalgameID {
				prev = prevSnapshot
			}
			if err := upsertRevision(galgameDB, catalogDB, r, prev); err != nil {
				return fmt.Errorf("revision id=%d (gid=%d rev=%d): %w", r.ID, r.GalgameID, r.Revision, err)
			}
			prevGid, prevSnapshot = r.GalgameID, r.Snapshot
			sum.RevisionsProcessed++
		}
		lastGid, lastRev = rows[len(rows)-1].GalgameID, rows[len(rows)-1].Revision
	}
}

func upsertRevision(galgameDB, catalogDB *gorm.DB, r *model.GalgameRevision, prevSnapshot []byte) error {
	action, ok := legacyActionMap[r.Action]
	if !ok {
		return fmt.Errorf("unmapped legacy action %q", r.Action)
	}
	snapshot, err := RekeySnapshotOldToNew(r.Snapshot)
	if err != nil {
		return err
	}
	meta := revisionLegacyMeta{
		Note:       r.Note,
		IsMinor:    r.IsMinor,
		RevertedTo: r.RevertedTo,
	}
	var changed []string
	if r.HasChangedFields() || string(r.ChangedFields) == "[]" {
		// Recorded set (possibly empty []) — re-key verbatim.
		changed, err = RekeyKeysOldToNew(r.ChangedFieldsList())
		if err != nil {
			return err
		}
	} else {
		// Legacy tri-state NULL: bake the old /diff fallback derivation
		// (whole-snapshot ChangedKeys vs the predecessor) and mark the row so
		// the wire adapter keeps omitting the field.
		meta.NullChangedFields = true
		changed, err = deriveLegacyChangedFields(galgameDB, r, prevSnapshot)
		if err != nil {
			return err
		}
	}
	changedJSON, err := json.Marshal(changed)
	if err != nil {
		return err
	}
	var metaParam any
	if metaJSON := marshalRevisionMeta(meta); metaJSON != nil {
		metaParam = string(metaJSON)
	}
	return catalogDB.Exec(`
		INSERT INTO edit_revision
		  (entity_family, entity_type, entity_id, seq, action, changed_fields,
		   snapshot, actor_uid, amender_uid, proposal_id, site, created_at,
		   legacy_action, legacy_id, legacy_meta)
		VALUES ('galgame', ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, NULL, NULL, ?, ?, ?, ?, ?::jsonb)
		ON CONFLICT (legacy_id) WHERE legacy_id IS NOT NULL DO UPDATE SET
		  entity_family = EXCLUDED.entity_family,
		  entity_type   = EXCLUDED.entity_type,
		  entity_id     = EXCLUDED.entity_id,
		  seq           = EXCLUDED.seq,
		  action        = EXCLUDED.action,
		  changed_fields = EXCLUDED.changed_fields,
		  snapshot      = EXCLUDED.snapshot,
		  actor_uid     = EXCLUDED.actor_uid,
		  site          = EXCLUDED.site,
		  created_at    = EXCLUDED.created_at,
		  legacy_action = EXCLUDED.legacy_action,
		  legacy_meta   = EXCLUDED.legacy_meta`,
		// proposal_id deliberately absent from DO UPDATE — the PR pass owns it.
		editspec.TypeGame, r.GalgameID, r.Revision, action, string(changedJSON),
		string(snapshot), r.UserID, editspec.SiteGalgameWiki, r.Created.Time(),
		r.Action, r.ID, metaParam,
	).Error
}

// marshalRevisionMeta returns nil when the meta carries nothing — the column
// stays NULL instead of storing "{}".
func marshalRevisionMeta(m revisionLegacyMeta) []byte {
	if m.Note == "" && !m.IsMinor && m.RevertedTo == nil && !m.NullChangedFields {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

// deriveLegacyChangedFields reproduces GetRevisionDiff's legacy fallback:
// model.ChangedKeys(previous snapshot, this snapshot), empty-snapshot baseline
// for revision 1 / missing predecessor. Result is re-keyed and sorted (KeysOf).
func deriveLegacyChangedFields(galgameDB *gorm.DB, r *model.GalgameRevision, prevSnapshot []byte) ([]string, error) {
	cur, err := model.SnapshotFromJSON(r.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	prev := &model.Snapshot{}
	if r.Revision > 1 {
		if prevSnapshot == nil {
			var prevRow model.GalgameRevision
			err := galgameDB.Where("galgame_id = ? AND revision = ?", r.GalgameID, r.Revision-1).
				First(&prevRow).Error
			if err == nil {
				prevSnapshot = prevRow.Snapshot
			} else if err != gorm.ErrRecordNotFound {
				return nil, err
			}
		}
		if prevSnapshot != nil {
			if ps, err := model.SnapshotFromJSON(prevSnapshot); err == nil {
				prev = ps
			}
		}
	}
	return RekeyKeysOldToNew(model.KeysOf(model.ChangedKeys(prev, cur)))
}

// transformMergedPRs migrates status=1 PRs into edit_proposal and links their
// produced revision (proposal_id back-pointer). Ground-truth verified: every
// merged PR carries a resolvable revision_id and base_revision.
func transformMergedPRs(galgameDB, catalogDB *gorm.DB, sum *TransformSummary) error {
	var prs []model.GalgamePR
	if err := galgameDB.Where("status = 1").Order("id ASC").Find(&prs).Error; err != nil {
		return err
	}
	for i := range prs {
		if err := upsertMergedPR(galgameDB, catalogDB, &prs[i]); err != nil {
			return fmt.Errorf("pr id=%d (gid=%d): %w", prs[i].ID, prs[i].GalgameID, err)
		}
		sum.ProposalsProcessed++
	}
	return nil
}

func upsertMergedPR(galgameDB, catalogDB *gorm.DB, pr *model.GalgamePR) error {
	if pr.RevisionID == nil {
		return fmt.Errorf("merged PR has no revision_id")
	}
	var baseRow model.GalgameRevision
	if err := galgameDB.Where("galgame_id = ? AND revision = ?", pr.GalgameID, pr.BaseRevision).
		First(&baseRow).Error; err != nil {
		return fmt.Errorf("base revision %d: %w", pr.BaseRevision, err)
	}
	patch, err := derivePRPatch(baseRow.Snapshot, pr.Snapshot)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(proposalLegacyMeta{
		Title: pr.Title, Message: pr.Message,
		Snapshot: json.RawMessage(pr.Snapshot), BaseRevision: pr.BaseRevision,
	})
	if err != nil {
		return err
	}
	var decidedBy any
	if pr.CompletedBy != nil {
		decidedBy = *pr.CompletedBy
	}
	var decidedAt any
	if pr.CompletedTime != nil {
		decidedAt = pr.CompletedTime.Time()
	}
	var proposalID int64
	if err := catalogDB.Raw(`
		INSERT INTO edit_proposal
		  (entity_family, entity_type, entity_id, base_revision_seq, patch,
		   proposer_uid, note, site, status, decided_by_uid, decided_at,
		   decision_note, created_at, updated_at, legacy_pr_id, legacy_meta)
		VALUES ('galgame', ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?::jsonb)
		ON CONFLICT (legacy_pr_id) WHERE legacy_pr_id IS NOT NULL DO UPDATE SET
		  entity_family = EXCLUDED.entity_family,
		  entity_type   = EXCLUDED.entity_type,
		  entity_id     = EXCLUDED.entity_id,
		  base_revision_seq = EXCLUDED.base_revision_seq,
		  patch         = EXCLUDED.patch,
		  proposer_uid  = EXCLUDED.proposer_uid,
		  note          = EXCLUDED.note,
		  site          = EXCLUDED.site,
		  status        = EXCLUDED.status,
		  decided_by_uid = EXCLUDED.decided_by_uid,
		  decided_at    = EXCLUDED.decided_at,
		  created_at    = EXCLUDED.created_at,
		  updated_at    = EXCLUDED.updated_at,
		  legacy_meta   = EXCLUDED.legacy_meta
		RETURNING id`,
		editspec.TypeGame, pr.GalgameID, pr.BaseRevision, string(patch),
		pr.UserID, prMergeNote(pr.Title, pr.Message), editspec.SiteGalgameWiki,
		editing.StatusMerged, decidedBy, decidedAt,
		pr.Created.Time(), pr.Updated.Time(), pr.ID, string(meta),
	).Scan(&proposalID).Error; err != nil {
		return err
	}
	return catalogDB.Exec(
		`UPDATE edit_revision SET proposal_id = ? WHERE legacy_id = ?`,
		proposalID, *pr.RevisionID,
	).Error
}

// derivePRPatch builds the engine patch of a migrated PR: every field whose
// PR value differs (JSON-normalized) from the base revision's value, plus
// fields the base era did not carry yet. Values keep the PR's raw bytes.
func derivePRPatch(baseRaw, prRaw []byte) ([]byte, error) {
	var base, pr map[string]json.RawMessage
	if err := json.Unmarshal(baseRaw, &base); err != nil {
		return nil, fmt.Errorf("decode base snapshot: %w", err)
	}
	if err := json.Unmarshal(prRaw, &pr); err != nil {
		return nil, fmt.Errorf("decode pr snapshot: %w", err)
	}
	keys := make([]string, 0, len(pr))
	for k := range pr {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	patch := make(map[string]json.RawMessage, len(keys))
	for _, k := range keys {
		newKey, ok := editspec.OldToNew[k]
		if !ok {
			return nil, fmt.Errorf("pr snapshot key %q has no eternal-key mapping", k)
		}
		baseVal, has := base[k]
		if !has || !jsonEqRaw(baseVal, pr[k]) {
			patch[newKey] = pr[k]
		}
	}
	return json.Marshal(patch)
}

// jsonEqRaw compares two raw JSON values by canonical re-encoding (map keys
// sort on marshal), mirroring editing.jsonValueEqual.
func jsonEqRaw(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ab, errA := json.Marshal(av)
	bb, errB := json.Marshal(bv)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

// bumpSequence advances a table's identity sequence past both the highest
// engine id and the source-table floor, so ids minted for NEW writes stay
// above every legacy wire id — including the ids of DROPPED pending/declined
// PRs, which must never resolve to an unrelated new row. Monotone and
// idempotent.
func bumpSequence(catalogDB *gorm.DB, table string, floor int64) error {
	stmt := fmt.Sprintf(`
		SELECT setval(
		  pg_get_serial_sequence('%s', 'id'),
		  GREATEST(
		    %d,
		    (SELECT COALESCE(MAX(id), 1) FROM %s)
		  ))`, table, floor, table)
	return catalogDB.Exec(stmt).Error
}

// ---- verification gates ------------------------------------------------------

// VerifyReport carries the gate evidence for the execution report.
type VerifyReport struct {
	ActionBuckets   map[string]int64 // legacy_action → migrated count (== old)
	GidsChecked     int64
	SampleChecked   int
	ProposalsLinked int64
}

// Verify runs the four reconciliation gates (03 号裁定 3). sample ≤ 0 checks
// every row. Any failure returns a descriptive error.
func Verify(galgameDB, catalogDB *gorm.DB, sample int) (*VerifyReport, error) {
	rep := &VerifyReport{ActionBuckets: map[string]int64{}}

	// Gate 1 — per-action bucket counts.
	oldBuckets, err := countByKey(galgameDB, `SELECT action AS k, count(*) AS n FROM galgame_revision GROUP BY action`)
	if err != nil {
		return nil, err
	}
	newBuckets, err := countByKey(catalogDB, `SELECT legacy_action AS k, count(*) AS n FROM edit_revision WHERE legacy_id IS NOT NULL GROUP BY legacy_action`)
	if err != nil {
		return nil, err
	}
	for action, n := range oldBuckets {
		if newBuckets[action] != n {
			return nil, fmt.Errorf("gate1: action %q old=%d migrated=%d", action, n, newBuckets[action])
		}
		rep.ActionBuckets[action] = n
	}
	if len(newBuckets) != len(oldBuckets) {
		return nil, fmt.Errorf("gate1: bucket sets differ (old %d, new %d)", len(oldBuckets), len(newBuckets))
	}

	// Gate 2 — per-gid seq contiguity + count parity.
	type gidAgg struct {
		Gid    int64
		N      int64
		MaxSeq int64
		MinSeq int64
	}
	var oldAgg, newAgg []gidAgg
	if err := galgameDB.Raw(`SELECT galgame_id AS gid, count(*) AS n, max(revision) AS max_seq, min(revision) AS min_seq
		FROM galgame_revision GROUP BY galgame_id ORDER BY galgame_id`).Scan(&oldAgg).Error; err != nil {
		return nil, err
	}
	if err := catalogDB.Raw(`SELECT entity_id AS gid, count(*) AS n, max(seq) AS max_seq, min(seq) AS min_seq
		FROM edit_revision WHERE entity_family='galgame' AND entity_type=? AND legacy_id IS NOT NULL
		GROUP BY entity_id ORDER BY entity_id`, editspec.TypeGame).Scan(&newAgg).Error; err != nil {
		return nil, err
	}
	if len(oldAgg) != len(newAgg) {
		return nil, fmt.Errorf("gate2: gid count old=%d new=%d", len(oldAgg), len(newAgg))
	}
	for i := range oldAgg {
		o, n := oldAgg[i], newAgg[i]
		if o.Gid != n.Gid || o.N != n.N || o.MaxSeq != n.MaxSeq || n.MinSeq != 1 || n.N != n.MaxSeq {
			return nil, fmt.Errorf("gate2: gid %d old(n=%d,max=%d) new(n=%d,min=%d,max=%d)", o.Gid, o.N, o.MaxSeq, n.N, n.MinSeq, n.MaxSeq)
		}
	}
	rep.GidsChecked = int64(len(oldAgg))

	// Gate 3 — sampled row fidelity.
	q := galgameDB.Model(&model.GalgameRevision{})
	if sample > 0 {
		q = q.Order("random()").Limit(sample)
	}
	var oldRows []model.GalgameRevision
	if err := q.Find(&oldRows).Error; err != nil {
		return nil, err
	}
	for i := range oldRows {
		if err := verifyRow(galgameDB, catalogDB, &oldRows[i]); err != nil {
			return nil, fmt.Errorf("gate3: old id=%d: %w", oldRows[i].ID, err)
		}
	}
	rep.SampleChecked = len(oldRows)

	// Gate 4 — merged-PR linkage.
	var oldMerged []model.GalgamePR
	if err := galgameDB.Where("status = 1").Find(&oldMerged).Error; err != nil {
		return nil, err
	}
	var legacyProposals int64
	if err := catalogDB.Model(&proposalRow{}).Where("legacy_pr_id IS NOT NULL").Count(&legacyProposals).Error; err != nil {
		return nil, err
	}
	if legacyProposals != int64(len(oldMerged)) {
		return nil, fmt.Errorf("gate4: old merged=%d migrated proposals=%d", len(oldMerged), legacyProposals)
	}
	for i := range oldMerged {
		pr := &oldMerged[i]
		var p proposalRow
		if err := catalogDB.Where("legacy_pr_id = ?", pr.ID).First(&p).Error; err != nil {
			return nil, fmt.Errorf("gate4: pr %d not migrated: %w", pr.ID, err)
		}
		if p.Status != editing.StatusMerged || p.BaseRevisionSeq != pr.BaseRevision || int(p.ProposerUID) != pr.UserID {
			return nil, fmt.Errorf("gate4: pr %d field mismatch (status=%d base=%d proposer=%d)", pr.ID, p.Status, p.BaseRevisionSeq, p.ProposerUID)
		}
		var rev revisionRow
		if err := catalogDB.Where("legacy_id = ?", *pr.RevisionID).First(&rev).Error; err != nil {
			return nil, fmt.Errorf("gate4: pr %d revision %d missing: %w", pr.ID, *pr.RevisionID, err)
		}
		if rev.ProposalID == nil || *rev.ProposalID != p.ID {
			return nil, fmt.Errorf("gate4: pr %d revision link broken (proposal_id=%v)", pr.ID, rev.ProposalID)
		}
	}
	rep.ProposalsLinked = legacyProposals
	return rep, nil
}

func verifyRow(galgameDB, catalogDB *gorm.DB, old *model.GalgameRevision) error {
	var row revisionRow
	if err := catalogDB.Where("legacy_id = ?", old.ID).First(&row).Error; err != nil {
		return fmt.Errorf("not migrated: %w", err)
	}
	if row.Seq != old.Revision || int(row.ActorUID) != old.UserID || row.Site != editspec.SiteGalgameWiki {
		return fmt.Errorf("identity mismatch (seq=%d actor=%d site=%q)", row.Seq, row.ActorUID, row.Site)
	}
	if row.LegacyAction == nil || *row.LegacyAction != old.Action || row.Action != legacyActionMap[old.Action] {
		return fmt.Errorf("action mismatch")
	}
	if !row.CreatedAt.Truncate(time.Microsecond).Equal(old.Created.Time().Truncate(time.Microsecond)) {
		return fmt.Errorf("created mismatch (%v vs %v)", row.CreatedAt, old.Created.Time())
	}
	want, err := RekeySnapshotOldToNew(old.Snapshot)
	if err != nil {
		return err
	}
	if !jsonEqRaw(json.RawMessage(want), json.RawMessage(row.Snapshot)) {
		return fmt.Errorf("snapshot fidelity mismatch")
	}
	meta := decodeRevisionMeta(row.LegacyMeta)
	if meta.Note != old.Note || meta.IsMinor != old.IsMinor || !intPtrEq(meta.RevertedTo, old.RevertedTo) {
		return fmt.Errorf("legacy_meta mismatch")
	}
	stored, err := decodeStringList(row.ChangedFields)
	if err != nil {
		return err
	}
	if old.HasChangedFields() || string(old.ChangedFields) == "[]" {
		if meta.NullChangedFields {
			return fmt.Errorf("recorded row marked null_changed_fields")
		}
		want, err := RekeyKeysOldToNew(old.ChangedFieldsList())
		if err != nil {
			return err
		}
		if !stringSetEq(stored, want) {
			return fmt.Errorf("changed_fields mismatch (stored %v want %v)", stored, want)
		}
	} else {
		if !meta.NullChangedFields {
			return fmt.Errorf("legacy-NULL row not marked null_changed_fields")
		}
		want, err := deriveLegacyChangedFields(galgameDB, old, nil)
		if err != nil {
			return err
		}
		if !stringSetEq(stored, want) {
			return fmt.Errorf("derived changed_fields mismatch (stored %v want %v)", stored, want)
		}
	}
	return nil
}

func countByKey(db *gorm.DB, query string) (map[string]int64, error) {
	var rows []struct {
		K string
		N int64
	}
	if err := db.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.K] = r.N
	}
	return out, nil
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func stringSetEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
