package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/editing"
)

// newTestTransformer builds the real thing: the catalog.work spec is registered
// exactly as the service registers it, so every assertion below is made against
// the validators production enforces. The nil pool is safe — Validate closures
// never touch the database (only Apply and LoadSnapshot do).
func newTestTransformer(t *testing.T) *transformer {
	t.Helper()
	reg := editing.NewRegistry()
	if err := editspec.RegisterWork(reg, nil); err != nil {
		t.Fatalf("register catalog.work: %v", err)
	}
	ids := &idMaps{
		Work:   map[int64]int64{1: 1001},
		Label:  map[int64]int64{90: 501, 91: 502},
		Tag:    map[int64]int64{7: 701, 8: 702},
		Engine: map[int64]int64{5: 601},
	}
	tr, err := newTransformer(ids, reg)
	if err != nil {
		t.Fatalf("new transformer: %v", err)
	}
	return tr
}

// fullWikiSnapshot is a realistic wiki snapshot: all 26 keys, shapes copied
// from the live corpus (a jsonb_pretty of edit_revision.snapshot).
func fullWikiSnapshot() map[string]any {
	return map[string]any{
		wikiNameJaJP: "ちっちゃいお姉ちゃん", wikiNameEnUS: "", wikiNameZhCN: "小巧的姐姐", wikiNameZhTW: "小巧的姐姐",
		wikiIntroJaJP: "あらすじ", wikiIntroEnUS: "", wikiIntroZhCN: "简介", wikiIntroZhTW: "",
		wikiAliases:      []any{"alias one"},
		wikiContentLimit: "nsfw", wikiOriginalLanguage: "ja-jp", wikiAgeLimit: "r18",
		wikiTagIDs: []any{float64(7), float64(8)}, wikiOfficialIDs: []any{float64(90)},
		wikiEngineIDs: []any{float64(5)}, wikiSeriesID: nil,
		wikiLinks: []any{
			map[string]any{"link": "https://vndb.org/v64606", "name": "VNDB", "source": "vndb", "source_key": "vndb"},
		},
		wikiCovers: []any{
			map[string]any{"image_hash": "aa", "kind": "", "sexual": float64(0), "violence": float64(0), "sort_order": float64(0), "source": "", "source_key": ""},
		},
		wikiScreenshots: []any{
			map[string]any{"image_hash": "bb", "caption": "cap", "sexual": float64(1), "violence": float64(0), "sort_order": float64(0), "source": "", "source_key": ""},
		},
		wikiBanner: "https://t.vndb.org/cv/45/132745.jpg",
		wikiVNDBID: "v64606", wikiBID: float64(12345),
		wikiReleaseDate: "2024-12-27", wikiReleaseDateTBA: false, wikiReleasePrecision: "day",
		wikiStatus: float64(0),
	}
}

