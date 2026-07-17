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

// Eternal field keys of the catalog.work pilot (§2.2b — never renamed,
// never reused). E0 keeps the set deliberately small: three scalar columns.
// List/relation fields (titles, credits) follow in E1/E2 with their own
// diff/apply semantics.
const (
	TypeWork = "catalog.work"

	FieldWorkDisplayName   = "catalog.work.display_name"
	FieldWorkOLang         = "catalog.work.olang"
	FieldWorkContentRating = "catalog.work.content_rating"
)

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
	return reg.Register(editing.EntityTypeSpec{
		Family: "catalog",
		Type:   TypeWork,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			var w catmodel.CatalogWork
			err := db.WithContext(ctx).
				Select("id", "display_name", "olang", "content_rating").
				First(&w, entityID).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, editing.ErrEntityNotFound
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{
				FieldWorkDisplayName:   w.DisplayName,
				FieldWorkOLang:         w.OLang,
				FieldWorkContentRating: int64(w.ContentRating),
			}, nil
		},
		Txn: func(ctx context.Context, fn func(tx *gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
		// E0 default policy: perm-gated proposals, perm-gated review, no
		// automerge. Site overlays (letmoe direct-editing its own mints,
		// doc 21 §2.5) arrive with E1 — the mechanism is live, the tuning
		// is a tenant decision.
		DefaultPolicy: editing.Policy{
			Propose:   editing.ProposePerm(string(perm.EditWork)),
			Review:    editing.ReviewPerm(string(perm.EditWorkReview)),
			Automerge: editing.AutomergeNever,
		},
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
