package jobs

import (
	"reflect"
	"testing"
)

// The whole point of the diff is that an already-known breakage must NOT
// re-alert: the 12 unrescuable references live in production would otherwise
// make this job permanently red, which is how an alert stops being read.
func TestNewlyBrokenOnlyReportsUnseenHashes(t *testing.T) {
	previous := map[string]struct{}{"aaa": {}, "bbb": {}}

	if got := newlyBroken([]string{"aaa", "bbb"}, previous); len(got) != 0 {
		t.Fatalf("known breakage must not alert, got %v", got)
	}
	if got := newlyBroken([]string{"aaa", "ccc"}, previous); !reflect.DeepEqual(got, []string{"ccc"}) {
		t.Fatalf("want [ccc], got %v", got)
	}
	// A repaired hash disappearing from the set is not an event either.
	if got := newlyBroken([]string{"aaa"}, previous); len(got) != 0 {
		t.Fatalf("shrinking set must not alert, got %v", got)
	}
	// No baseline at all (empty map) reports everything — Run guards this
	// case separately via hasBaseline so the first deploy doesn't page.
	if got := newlyBroken([]string{"aaa"}, map[string]struct{}{}); !reflect.DeepEqual(got, []string{"aaa"}) {
		t.Fatalf("want [aaa], got %v", got)
	}
}

// The persisted list is what the next run diffs against, so its order must not
// depend on map iteration or every run would look like churn.
func TestDistinctHashesIsSortedAndDeduped(t *testing.T) {
	refs := []imageRef{
		{Hash: "ccc", Kind: "work_cover", EntityID: 1},
		{Hash: "aaa", Kind: "work_screenshot", EntityID: 2},
		{Hash: "ccc", Kind: "work_screenshot", EntityID: 3},
		{Hash: "bbb", Kind: "character_portrait", EntityID: 4},
	}
	want := []string{"aaa", "bbb", "ccc"}
	for i := range 5 {
		if got := distinctHashes(refs); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: want %v, got %v", i, want, got)
		}
	}
}

// A summary that says "8 hashes" is not actionable; "3 works" is what a human
// goes and looks at. One work with several dead covers must count once.
func TestAffectedEntitiesCountsDistinctEntitiesPerKind(t *testing.T) {
	refs := []imageRef{
		{Hash: "a", Kind: "work_cover", EntityID: 10},
		{Hash: "b", Kind: "work_cover", EntityID: 10},
		{Hash: "c", Kind: "work_cover", EntityID: 11},
		{Hash: "d", Kind: "work_screenshot", EntityID: 10},
		{Hash: "e", Kind: "character_portrait", EntityID: 99},
	}
	want := map[string]int{"work_cover": 2, "work_screenshot": 1, "character_portrait": 1}
	if got := affectedEntities(refs); !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// The audit only means anything if it runs after the sweeps that mutate the
// state it reads: image-gc hard-deletes, and the refpings are what keep a
// hash alive in the first place.
func TestImageRefAuditRunsAfterGCAndRefpings(t *testing.T) {
	r := NewRegistry()
	RegisterAll(r)

	at := map[string]string{}
	for _, j := range r.List() {
		at[j.Name] = j.Schedule.DailyAt
	}

	audit, ok := at[JobImageRefAudit]
	if !ok {
		t.Fatalf("%s is not registered", JobImageRefAudit)
	}
	for _, earlier := range []string{"image-gc", "galgame-image-refping", "catalog-image-refping", "user-avatar-refping"} {
		when, ok := at[earlier]
		if !ok {
			t.Fatalf("%s is not registered", earlier)
		}
		if when >= audit {
			t.Errorf("%s runs at %s, audit at %s — the audit must be last or it reads a mid-sweep state",
				earlier, when, audit)
		}
	}
}
