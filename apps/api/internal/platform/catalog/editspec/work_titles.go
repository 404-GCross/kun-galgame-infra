package editspec

import (
	"context"
	"fmt"
	"strings"

	catmodel "api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// catalog.work.titles (E1) — the list field: the FULL set of a work's title
// rows as one field value (changed_fields granularity = whole field, the
// VNDB full-row-revision posture; 02 号裁定 3).
//
// Canonical element shape mirrors CatalogWorkTitle (model/work.go):
//
//	{"lang": <BCP-47, olang whitelist>, "title": <non-empty>, "kind": 0..3,
//	 "latin": <optional, non-empty when present>}
//
// No other keys are accepted, and "latin" is omitted (never "") — so a
// LoadSnapshot value and a submitted patch value that mean the same titles
// encode to the same canonical JSON (no-op detection + clean revert
// round-trips). Element order is meaningful and preserved: Apply inserts in
// array order and LoadSnapshot reads back ORDER BY id, so the value
// round-trips exactly.
//
// Apply = transactional full replace of the work's title rows + the
// display_name derivation ("titles are truth", the denormalized fast-path):
// display_name = the official (kind=0) title in the work's olang, falling
// back to the first official title in array order. This follows the existing
// catalog write paths verbatim (survey evidence, 02 号执行报告):
//   - importer/bangumixmedia.go registerXmedia: DisplayName = the ja
//     (olang) official name, falling back to nameCN (the other official);
//   - importer/dlsite.go / egdlsite_create.go: DisplayName = the ja official
//     title (kana lands as a search_hint row, never the display name);
//   - model/work.go: "OLang ... decides which title row is primary (VNDB
//     model)" / "DisplayName is the denormalized display fast-path; titles
//     are truth".
//
// Validation therefore requires at least one official element, making the
// derivation total. search_hint (kind=3) rows are part of the value: a full
// replace that dropped them would silently destroy findability data.

const (
	titleKindOfficial   = int64(catmodel.WorkTitleKindOfficial)
	titleKindSearchHint = int64(catmodel.WorkTitleKindSearchHint)

	maxTitleElements = 100
	maxTitleRunes    = 500
)

// workTitle is one parsed canonical element.
type workTitle struct {
	Lang  string
	Title string
	Latin string // "" = absent
	Kind  int64
}

// parseTitles validates and parses a titles field value (the wire truth: a
// decoded JSON array). It returns the parsed elements so Apply never
// re-validates.
func parseTitles(v any) ([]workTitle, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of title objects")
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("must contain at least one title")
	}
	if len(arr) > maxTitleElements {
		return nil, fmt.Errorf("must contain at most %d titles", maxTitleElements)
	}
	out := make([]workTitle, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	officials := 0
	for i, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("element %d: must be an object", i)
		}
		for key := range obj {
			switch key {
			case "lang", "title", "kind", "latin":
			default:
				return nil, fmt.Errorf("element %d: unknown key %q", i, key)
			}
		}
		lang, ok := obj["lang"].(string)
		if !ok {
			return nil, fmt.Errorf("element %d: lang must be a string", i)
		}
		if _, allowed := olangAllowed[lang]; !allowed {
			return nil, fmt.Errorf("element %d: %q is not an allowed language", i, lang)
		}
		title, ok := obj["title"].(string)
		if !ok {
			return nil, fmt.Errorf("element %d: title must be a string", i)
		}
		if strings.TrimSpace(title) == "" {
			return nil, fmt.Errorf("element %d: title must not be empty", i)
		}
		if len([]rune(title)) > maxTitleRunes {
			return nil, fmt.Errorf("element %d: title must be at most %d characters", i, maxTitleRunes)
		}
		kindF, ok := obj["kind"].(float64)
		if !ok || kindF != float64(int64(kindF)) {
			return nil, fmt.Errorf("element %d: kind must be an integer", i)
		}
		kind := int64(kindF)
		if kind < titleKindOfficial || kind > titleKindSearchHint {
			return nil, fmt.Errorf("element %d: kind must be 0 (official), 1 (alias), 2 (abbreviation) or 3 (search_hint)", i)
		}
		latin := ""
		if raw, present := obj["latin"]; present {
			latin, ok = raw.(string)
			if !ok || strings.TrimSpace(latin) == "" {
				return nil, fmt.Errorf("element %d: latin must be a non-empty string when present", i)
			}
			if len([]rune(latin)) > maxTitleRunes {
				return nil, fmt.Errorf("element %d: latin must be at most %d characters", i, maxTitleRunes)
			}
		}
		// Mirrors uq_catalog_work_title (work_id, lang, title, kind).
		uniq := fmt.Sprintf("%s\x00%s\x00%d", lang, title, kind)
		if _, dup := seen[uniq]; dup {
			return nil, fmt.Errorf("element %d: duplicate (lang, title, kind)", i)
		}
		seen[uniq] = struct{}{}
		if kind == titleKindOfficial {
			officials++
		}
		out = append(out, workTitle{Lang: lang, Title: title, Latin: latin, Kind: kind})
	}
	if officials == 0 {
		return nil, fmt.Errorf("must contain at least one official (kind 0) title")
	}
	return out, nil
}

func validateTitles(v any) error {
	_, err := parseTitles(v)
	return err
}

// deriveDisplayName picks the display name from parsed titles: the official
// title in olang, else the first official (parseTitles guarantees one).
func deriveDisplayName(titles []workTitle, olang string) string {
	first := ""
	for _, t := range titles {
		if t.Kind != titleKindOfficial {
			continue
		}
		if t.Lang == olang {
			return t.Title
		}
		if first == "" {
			first = t.Title
		}
	}
	return first
}

// applyTitles full-replaces the work's title rows inside the engine-provided
// family transaction and re-derives display_name. The work row is read first
// (soft-delete scoped) both as the existence check and for olang — when the
// same merge also patched olang, that Apply ran before this one (sorted key
// order), so the derivation sees the new value.
func applyTitles(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	titles, err := parseTitles(value)
	if err != nil {
		return fmt.Errorf("editspec: titles: %w", err)
	}
	var work catmodel.CatalogWork
	if err := tx.WithContext(ctx).Select("id", "olang").First(&work, entityID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return editing.ErrEntityNotFound
		}
		return err
	}
	if err := tx.WithContext(ctx).
		Where("work_id = ?", entityID).Delete(&catmodel.CatalogWorkTitle{}).Error; err != nil {
		return err
	}
	rows := make([]catmodel.CatalogWorkTitle, 0, len(titles))
	for _, t := range titles {
		row := catmodel.CatalogWorkTitle{WorkID: entityID, Lang: t.Lang, Title: t.Title, Kind: int16(t.Kind)}
		if t.Latin != "" {
			latin := t.Latin
			row.Latin = &latin
		}
		rows = append(rows, row)
	}
	if err := tx.WithContext(ctx).Create(&rows).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Model(&catmodel.CatalogWork{}).
		Where("id = ?", entityID).
		Update("display_name", deriveDisplayName(titles, work.OLang)).Error
}

// loadTitles reads the work's title rows as the canonical field value.
// ORDER BY id preserves Apply's insertion order, so the value round-trips.
func loadTitles(ctx context.Context, db *gorm.DB, workID int64) ([]any, error) {
	var rows []catmodel.CatalogWorkTitle
	if err := db.WithContext(ctx).
		Where("work_id = ?", workID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		el := map[string]any{"lang": r.Lang, "title": r.Title, "kind": int64(r.Kind)}
		if r.Latin != nil && *r.Latin != "" {
			el["latin"] = *r.Latin
		}
		out = append(out, el)
	}
	return out, nil
}
