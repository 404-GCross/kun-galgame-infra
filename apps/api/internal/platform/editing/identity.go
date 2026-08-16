package editing

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

type IdentityRef struct {
	Segment    int
	EntityTag  string
	ZeroAbsent bool
}

type IdentitySpec struct {
	Segments     int
	TrailingText bool
	Refs         []IdentityRef
	KeyCheck     func(key string) error
}

type Stmt struct {
	SQL  string
	Args []any
}

func validateIdentitySpec(spec IdentitySpec) error {
	if spec.Segments < 2 {
		return fmt.Errorf("needs at least 2 segments, has %d", spec.Segments)
	}
	if spec.KeyCheck == nil {
		return fmt.Errorf("needs a KeyCheck: without one a client that builds a key the field's own helper would not have built stores a suppression that silently matches nothing")
	}
	if spec.TrailingText && len(spec.Refs) > 0 {
		return fmt.Errorf("a key with entity refs must not carry a free-text segment: the follow statements rebuild the key with split_part, and textnorm.Clean leaves colons in place")
	}
	seen := make(map[int]struct{}, len(spec.Refs))
	for _, ref := range spec.Refs {
		if ref.Segment < 2 || ref.Segment > spec.Segments {
			return fmt.Errorf("ref segment %d is outside [2, %d]", ref.Segment, spec.Segments)
		}
		if ref.EntityTag == "" {
			return fmt.Errorf("ref segment %d declares no EntityTag", ref.Segment)
		}
		if _, dup := seen[ref.Segment]; dup {
			return fmt.Errorf("ref segment %d is declared twice", ref.Segment)
		}
		seen[ref.Segment] = struct{}{}
	}
	return nil
}

// IdentityFollowStmts walks the registry and returns, for every field whose
// identity key carries a segment tagged entityTag, the pair of statements that
// moves src's suppressions onto dst and drops the ones that could not move
// because dst already carries that key.
//
// It deliberately does not filter on entity_id: merging one credit_name rewrites
// credit rows spread over many works, and those works are not themselves part of
// the merge. That makes it a different shape from suppressedRowStmts, which
// moves a merged entity's OWN rows to the survivor. scopeEntityIDs narrows it
// back down for callers that only moved part of an entity's rows.
func (r *Registry) IdentityFollowStmts(entityTag string, src, dst int64, scopeEntityIDs []int64) []Stmt {
	types := make([]string, 0, len(r.types))
	for t := range r.types {
		types = append(types, t)
	}
	sort.Strings(types)

	var out []Stmt
	for _, t := range types {
		spec := r.types[t]
		for i := range spec.Fields {
			f := &spec.Fields[i]
			if f.Identity == nil {
				continue
			}
			for _, ref := range f.Identity.Refs {
				if ref.EntityTag != entityTag {
					continue
				}
				out = append(out, identityFollowPair(spec.Type, f.Key, *f.Identity, ref, src, dst, scopeEntityIDs)...)
			}
		}
	}
	return out
}

type KeyMove struct {
	EntityID int64
	From, To string
}

