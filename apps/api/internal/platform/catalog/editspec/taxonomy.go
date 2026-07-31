package editspec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	catmodel "api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// taxonomy.go — the NARROW registrations of the four vocabulary entities
// (03 定案 §4): catalog.label, catalog.tag, catalog.engine, catalog.series.
//
// The design ruling behind "narrow": taxonomy is not wiki content, it is the
// registry's vocabulary layer, and the evidence is its own history — 124
// taxonomy_revision rows across the whole life of two CRUD consoles. So the
// two natures are split:
//
//   - FIELD EDITING (here): the describable properties of a vocabulary entry —
//     its name, its intro, its links. Registered on the same engine as every
//     other family, which is what makes revisions, diffs, per-field policy and
//     revert arrive for free and lets taxonomy_revision retire.
//   - VOCABULARY / IDENTITY OPERATIONS (not here, and never): create, delete,
//     merge, redirect, re-category. Those are curation of the registry itself
//     (03 §0 line 3), they run on the admin curation surface, and "revert" does
//     not mean for them what it means for a field.
//
// Per-family narrowness, each with a reason:
//
//   - catalog.tag registers INTRO ONLY. A canonical tag's NAME is the join key
//     of the whole convergence layer (catalog_tag_source_map, the folksonomy
//     rows, every cross-source fold tagcanon computed); renaming one by field
//     edit would silently re-point that machinery. Names change through
//     curation, with the fold recomputed.
//   - catalog.series carries name + intro; membership is a WORK field
//     (catalog.work.series_ids), because that is where a human decides it.
//   - catalog.engine's intro is a COLUMN (catalog_engine.description) rather
//     than an intro table — the engine family never got one, and adding a table
//     to match a sibling's shape would be a migration bought with nothing.
//
// Registration only in N1: no user-reachable route exists until N2. The generic
// /internal/edit face serves these the moment they are registered, which is the
// acceptance surface for this wave.

const (
	TypeLabel  = "catalog.label"
	TypeTag    = "catalog.tag"
	TypeEngine = "catalog.engine"
	TypeSeries = "catalog.series"

	FieldLabelName   = "catalog.label.name"
	FieldLabelIntros = "catalog.label.intros"
	FieldLabelLinks  = "catalog.label.links"

	FieldTagIntros = "catalog.tag.intros"

	FieldEngineName    = "catalog.engine.name"
	FieldEngineIntro   = "catalog.engine.intro"
	FieldEngineAliases = "catalog.engine.aliases"

	FieldSeriesName   = "catalog.series.name"
	FieldSeriesIntros = "catalog.series.intros"
)

// maxNameRunes bounds a vocabulary entry's display name.
const maxNameRunes = 300

// taxonomyPolicy is the shared default for all four families: proposing and
// reviewing are both permission-gated and nothing automerges. Vocabulary is
// small, shared by every consumer of the registry, and — unlike a work's own
// fields — has no owning site to grant anyone a direct-edit lane.
func taxonomyPolicy() editing.Policy {
	return editing.Policy{
		Propose:   editing.ProposePerm(string(perm.EditTaxonomy)),
		Review:    editing.ReviewPerm(string(perm.EditTaxonomyReview)),
		Automerge: editing.AutomergeNever,
	}
}

// RegisterTaxonomy registers all four vocabulary entity types on a registry.
// db is the CATALOG pool. One entry point because the four are one decision:
// either the vocabulary layer is editable through the engine or it is not.
func RegisterTaxonomy(reg *editing.Registry, db *gorm.DB) error {
	for _, register := range []func(*editing.Registry, *gorm.DB) error{
		registerLabel, registerTag, registerEngine, registerSeries,
	} {
		if err := register(reg, db); err != nil {
			return err
		}
	}
	return nil
}

