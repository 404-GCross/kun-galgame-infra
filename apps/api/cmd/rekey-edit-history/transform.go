package main

import (
	"fmt"
	"sort"
	"strings"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/editing"
)

type docMode int

const (
	modeSnapshot docMode = iota
	modePatch
)

type transformer struct {
	ids      *idMaps
	validate map[string]func(any) error
	stats    *keyStats
}

type keyStats struct {
	Mapped  map[string]int
	Retired map[string]int
	Reasons map[string]int
}

func newKeyStats() *keyStats {
	return &keyStats{Mapped: map[string]int{}, Retired: map[string]int{}, Reasons: map[string]int{}}
}

func (s *keyStats) mapped(key string) { s.Mapped[key]++ }

func (s *keyStats) retired(key, reason string) {
	s.Retired[key]++
	s.Reasons[key+": "+reason]++
}

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

	if mode == modeSnapshot {
		if titles, sources, ok := t.foldTitles(in); ok {
			emit(editspec.FieldWorkTitles, titles, sources...)
		}
		if intros, sources, ok := t.foldIntros(in); ok {
			emit(editspec.FieldWorkIntros, intros, sources...)
		}
	}

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
			continue
		}
		if newValue, target, ok := t.scalarOrList(key, value); ok {
			emit(target, newValue, key)
		}
	}

	for key, value := range in {
		if _, moved := route[key]; !moved {
			out[key] = value
		}
	}
	return out, route
}

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
	t.stats.retired(key, "key is not in the migration's wiki vocabulary")
	return nil, "", false
}

func (t *transformer) foldTitles(in map[string]any) ([]any, []string, bool) {
	var titles []any
	var sources []string
	officials := 0
	seen := map[string]bool{}
	add := func(lang, name string, kind float64) {
		if strings.TrimSpace(name) == "" {
			return
		}
		key := fmt.Sprintf("%s\x00%s\x00%v", lang, name, kind)
		if seen[key] {
			return
		}
		seen[key] = true
		if kind == 0 {
			officials++
		}
		titles = append(titles, map[string]any{"lang": lang, "title": name, "kind": kind})
	}
	for _, p := range nameFold {
		raw, present := in[p.Key]
		if !present {
			continue
		}
		sources = append(sources, p.Key)
		name, _ := raw.(string)
		add(p.Lang, name, 0)
	}
	aliasesOK := true
	if raw, present := in[wikiAliases]; present {
		sources = append(sources, wikiAliases)
		switch list := raw.(type) {
		case nil:
		case []any:
			for _, el := range list {
				name, ok := el.(string)
				if !ok {
					aliasesOK = false
					break
				}
				add("", name, float64(aliasesFoldKind))
			}
		default:
			aliasesOK = false
		}
	}
	if !aliasesOK {
		for _, src := range sources {
			t.stats.retired(src, "the alias list is not an array of strings: the titles fold would have to drop part of it")
		}
		return nil, nil, false
	}
	if officials == 0 {
		for _, src := range sources {
			t.stats.retired(src, "no non-empty official name in any language: titles requires one, and inventing it is not on the table")
		}
		return nil, nil, false
	}
	return titles, sources, true
}

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
			continue
		}
		seen[target] = true
		out = append(out, float64(target))
	}
	return out, true
}

func (t *transformer) mapLinks(key string, value any) ([]any, bool) {
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
			continue
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