func TestSnapshotFoldsAndRetiresTheWholeVocabulary(t *testing.T) {
	tr := newTestTransformer(t)
	out, route := tr.document(fullWikiSnapshot(), modeSnapshot)

	// The nine catalog keys the corpus can reach, with their folded values.
	wantTitles := []any{
		map[string]any{"lang": "ja", "title": "ちっちゃいお姉ちゃん", "kind": float64(0)},
		map[string]any{"lang": "zh-Hans", "title": "小巧的姐姐", "kind": float64(0)},
		map[string]any{"lang": "zh-Hant", "title": "小巧的姐姐", "kind": float64(0)},
		// Aliases fold in as lang-less alias rows, after the officials, in
		// mirror step p's own order.
		map[string]any{"lang": "", "title": "alias one", "kind": float64(aliasesFoldKind)},
	}
	if got := out[editspec.FieldWorkTitles]; !reflect.DeepEqual(got, wantTitles) {
		t.Errorf("titles = %#v, want %#v (empty names dropped, mirror's language codes)", got, wantTitles)
	}
	wantIntros := []any{
		map[string]any{"lang": "ja", "intro": "あらすじ"},
		map[string]any{"lang": "zh-Hans", "intro": "简介"},
	}
	if got := out[editspec.FieldWorkIntros]; !reflect.DeepEqual(got, wantIntros) {
		t.Errorf("intros = %#v, want %#v", got, wantIntros)
	}
	if out[editspec.FieldWorkDisplayNSFW] != true {
		t.Errorf("display_nsfw = %v, want true (content_limit=nsfw)", out[editspec.FieldWorkDisplayNSFW])
	}
	if out[editspec.FieldWorkOLang] != "ja" {
		t.Errorf("olang = %v, want ja (ja-jp recoded to BCP-47)", out[editspec.FieldWorkOLang])
	}
	if out[editspec.FieldWorkContentRating] != float64(2) {
		t.Errorf("content_rating = %v, want 2 (r18)", out[editspec.FieldWorkContentRating])
	}
	if got := out[editspec.FieldWorkTagIDs]; !reflect.DeepEqual(got, []any{float64(701), float64(702)}) {
		t.Errorf("tag_ids = %#v, want the catalog ids", got)
	}
	wantLabels := []any{map[string]any{"label_id": float64(501), "kind": float64(labelEdgeKind)}}
	if got := out[editspec.FieldWorkLabels]; !reflect.DeepEqual(got, wantLabels) {
		t.Errorf("labels = %#v, want %#v (brand edges, the mirror's kind)", got, wantLabels)
	}
	if got := out[editspec.FieldWorkLinks]; !reflect.DeepEqual(got, []any{"https://vndb.org/v64606"}) {
		t.Errorf("links = %#v, want the bare canonical URL", got)
	}
	wantCovers := []any{map[string]any{"image_hash": "aa", "kind": "", "portrait_pinned": false, "sexual": float64(0), "violence": float64(0)}}
	if got := out[editspec.FieldWorkCovers]; !reflect.DeepEqual(got, wantCovers) {
		t.Errorf("covers = %#v, want %#v (source/source_key/sort_order dropped)", got, wantCovers)
	}

	// The nine retired keys survive verbatim under their wiki spelling.
	for _, key := range []string{
		wikiBanner, wikiVNDBID, wikiBID,
		wikiReleaseDate, wikiReleaseDateTBA, wikiReleasePrecision, wikiStatus, wikiSeriesID,
	} {
		if _, present := out[key]; !present {
			t.Errorf("retired key %q vanished — history must keep it", key)
		}
		if _, moved := route[key]; moved {
			t.Errorf("retired key %q was routed onto a catalog key", key)
		}
	}
	// Nothing invented: every output key is either a catalog key or a wiki key.
	for key := range out {
		if _, retired := retiredKeys[key]; retired {
			continue
		}
		if _, isTarget := map[string]bool{
			editspec.FieldWorkTitles: true, editspec.FieldWorkIntros: true, editspec.FieldWorkDisplayNSFW: true,
			editspec.FieldWorkOLang: true, editspec.FieldWorkContentRating: true, editspec.FieldWorkTagIDs: true,
			editspec.FieldWorkLabels: true, editspec.FieldWorkEngineIDs: true, editspec.FieldWorkLinks: true,
			editspec.FieldWorkCovers: true, editspec.FieldWorkScreenshots: true,
		}[key]; !isTarget {
			t.Errorf("unexpected output key %q", key)
		}
	}
}

func TestPatchRetiresFoldKeysButMapsOneToOneKeys(t *testing.T) {
	tr := newTestTransformer(t)
	patch := map[string]any{
		wikiIntroZhCN:    "a new synopsis",
		wikiNameZhCN:     "a new name",
		wikiContentLimit: "sfw",
		wikiTagIDs:       []any{float64(7)},
	}
	out, _ := tr.document(patch, modePatch)

	// The fold keys stay: a full-replace titles/intros value built from one
	// language would state something the proposer never proposed.
	if out[wikiIntroZhCN] != "a new synopsis" || out[wikiNameZhCN] != "a new name" {
		t.Errorf("fold keys must survive a patch verbatim, got %#v", out)
	}
	if _, folded := out[editspec.FieldWorkTitles]; folded {
		t.Error("a partial patch must not be folded into catalog.work.titles")
	}
	if _, folded := out[editspec.FieldWorkIntros]; folded {
		t.Error("a partial patch must not be folded into catalog.work.intros")
	}
	// The 1:1 keys map exactly as in a snapshot.
	if out[editspec.FieldWorkDisplayNSFW] != false {
		t.Errorf("display_nsfw = %v, want false", out[editspec.FieldWorkDisplayNSFW])
	}
	if got := out[editspec.FieldWorkTagIDs]; !reflect.DeepEqual(got, []any{float64(701)}) {
		t.Errorf("tag_ids = %#v", got)
	}
}