func txnOn(db *gorm.DB) func(context.Context, func(*gorm.DB) error) error {
	return func(ctx context.Context, fn func(tx *gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}

func registerLabel(reg *editing.Registry, db *gorm.DB) error {
	return reg.Register(editing.EntityTypeSpec{
		Family: "catalog", Type: TypeLabel,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			var l catmodel.CatalogLabel
			if err := firstEntity(ctx, db, &l, entityID, "id", "display_name"); err != nil {
				return nil, err
			}
			intros, err := loadEntityIntros(ctx, db, introTableLabel, entityID)
			if err != nil {
				return nil, err
			}
			links, err := loadLinksFor(ctx, db, catmodel.EntityTypeLabel, entityID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				FieldLabelName:   l.DisplayName,
				FieldLabelIntros: intros,
				FieldLabelLinks:  links,
			}, nil
		},
		Txn:           txnOn(db),
		DefaultPolicy: taxonomyPolicy(),
		Fields: []editing.FieldSpec{
			{
				Key: FieldLabelName, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Validate: validateName,
				Apply:    applyEntityColumn(&catmodel.CatalogLabel{}, "display_name", asString),
			},
			{
				Key: FieldLabelIntros, Kind: editing.KindList, DiffHint: editing.DiffHintLines,
				Validate: validateIntros,
				Apply:    applyEntityIntros(introTableLabel),
			},
			{
				Key: FieldLabelLinks, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateLinks,
				Apply: func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
					if err := firstEntity(ctx, tx, &catmodel.CatalogLabel{}, entityID, "id"); err != nil {
						return err
					}
					return applyLinksFor(ctx, tx, catmodel.EntityTypeLabel, entityID, value)
				},
			},
		},
	})
}

func registerTag(reg *editing.Registry, db *gorm.DB) error {
	return reg.Register(editing.EntityTypeSpec{
		Family: "catalog", Type: TypeTag,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			var t catmodel.CatalogTag
			if err := firstEntity(ctx, db, &t, entityID, "id", "name"); err != nil {
				return nil, err
			}
			intros, err := loadEntityIntros(ctx, db, introTableTag, entityID)
			if err != nil {
				return nil, err
			}
			return map[string]any{FieldTagIntros: intros}, nil
		},
		Txn:           txnOn(db),
		DefaultPolicy: taxonomyPolicy(),
		Fields: []editing.FieldSpec{
			{
				Key: FieldTagIntros, Kind: editing.KindList, DiffHint: editing.DiffHintLines,
				Validate: validateIntros,
				Apply:    applyEntityIntros(introTableTag),
			},
		},
	})
}

func registerEngine(reg *editing.Registry, db *gorm.DB) error {
	return reg.Register(editing.EntityTypeSpec{
		Family: "catalog", Type: TypeEngine,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			var e catmodel.CatalogEngine
			if err := firstEntity(ctx, db, &e, entityID, "id", "name", "description", "aliases"); err != nil {
				return nil, err
			}
			aliases, err := decodeAliases(e.Aliases)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				FieldEngineName:    e.Name,
				FieldEngineIntro:   e.Description,
				FieldEngineAliases: aliases,
			}, nil
		},
		Txn:           txnOn(db),
		DefaultPolicy: taxonomyPolicy(),
		Fields: []editing.FieldSpec{
			{
				Key: FieldEngineName, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Validate: validateName,
				Apply:    applyEntityColumn(&catmodel.CatalogEngine{}, "name", asString),
			},
			{
				Key: FieldEngineIntro, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
				Validate: validateIntroText,
				Apply:    applyEntityColumn(&catmodel.CatalogEngine{}, "description", asString),
			},
			{
				Key: FieldEngineAliases, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
				Validate: validateAliases,
				Apply:    applyEngineAliases,
			},
		},
	})
}

func registerSeries(reg *editing.Registry, db *gorm.DB) error {
	return reg.Register(editing.EntityTypeSpec{
		Family: "catalog", Type: TypeSeries,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			var s catmodel.CatalogSeries
			if err := firstEntity(ctx, db, &s, entityID, "id", "display_name"); err != nil {
				return nil, err
			}
			intros, err := loadEntityIntros(ctx, db, introTableSeries, entityID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				FieldSeriesName:   s.DisplayName,
				FieldSeriesIntros: intros,
			}, nil
		},
		Txn:           txnOn(db),
		DefaultPolicy: taxonomyPolicy(),
		Fields: []editing.FieldSpec{
			{
				Key: FieldSeriesName, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Validate: validateName,
				Apply:    curatedOnly(applyEntityColumn(&catmodel.CatalogSeries{}, "display_name", asString)),
			},
			{
				Key: FieldSeriesIntros, Kind: editing.KindList, DiffHint: editing.DiffHintLines,
				Validate: validateIntros,
				Apply:    applyEntityIntros(introTableSeries),
			},
		},
	})
}

