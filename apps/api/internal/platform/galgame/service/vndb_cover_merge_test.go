package service

import (
	"testing"

	"api/internal/platform/galgame/model"
)

// TestMergeUserAndVndbCovers locks the data-preservation contract: a user edit
// keeps its own covers (pin/order), can't delete the synced VNDB covers it omits,
// and at most one cover stays pinned.
func TestMergeUserAndVndbCovers(t *testing.T) {
	user := []model.SnapshotCover{
		{ImageHash: "u1", SortOrder: 0, Source: ""}, // user pins their own cover
		{ImageHash: "u2", SortOrder: 1, Source: ""},
		{ImageHash: "u1", SortOrder: 9}, // duplicate hash → collapsed
	}
	vndb := []model.SnapshotCover{
		{ImageHash: "v1", SortOrder: 0, Source: "vndb", Kind: "main"},     // omitted by user → preserved + demoted
		{ImageHash: "v2", SortOrder: 1, Source: "vndb", Kind: "pkgfront"}, // omitted by user → preserved
	}

	out := mergeUserAndVndbCovers(user, vndb)

	pinned := 0
	byHash := map[string]model.SnapshotCover{}
	for _, c := range out {
		if c.SortOrder == 0 {
			pinned++
		}
		byHash[c.ImageHash] = c
	}
	if len(out) != 4 {
		t.Fatalf("want 4 covers (u1,u2 + preserved v1,v2), got %d: %+v", len(out), out)
	}
	if pinned != 1 || byHash["u1"].SortOrder != 0 {
		t.Fatalf("user pin (u1) must win as the only sort_order=0: %+v", out)
	}
	if byHash["v1"].SortOrder == 0 {
		t.Fatalf("vndb v1 must be demoted off the pinned slot: %+v", byHash["v1"])
	}
	if byHash["v1"].Source != "vndb" || byHash["v1"].Kind != "main" {
		t.Fatalf("vndb v1 metadata lost: %+v", byHash["v1"])
	}
	if _, ok := byHash["v2"]; !ok {
		t.Fatalf("omitted vndb cover v2 must be preserved: %+v", out)
	}
}

// A user-echoed vndb cover (same hash) collapses to one: the user's sort_order is
// honored (repin), but source/source_key/kind are restored from cur even when the
// client stripped them (e.g. forum's coversWire drops `kind`).
func TestMergeUserAndVndbCovers_EchoedRestoresProvenance(t *testing.T) {
	user := []model.SnapshotCover{{ImageHash: "v1", SortOrder: 0, Source: "", SourceKey: "", Kind: ""}}
	vndb := []model.SnapshotCover{{ImageHash: "v1", SortOrder: 1, Source: "vndb", SourceKey: "cv9", Kind: "main"}}

	out := mergeUserAndVndbCovers(user, vndb)
	if len(out) != 1 {
		t.Fatalf("echoed cover must collapse to 1, got %d: %+v", len(out), out)
	}
	if out[0].SortOrder != 0 {
		t.Fatalf("user's repin (sort_order=0) must be honored: %+v", out[0])
	}
	if out[0].Source != "vndb" || out[0].SourceKey != "cv9" || out[0].Kind != "main" {
		t.Fatalf("provenance must be restored from cur despite the stripped echo: %+v", out[0])
	}
}

// When the user provides no pinned cover, the managed (vndb) pinned cover stays.
func TestMergeUserAndVndbCovers_NoUserPinned(t *testing.T) {
	user := []model.SnapshotCover{{ImageHash: "u1", SortOrder: 1}}
	vndb := []model.SnapshotCover{{ImageHash: "v1", SortOrder: 0, Source: "vndb", Kind: "main"}}

	out := mergeUserAndVndbCovers(user, vndb)
	var pinned string
	for _, c := range out {
		if c.SortOrder == 0 {
			pinned = c.ImageHash
		}
	}
	if pinned != "v1" {
		t.Fatalf("vndb main should stay pinned when the user has none, pinned=%q: %+v", pinned, out)
	}
}

func TestVndbManagedCovers(t *testing.T) {
	in := []model.SnapshotCover{
		{ImageHash: "a", Source: ""},
		{ImageHash: "b", Source: "vndb"},
		{ImageHash: "c", Source: "vndb"},
	}
	got := vndbManagedCovers(in)
	if len(got) != 2 || got[0].ImageHash != "b" || got[1].ImageHash != "c" {
		t.Fatalf("want only b,c (source=vndb), got %+v", got)
	}
}
