package editspec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"

	"gorm.io/gorm"
)

// RegisterGame registers the galgame.game entity type on an engine registry.
// db is the GALGAME pool — the closures are the only key→column translation
// layer for this family.
//
// Policy mirrors the old kungal write face (03 号裁定 1 — mirror, don't
// invent): any logged-in user could submit a PR → propose=open; merge /
// decline / revert were owner-or-admin → review=perm:edit.galgame.game.review
// (the strangler adapter grants it situationally for entity creators);
// direct edits automerge for the adapter's asserted-trusted actors
// (automerge=trusted — the old-wire PR route asserts tier 0, the old-wire
// direct-edit routes assert tier 2 after their own permission checks).
// Exceptions: bid is locked (sync-managed, never user-editable — old
// changed_fields never once contains it), vndb_id is perm-gated (squatting
// lesson; the literal doc-21 "locked" would also break draft self-fixes,
// see the E2a report), status is the perm-gated management direct-edit
// field (approve/ban/unban/decline land as engine direct edits).
func RegisterGame(reg *editing.Registry, db *gorm.DB) error {
	reviewRule := editing.ReviewPerm(string(perm.EditGameReview))

	lockedPolicy := &editing.Policy{
		Propose:   editing.ProposeLocked,
		Review:    reviewRule,
		Automerge: editing.AutomergeNever,
	}
	vndbPolicy := &editing.Policy{
		Propose:   editing.ProposePerm(string(perm.EditGameVNDBID)),
		Review:    reviewRule,
		Automerge: editing.AutomergeTrusted,
	}
	statusPolicy := &editing.Policy{
		Propose:   editing.ProposePerm(string(perm.EditGameStatus)),
		Review:    editing.ReviewPerm(string(perm.EditGameStatus)),
		Automerge: editing.AutomergeTrusted,
	}

	fields := []editing.FieldSpec{
		{Key: FieldVNDBID, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
			Policy: vndbPolicy, Validate: validateVNDBID, Apply: applyGameColumn("vndb_id", convString)},
		{Key: FieldBID, Kind: editing.KindInt, DiffHint: editing.DiffHintInline,
			Policy: lockedPolicy, Validate: validateNullableInt(1), Apply: applyGameColumn("bid", convNullableInt)},
		{Key: FieldReleaseDate, Kind: editing.KindDate, DiffHint: editing.DiffHintInline,
			Validate: validateReleaseDate, Apply: applyGameColumn("release_date", convReleaseDate)},
		{Key: FieldReleaseDateTBA, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
			Validate: validateBool, Apply: applyGameColumn("release_date_tba", convBool)},
		{Key: FieldReleasePrecision, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
			Validate: validateReleasePrecision, Apply: applyGameColumn("release_precision", convReleasePrecision)},
		{Key: FieldNameEnUS, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
			Validate: validateStringMax(1000), Apply: applyGameColumn("name_en_us", convString)},
		{Key: FieldNameJaJP, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
			Validate: validateStringMax(1000), Apply: applyGameColumn("name_ja_jp", convString)},
		{Key: FieldNameZhCN, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
			Validate: validateStringMax(1000), Apply: applyGameColumn("name_zh_cn", convString)},
		{Key: FieldNameZhTW, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
			Validate: validateStringMax(1000), Apply: applyGameColumn("name_zh_tw", convString)},
		{Key: FieldBanner, Kind: editing.KindImageHash, DiffHint: editing.DiffHintImage,
			Validate: validateStringMax(1000), Apply: applyGameColumn("banner", convString)},
		{Key: FieldIntroEnUS, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
			Validate: validateStringMax(introMaxRunes), Apply: applyGameColumn("intro_en_us", convIntro)},
		{Key: FieldIntroJaJP, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
			Validate: validateStringMax(introMaxRunes), Apply: applyGameColumn("intro_ja_jp", convIntro)},
		{Key: FieldIntroZhCN, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
			Validate: validateStringMax(introMaxRunes), Apply: applyGameColumn("intro_zh_cn", convIntro)},
		{Key: FieldIntroZhTW, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
			Validate: validateStringMax(introMaxRunes), Apply: applyGameColumn("intro_zh_tw", convIntro)},
		{Key: FieldContentLimit, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
			Validate: validateContentLimit, Apply: applyGameColumn("content_limit", convString)},
		{Key: FieldOriginalLanguage, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
			Validate: validateShortString, Apply: applyGameColumn("original_language", convString)},
		{Key: FieldAgeLimit, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
			Validate: validateShortString, Apply: applyGameColumn("age_limit", convString)},
		{Key: FieldSeriesID, Kind: editing.KindRef, DiffHint: editing.DiffHintInline,
			Validate: validateNullableInt(1), Apply: applyGameColumn("series_id", convNullableInt)},
		{Key: FieldAliases, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
			Validate: validateAliases, Apply: applyAliases},
		{Key: FieldTagIDs, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
			Validate: validateIDList, Apply: applyIDList(FieldTagIDs)},
		{Key: FieldOfficialIDs, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
			Validate: validateIDList, Apply: applyIDList(FieldOfficialIDs)},
		{Key: FieldEngineIDs, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
			Validate: validateIDList, Apply: applyIDList(FieldEngineIDs)},
		{Key: FieldLinks, Kind: editing.KindList, DiffHint: editing.DiffHintItems,
			Validate: validateLinks, Apply: applyLinks},
		{Key: FieldCovers, Kind: editing.KindList, DiffHint: editing.DiffHintImage,
			Validate: validateCovers, Apply: applyCovers},
		{Key: FieldScreenshots, Kind: editing.KindList, DiffHint: editing.DiffHintImage,
			Validate: validateScreenshots, Apply: applyScreenshots},
		{Key: FieldStatus, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
			Policy: statusPolicy, Validate: validateStatus, Apply: applyGameColumn("status", convStatus)},
	}

	// kungal overlay (E3a ruling 2): every DEFAULT-POLICY field files an
	// open proposal into the kungal review queue — propose mirrors the old
	// wiki SubmitPR (any logged-in user), review holds the galgame review
	// perm, and nothing automerges. OwnerReview (E3b ruling 2) additionally
	// lets the entity's asserted owner adjudicate these default keys — the
	// old wire's owner-merge privilege, migrated as an explicit overlay
	// capability. Fields carrying their own policy (bid locked, vndb_id,
	// status) are deliberately NOT overlaid — the E2a posture stands on
	// every site, and owners never adjudicate the special keys.
	kungalPolicy := editing.Policy{
		Propose:     editing.ProposeOpen,
		Review:      reviewRule,
		Automerge:   editing.AutomergeNever,
		OwnerReview: true,
	}
	kungalOverlay := make(map[string]editing.Policy, len(fields))
	for _, f := range fields {
		if f.Policy == nil {
			kungalOverlay[f.Key] = kungalPolicy
		}
	}

	return reg.Register(editing.EntityTypeSpec{
		Family:       "galgame",
		Type:         TypeGame,
		LoadSnapshot: gameSnapshotLoader(db),
		Txn: func(ctx context.Context, fn func(tx *gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
		DefaultPolicy: editing.Policy{
			Propose:   editing.ProposeOpen,
			Review:    reviewRule,
			Automerge: editing.AutomergeTrusted,
		},
		Fields:       fields,
		SiteOverlays: map[string]map[string]editing.Policy{SiteKungal: kungalOverlay},
	})
}

// gameSnapshotLoader builds the LoadSnapshot closure: full galgame + relations
// → model.TakeSnapshot → JSON round-trip → eternal-key map + the status
// column (the one registered field outside the historical snapshot).
func gameSnapshotLoader(db *gorm.DB) func(ctx context.Context, entityID int64) (map[string]any, error) {
	return func(ctx context.Context, entityID int64) (map[string]any, error) {
		g, err := loadGameWithRelations(db.WithContext(ctx), int(entityID))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, editing.ErrEntityNotFound
			}
			return nil, err
		}
		raw, err := json.Marshal(model.TakeSnapshot(g))
		if err != nil {
			return nil, err
		}
		var oldKeyed map[string]any
		if err := json.Unmarshal(raw, &oldKeyed); err != nil {
			return nil, err
		}
		out := make(map[string]any, len(oldKeyed)+1)
		for oldKey, v := range oldKeyed {
			newKey, ok := OldToNew[oldKey]
			if !ok {
				// A snapshot key without an eternal-key mapping means the model
				// grew a field this registry never learned — fail loud.
				return nil, fmt.Errorf("editspec: snapshot key %q has no galgame.game field key", oldKey)
			}
			out[newKey] = v
		}
		// Status is a real registered field with no old-snapshot slot; keep it
		// float64 so every value in the map is a decoded-JSON shape.
		out[FieldStatus] = float64(g.Status)
		return out, nil
	}
}

// loadGameWithRelations mirrors service.loadGalgameWithRelations' preload set
// (same relation ORDER BYs — Link by insertion id, Cover/Screenshot by
// sort_order then created) so the engine snapshot is byte-identical to the
// snapshots the old write paths recorded. Duplicated here because the service
// layer imports editspec, not the other way around.
func loadGameWithRelations(tx *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := tx.
		Preload("Alias").
		Preload("Tag").
		Preload("Official").
		Preload("Engine").
		Preload("Link", func(db *gorm.DB) *gorm.DB {
			return db.Order("id ASC")
		}).
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Preload("Screenshot", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		First(&g, id).Error
	return &g, err
}
