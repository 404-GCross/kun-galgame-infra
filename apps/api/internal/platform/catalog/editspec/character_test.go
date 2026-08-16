package editspec_test

import (
	"errors"
	"sort"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
	"api/internal/platform/provenance"
)

func newCharacterEngine(t *testing.T) *editing.Engine {
	t.Helper()
	e := newEngine(t)
	for _, table := range []string{
		"catalog_character_alias", "catalog_character_intro", "catalog_character",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	if err := editspec.RegisterCharacter(e.Registry(), testDB); err != nil {
		t.Fatalf("register catalog.character: %v", err)
	}
	return e
}

func createCharacter(t *testing.T, displayName string) *model.CatalogCharacter {
	t.Helper()
	c := &model.CatalogCharacter{DisplayName: displayName}
	if err := testDB.Create(c).Error; err != nil {
		t.Fatalf("create character: %v", err)
	}
	return c
}

func reloadCharacter(t *testing.T, id int64) model.CatalogCharacter {
	t.Helper()
	var c model.CatalogCharacter
	if err := testDB.First(&c, id).Error; err != nil {
		t.Fatalf("reload character %d: %v", id, err)
	}
	return c
}

func TestCharacterScalarEdits(t *testing.T) {
	e := newCharacterEngine(t)
	ch := createCharacter(t, "初期名")

	snap := mergeOn(t, e, editspec.TypeCharacter, ch.ID, map[string]any{
		editspec.FieldCharacterDisplayName: "本名",
		editspec.FieldCharacterLatin:       "Honmyou",
		editspec.FieldCharacterLang:        "ja",
		editspec.FieldCharacterDescription: "人手で書いた説明",
		editspec.FieldCharacterGender:      float64(model.GenderFemale),
		editspec.FieldCharacterHeightCm:    float64(158),
		editspec.FieldCharacterBloodType:   float64(model.BloodTypeAB),
		editspec.FieldCharacterCup:         "C",
	})
	if snap[editspec.FieldCharacterDisplayName] != "本名" {
		t.Fatalf("snapshot display_name = %#v", snap[editspec.FieldCharacterDisplayName])
	}

	got := reloadCharacter(t, ch.ID)
	if got.DisplayName != "本名" || got.Latin == nil || *got.Latin != "Honmyou" ||
		got.Description != "人手で書いた説明" || got.Gender == nil || *got.Gender != model.GenderFemale ||
		got.HeightCm == nil || *got.HeightCm != 158 || got.Cup == nil || *got.Cup != "C" {
		t.Fatalf("character after merge: %+v", got)
	}

	prov := reloadCharacter(t, ch.ID).FieldProvenance
	for _, column := range []string{
		"display_name", "latin", "lang", "description", "gender", "height_cm", "blood_type", "cup",
	} {
		if head := provenance.FirstSource(prov, column); head != provenance.SourceCurated {
			t.Errorf("catalog_character.field_provenance[%s] head = %q, want %q",
				column, head, provenance.SourceCurated)
		}
	}

	// A null must reach the column as NULL: "the editor cleared this attribute"
	// and "the editor did not touch it" are different edits.
	mergeOn(t, e, editspec.TypeCharacter, ch.ID, map[string]any{
		editspec.FieldCharacterHeightCm: nil,
		editspec.FieldCharacterLatin:    nil,
	})
	cleared := reloadCharacter(t, ch.ID)
	if cleared.HeightCm != nil || cleared.Latin != nil {
		t.Fatalf("clearing a nullable scalar left %+v", cleared)
	}
}

func TestCharacterScalarValidators(t *testing.T) {
	e := newCharacterEngine(t)
	ch := createCharacter(t, "validator target")
	editor := realActor(100, "admin")

	var valErr *editing.ValidationError
	cases := []map[string]any{
		{editspec.FieldCharacterDisplayName: ""},
		{editspec.FieldCharacterDisplayName: "   "},
		{editspec.FieldCharacterDisplayName: nil},
		{editspec.FieldCharacterLang: "xx"},
		{editspec.FieldCharacterGender: float64(9)},
		{editspec.FieldCharacterGender: "female"},
		{editspec.FieldCharacterBloodType: float64(5)},
		{editspec.FieldCharacterBirthdayMonth: float64(13)},
		{editspec.FieldCharacterBirthdayDay: float64(0)},
		{editspec.FieldCharacterHeightCm: 158.5},
		{editspec.FieldCharacterCup: "much too long"},
	}
	for i, patch := range cases {
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeCharacter, EntityID: ch.ID, Patch: patch, Actor: editor,
		}); !errors.As(err, &valErr) {
			t.Errorf("case %d (%v): want ValidationError, got %v", i, patch, err)
		}
	}

	if err := testDB.Delete(&model.CatalogCharacter{}, ch.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeCharacter, EntityID: ch.ID,
		Patch: map[string]any{editspec.FieldCharacterDisplayName: "ghost"}, Actor: editor,
	}); !errors.Is(err, editing.ErrEntityNotFound) {
		t.Fatalf("soft-deleted character: %v", err)
	}
}

func TestCharacterIntrosAreTheCuratedLane(t *testing.T) {
	e := newCharacterEngine(t)
	ch := createCharacter(t, "紹介文の主")

	upstream := model.CatalogCharacterIntro{
		CharacterID: ch.ID, Lang: "ja", Intro: "バンガミの紹介",
		SourceID: 3, Provenance: model.IntroProvenanceSource,
	}
	if err := testDB.Create(&upstream).Error; err != nil {
		t.Fatal(err)
	}

	intros := []any{map[string]any{"lang": "ja", "intro": "人手の紹介"}}
	snap := mergeOn(t, e, editspec.TypeCharacter, ch.ID,
		map[string]any{editspec.FieldCharacterIntros: intros})
	sameJSON(t, "character intros", snap[editspec.FieldCharacterIntros], intros)

	// The upstream row is its importer's and stays put; the curated row is a
	// second physical row in the same lang, and the read-path fold picks it.
	var rows []model.CatalogCharacterIntro
	if err := testDB.Where("character_id = ?", ch.ID).Order("source_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("character intro rows = %+v, want the upstream row kept alongside the curated one", rows)
	}
}

