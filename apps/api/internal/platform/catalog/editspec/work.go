// Package editspec declares the catalog family's editing-engine
// registrations (doc 21 §2.2): field tables, validators, and apply closures
// carrying the catalog DB pool. The engine stays family-agnostic — this
// package is the catalog side of the dependency injection, wired at the
// cmd/catalog assembly point (charter ruling 1).
package editspec

import (
	"context"
	"errors"
	"fmt"
	"strings"

	catmodel "api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// Eternal field keys of catalog.work (§2.2b — never renamed, never reused).
// E0 shipped the three scalar columns; E1 added titles (the list field, see
// work_titles.go); wave 154 (03 定案 §2) completes the matrix so the catalog
// entity is editable in full and the wiki's 26-key surface has a native
// counterpart for everything it is losing.
//
// TWO KEYS THE MATRIX DELIBERATELY DOES NOT HAVE (03 §6-1, authorized cuts):
//
//   - no release date. The wiki carried a work-level date plus a TBA flag and a
//     precision enum; the catalog models dates on catalog_release rows whose
//     nullable y/m/d ARE the precision, and 99%+ of entries already get their
//     date from the release aggregate. A date edit is therefore a curated
//     release row — the future release族 registration — not three work columns.
//   - no status. Lifecycle is claim_state end to end (§3), reached through
//     semantic actions (approve/decline/ban), never by patching a field. That
//     is also why this spec cannot be used to publish anything.
//
// Credits and character edges stay OUT by the 07-30 ruling: they wait for the
// cross-source person identity resolution, without which an edge edit would be
// guessing which person it points at.
const (
	TypeWork = "catalog.work"

	FieldWorkDisplayName   = "catalog.work.display_name"
	FieldWorkOLang         = "catalog.work.olang"
	FieldWorkContentRating = "catalog.work.content_rating"
	FieldWorkTitles        = "catalog.work.titles"
	FieldWorkIntros        = "catalog.work.intros"
	FieldWorkDisplayNSFW   = "catalog.work.display_nsfw"
	FieldWorkTagIDs        = "catalog.work.tag_ids"
	FieldWorkLabels        = "catalog.work.labels"
	FieldWorkEngineIDs     = "catalog.work.engine_ids"
	FieldWorkSeriesIDs     = "catalog.work.series_ids"
	FieldWorkLinks         = "catalog.work.links"
	FieldWorkCovers        = "catalog.work.covers"
	FieldWorkScreenshots   = "catalog.work.screenshots"
)

// letmoeSites are the letmoe tenant keys sharing one overlay policy (02 号
// 裁定 4): prod `letmoe`, staging `letmoe-staging`, and `letmoe-dev` (a
// harmless superset — whichever catalog_site binding is in play is covered).
var letmoeSites = []string{"letmoe", "letmoe-staging", "letmoe-dev"}

// olangAllowed is the BCP-47 whitelist for catalog.work.olang — the VNDB
// original-language vocabulary (the dominant upstream) plus the zh scripts.
// A miss here is a validation 422, never a crash; extending the list is a
// one-line change shipping with code.
var olangAllowed = map[string]struct{}{}

func init() {
	for _, lang := range []string{
		"ar", "be", "bg", "ca", "cs", "da", "de", "el", "en", "eo", "es",
		"fi", "fr", "ga", "gd", "he", "hi", "hr", "hu", "id", "it", "iu",
		"ja", "ko", "la", "lt", "lv", "mk", "ms", "nl", "no", "pl",
		"pt-br", "pt-pt", "ro", "ru", "sk", "sl", "sr", "sv", "ta", "th",
		"tr", "uk", "ur", "vi", "zh", "zh-Hans", "zh-Hant",
	} {
		olangAllowed[lang] = struct{}{}
	}
}

// RegisterWork registers the catalog.work entity type on an engine registry.
// db is the CATALOG pool — the closures are the only key→column translation
// layer for this family.
func RegisterWork(reg *editing.Registry, db *gorm.DB) error {
	// letmoe overlay (E1, 02 号裁定 4): trusted proposals, owner automerge —
	// a letmoe actor editing a work letmoe claimed lands directly (single
	// revision, still logged); everyone else's proposal enters the review
	// queue (existing edit.catalog.work.review authority).
	letmoePolicy := editing.Policy{
		Propose:   editing.ProposeTrusted,
		Review:    editing.ReviewPerm(string(perm.EditWorkReview)),
		Automerge: editing.AutomergeOwner,
	}
	fieldKeys := []string{
		FieldWorkDisplayName, FieldWorkOLang, FieldWorkContentRating, FieldWorkTitles,
		FieldWorkIntros, FieldWorkDisplayNSFW, FieldWorkTagIDs, FieldWorkLabels,
		FieldWorkEngineIDs, FieldWorkSeriesIDs, FieldWorkLinks,
		FieldWorkCovers, FieldWorkScreenshots,
	}
	overlays := make(map[string]map[string]editing.Policy, len(letmoeSites))
	for _, site := range letmoeSites {
		overlay := make(map[string]editing.Policy, len(fieldKeys))
		for _, key := range fieldKeys {
			overlay[key] = letmoePolicy
		}
		overlays[site] = overlay
	}

	return reg.Register(editing.EntityTypeSpec{
		Family: "catalog",
		Type:   TypeWork,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			var w catmodel.CatalogWork
			err := db.WithContext(ctx).
				Select("id", "display_name", "olang", "content_rating", "display_nsfw").
				First(&w, entityID).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, editing.ErrEntityNotFound
			}
			if err != nil {
				return nil, err
			}
			snapshot := map[string]any{
				FieldWorkDisplayName:   w.DisplayName,
				FieldWorkOLang:         w.OLang,
				FieldWorkContentRating: int64(w.ContentRating),
				FieldWorkDisplayNSFW:   w.DisplayNSFW,
			}
			// Each facet reads its own curated lane. They are separate queries
			// rather than one join because the lanes have nothing in common but
			// the work id, and a snapshot is taken once per edit — clarity is
			// worth more here than a saved round trip.
			for key, load := range map[string]func(context.Context, *gorm.DB, int64) ([]any, error){
				FieldWorkTitles:      loadTitles,
				FieldWorkIntros:      loadIntros,
				FieldWorkTagIDs:      loadTagIDs,
				FieldWorkLabels:      loadLabels,
				FieldWorkEngineIDs:   loadEngineIDs,
				FieldWorkSeriesIDs:   loadSeriesIDs,
				FieldWorkLinks:       loadLinks,
				FieldWorkCovers:      loadCovers,
				FieldWorkScreenshots: loadScreenshots,
			} {
				value, err := load(ctx, db, entityID)
				if err != nil {
					return nil, err
				}
				snapshot[key] = value
			}
			return snapshot, nil
		},
		Txn: func(ctx context.Context, fn func(tx *gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
		// OwnerSite = the claiming site (work.Site; nil = unclaimed) — what
		// the owner automerge rule compares the proposal's site against.
		OwnerSite: func(ctx context.Context, entityID int64) (*string, error) {
			var w catmodel.CatalogWork
			err := db.WithContext(ctx).Select("id", "site").First(&w, entityID).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, editing.ErrEntityNotFound
			}
			if err != nil {
				return nil, err
			}
			return w.Site, nil
		},
		// Default policy (E0): perm-gated proposals, perm-gated review, no
		// automerge. The letmoe site overlay above is the E1 tenant tuning.
		DefaultPolicy: editing.Policy{
			Propose:   editing.ProposePerm(string(perm.EditWork)),
			Review:    editing.ReviewPerm(string(perm.EditWorkReview)),
			Automerge: editing.AutomergeNever,
		},
		SiteOverlays: overlays,
		Fields: []editing.FieldSpec{
			{
				Key: FieldWorkDisplayName, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Validate: validateDisplayName,
				Apply:    applyWorkColumn("display_name", asString),
			},
			{
				Key: FieldWorkOLang, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
				Validate: validateOLang,
				Apply:    applyWorkColumn("olang", asString),
			},
			{
				Key: FieldWorkContentRating, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
				Validate: validateContentRating,
				Apply:    applyWorkColumn("content_rating", asContentRating),
			},
			{
				Key: FieldWorkTitles, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateTitles,
				Apply:    applyTitles,
			},
			{
				Key: FieldWorkIntros, Kind: editing.KindList, DiffHint: editing.DiffHintLines,
				Validate: validateIntros,
				Apply:    applyIntros,
			},
			{
				Key: FieldWorkDisplayNSFW, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
				Validate: validateBool,
				Apply:    applyWorkColumn("display_nsfw", asBool),
			},
			{
				Key: FieldWorkTagIDs, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateTagIDs,
				Apply:    applyTagIDs,
			},
			{
				Key: FieldWorkLabels, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateLabels,
				Apply:    applyLabels,
			},
			{
				Key: FieldWorkEngineIDs, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateEngineIDs,
				Apply:    applyEngineIDs,
			},
			{
				Key: FieldWorkSeriesIDs, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateSeriesIDs,
				Apply:    applySeriesIDs,
			},
			{
				Key: FieldWorkLinks, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateLinks,
				Apply:    applyLinks,
			},
			{
				Key: FieldWorkCovers, Kind: editing.KindList, DiffHint: editing.DiffHintImage,
				Validate: validateCovers,
				Apply:    applyCovers,
			},
			{
				Key: FieldWorkScreenshots, Kind: editing.KindList, DiffHint: editing.DiffHintImage,
				Validate: validateScreenshots,
				Apply:    applyScreenshots,
			},
		},
	})
}

