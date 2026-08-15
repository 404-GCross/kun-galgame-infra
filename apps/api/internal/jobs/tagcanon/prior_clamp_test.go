package tagcanon

import (
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/model"
)

func TestPairKeyUnordered(t *testing.T) {
	a := pairKey("bangumi", "母系", "vndb", "Matriarchy")
	b := pairKey("vndb", "Matriarchy", "bangumi", "母系")
	if a != b {
		t.Fatalf("pairKey is order-dependent: %q vs %q", a, b)
	}
	if a == pairKey("bangumi", "母系", "vndb", "Motherhood") {
		t.Fatalf("distinct pairs collide")
	}
}

func TestLoadPriorPairKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	recs := []pairRec{
		{Kind: "pair", ASource: "bangumi", AName: "百合", BSource: "vndb", BName: "Yuri", Relation: "exact", Confidence: 0.95},
		{Kind: "single", Source: "bangumi", Name: "幼驯染", Usage: 380, Tier: i16p(model.TagTierCore), Kind_: i16p(model.TagKindContent)},
	}
	if err := writeRecords(path, recs); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	keys, names, err := loadPriorKeys(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("want exactly the pair key, got %d keys", len(keys))
	}
	if _, ok := keys[pairKey("vndb", "Yuri", "bangumi", "百合")]; !ok {
		t.Fatalf("pair not matched order-independently")
	}
	if _, ok := keys[pairKey("bangumi", "幼驯染", "bangumi", "幼驯染")]; ok {
		t.Fatalf("single leaked into the pair skip set")
	}
	if len(names) != 1 {
		t.Fatalf("want the judged single name, got %d", len(names))
	}
	if _, ok := names[nameKey("bangumi", "幼驯染")]; !ok {
		t.Fatalf("a judged single must not be classified again")
	}

	if _, _, err := loadPriorKeys(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatalf("missing prior file must be a hard error")
	}
}

func TestSingleCoreUsageFloorClamp(t *testing.T) {
	keyToID := map[string]int16{sourceKeyBangumi: 3}
	recs := []pairRec{
		{Kind: "single", Source: "bangumi", Name: "幼驯染", Usage: 380, Approve: true,
			Tier: i16p(model.TagTierCore), Kind_: i16p(model.TagKindContent)},
		{Kind: "single", Source: "bangumi", Name: "校园", Usage: 1500, Approve: true,
			Tier: i16p(model.TagTierCore), Kind_: i16p(model.TagKindContent)},
		{Kind: "single", Source: "bangumi", Name: "重口", Usage: 5000, Approve: true,
			Tier: i16p(model.TagTierLongtail), Kind_: i16p(model.TagKindContent)},
	}
	rows := singlesFromRecords(recs, keyToID, map[string]struct{}{})
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	got := map[string]int16{}
	for _, r := range rows {
		got[r.group.CanonicalName] = r.group.Tier
	}
	if got["幼驯染"] != model.TagTierLongtail {
		t.Fatalf("core below floor not clamped: tier=%d", got["幼驯染"])
	}
	if got["校园"] != model.TagTierCore {
		t.Fatalf("core at/above floor must stick: tier=%d", got["校园"])
	}
	if got["重口"] != model.TagTierLongtail {
		t.Fatalf("clamp must never promote: tier=%d", got["重口"])
	}
}
