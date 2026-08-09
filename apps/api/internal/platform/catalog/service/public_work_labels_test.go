// public_work_labels_test.go — wave 200: one entry per company.
//
// The storage grain is (work, label, kind), so a studio that both developed and
// published a work is two rows. 56,438 works carried at least one company twice
// and every consumer printed it twice. This pins the collapse, and pins that
// the singular `kind` field stays the most identifying capacity so a consumer
// reading only that word is not told a brand is "publisher".
package service

import (
	"slices"
	"testing"

	"api/internal/platform/catalog/model"
)

func TestPublicWorkLabelsCollapseOneEntryPerCompany(t *testing.T) {
	rows := []LabelAttribution{
		// きゃべつそふと, developer AND publisher — vndb's own dual flag.
		{LabelID: 421, DisplayName: "きゃべつそふと", Kind: model.WorkLabelKindPublisher},
		{LabelID: 421, DisplayName: "きゃべつそふと", Kind: model.WorkLabelKindDeveloper},
		{LabelID: 763, DisplayName: "HuneX", Kind: model.WorkLabelKindPublisher},
	}
	out := publicWorkLabels(rows)
	if len(out) != 2 {
		t.Fatalf("labels = %d entries, want 2 (one per company)", len(out))
	}
	if out[0].ID != 421 || !slices.Equal(out[0].Kinds, []string{"developer", "publisher"}) {
		t.Fatalf("company 421 kinds = %v, want [developer publisher]", out[0].Kinds)
	}
	// developer outranks publisher: "who made it" identifies the work better.
	if out[0].Kind != "developer" {
		t.Fatalf("company 421 kind = %q, want developer", out[0].Kind)
	}
	if out[1].ID != 763 || !slices.Equal(out[1].Kinds, []string{"publisher"}) {
		t.Fatalf("company 763 = %+v, want the single publisher capacity", out[1])
	}
}

// A brand outranks everything: it is the name the game shipped under, and a
// work whose brand is also credited as developer must not read as "developer".
func TestPublicWorkLabelsPrimaryKindPrefersTheShippingName(t *testing.T) {
	out := publicWorkLabels([]LabelAttribution{
		{LabelID: 7, DisplayName: "Key", Kind: model.WorkLabelKindDeveloper},
		{LabelID: 7, DisplayName: "Key", Kind: model.WorkLabelKindBrand},
	})
	if len(out) != 1 || out[0].Kind != "brand" {
		t.Fatalf("out = %+v, want a single entry whose kind is brand", out)
	}
	if !slices.Equal(out[0].Kinds, []string{"brand", "developer"}) {
		t.Fatalf("kinds = %v, want both capacities kept", out[0].Kinds)
	}
}

// Kinds is never empty for an entry that exists — a consumer that renders only
// kinds[] must not silently drop a company.
func TestPublicWorkLabelsAlwaysCarryAtLeastOneKind(t *testing.T) {
	for _, out := range publicWorkLabels([]LabelAttribution{
		{LabelID: 1, DisplayName: "a", Kind: model.WorkLabelKindCircle},
		{LabelID: 2, DisplayName: "b", Kind: model.WorkLabelKindBrand},
	}) {
		if len(out.Kinds) == 0 {
			t.Fatalf("company %d has no kinds", out.ID)
		}
	}
}
