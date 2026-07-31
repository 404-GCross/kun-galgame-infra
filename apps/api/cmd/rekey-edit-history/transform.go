package main

import (
	"fmt"
	"sort"
	"strings"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/editing"
)

// docMode says which of the three JSONB documents is being transformed. It
// exists for ONE reason: the many→one folds (four name columns → titles, four
// intro columns → intros) are only true of a document that carries the
// entity's whole state.
//
//   - modeSnapshot: edit_revision.snapshot — full state. The fold is total, so
//     it is performed.
//   - modePatch: edit_proposal.patch and edit_proposal_amendment.patch_delta —
//     a SUBSET of fields. catalog.work.titles is a full-replace field, so
//     folding a patch that touched only the zh title would record the intent
//     "this work's only title is <zh>", which the proposer never expressed.
//     Fold keys are therefore retired in place here; every 1:1 key still maps.
//
// The asymmetry is safe by fact as well as by argument: the corpus holds ZERO
// open proposals (848 rows: 832 merged, 11 declined, 5 withdrawn), so no patch
// is ever replayed — a patch is a record, and the record stays in the
// vocabulary the proposer actually used. The tool refuses to migrate any
// proposal still OPEN at run time for exactly this reason (see main.go).
type docMode int

const (
	modeSnapshot docMode = iota
	modePatch
)

// transformer turns one wiki JSONB document into its catalog counterpart.
// Every mapped value is validated with the catalog field's own Validate
// closure before it is accepted; a value that fails is demoted to
// "retired in place" rather than written (keymap.go, class 3).
type transformer struct {
	ids      *idMaps
	validate map[string]func(any) error
	stats    *keyStats
}

// keyStats is the per-key ledger: what landed on which catalog key, and what
// stayed behind with which reason.
type keyStats struct {
	Mapped  map[string]int // catalog key → documents carrying it
	Retired map[string]int // wiki key → documents that kept it
	Reasons map[string]int // "wiki key: reason" → count
}

func newKeyStats() *keyStats {
	return &keyStats{Mapped: map[string]int{}, Retired: map[string]int{}, Reasons: map[string]int{}}
}

func (s *keyStats) mapped(key string) { s.Mapped[key]++ }

func (s *keyStats) retired(key, reason string) {
	s.Retired[key]++
	s.Reasons[key+": "+reason]++
}

// newTransformer wires the catalog.work field validators off a live registry —
// the same closures the running service enforces, so "spec-valid" here means
// the same thing it means at merge time.
func newTransformer(ids *idMaps, reg *editing.Registry) (*transformer, error) {
	spec, ok := reg.Type(editspec.TypeWork)
	if !ok {
		return nil, fmt.Errorf("entity type %q is not registered", editspec.TypeWork)
	}
	validate := make(map[string]func(any) error)
	for _, key := range []string{
		editspec.FieldWorkTitles, editspec.FieldWorkIntros, editspec.FieldWorkDisplayNSFW,
		editspec.FieldWorkOLang, editspec.FieldWorkContentRating, editspec.FieldWorkTagIDs,
		editspec.FieldWorkLabels, editspec.FieldWorkEngineIDs, editspec.FieldWorkLinks,
		editspec.FieldWorkCovers, editspec.FieldWorkScreenshots,
	} {
		f, ok := spec.Field(key)
		if !ok || f.Validate == nil {
			return nil, fmt.Errorf("catalog field %q has no validator", key)
		}
		validate[key] = f.Validate
	}
	return &transformer{ids: ids, validate: validate, stats: newKeyStats()}, nil
}