func TestCharacterSiteOverlays(t *testing.T) {
	reg := editing.NewRegistry()
	if err := editspec.RegisterCharacter(reg, testDB); err != nil {
		t.Fatalf("register: %v", err)
	}
	spec, ok := reg.Type(editspec.TypeCharacter)
	if !ok {
		t.Fatal("catalog.character is not registered")
	}
	for i := range spec.Fields {
		key := spec.Fields[i].Key
		kungal := spec.EffectivePolicy(key, "kungal")
		if kungal.Propose != editing.ProposeOpen || kungal.Automerge != editing.AutomergeNever {
			t.Fatalf("kungal policy for %s = %+v, want {open, …, never}", key, kungal)
		}
		for _, site := range []string{"letmoe", "letmoe-staging", "letmoe-dev"} {
			letmoe := spec.EffectivePolicy(key, site)
			if letmoe.Propose != editing.ProposeTrusted || letmoe.Automerge != editing.AutomergeOwner {
				t.Fatalf("%s policy for %s = %+v, want {trusted, …, owner}", site, key, letmoe)
			}
		}
		nextmoe := spec.EffectivePolicy(key, "nextmoe")
		if nextmoe.Propose != editing.ProposePerm("edit.catalog.character") ||
			nextmoe.Automerge != editing.AutomergeNever {
			t.Fatalf("default policy for %s = %+v", key, nextmoe)
		}
	}
}

// A kungal reviewer holding edit.catalog.character.review still has to merge a
// proposal by hand: a character fans out to every work it appears in, so it is
// on the taxonomy side of the automerge line, not the work side.
func TestKungalCharacterNeverAutomerges(t *testing.T) {
	e := newCharacterEngine(t)
	ch := createCharacter(t, "共有キャラ")

	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeCharacter, EntityID: ch.ID,
		Patch: map[string]any{editspec.FieldCharacterDisplayName: "レビュー権付き改名"},
		Actor: kungalActor(102, "admin"),
	})
	if err != nil {
		t.Fatalf("admin propose on kungal: %v", err)
	}
	if rev != nil {
		t.Fatal("kungal catalog.character must be automerge=never even for a review-perm holder")
	}

	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeCharacter, EntityID: ch.ID,
		Patch: map[string]any{editspec.FieldCharacterCup: "D"},
		Actor: kungalActor(101),
	}); err != nil {
		t.Fatalf("plain kungal user propose: %v", err)
	}

	var permErr *editing.PermissionError
	if _, err := e.MergeProposal(testCtx, prop.ID, kungalActor(103, "moderator"), ""); !errors.As(err, &permErr) {
		t.Fatalf("moderator merge: %v, want PermissionError", err)
	}
	if _, err := e.MergeProposal(testCtx, prop.ID, kungalActor(102, "admin"), "ok"); err != nil {
		t.Fatalf("admin merge: %v", err)
	}
	if got := reloadCharacter(t, ch.ID).DisplayName; got != "レビュー権付き改名" {
		t.Fatalf("display_name after merge = %q", got)
	}
}

func TestLetmoeCharacterProposeIsTrusted(t *testing.T) {
	e := newCharacterEngine(t)
	ch := createCharacter(t, "letmoe キャラ")

	untrusted := editing.PolicyContext{
		UserID: 400, Site: "letmoe", TrustTier: 0, HasPerm: func(string) bool { return false },
	}
	var permErr *editing.PermissionError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeCharacter, EntityID: ch.ID,
		Patch: map[string]any{editspec.FieldCharacterDisplayName: "x"}, Actor: untrusted,
	}); !errors.As(err, &permErr) {
		t.Fatalf("untrusted letmoe propose: %v, want PermissionError", err)
	}

	trusted := untrusted
	trusted.TrustTier = editing.TrustedTier
	_, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeCharacter, EntityID: ch.ID,
		Patch: map[string]any{editspec.FieldCharacterDisplayName: "信頼層の提案"}, Actor: trusted,
	})
	if err != nil {
		t.Fatalf("trusted letmoe propose: %v", err)
	}
	// automerge=owner with no owner hook value means never — a character belongs
	// to no site, so the letmoe overlay's shape matches work without granting it.
	if rev != nil {
		t.Fatal("a character has no owning site, so automerge=owner must never fire")
	}
}

// The two ids the SQL folds order on must be exactly the two source keys
// provenance calls human; anything else silently promotes or demotes a lane.
func TestHumanSourceIDsAreTheHumanProvenanceKeys(t *testing.T) {
	var keys []string
	if err := testDB.Raw(`SELECT key FROM catalog_source WHERE id IN ? ORDER BY key`,
		editspec.HumanSourceIDs()).Scan(&keys).Error; err != nil {
		t.Fatalf("load source keys: %v", err)
	}
	want := append([]string(nil), provenance.HumanSources()...)
	sort.Strings(want)
	if len(keys) != len(want) {
		t.Fatalf("catalog_source keys for %v = %v, want %v", editspec.HumanSourceIDs(), keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("source key %d = %q, want %q", i, keys[i], want[i])
		}
	}
}