func TestUnmappableValuesKeepTheWikiKey(t *testing.T) {
	tr := newTestTransformer(t)
	cases := map[string]map[string]any{
		"tag outside the canonical vocabulary": {wikiTagIDs: []any{float64(7), float64(999)}},
		"official with no label":               {wikiOfficialIDs: []any{float64(999)}},
		"original_language others":             {wikiOriginalLanguage: "others"},
		"age_limit outside the verdict set":    {wikiAgeLimit: "r15"},
		"content_limit outside the axis":       {wikiContentLimit: "unknown"},
		"safety axis out of range":             {wikiCovers: []any{map[string]any{"image_hash": "aa", "sexual": float64(5)}}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, route := tr.document(in, modeSnapshot)
			for key, value := range in {
				if _, moved := route[key]; moved {
					t.Fatalf("%q must not map", key)
				}
				if !reflect.DeepEqual(out[key], value) {
					t.Fatalf("%q must survive verbatim, got %#v", key, out[key])
				}
			}
		})
	}
}

// An unmapped id must not produce a SHORTENED list: half a full-replace edge
// list is the silent data loss this rule exists to prevent.
func TestPartialIDListIsNeverWritten(t *testing.T) {
	tr := newTestTransformer(t)
	out, _ := tr.document(map[string]any{wikiTagIDs: []any{float64(7), float64(999)}}, modeSnapshot)
	if got, present := out[editspec.FieldWorkTagIDs]; present {
		t.Fatalf("catalog.work.tag_ids = %#v, want absent — one unmapped id retires the whole key", got)
	}
}

func TestLinksAreCanonicalizedAndDeduplicated(t *testing.T) {
	tr := newTestTransformer(t)
	out, _ := tr.document(map[string]any{wikiLinks: []any{
		map[string]any{"link": "https://vndb.org/v100", "name": "VNDB"},
		map[string]any{"link": "https://vndb.org/v100/", "name": "VNDB again"},
		map[string]any{"link": "https://example.com/x", "name": "official"},
	}}, modeSnapshot)
	got, _ := out[editspec.FieldWorkLinks].([]any)
	if len(got) != 2 {
		t.Fatalf("links = %#v, want two entries (the vndb pair folds onto one canonical URL)", got)
	}
	if got[0] != "https://vndb.org/v100" {
		t.Errorf("links[0] = %v, want the canonical vndb URL", got[0])
	}
}

func TestNullMediaBecomesTheEmptyList(t *testing.T) {
	tr := newTestTransformer(t)
	out, _ := tr.document(map[string]any{wikiCovers: nil, wikiScreenshots: nil}, modeSnapshot)
	for _, key := range []string{editspec.FieldWorkCovers, editspec.FieldWorkScreenshots} {
		got, ok := out[key].([]any)
		if !ok || len(got) != 0 {
			t.Errorf("%s = %#v, want []", key, out[key])
		}
	}
}

func TestTitlesWithNothingToFoldStayBehind(t *testing.T) {
	tr := newTestTransformer(t)
	in := map[string]any{wikiNameJaJP: "", wikiNameEnUS: "", wikiNameZhCN: " ", wikiNameZhTW: ""}
	out, route := tr.document(in, modeSnapshot)
	if _, folded := out[editspec.FieldWorkTitles]; folded {
		t.Fatal("titles requires an official title; an all-empty fold must not be written")
	}
	if len(route) != 0 || len(out) != 4 {
		t.Fatalf("the four name keys must survive, got %#v", out)
	}
}

func TestChangedFieldsFollowTheSnapshotRouting(t *testing.T) {
	tr := newTestTransformer(t)
	_, route := tr.document(fullWikiSnapshot(), modeSnapshot)
	got := rekeyChangedFields([]string{
		wikiNameJaJP, wikiNameZhCN, // both fold onto titles → one entry
		wikiReleaseDate, // retired → survives
		wikiTagIDs,      // mapped
	}, route)
	want := []string{editspec.FieldWorkTitles, wikiReleaseDate, editspec.FieldWorkTagIDs}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changed_fields = %#v, want %#v", got, want)
	}
}