// document transforms one wiki document. It returns the new document and the
// routing table (wiki key → catalog key) for the keys that were mapped; every
// wiki key NOT in the routing table survives verbatim in the output under its
// original spelling.
func (t *transformer) document(in map[string]any, mode docMode) (map[string]any, map[string]string) {
	out := make(map[string]any, len(in))
	route := make(map[string]string, len(in))

	emit := func(catalogKey string, value any, sources ...string) bool {
		if err := t.validate[catalogKey](value); err != nil {
			for _, src := range sources {
				t.stats.retired(src, "transformed value rejected by "+catalogKey+" validator: "+err.Error())
			}
			return false
		}
		out[catalogKey] = value
		t.stats.mapped(catalogKey)
		for _, src := range sources {
			route[src] = catalogKey
		}
		return true
	}

	// ── the two folds (snapshot documents only) ──────────────────────────────
	if mode == modeSnapshot {
		if titles, sources, ok := t.foldTitles(in); ok {
			emit(editspec.FieldWorkTitles, titles, sources...)
		}
		if intros, sources, ok := t.foldIntros(in); ok {
			emit(editspec.FieldWorkIntros, intros, sources...)
		}
	}

	// ── the 1:1 keys ─────────────────────────────────────────────────────────
	for key, value := range in {
		if _, done := route[key]; done {
			continue
		}
		if reason, retired := retiredKeys[key]; retired {
			t.stats.retired(key, reason)
			continue
		}
		if foldKeys[key] {
			if mode == modePatch {
				t.stats.retired(key, "fold key in a partial document: a full-replace value built from a subset would misstate the patch")
			}
			continue // snapshot mode already routed it, or the fold failed
		}
		if newValue, target, ok := t.scalarOrList(key, value); ok {
			emit(target, newValue, key)
		}
	}

	// Everything not routed survives under its own key.
	for key, value := range in {
		if _, moved := route[key]; !moved {
			out[key] = value
		}
	}
	return out, route
}

// scalarOrList transforms the 1:1 keys. ok=false means the value could not be
// transformed faithfully; the reason has already been counted.
func (t *transformer) scalarOrList(key string, value any) (any, string, bool) {
	switch key {
	case wikiContentLimit:
		switch value {
		case "sfw":
			return false, editspec.FieldWorkDisplayNSFW, true
		case "nsfw":
			return true, editspec.FieldWorkDisplayNSFW, true
		}
		t.stats.retired(key, fmt.Sprintf("content_limit value %v is outside the {sfw,nsfw} display axis", value))
		return nil, "", false

	case wikiOriginalLanguage:
		s, _ := value.(string)
		if lang, ok := olangMap[s]; ok {
			return lang, editspec.FieldWorkOLang, true
		}
		t.stats.retired(key, fmt.Sprintf("original_language %q has no BCP-47 counterpart", s))
		return nil, "", false

	case wikiAgeLimit:
		s, _ := value.(string)
		if rating, ok := ageLimitMap[s]; ok {
			return float64(rating), editspec.FieldWorkContentRating, true
		}
		t.stats.retired(key, fmt.Sprintf("age_limit %q gets no content-rating verdict (same refusal as the releasemeta job)", s))
		return nil, "", false

	case wikiTagIDs:
		ids, ok := t.mapIDList(key, value, t.ids.Tag, "catalog_tag")
		if !ok {
			return nil, "", false
		}
		return ids, editspec.FieldWorkTagIDs, true

	case wikiEngineIDs:
		ids, ok := t.mapIDList(key, value, t.ids.Engine, "catalog_engine")
		if !ok {
			return nil, "", false
		}
		return ids, editspec.FieldWorkEngineIDs, true

	case wikiOfficialIDs:
		ids, ok := t.mapIDList(key, value, t.ids.Label, "catalog_label")
		if !ok {
			return nil, "", false
		}
		labels := make([]any, 0, len(ids))
		for _, id := range ids {
			labels = append(labels, map[string]any{"label_id": id, "kind": float64(labelEdgeKind)})
		}
		return labels, editspec.FieldWorkLabels, true

	case wikiLinks:
		urls, ok := t.mapLinks(key, value)
		if !ok {
			return nil, "", false
		}
		return urls, editspec.FieldWorkLinks, true

	case wikiCovers:
		covers, ok := t.mapCovers(key, value)
		if !ok {
			return nil, "", false
		}
		return covers, editspec.FieldWorkCovers, true

	case wikiScreenshots:
		shots, ok := t.mapScreenshots(key, value)
		if !ok {
			return nil, "", false
		}
		return shots, editspec.FieldWorkScreenshots, true
	}
	// An unknown key is not silently dropped: it survives under its own name
	// and shows up in the ledger, so a vocabulary the survey missed is loud.
	t.stats.retired(key, "key is not in the migration's wiki vocabulary")
	return nil, "", false
}

