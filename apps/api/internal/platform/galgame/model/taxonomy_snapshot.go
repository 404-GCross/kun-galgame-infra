package model

import (
	"encoding/json"
	"sort"
)

// taxonomy_snapshot.go centralises the per-entity Take / ChangedKeys
// helpers. The four entities share the same shape "scalars + sorted
// aliases", with engine alias and series-without-aliases as small
// implementation differences absorbed inside Take*Snapshot.
//
// Invariants (mirrors model.TakeSnapshot for galgame, §1.5):
//   - Aliases canonical-sorted (string ascending)
//   - Empty alias slice is `make([]string, 0)`, never nil (JSON: `[]`)
//   - Empty / unloaded relation slices on the input model yield empty
//     Aliases on the output snapshot
//
// Change-detection (Changed*Keys) returns the SET of field names that
// differ between two snapshots, in the same shape galgame's
// ChangedKeys uses (map[string]bool). The string keys here are the
// `json:"…"` tag values of the per-entity Snapshot struct fields and
// are what TaxonomyRevision.ChangedFields stores literally.

// ─────────────────────────── Tag ───────────────────────────

// TakeTagSnapshot builds the canonical snapshot of a tag from a loaded
// model.GalgameTag (Alias preload required). The returned snapshot's
// Aliases is sorted ASC so two semantically equal snapshots serialise
// to byte-identical JSON.
func TakeTagSnapshot(t *GalgameTag) *TagSnapshot {
	aliases := make([]string, 0, len(t.Alias))
	for _, a := range t.Alias {
		aliases = append(aliases, a.Name)
	}
	sort.Strings(aliases)
	return &TagSnapshot{
		Name:        t.Name,
		Category:    t.Category,
		Description: t.Description,
		Aliases:     aliases,
	}
}

// ChangedTagKeys returns the json-tag names of every TagSnapshot field
// that differs between old and new. Aliases compared as a canonical-
// sorted set (TakeTagSnapshot guarantees the inputs are sorted).
func ChangedTagKeys(old, new *TagSnapshot) map[string]bool {
	keys := map[string]bool{}
	if old.Name != new.Name {
		keys["name"] = true
	}
	if old.Category != new.Category {
		keys["category"] = true
	}
	if old.Description != new.Description {
		keys["description"] = true
	}
	if !stringSliceEqual(old.Aliases, new.Aliases) {
		keys["aliases"] = true
	}
	return keys
}

// TagSnapshotFieldNames is the canonical list of every editable field
// on TagSnapshot — used for action='created' ChangedFields (per §6.5
// "created lists all field names"). Keep this in sync with the struct.
func TagSnapshotFieldNames() []string {
	return []string{"name", "category", "description", "aliases"}
}

