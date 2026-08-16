package editing_test

import (
	"context"
	"errors"
	"testing"

	"api/internal/platform/editing"

	"gorm.io/gorm"
)

func TestParseSuppressedKeys(t *testing.T) {
	ok, err := editing.ParseSuppressedKeys([]any{"a", "b", "c"})
	if err != nil || len(ok) != 3 || ok[0] != "a" || ok[2] != "c" {
		t.Fatalf("parse: %v %v", ok, err)
	}
	if got, err := editing.ParseSuppressedKeys([]any{}); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v %v", got, err)
	}
	bad := []any{
		"not-a-list",
		[]any{1.0},
		[]any{""},
		[]any{"b", "a"},
		[]any{"a", "a"},
		map[string]any{"a": true},
	}
	for i, v := range bad {
		if _, err := editing.ParseSuppressedKeys(v); err == nil {
			t.Fatalf("case %d (%#v) was accepted", i, v)
		}
	}
	long := make([]any, 0, 201)
	for i := 0; i < 201; i++ {
		long = append(long, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if _, err := editing.ParseSuppressedKeys(long); err == nil {
		t.Fatal("201 keys were accepted")
	}
}

func TestSuppressedFieldSpecShape(t *testing.T) {
	parentPolicy := editing.Policy{
		Propose:   editing.ProposeTrusted,
		Review:    editing.ReviewPerm("edit.test.widget.review"),
		Automerge: editing.AutomergeOwner,
	}
	parent := editing.FieldSpec{Key: "test.widget.rows", Kind: editing.KindList, Policy: &parentPolicy}
	spec := editing.SuppressedFieldSpec("test.widget", parent)
	if spec.Key != "test.widget.rows.suppressed" {
		t.Fatalf("key = %q", spec.Key)
	}
	if spec.Key != editing.SuppressedFieldKey(parent.Key) {
		t.Fatalf("key does not match SuppressedFieldKey: %q", spec.Key)
	}
	if spec.Policy == nil || *spec.Policy != parentPolicy {
		t.Fatalf("policy = %+v, want the parent's", spec.Policy)
	}
	if spec.Kind != editing.KindList || spec.DiffHint != editing.DiffHintItems {
		t.Fatalf("kind/diff hint = %q/%q", spec.Kind, spec.DiffHint)
	}
	if spec.Validate == nil || spec.Apply == nil {
		t.Fatal("the companion must carry Validate and Apply")
	}
}

func TestReconcileSuppressedRoundTrip(t *testing.T) {
	cleanTables(t)
	const entityType, fieldKey = "test.widget", "test.widget.rows"

	if err := editing.ReconcileSuppressed(testCtx, testDB, entityType, 1, fieldKey, []string{"b", "a"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := editing.ReconcileSuppressed(testCtx, testDB, entityType, 2, fieldKey, []string{"a"}); err != nil {
		t.Fatalf("reconcile other entity: %v", err)
	}
	keys, err := editing.LoadSuppressedKeys(testCtx, testDB, entityType, 1, fieldKey)
	if err != nil || len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("load = %v err=%v (must come back sorted)", keys, err)
	}

	if err := editing.ReconcileSuppressed(testCtx, testDB, entityType, 1, fieldKey, []string{"a", "c"}); err != nil {
		t.Fatalf("reconcile again: %v", err)
	}
	keys, err = editing.LoadSuppressedKeys(testCtx, testDB, entityType, 1, fieldKey)
	if err != nil || len(keys) != 2 || keys[0] != "a" || keys[1] != "c" {
		t.Fatalf("after re-reconcile = %v err=%v", keys, err)
	}

	byEntity, err := editing.SuppressedKeysFor(testCtx, testDB, entityType, fieldKey, []int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(byEntity) != 2 || len(byEntity[1]) != 2 || len(byEntity[2]) != 1 {
		t.Fatalf("batch = %v", byEntity)
	}
	if empty, err := editing.SuppressedKeysFor(testCtx, testDB, entityType, fieldKey, nil); err != nil || len(empty) != 0 {
		t.Fatalf("an empty id list must not mean every entity: %v %v", empty, err)
	}
	all, err := editing.AllSuppressedKeys(testCtx, testDB, entityType, fieldKey)
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %v err=%v", all, err)
	}
	other, err := editing.AllSuppressedKeys(testCtx, testDB, entityType, "test.widget.other")
	if err != nil || len(other) != 0 {
		t.Fatalf("field keys must not bleed: %v %v", other, err)
	}

	if err := editing.ReconcileSuppressed(testCtx, testDB, entityType, 1, fieldKey, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	keys, err = editing.LoadSuppressedKeys(testCtx, testDB, entityType, 1, fieldKey)
	if err != nil || len(keys) != 0 {
		t.Fatalf("after clear = %v err=%v", keys, err)
	}
	value, err := editing.LoadSuppressedValue(testCtx, testDB, entityType, 1, fieldKey)
	if err != nil || value == nil || len(value) != 0 {
		t.Fatalf("empty snapshot value must be a non-nil empty list: %#v err=%v", value, err)
	}
	keys, err = editing.LoadSuppressedKeys(testCtx, testDB, entityType, 2, fieldKey)
	if err != nil || len(keys) != 1 {
		t.Fatalf("clearing entity 1 must not touch entity 2: %v err=%v", keys, err)
	}
}

const (
	fRows      = "test.widget.rows"
	fRowsSuppr = fRows + editing.SuppressedFieldSuffix
)

func widgetSuppressionEngine(t *testing.T) *editing.Engine {
	t.Helper()
	cleanTables(t)
	spec := widgetSpec(testDB)
	parent := editing.FieldSpec{
		Key: fRows, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
		Validate: func(any) error { return nil },
		Apply:    func(context.Context, *gorm.DB, int64, any) error { return nil },
	}
	spec.Fields = append(spec.Fields, parent, editing.SuppressedFieldSpec(spec.Type, parent))
	base := spec.LoadSnapshot
	entityType := spec.Type
	spec.LoadSnapshot = func(ctx context.Context, id int64) (map[string]any, error) {
		snap, err := base(ctx, id)
		if err != nil {
			return nil, err
		}
		suppressed, err := editing.LoadSuppressedValue(ctx, testDB, entityType, id, fRows)
		if err != nil {
			return nil, err
		}
		snap[fRows] = []any{}
		snap[fRowsSuppr] = suppressed
		return snap, nil
	}
	reg := editing.NewRegistry()
	if err := reg.Register(spec); err != nil {
		t.Fatalf("register widget spec with a suppressible list: %v", err)
	}
	return editing.NewEngine(testDB, reg)
}

func TestSuppressionIsAnOrdinaryEditOnAnyEntityType(t *testing.T) {
	e := widgetSuppressionEngine(t)
	createWidget(t, 1)

	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fRowsSuppr: []any{"row:1", "row:2"}},
		Note:  "two bad upstream rows", Actor: editorActor(11),
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if rev != nil {
		t.Fatal("the companion inherits automerge=never from the default policy")
	}
	merged, err := e.MergeProposal(testCtx, prop.ID, reviewerActor(22), "ok")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ActorUID != 11 {
		t.Fatalf("revision actor = %d, want the proposer", merged.ActorUID)
	}
	keys, err := editing.LoadSuppressedKeys(testCtx, testDB, "test.widget", 1, fRows)
	if err != nil || len(keys) != 2 {
		t.Fatalf("stored keys = %v err=%v", keys, err)
	}

	noop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fRowsSuppr: []any{"row:1", "row:2"}}, Actor: editorActor(11),
	})
	if err != nil {
		t.Fatalf("propose the same set again: %v", err)
	}
	if _, err := e.MergeProposal(testCtx, noop.ID, reviewerActor(22), ""); !errors.Is(err, editing.ErrNoEffectiveChanges) {
		t.Fatalf("merging the same set: %v, want ErrNoEffectiveChanges", err)
	}

	unsup, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fRowsSuppr: []any{"row:1"}}, Actor: editorActor(11),
	})
	if err != nil {
		t.Fatalf("propose release: %v", err)
	}
	if _, err := e.MergeProposal(testCtx, unsup.ID, reviewerActor(22), ""); err != nil {
		t.Fatalf("merge release: %v", err)
	}

	diffs, err := e.Diff(testCtx, "test.widget", 1, 1, 2)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Key != fRowsSuppr || diffs[0].DiffHint != editing.DiffHintItems {
		t.Fatalf("diff = %+v", diffs)
	}

	if _, _, err := e.Revert(testCtx, editing.RevertInput{
		EntityType: "test.widget", EntityID: 1, ToSeq: 1, Actor: reviewerActor(22),
	}); err != nil {
		t.Fatalf("revert: %v", err)
	}
	keys, err = editing.LoadSuppressedKeys(testCtx, testDB, "test.widget", 1, fRows)
	if err != nil || len(keys) != 2 {
		t.Fatalf("after revert = %v err=%v", keys, err)
	}
}
