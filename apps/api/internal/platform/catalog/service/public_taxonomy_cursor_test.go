package service

import "testing"

// TestTaxonomyNextCursorEndsTheWalk pins the fix for a bug all four browse lanes
// shared: they emitted a cursor whenever a page came back exactly full, which is
// indistinguishable from the last page. Walking a facet whose size is a multiple
// of the limit therefore ended on a cursor pointing at an empty page — caught by
// the series lane's keyset test (2 rows at limit=1).
//
// The lanes now SELECT limit+1 and trim, so "there is more" is evidence rather
// than a guess.
func TestTaxonomyNextCursorEndsTheWalk(t *testing.T) {
	// An exactly-full FINAL page: the over-fetch found no extra row.
	if c := taxonomyNextCursor(taxonomyLaneSeries, []int64{2}, false); c != nil {
		t.Errorf("a full last page must end the walk, got cursor %q", *c)
	}
	// An exactly-full page with more behind it.
	c := taxonomyNextCursor(taxonomyLaneSeries, []int64{1}, true)
	if c == nil {
		t.Fatal("a full page with more behind it must carry a cursor")
	}
	// The cursor is lane-pinned and keyed on the LAST id of the page.
	cur, err := decodePublicCursor(*c, taxonomyLaneSeries)
	if err != nil {
		t.Fatalf("decode own cursor: %v", err)
	}
	if cur.ID != 1 {
		t.Errorf("cursor id = %d, want the page's last id 1", cur.ID)
	}
	if _, err := decodePublicCursor(*c, taxonomyLaneTags); err == nil {
		t.Error("a series cursor must not decode on the tags lane")
	}
	// An empty page never yields a cursor, whatever `more` claims.
	if c := taxonomyNextCursor(taxonomyLaneSeries, nil, true); c != nil {
		t.Error("an empty page must not carry a cursor")
	}
}

// TestTaxonomyTrim: the over-fetched row is removed from the page it was only
// ever evidence for — leaking it would hand callers limit+1 rows.
func TestTaxonomyTrim(t *testing.T) {
	page, more := taxonomyTrim([]int{1, 2, 3}, 2)
	if len(page) != 2 || !more {
		t.Errorf("over-fetched: got %v more=%v, want 2 rows and more=true", page, more)
	}
	if page, more := taxonomyTrim([]int{1, 2}, 2); len(page) != 2 || more {
		t.Errorf("exactly full: got %v more=%v, want 2 rows and more=false", page, more)
	}
	if page, more := taxonomyTrim([]int{1}, 2); len(page) != 1 || more {
		t.Errorf("short page: got %v more=%v, want 1 row and more=false", page, more)
	}
	if page, more := taxonomyTrim([]int(nil), 2); len(page) != 0 || more {
		t.Errorf("empty page: got %v more=%v", page, more)
	}
}
