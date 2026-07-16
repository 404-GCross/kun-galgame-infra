package dto

import (
	"encoding/json"
	"testing"
	"time"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// snapshotJSON marshals a representative snapshot to jsonb bytes (as the write
// path stores it), so decode->reencode in the DTO mappers is an identity for
// well-formed snapshots.
func snapshotJSON(t *testing.T) datatypes.JSON {
	t.Helper()
	rd := "2026-07-09"
	b, err := json.Marshal(model.Snapshot{
		VNDBID: "v54934", ReleaseDate: &rd, ReleasePrecision: "day",
		NameZhCN: "名字", ContentLimit: "sfw", OriginalLanguage: "ja-jp", AgeLimit: "r18",
		Aliases: []string{"a"}, TagIDs: []int{1, 2}, OfficialIDs: []int{144}, EngineIDs: []int{},
		Links:       []model.SnapshotLink{{Name: "官网", Link: "https://x"}},
		Covers:      []model.SnapshotCover{{ImageHash: "h0", SortOrder: 0, Kind: "main"}},
		Screenshots: []model.SnapshotScreenshot{},
	})
	require.NoError(t, err)
	return datatypes.JSON(b)
}

// TestRevisionResponse_WireIdenticalToModel pins RevisionResponse to
// model.GalgameRevision's JSON (snapshot decoded from the stored jsonb).
func TestRevisionResponse_WireIdenticalToModel(t *testing.T) {
	ts := model.Timestamp(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	revertedTo := 3
	r := &model.GalgameRevision{
		ID: 1, GalgameID: 10, Revision: 5, UserID: 2,
		Action: "updated", Note: "n", Snapshot: snapshotJSON(t), IsMinor: false,
		RevertedTo:    &revertedTo,
		ChangedFields: datatypes.JSON([]byte(`["name_zh_cn","tag_ids"]`)),
		Created:       ts,
	}
	oldJSON, err := json.Marshal(r)
	require.NoError(t, err)
	newJSON, err := json.Marshal(NewRevisionResponse(r))
	require.NoError(t, err)
	assert.JSONEq(t, string(oldJSON), string(newJSON),
		"RevisionResponse must be wire-identical to model.GalgameRevision")
}

// TestPRResponse_WireIdenticalToModel pins PRResponse to model.GalgamePR's JSON.
func TestPRResponse_WireIdenticalToModel(t *testing.T) {
	ts := model.Timestamp(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	completedBy := 9
	revisionID := 5
	p := &model.GalgamePR{
		ID: 1, GalgameID: 10, UserID: 2, Status: 1,
		Title: "t", Message: "m", BaseRevision: 4, Snapshot: snapshotJSON(t),
		CompletedBy: &completedBy, CompletedTime: &ts, RevisionID: &revisionID,
		Created: ts, Updated: ts,
	}
	oldJSON, err := json.Marshal(p)
	require.NoError(t, err)
	newJSON, err := json.Marshal(NewPRResponse(p))
	require.NoError(t, err)
	assert.JSONEq(t, string(oldJSON), string(newJSON),
		"PRResponse must be wire-identical to model.GalgamePR")
}
