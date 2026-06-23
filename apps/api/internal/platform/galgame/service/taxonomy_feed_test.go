package service

import (
	"testing"

	"gorm.io/datatypes"
)

// snapshotName powers the self-describing `name` of the /galgame/taxonomy/recent
// feed. It must extract the top-level name from any of the four entity snapshots
// and degrade to "" (never panic) on empty/malformed input.
func TestSnapshotName(t *testing.T) {
	cases := []struct {
		name string
		in   datatypes.JSON
		want string
	}{
		{"series", datatypes.JSON(`{"name":"Fate 系列","description":"x"}`), "Fate 系列"},
		{"tag", datatypes.JSON(`{"name":"萌","aliases":["moe"]}`), "萌"},
		{"empty bytes", datatypes.JSON(``), ""},
		{"nil", nil, ""},
		{"no name key", datatypes.JSON(`{"description":"x"}`), ""},
		{"malformed", datatypes.JSON(`{not json`), ""},
	}
	for _, c := range cases {
		if got := snapshotName(c.in); got != c.want {
			t.Errorf("%s: snapshotName() = %q, want %q", c.name, got, c.want)
		}
	}
}