// TagSnapshotFromJSON deserialises a stored snapshot byte slice. Empty
// input → nil, nil (caller treats absence as "no prior snapshot").
func TagSnapshotFromJSON(data []byte) (*TagSnapshot, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s TagSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─────────────────────────── Official ───────────────────────────

// TakeOfficialSnapshot — same contract as TakeTagSnapshot but for
// model.GalgameOfficial (which has more scalars: Original/Link/Lang).
// Alias preload required.
func TakeOfficialSnapshot(o *GalgameOfficial) *OfficialSnapshot {
	aliases := make([]string, 0, len(o.Alias))
	for _, a := range o.Alias {
		aliases = append(aliases, a.Name)
	}
	sort.Strings(aliases)
	return &OfficialSnapshot{
		Name:        o.Name,
		Original:    o.Original,
		Link:        o.Link,
		Category:    o.Category,
		Lang:        o.Lang,
		Description: o.Description,
		Aliases:     aliases,
	}
}

func ChangedOfficialKeys(old, new *OfficialSnapshot) map[string]bool {
	keys := map[string]bool{}
	if old.Name != new.Name {
		keys["name"] = true
	}
	if old.Original != new.Original {
		keys["original"] = true
	}
	if old.Link != new.Link {
		keys["link"] = true
	}
	if old.Category != new.Category {
		keys["category"] = true
	}
	if old.Lang != new.Lang {
		keys["lang"] = true
	}
	if old.Description != new.Description {
		keys["description"] = true
	}
	if !stringSliceEqual(old.Aliases, new.Aliases) {
		keys["aliases"] = true
	}
	return keys
}

func OfficialSnapshotFieldNames() []string {
	return []string{"name", "original", "link", "category", "lang", "description", "aliases"}
}

func OfficialSnapshotFromJSON(data []byte) (*OfficialSnapshot, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s OfficialSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─────────────────────────── Engine ───────────────────────────

// TakeEngineSnapshot — engine's Alias is a jsonb-inlined column on the
// engine row (not a separate alias table), so reading it requires a
// JSON decode. Snapshot still presents it as []string for uniformity
// with tag/official.
func TakeEngineSnapshot(e *GalgameEngine) *EngineSnapshot {
	aliases := decodeEngineAliasJSON(e.Alias)
	sort.Strings(aliases)
	return &EngineSnapshot{
		Name:        e.Name,
		Description: e.Description,
		Aliases:     aliases,
	}
}

// decodeEngineAliasJSON safely unmarshals the engine's jsonb Alias
// column to []string. Empty / invalid input returns an empty slice
// (consistent with the rest of the snapshot layer's "always non-nil
// empty slice" contract).
func decodeEngineAliasJSON(raw []byte) []string {
	out := make([]string, 0)
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return []string{}
	}
	return out
}

func ChangedEngineKeys(old, new *EngineSnapshot) map[string]bool {
	keys := map[string]bool{}
	if old.Name != new.Name {
		keys["name"] = true
	}
	if old.Description != new.Description {
		keys["description"] = true
	}
	if !stringSliceEqual(old.Aliases, new.Aliases) {
		keys["aliases"] = true
	}
	return keys
}

func EngineSnapshotFieldNames() []string {
	return []string{"name", "description", "aliases"}
}

func EngineSnapshotFromJSON(data []byte) (*EngineSnapshot, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s EngineSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─────────────────────────── Series ───────────────────────────

// TakeSeriesSnapshot — series has only Name + Description. Membership
// (which galgame ids belong) is owned by the galgame side via
// galgame.series_id and recorded in galgame_revision when changed;
// SeriesSnapshot intentionally does NOT carry GalgameIDs. See §7.5
// of the upgrade plan for the rationale.
func TakeSeriesSnapshot(s *GalgameSeries) *SeriesSnapshot {
	return &SeriesSnapshot{
		Name:        s.Name,
		Description: s.Description,
	}
}

func ChangedSeriesKeys(old, new *SeriesSnapshot) map[string]bool {
	keys := map[string]bool{}
	if old.Name != new.Name {
		keys["name"] = true
	}
	if old.Description != new.Description {
		keys["description"] = true
	}
	return keys
}

func SeriesSnapshotFieldNames() []string {
	return []string{"name", "description"}
}

func SeriesSnapshotFromJSON(data []byte) (*SeriesSnapshot, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s SeriesSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─────────────────────────── Overlay helpers ───────────────────────────
//
// Overlay applies a partial edit (the "request" snapshot) onto the
// current canonical snapshot to produce the proposed-next snapshot.
// Mirrors galgame's overlayUpdate: pointer fields = presence-semantic
// (nil keeps cur, non-nil overwrites). Aliases / GalgameIDs are
// pointer-to-slice so omission = keep, empty = clear.
//
// The overlay functions take the raw request shape (lifted *string /
// *[]string) so the service layer can do dto → overlay → next →
// ChangedKeys → ApplySnapshot in five mechanical steps identical to
// galgame's flow.

// OverlayTag returns a new TagSnapshot = cur with nil-pointer fields
// preserved, non-nil fields overwritten. Aliases are canonical-sorted
// on the way out so the comparison in ChangedTagKeys is stable even
// when the request supplies aliases in arbitrary order.
func OverlayTag(cur *TagSnapshot, name, category, description *string, aliases *[]string) *TagSnapshot {
	n := *cur
	if name != nil {
		n.Name = *name
	}
	if category != nil {
		n.Category = *category
	}
	if description != nil {
		n.Description = *description
	}
	if aliases != nil {
		next := append([]string(nil), (*aliases)...)
		sort.Strings(next)
		n.Aliases = next
	}
	return &n
}

func OverlayOfficial(cur *OfficialSnapshot, name, original, link, category, lang, description *string, aliases *[]string) *OfficialSnapshot {
	n := *cur
	if name != nil {
		n.Name = *name
	}
	if original != nil {
		n.Original = *original
	}
	if link != nil {
		n.Link = *link
	}
	if category != nil {
		n.Category = *category
	}
	if lang != nil {
		n.Lang = *lang
	}
	if description != nil {
		n.Description = *description
	}
	if aliases != nil {
		next := append([]string(nil), (*aliases)...)
		sort.Strings(next)
		n.Aliases = next
	}
	return &n
}

func OverlayEngine(cur *EngineSnapshot, name, description *string, aliases *[]string) *EngineSnapshot {
	n := *cur
	if name != nil {
		n.Name = *name
	}
	if description != nil {
		n.Description = *description
	}
	if aliases != nil {
		next := append([]string(nil), (*aliases)...)
		sort.Strings(next)
		n.Aliases = next
	}
	return &n
}

func OverlaySeries(cur *SeriesSnapshot, name, description *string) *SeriesSnapshot {
	n := *cur
	if name != nil {
		n.Name = *name
	}
	if description != nil {
		n.Description = *description
	}
	return &n
}

// SortedKeys returns the keys of a changed-set in deterministic order,
// suitable for storing into TaxonomyRevision.ChangedFields. Keys with
// boolean value `true` are included; anything else is dropped.
func SortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k, v := range set {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