// foldTitles builds catalog.work.titles from the four name columns. Empty
// names are skipped (the wiki stores "" for "no name in this language", which
// is the absence of a title row, not an empty one). Nothing to fold = no
// titles value: the four keys stay put rather than produce a value the
// validator would reject anyway (titles requires one official).
func (t *transformer) foldTitles(in map[string]any) ([]any, []string, bool) {
	var titles []any
	var sources []string
	seen := map[string]bool{}
	for _, p := range nameFold {
		raw, present := in[p.Key]
		if !present {
			continue
		}
		sources = append(sources, p.Key)
		name, _ := raw.(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		if seen[p.Lang+"\x00"+name] {
			continue
		}
		seen[p.Lang+"\x00"+name] = true
		titles = append(titles, map[string]any{"lang": p.Lang, "title": name, "kind": float64(0)})
	}
	if len(titles) == 0 {
		for _, src := range sources {
			t.stats.retired(src, "no non-empty name in any language: there is no titles value to fold onto")
		}
		return nil, nil, false
	}
	return titles, sources, true
}

// foldIntros builds catalog.work.intros. An all-empty fold is legal here (the
// empty list is a valid intros value meaning "no curated synopsis") — unlike
// titles, which requires an official.
func (t *transformer) foldIntros(in map[string]any) ([]any, []string, bool) {
	intros := []any{}
	var sources []string
	for _, p := range introFold {
		raw, present := in[p.Key]
		if !present {
			continue
		}
		sources = append(sources, p.Key)
		body, _ := raw.(string)
		if strings.TrimSpace(body) == "" {
			continue
		}
		intros = append(intros, map[string]any{"lang": p.Lang, "intro": body})
	}
	if len(sources) == 0 {
		return nil, nil, false
	}
	return intros, sources, true
}

// mapIDList translates a wiki id list through an id space. ALL OR NOTHING: one
// unmapped id retires the whole key. A partial list is silent data loss the
// moment a reader treats the snapshot as the state of the work — and a
// catastrophic one the moment somebody reverts to it, because these are
// full-replace fields.
func (t *transformer) mapIDList(key string, value any, space map[int64]int64, what string) ([]any, bool) {
	if value == nil {
		return []any{}, true
	}
	arr, ok := value.([]any)
	if !ok {
		t.stats.retired(key, "value is not an array")
		return nil, false
	}
	out := make([]any, 0, len(arr))
	seen := map[int64]bool{}
	for _, el := range arr {
		n, ok := el.(float64)
		if !ok || n != float64(int64(n)) {
			t.stats.retired(key, "value contains a non-integer id")
			return nil, false
		}
		target, ok := space[int64(n)]
		if !ok {
			t.stats.retired(key, fmt.Sprintf("wiki id has no %s counterpart (all-or-nothing: the whole list stays in the wiki id space)", what))
			return nil, false
		}
		if seen[target] {
			continue // two wiki rows folded onto one catalog row
		}
		seen[target] = true
		out = append(out, float64(target))
	}
	return out, true
}

// mapLinks reduces the wiki's {name, link, source, source_key} objects to the
// catalog field's bare canonical URLs. Names are dropped by 03 §6-2. The
// canonicalization is the catalog spec's OWN (editspec.CanonicalWorkLinks), so
// a migrated value is byte-identical to what re-reading the curated lane would
// return — the property that keeps a later diff from showing a phantom change.
func (t *transformer) mapLinks(key string, value any) ([]any, bool) {
	if value == nil {
		return []any{}, true
	}
	arr, ok := value.([]any)
	if !ok {
		t.stats.retired(key, "value is not an array")
		return nil, false
	}
	// Canonicalized ONE AT A TIME, then deduplicated. The wiki's link list was
	// not keyed, so it happily held the same target twice (an http and an https
	// spelling, a mobile host, a web.archive.org wrapper) — forms that collapse
	// onto one URL here. Handing the whole list to the field's parser at once
	// would reject the row over a duplicate that only exists after
	// canonicalization; folding first keeps the row and loses nothing a reader
	// could see.
	out := make([]any, 0, len(arr))
	seen := map[string]bool{}
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			t.stats.retired(key, "value contains a non-object element")
			return nil, false
		}
		url, _ := obj["link"].(string)
		canonical, err := editspec.CanonicalWorkLinks([]any{url})
		if err != nil {
			t.stats.retired(key, "link does not canonicalize: "+err.Error())
			return nil, false
		}
		if seen[canonical[0]] {
			continue
		}
		seen[canonical[0]] = true
		out = append(out, canonical[0])
	}
	return out, true
}