func TestPatchDeltaRekeysBothHalves(t *testing.T) {
	tr := newTestTransformer(t)
	got := tr.patchDelta(map[string]any{
		"set":   map[string]any{wikiContentLimit: "sfw", wikiIntroZhCN: "body"},
		"unset": []any{wikiReleaseDate, wikiReleasePrecision, wikiTagIDs, wikiNameZhCN},
	})
	set := got["set"].(map[string]any)
	if set[editspec.FieldWorkDisplayNSFW] != false {
		t.Errorf("set half did not map content_limit: %#v", set)
	}
	if set[wikiIntroZhCN] != "body" {
		t.Errorf("set half must keep the fold key: %#v", set)
	}
	unset := got["unset"].([]any)
	want := []any{wikiNameZhCN, wikiReleaseDate, wikiReleasePrecision, editspec.FieldWorkTagIDs}
	if !reflect.DeepEqual(unset, want) {
		t.Errorf("unset = %#v, want %#v", unset, want)
	}
}

// Running the transform over its own output must change nothing. This is what
// makes a re-run of the migration safe even on a document that somehow got
// transformed twice.
func TestTransformIsIdempotent(t *testing.T) {
	tr := newTestTransformer(t)
	once, _ := tr.document(fullWikiSnapshot(), modeSnapshot)
	twice, _ := tr.document(once, modeSnapshot)
	a, _ := json.Marshal(once)
	b, _ := json.Marshal(twice)
	if string(a) != string(b) {
		t.Errorf("second pass changed the document:\n once: %s\ntwice: %s", a, b)
	}
}

// Every mapped value must be one the live field would accept — the property the
// migration relies on so that a revert of a migrated revision cannot 422.
func TestEveryMappedValuePassesTheLiveValidator(t *testing.T) {
	tr := newTestTransformer(t)
	out, route := tr.document(fullWikiSnapshot(), modeSnapshot)
	targets := map[string]bool{}
	for _, target := range route {
		targets[target] = true
	}
	if len(targets) < 9 {
		t.Fatalf("only %d catalog keys were produced, expected the full mapped set", len(targets))
	}
	for key := range targets {
		if err := tr.validate[key](out[key]); err != nil {
			t.Errorf("%s value does not validate: %v", key, err)
		}
	}
}

// The alias fold is the wave-161 correction to the first draft, which retired
// aliases in place. Retiring them meant a revert of a migrated revision wrote a
// titles value holding officials only — and applyTitles full-replaces kinds
// 0..2, so it would have deleted every alias row the work had. Folding them in
// (now that a lang-less alias is legal) makes the revert restore them instead.
func TestAliasesFoldIntoTitlesWithNoLanguage(t *testing.T) {
	tr := newTestTransformer(t)
	out, route := tr.document(map[string]any{
		wikiNameJaJP: "本名",
		wikiAliases:  []any{"first", "second", "first"},
	}, modeSnapshot)

	if route[wikiAliases] != editspec.FieldWorkTitles {
		t.Fatalf("aliases must route onto titles, got %q", route[wikiAliases])
	}
	if _, left := out[wikiAliases]; left {
		t.Error("a folded key must not also survive under its wiki spelling")
	}
	got := out[editspec.FieldWorkTitles].([]any)
	want := []any{
		map[string]any{"lang": "ja", "title": "本名", "kind": float64(0)},
		map[string]any{"lang": "", "title": "first", "kind": float64(1)},
		map[string]any{"lang": "", "title": "second", "kind": float64(1)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %#v, want %#v (duplicate alias folded away)", got, want)
	}
	if err := tr.validate[editspec.FieldWorkTitles](got); err != nil {
		t.Errorf("the folded value must pass the live validator: %v", err)
	}
}

// An alias-only work cannot be expressed: titles requires an official, and the
// migration will not fabricate one. Both keys stay in the wiki vocabulary.
func TestAliasOnlyWorkKeepsTheWikiKeys(t *testing.T) {
	tr := newTestTransformer(t)
	in := map[string]any{wikiNameJaJP: "", wikiAliases: []any{"only an alias"}}
	out, route := tr.document(in, modeSnapshot)
	if len(route) != 0 {
		t.Fatalf("nothing may be routed, got %v", route)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("both keys must survive verbatim, got %#v", out)
	}
}
