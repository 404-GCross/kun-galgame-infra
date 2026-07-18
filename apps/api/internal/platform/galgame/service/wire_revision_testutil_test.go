package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// revertGalgameForTest reverts a galgame to an earlier revision through the
// engine directly — the galgame-service Revert wrapper retired at E3b, so tests
// that exercise revert (a galgame-specific apply, e.g. restoring covers) drive
// the engine the same way the retired wrapper did.
func revertGalgameForTest(t *testing.T, userID, gid, toSeq int) {
	t.Helper()
	_, _, err := testEngine.Revert(context.Background(), editing.RevertInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(gid),
		ToSeq:      toSeq,
		Note:       fmt.Sprintf("回滚到版本 %d", toSeq),
		Actor:      editActor(userID, 2, perm.EditGameReview, perm.EditGameStatus),
	})
	require.NoError(t, err)
}

// submitProposalForTest files an OPEN proposal (tier 0, no automerge) and
// returns its id — the engine equivalent of the retired SubmitPR wrapper.
func submitProposalForTest(t *testing.T, proposerUID, gid int, patch map[string]any, note string) int64 {
	t.Helper()
	prop, _, err := testEngine.CreateProposal(context.Background(), editing.CreateProposalInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(gid),
		Patch:      patch,
		Note:       note,
		Actor:      editActor(proposerUID, 0),
	})
	require.NoError(t, err)
	return prop.ID
}

// mergeProposalForTest merges a proposal as a reviewer — the engine equivalent
// of the retired MergePR wrapper.
func mergeProposalForTest(t *testing.T, reviewerUID int, proposalID int64) {
	t.Helper()
	_, err := testEngine.MergeProposal(context.Background(), proposalID,
		editActor(reviewerUID, 2, perm.EditGameReview), "")
	require.NoError(t, err)
}

// This file is a TEST-ONLY reconstruction of the retired old-wire revision
// shape. The production bridge that reproduced model.GalgameRevision from the
// edit_* tables retired at E3b (the galgame surface no longer serves old-wire
// revision reads), but the surviving write-path tests still assert galgame
// revisions in the OLD vocabulary (model.Snapshot keys, "created"/"updated"/
// "reverted"/"merged" action strings, old-key changed_fields). Reconstructing
// that shape from the engine's edit_revision row here keeps those tests
// meaningful without resurrecting any production adapter code.

type editRevisionTestRow struct {
	Seq           int            `gorm:"column:seq"`
	Action        int16          `gorm:"column:action"`
	ChangedFields datatypes.JSON `gorm:"column:changed_fields"`
	Snapshot      datatypes.JSON `gorm:"column:snapshot"`
	ActorUID      int64          `gorm:"column:actor_uid"`
	ProposalID    *int64         `gorm:"column:proposal_id"`
}

func (editRevisionTestRow) TableName() string { return "edit_revision" }

// bridgeRevision reads one galgame revision back in the old-wire shape (rev <= 0
// means latest), reconstructed from edit_revision.
func bridgeRevision(t *testing.T, gid, rev int) *model.GalgameRevision {
	t.Helper()
	q := testDB.Model(&editRevisionTestRow{}).
		Where("entity_family = ? AND entity_type = ? AND entity_id = ?", "galgame", editspec.TypeGame, gid)
	var row editRevisionTestRow
	var err error
	if rev > 0 {
		err = q.Where("seq = ?", rev).First(&row).Error
	} else {
		err = q.Order("seq DESC").First(&row).Error
	}
	if err != nil {
		t.Fatalf("bridgeRevision(%d, %d): %v", gid, rev, err)
	}

	note := ""
	if row.ProposalID != nil {
		var p struct {
			Note string `gorm:"column:note"`
		}
		if err := testDB.Table("edit_proposal").Select("note").
			Where("id = ?", *row.ProposalID).Scan(&p).Error; err == nil {
			title, message := decodeNoteForTest(p.Note)
			switch {
			case title != "" && message != "":
				note = title + "\n\n" + message
			case title != "":
				note = title
			default:
				note = message
			}
		}
	}

	out := &model.GalgameRevision{
		GalgameID: gid,
		Revision:  row.Seq,
		UserID:    int(row.ActorUID),
		Action:    actionStringForTest(row.Action),
		Note:      note,
		Snapshot:  rekeySnapshotNewToOldForTest(t, row.Snapshot),
	}
	if len(row.ChangedFields) > 0 {
		var keys []string
		if err := json.Unmarshal(row.ChangedFields, &keys); err != nil {
			t.Fatalf("bridgeRevision changed_fields decode: %v", err)
		}
		rekeyed, err := json.Marshal(rekeyKeysNewToOldForTest(keys))
		if err != nil {
			t.Fatalf("bridgeRevision changed_fields encode: %v", err)
		}
		out.ChangedFields = datatypes.JSON(rekeyed)
	}
	return out
}

// bridgeRevisionCount counts a galgame's revisions in the engine log.
func bridgeRevisionCount(t *testing.T, gid int) int64 {
	t.Helper()
	var n int64
	if err := testDB.Model(&editRevisionTestRow{}).
		Where("entity_family = ? AND entity_type = ? AND entity_id = ?", "galgame", editspec.TypeGame, gid).
		Count(&n).Error; err != nil {
		t.Fatalf("bridgeRevisionCount(%d): %v", gid, err)
	}
	return n
}

// actionStringForTest mirrors the retired bridge's new-era action rendering.
func actionStringForTest(action int16) string {
	switch action {
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

// decodeNoteForTest is the inverse of the retired EncodeNote packing
// (title + "\n\n" + message); plain single-string notes decode as the title.
func decodeNoteForTest(note string) (title, message string) {
	const sep = "\n\n"
	if len(note) >= 2 && note[:2] == sep {
		return "", note[2:]
	}
	for i := 0; i+1 < len(note); i++ {
		if note[i] == '\n' && note[i+1] == '\n' {
			return note[:i], note[i+2:]
		}
	}
	return note, ""
}

// rekeySnapshotNewToOldForTest renames an engine snapshot back to the old wire
// vocabulary (status is dropped — the old snapshot never carried it).
func rekeySnapshotNewToOldForTest(t *testing.T, raw datatypes.JSON) datatypes.JSON {
	t.Helper()
	if len(raw) == 0 {
		return raw
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("rekeySnapshot decode: %v", err)
	}
	out := make(map[string]json.RawMessage, len(doc))
	for k, v := range doc {
		if k == editspec.FieldStatus {
			continue
		}
		oldKey, ok := editspec.NewToOld[k]
		if !ok {
			t.Fatalf("rekeySnapshot: engine key %q has no old-wire mapping", k)
		}
		out[oldKey] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("rekeySnapshot encode: %v", err)
	}
	return datatypes.JSON(b)
}

// rekeyKeysNewToOldForTest maps an engine changed_fields list back to old keys
// (status included; unknown keys pass through).
func rekeyKeysNewToOldForTest(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if oldKey, ok := editspec.NewToOld[k]; ok {
			out = append(out, oldKey)
		} else {
			out = append(out, k)
		}
	}
	return out
}
