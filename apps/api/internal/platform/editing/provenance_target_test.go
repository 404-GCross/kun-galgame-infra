package editing_test

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/editing"

	"gorm.io/gorm"
)

func stampingField(key string, targets ...editing.ProvenanceTarget) editing.FieldSpec {
	return editing.FieldSpec{
		Key: key, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
		Provenance: targets,
		Validate:   func(any) error { return nil },
		Apply:      func(context.Context, *gorm.DB, int64, any) error { return nil },
	}
}

// TestHeadTableProvenanceTargetsAreSingleTable pins the registration-time rule
// that makes a child-table stamp impossible to get wrong by omission. A target
// with no Rows resolver stamps `WHERE id = <entity id>`, so it is only correct
// on the table whose primary key IS the entity id; a second table there is a
// child table being addressed by its parent's id, which raises no error and
// stamps somebody else's row.
func TestHeadTableProvenanceTargetsAreSingleTable(t *testing.T) {
	register := func(fields ...editing.FieldSpec) error {
		spec := widgetSpec(testDB)
		spec.Fields = append(spec.Fields, fields...)
		return editing.NewRegistry().Register(spec)
	}

	if err := register(
		stampingField("test.widget.head_a", editing.ProvenanceTarget{Table: "widget", Column: "name"}),
		stampingField("test.widget.head_b", editing.ProvenanceTarget{Table: "widget", Column: "olang"}),
	); err != nil {
		t.Fatalf("two targets on the one head table must register: %v", err)
	}

	err := register(
		stampingField("test.widget.head_a", editing.ProvenanceTarget{Table: "widget", Column: "name"}),
		stampingField("test.widget.child", editing.ProvenanceTarget{Table: "widget_part", Column: "kind"}),
	)
	if err == nil {
		t.Fatal("a second table with no Rows resolver must fail registration, not stamp by the parent's id")
	}
	if !strings.Contains(err.Error(), "widget_part") || !strings.Contains(err.Error(), "Rows resolver") {
		t.Fatalf("the error must name the offending table and the way out: %v", err)
	}

	if err := register(
		stampingField("test.widget.head_a", editing.ProvenanceTarget{Table: "widget", Column: "name"}),
		stampingField("test.widget.child", editing.ProvenanceTarget{
			Table: "widget_part", Column: "kind",
			Rows: func(context.Context, *gorm.DB, int64, any) ([]int64, error) { return nil, nil },
		}),
	); err != nil {
		t.Fatalf("a child table that declares Rows must register: %v", err)
	}
}