// RekeySuppressed is for the jobs that legitimately rewrite a frozen-vocabulary
// scalar segment of a row identity (pillar 7 (iii)). Such a job knows exactly
// which rows it moved and where to, which the tag-driven IdentityFollowStmts
// cannot express: there is no catalog_redirect for "this credit was
// reclassified from other-staff to composer", and the rewrite is a subset of
// the field's rows rather than every row carrying an id.
//
// Every To passes the field's own KeyCheck — the same gate the write face
// applies — and a To that already exists on that entity drops the source row
// instead of raising 23505, matching identityFollowPair's dedup semantics.
func (r *Registry) RekeySuppressed(ctx context.Context, tx *gorm.DB,
	entityType, fieldKey string, moves []KeyMove) (moved, dropped int, err error) {
	spec, ok := r.types[entityType]
	if !ok {
		return 0, 0, fmt.Errorf("editing: type %q is not registered", entityType)
	}
	f, ok := spec.fields[fieldKey]
	if !ok {
		return 0, 0, fmt.Errorf("editing: type %q has no field %q", entityType, fieldKey)
	}
	if f.Identity == nil {
		return 0, 0, fmt.Errorf("editing: field %q declares no Identity", fieldKey)
	}
	for i, m := range moves {
		if err := f.Identity.KeyCheck(m.To); err != nil {
			return moved, dropped, fmt.Errorf("editing: move %d: target key: %w", i, err)
		}
	}
	for _, m := range moves {
		if m.From == m.To {
			continue
		}
		res := tx.WithContext(ctx).Model(&SuppressedRow{}).
			Where("entity_type = ? AND entity_id = ? AND field_key = ? AND identity_key = ?",
				entityType, m.EntityID, fieldKey, m.From).
			Where(`NOT EXISTS (SELECT 1 FROM edit_suppressed_row x
			                    WHERE x.entity_type = ? AND x.entity_id = ?
			                      AND x.field_key = ? AND x.identity_key = ?)`,
				entityType, m.EntityID, fieldKey, m.To).
			Update("identity_key", m.To)
		if res.Error != nil {
			return moved, dropped, res.Error
		}
		if res.RowsAffected > 0 {
			moved += int(res.RowsAffected)
			continue
		}
		del := tx.WithContext(ctx).
			Where("entity_type = ? AND entity_id = ? AND field_key = ? AND identity_key = ?",
				entityType, m.EntityID, fieldKey, m.From).
			Delete(&SuppressedRow{})
		if del.Error != nil {
			return moved, dropped, del.Error
		}
		dropped += int(del.RowsAffected)
	}
	return moved, dropped, nil
}

func identityFollowPair(entityType, fieldKey string, spec IdentitySpec, ref IdentityRef,
	src, dst int64, scope []int64) []Stmt {
	seg := func(alias string) string {
		return fmt.Sprintf("split_part(%s.identity_key, ':', %d)", alias, ref.Segment)
	}
	rebuilt := func() string {
		parts := make([]string, 0, spec.Segments)
		for i := 1; i <= spec.Segments; i++ {
			if i == ref.Segment {
				parts = append(parts, "?::text")
				continue
			}
			parts = append(parts, fmt.Sprintf("split_part(s.identity_key, ':', %d)", i))
		}
		return strings.Join(parts, " || ':' || ")
	}
	srcText, dstText := strconv.FormatInt(src, 10), strconv.FormatInt(dst, 10)

	var move strings.Builder
	moveArgs := []any{dstText, entityType, fieldKey, srcText}
	fmt.Fprintf(&move, "UPDATE edit_suppressed_row s SET identity_key = %s\n", rebuilt())
	fmt.Fprintf(&move, " WHERE s.entity_type = ? AND s.field_key = ? AND %s = ?", seg("s"))
	if ref.ZeroAbsent {
		// The engine cannot assume the caller's ids start at 1, so "0 is the
		// sentinel, never an id" is stated here rather than inferred.
		fmt.Fprintf(&move, "\n   AND %s <> '0'", seg("s"))
	}
	if len(scope) > 0 {
		move.WriteString("\n   AND s.entity_id IN ?")
		moveArgs = append(moveArgs, scope)
	}
	fmt.Fprintf(&move, `
   AND NOT EXISTS (SELECT 1 FROM edit_suppressed_row x
                    WHERE x.entity_type = s.entity_type AND x.entity_id = s.entity_id
                      AND x.field_key = s.field_key AND x.identity_key = %s)`, rebuilt())
	moveArgs = append(moveArgs, dstText)

	var drop strings.Builder
	dropArgs := []any{entityType, fieldKey, srcText}
	fmt.Fprintf(&drop, "DELETE FROM edit_suppressed_row s\n WHERE s.entity_type = ? AND s.field_key = ? AND %s = ?", seg("s"))
	if ref.ZeroAbsent {
		fmt.Fprintf(&drop, "\n   AND %s <> '0'", seg("s"))
	}
	if len(scope) > 0 {
		drop.WriteString("\n   AND s.entity_id IN ?")
		dropArgs = append(dropArgs, scope)
	}

	return []Stmt{{SQL: move.String(), Args: moveArgs}, {SQL: drop.String(), Args: dropArgs}}
}
