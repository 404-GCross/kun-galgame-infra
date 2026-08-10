package service

import (
	"testing"

	"api/internal/platform/trust/model"
)

func TestTermASCIIBoundary(t *testing.T) {
	cleanTables(t)
	svc := NewTermService(testDB, nil)
	mkTerm(t, svc, nil, "ice", model.TermKindBanned)
	mkTerm(t, svc, nil, "master", model.TermKindBanned)
	mkTerm(t, svc, nil, "250", model.TermKindBanned)

	for _, text := range []string{
		"our service is good",
		"paid by mastercard",
		"order 12500 shipped",
		"masterful",
	} {
		if r := check(t, svc, tSite, text); r.Decision != DecisionAllow {
			t.Fatalf("%q → %s/%v, want allow (substring is not a hit)", text, r.Decision, r.Matched)
		}
	}

	for _, text := range []string{
		"ice",
		"buy ice now",
		"ice, cheap",
		"the master here",
		"price is 250 yuan",
		"250",
	} {
		if r := check(t, svc, tSite, text); r.Decision != DecisionDeny {
			t.Fatalf("%q → %s, want deny (real hit at a boundary)", text, r.Decision)
		}
	}
}

func TestTermCJKKeepsSubstringSemantics(t *testing.T) {
	cleanTables(t)
	svc := NewTermService(testDB, nil)
	mkTerm(t, svc, nil, "第一次", model.TermKindBanned)

	if r := check(t, svc, tSite, "这是我第一次来"); r.Decision != DecisionDeny {
		t.Fatalf("CJK embedded → %s, want deny (no boundary demanded)", r.Decision)
	}
}

// A term whose ends are non-alphanumeric demands nothing on those sides, but a
// term ending in a letter still does: ".cn" must not fire inside ".cnn".
func TestTermMixedEndsBoundary(t *testing.T) {
	cleanTables(t)
	svc := NewTermService(testDB, nil)
	mkTerm(t, svc, nil, ".cn", model.TermKindBanned)

	if r := check(t, svc, tSite, "go to example.cn today"); r.Decision != DecisionDeny {
		t.Fatalf("example.cn → %s, want deny", r.Decision)
	}
	if r := check(t, svc, tSite, "watch example.cnn today"); r.Decision != DecisionAllow {
		t.Fatalf("example.cnn → %s, want allow", r.Decision)
	}
}

// The automaton reports a term once however often it occurs, so the boundary
// re-check must scan every occurrence: a bad one first must not mask a good one.
func TestTermBoundaryScansAllOccurrences(t *testing.T) {
	cleanTables(t)
	svc := NewTermService(testDB, nil)
	mkTerm(t, svc, nil, "ice", model.TermKindBanned)

	if r := check(t, svc, tSite, "service then ice"); r.Decision != DecisionDeny {
		t.Fatalf("service+ice → %s, want deny", r.Decision)
	}
	if r := check(t, svc, tSite, "service and price"); r.Decision != DecisionAllow {
		t.Fatalf("no real hit → %s, want allow", r.Decision)
	}
}