func validateDisplayName(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("must not be empty")
	}
	if len([]rune(s)) > 500 {
		return fmt.Errorf("must be at most 500 characters")
	}
	return nil
}

func validateOLang(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	if _, allowed := olangAllowed[s]; !allowed {
		return fmt.Errorf("%q is not an allowed original language", s)
	}
	return nil
}

func validateContentRating(v any) error {
	f, ok := v.(float64)
	if !ok || f != float64(int64(f)) {
		return fmt.Errorf("must be an integer")
	}
	switch int16(f) {
	case catmodel.ContentRatingAllAges, catmodel.ContentRatingSensitive, catmodel.ContentRatingR18:
		return nil
	}
	return fmt.Errorf("must be 0 (all_ages), 1 (sensitive) or 2 (r18)")
}

// asString passes a validated string value through unchanged.
func asString(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("must be a string")
	}
	return s, nil
}

// asContentRating converts the JSON number (float64 after decode, or an
// int64 from a snapshot round-trip) to the smallint column value.
func asContentRating(v any) (any, error) {
	switch n := v.(type) {
	case float64:
		return int16(n), nil
	case int64:
		return int16(n), nil
	}
	return nil, fmt.Errorf("must be a number")
}

// applyWorkColumn writes one scalar catalog_work column on the transaction
// the engine passes in (opened via this spec's Txn). GORM's soft-delete
// scope keeps deleted works unreachable (RowsAffected 0 → entity not found),
// and the model Update touches updated_at.
func applyWorkColumn(column string, conv func(any) (any, error)) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		v, err := conv(value)
		if err != nil {
			return fmt.Errorf("editspec: %s: %w", column, err)
		}
		res := tx.WithContext(ctx).Model(&catmodel.CatalogWork{}).
			Where("id = ?", entityID).Update(column, v)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return editing.ErrEntityNotFound
		}
		return nil
	}
}
