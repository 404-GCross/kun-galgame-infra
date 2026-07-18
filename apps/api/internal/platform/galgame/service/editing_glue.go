package service

import (
	"encoding/json"
	"fmt"
	"sort"

	"api/internal/platform/authz"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/pkg/errors"
)

// This file is the service-side glue between the galgame write paths and the
// editing engine (E2a strangler, doc 21 §1.6): create / update / submit /
// patch-draft / admin status-change keep their validation + side-effect logic
// but persist every revision through the engine (single write path). Each
// caller asserts the PolicyContext its route authorization already established:
// trust tier 2 for direct-edit routes (automerge=trusted fires), tier 0 for the
// submission/proposal route, permission keys granted exactly where the checks
// passed. (The old-wire PR/revision adapter this glue once fed retired at E3b.)

// editActor builds the engine PolicyContext for a galgame write caller.
func editActor(userID int, tier int16, perms ...authz.Permission) editing.PolicyContext {
	set := make(map[string]bool, len(perms))
	for _, p := range perms {
		set[string(p)] = true
	}
	return editing.PolicyContext{
		UserID:    int64(userID),
		Site:      editspec.SiteGalgameWiki,
		TrustTier: tier,
		HasPerm:   func(key string) bool { return set[key] },
	}
}

// rekeyedPatch lifts the changed subset of an old-shape snapshot into an
// engine patch: eternal field keys → decoded-JSON values. Values go through
// a marshal/unmarshal round trip so numbers arrive as float64 — the same
// form the engine reads back from stored JSONB. The set-valued fields are
// canonical-sorted first (TakeSnapshot's discipline) so stored engine
// snapshots keep the old machine's canonical set order — overlay/PR inputs
// arrive in user order, and without this an order-only difference would leak
// into the log as a value change.
func rekeyedPatch(snap *model.Snapshot, changed map[string]bool) (map[string]any, error) {
	c := *snap // shallow copy; the sorts below replace slices wholesale
	c.Aliases = append(make([]string, 0, len(snap.Aliases)), snap.Aliases...)
	sort.Strings(c.Aliases)
	for _, ids := range []*[]int{&c.TagIDs, &c.OfficialIDs, &c.EngineIDs} {
		sorted := append(make([]int, 0, len(*ids)), *ids...)
		sort.Ints(sorted)
		*ids = sorted
	}
	c.Covers = append(make([]model.SnapshotCover, 0, len(snap.Covers)), snap.Covers...)
	sort.Slice(c.Covers, func(i, j int) bool { return c.Covers[i].ImageHash < c.Covers[j].ImageHash })
	c.Screenshots = append(make([]model.SnapshotScreenshot, 0, len(snap.Screenshots)), snap.Screenshots...)
	sort.Slice(c.Screenshots, func(i, j int) bool { return c.Screenshots[i].ImageHash < c.Screenshots[j].ImageHash })
	// Links keep their order (order-significant, linksEqual is ordered) but a
	// nil slice must still marshal as [] — old snapshots never carry null and
	// the list validators reject it.
	if c.Links == nil {
		c.Links = []model.SnapshotLink{}
	}
	raw, err := json.Marshal(&c)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	// omitempty fields drop out of the marshal when zero; restore the two
	// affected keys so a patch can carry their explicit empty value.
	if _, ok := m["release_precision"]; !ok {
		m["release_precision"] = snap.ReleasePrecision
	}
	if _, ok := m["bid"]; !ok {
		m["bid"] = nil
	}
	patch := make(map[string]any, len(changed))
	for oldKey := range changed {
		newKey, ok := editspec.OldToNew[oldKey]
		if !ok {
			return nil, fmt.Errorf("no eternal field key for snapshot key %q", oldKey)
		}
		patch[newKey] = m[oldKey]
	}
	return patch, nil
}

// mapEngineWriteError translates engine-typed failures into the AppError
// vocabulary the old handlers already map (403 for permission, 400 for
// validation-class, passthrough otherwise).
func mapEngineWriteError(err error) error {
	if err == nil {
		return nil
	}
	switch e := err.(type) {
	case *editing.PermissionError:
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	case *editing.LockedFieldError:
		return errors.New(errors.ErrValidationFailed, e.Error())
	case *editing.ValidationError:
		return errors.New(errors.ErrValidationFailed, e.Error())
	case *editing.UnknownFieldError:
		return errors.New(errors.ErrValidationFailed, e.Error())
	}
	return err
}