// ── shared field machinery ──────────────────────────────────────────────────

func validateName(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("must not be empty")
	}
	if len([]rune(s)) > maxNameRunes {
		return fmt.Errorf("must be at most %d characters", maxNameRunes)
	}
	return nil
}

// validateIntroText is the single-column intro (engine.description). An empty
// string is legal: "this engine has no description" is a state, unlike a name.
func validateIntroText(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	if len([]rune(s)) > maxIntroRunes {
		return fmt.Errorf("must be at most %d characters", maxIntroRunes)
	}
	return nil
}

func validateAliases(v any) error {
	_, err := parseAliases(v)
	return err
}

func parseAliases(v any) ([]string, error) {
	arr, err := asArray(v, "alias strings")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for i, el := range arr {
		s, ok := el.(string)
		if !ok {
			return nil, fmt.Errorf("element %d: must be a string", i)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("element %d: must not be empty", i)
		}
		if len([]rune(s)) > maxNameRunes {
			return nil, fmt.Errorf("element %d: must be at most %d characters", i, maxNameRunes)
		}
		if _, dup := seen[s]; dup {
			return nil, fmt.Errorf("element %d: duplicate alias", i)
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}

// decodeAliases reads catalog_engine.aliases (a jsonb array) as the field
// value. A NULL / empty / malformed column reads as the empty list rather than
// failing the snapshot: an unreadable legacy value must not make the entity
// uneditable — the first edit replaces it with a well-formed array.
func decodeAliases(raw []byte) ([]any, error) {
	if len(raw) == 0 {
		return []any{}, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return []any{}, nil
	}
	out := make([]any, 0, len(names))
	for _, n := range names {
		out = append(out, n)
	}
	return out, nil
}

func applyEngineAliases(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	aliases, err := parseAliases(value)
	if err != nil {
		return fmt.Errorf("editspec: aliases: %w", err)
	}
	encoded, err := json.Marshal(aliases)
	if err != nil {
		return err
	}
	res := tx.WithContext(ctx).Model(&catmodel.CatalogEngine{}).
		Where("id = ?", entityID).Update("aliases", encoded)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return editing.ErrEntityNotFound
	}
	return nil
}

// applyEntityColumn is applyWorkColumn for any vocabulary entity: one scalar
// column, existence asserted by RowsAffected.
// curatedOnly refuses a series NAME edit on a series an importer reconciles
// (wave 155 §3.1). jobs/workseries rewrites display_name of every dlsite-sourced
// series whenever it differs from the upstream title, and DELETES the row
// outright when it falls out of the upstream set — so a human rename there is
// undone on the next run, and the revert history would point at a row that no
// longer exists. The same narrowing, for the same reason, as applySeriesIDs'
// "curated series only" rule: naming an upstream series is an identity-face
// operation (03 定案 §0 line 3), not a field edit. Curated series (source 12,
// the ones a human minted) are unaffected.
//
// Only the NAME is guarded: catalog_series_intro carries no importer writer, so
// the intro of an upstream series is a legitimate curated addition.
func curatedOnly(inner editing.ApplyFunc) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		var n int64
		if err := tx.WithContext(ctx).Model(&catmodel.CatalogSeries{}).
			Where("id = ? AND source_id = ?", entityID, curatedSourceID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("editspec: series name: only a CURATED series can be renamed here " +
				"(an upstream series' name is reconciled by its importer)")
		}
		return inner(ctx, tx, entityID, value)
	}
}

func applyEntityColumn(model any, column string, conv func(any) (any, error)) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		v, err := conv(value)
		if err != nil {
			return fmt.Errorf("editspec: %s: %w", column, err)
		}
		res := tx.WithContext(ctx).Model(model).Where("id = ?", entityID).Update(column, v)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return editing.ErrEntityNotFound
		}
		return nil
	}
}

func firstEntity(ctx context.Context, db *gorm.DB, dest any, entityID int64, columns ...string) error {
	err := db.WithContext(ctx).Select(columns).First(dest, entityID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return editing.ErrEntityNotFound
	}
	return err
}