// mapCovers recodes cover elements. Dropped: source / source_key (the curated
// lane is the editing face's own source by construction) and sort_order (array
// position IS the order in the catalog field, which round-trips through
// LoadSnapshot's ORDER BY id). Added: portrait_pinned=false — the wiki had no
// such concept, and false is its absence, not a guess.
func (t *transformer) mapCovers(key string, value any) ([]any, bool) {
	return t.mapMedia(key, value, func(obj map[string]any) map[string]any {
		return map[string]any{
			"image_hash":      obj["image_hash"],
			"kind":            stringOr(obj["kind"], ""),
			"portrait_pinned": false,
			"sexual":          numberOr(obj["sexual"], 0),
			"violence":        numberOr(obj["violence"], 0),
		}
	})
}

func (t *transformer) mapScreenshots(key string, value any) ([]any, bool) {
	return t.mapMedia(key, value, func(obj map[string]any) map[string]any {
		return map[string]any{
			"image_hash": obj["image_hash"],
			"caption":    stringOr(obj["caption"], ""),
			"sexual":     numberOr(obj["sexual"], 0),
			"violence":   numberOr(obj["violence"], 0),
		}
	})
}

// mapMedia is the shared media recode. JSON null becomes the empty list: the
// wiki wrote null for "this revision has no media rows", and the catalog field
// has no null — [] is the same statement in the vocabulary that survives.
func (t *transformer) mapMedia(key string, value any, recode func(map[string]any) map[string]any) ([]any, bool) {
	if value == nil {
		return []any{}, true
	}
	arr, ok := value.([]any)
	if !ok {
		t.stats.retired(key, "value is not an array")
		return nil, false
	}
	out := make([]any, 0, len(arr))
	seen := map[string]bool{}
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			t.stats.retired(key, "value contains a non-object element")
			return nil, false
		}
		hash, _ := obj["image_hash"].(string)
		if seen[hash] {
			continue // the catalog field is keyed by hash; the wiki was not
		}
		seen[hash] = true
		out = append(out, recode(obj))
	}
	return out, true
}

func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

func numberOr(v any, fallback float64) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return fallback
}

// rekeyChangedFields translates a revision's changed_fields list through the
// SAME routing table the row's snapshot produced, so the two can never
// disagree about whether a key moved. Order is preserved and a fold's
// collapsed members are deduplicated.
func rekeyChangedFields(fields []string, route map[string]string) []string {
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		key := f
		if target, moved := route[f]; moved {
			key = target
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// rekeyPatchDelta rewrites an amendment's {"set":{...},"unset":[...]} document.
// The set half goes through the value transform; the unset half is a bare key
// list routed by the same decisions.
func (t *transformer) patchDelta(delta map[string]any) map[string]any {
	out := make(map[string]any, len(delta))
	for k, v := range delta {
		out[k] = v
	}
	if set, ok := delta["set"].(map[string]any); ok {
		newSet, _ := t.document(set, modePatch)
		out["set"] = newSet
	}
	if unset, ok := delta["unset"].([]any); ok {
		// Route an unset key by transforming a probe document holding it: the
		// decision must be the same one the set half would make.
		keys := make([]string, 0, len(unset))
		for _, el := range unset {
			if s, ok := el.(string); ok {
				keys = append(keys, s)
			}
		}
		sort.Strings(keys)
		newUnset := make([]any, 0, len(keys))
		seen := map[string]bool{}
		for _, k := range keys {
			// A fold key stays in the wiki vocabulary here for the same reason
			// it does in the set half: an amendment that unsets one language's
			// title did not unset the whole titles field.
			target := k
			if _, retired := retiredKeys[k]; !retired && !foldKeys[k] {
				if mapped, ok := mappedTargets[k]; ok {
					target = mapped
				}
			}
			if seen[target] {
				continue
			}
			seen[target] = true
			newUnset = append(newUnset, target)
		}
		out["unset"] = newUnset
	}
	return out
}
