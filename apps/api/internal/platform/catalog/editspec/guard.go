package editspec

import (
	"context"
	"fmt"

	catmodel "api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// The MIRROR GATE (wave 155 §3.1, 03 定案 §8-3).
//
// Five facets of a CLAIMED wiki work are still reclaimed every duty-chain run
// by the W1-pre mirror steps, whose set-diff owns exactly the coordinates a
// curated edit writes into:
//
//	titles       step p — scope (claimed × kind{official,alias}); the table has
//	                      NO source column, so a human row is indistinguishable
//	                      from a mirrored one and the delete arm takes it.
//	intros ja/en step q — scope (claimed × source 12 × lang{ja,en} × prov=source),
//	                      which IS the curated lane, letter for letter.
//	display_nsfw step q — the second arm mirrors the column both ways over the
//	                      whole claimed population.
//	tag_ids      step r — scope (claimed × source {12, vndb}); curated writes 12.
//	covers /     steps m,n — fill-missing, so they delete nothing; but the claimed
//	screenshots            read face still bridges galgame_cover (an edit is
//	                       invisible) and the curated full-replace shares source
//	                       12 with the W2 rescue rows that are the ONLY thing
//	                       referencing 174k image hashes after the DROP.
//
// So until each facet's mirror step retires — the SAME-WINDOW handover N5
// performs — a human write on those coordinates is either erased by the next
// run or silently breaks the byte rescue. The gate is what keeps "one row, one
// persistent writer" true while both writers exist.
//
// Why it lives HERE and not in the engine's policy layer: an edit policy is
// keyed on (field × proposer site) and has no per-ENTITY dimension, but the
// hazard is scoped to one population — the 64,483 works with
// site='galgame_wiki' AND product_work_id IS NOT NULL. The other 164,090
// registry works share none of these writers and stay fully editable, which a
// field-level `locked` policy could not express. Apply is the only write point
// the engine hands an entity id, and it is on the path of proposal merge,
// direct edit AND revert alike (mutate.go mergeLocked), so one guard covers all
// three. The cost, accepted at acceptance: the edit-schema projection cannot
// advertise the gate, so a caller learns of it at merge time.
//
// Removing the gate is deleting the `gated(...)` wrappers — the field specs
// underneath are already the terminal ones.

// mirrorSite is the claiming site whose works the duty chain mirrors. Same
// literal the mirror steps' ownership scope binds (wikirescue.siteGalgameWiki);
// duplicated rather than imported because a jobs package is the wrong
// dependency for a spec, and it dies with the chain.
const mirrorSite = "galgame_wiki"

// mirrorGateLangs are the two intro languages step q owns. zh-Hans/zh-Hant are
// deliberately NOT here: ruling ① left the wiki's zh bodies out of the mirror,
// so the curated zh lane has no second writer and is open for editing — the one
// facet 03 §2 most wants open.
var mirrorGateLangs = map[string]struct{}{"ja": {}, "en": {}}

// MirrorGateError is the refusal a gated facet raises. The handler maps it to
// 409 (a state-of-the-world conflict, not a malformed request or a missing
// permission): the same patch becomes valid, unchanged, once the facet's mirror
// step retires.
type MirrorGateError struct {
	Field  string
	WorkID int64
}

func (e *MirrorGateError) Error() string {
	return fmt.Sprintf(
		"%s is still mirrored from the retiring wiki tables for work %d: the duty chain would revert this edit on its next run. "+
			"This facet opens in the same window its mirror step retires (wave N5)",
		e.Field, e.WorkID)
}

// isMirrored reports whether the work is inside the mirror steps' ownership
// scope. Same predicate as wikirescue.claimedSpan, minus deleted_at — GORM's
// soft-delete scope on the model already excludes deleted rows, and a deleted
// work is outside the span either way.
func isMirrored(ctx context.Context, tx *gorm.DB, entityID int64) (bool, error) {
	var n int64
	if err := tx.WithContext(ctx).Model(&catmodel.CatalogWork{}).
		Where("id = ? AND site = ? AND product_work_id IS NOT NULL", entityID, mirrorSite).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// gated wraps an Apply so it refuses outright on a mirrored work. Used by the
// facets whose every write collides (titles, display_nsfw, tag_ids, covers,
// screenshots); intros gates per language instead, inside its own Apply.
func gated(field string, inner func(context.Context, *gorm.DB, int64, any) error) func(context.Context, *gorm.DB, int64, any) error {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		mirrored, err := isMirrored(ctx, tx, entityID)
		if err != nil {
			return err
		}
		if mirrored {
			return &MirrorGateError{Field: field, WorkID: entityID}
		}
		return inner(ctx, tx, entityID, value)
	}
}

// guardIntroLangs refuses an intros write that would CHANGE what step q owns:
// the ja/en source rows of a mirrored work. A patch that carries those two
// languages unchanged — which every honest round-trip does, since the snapshot
// loads them — passes, so editing zh on a wiki work stays possible. Removing a
// ja/en row counts as a change; adding one to a work that has none does too.
func guardIntroLangs(ctx context.Context, tx *gorm.DB, entityID int64, want []workIntro) error {
	mirrored, err := isMirrored(ctx, tx, entityID)
	if err != nil || !mirrored {
		return err
	}
	var rows []catmodel.CatalogWorkIntro
	if err := tx.WithContext(ctx).
		Where("work_id = ? AND source_id = ? AND provenance = ?",
			entityID, curatedSourceID, catmodel.IntroProvenanceSource).
		Find(&rows).Error; err != nil {
		return err
	}
	have := make(map[string]string, len(rows))
	for _, r := range rows {
		if _, gated := mirrorGateLangs[r.Lang]; gated {
			have[r.Lang] = r.Intro
		}
	}
	for _, in := range want {
		if _, gated := mirrorGateLangs[in.Lang]; !gated {
			continue
		}
		existing, present := have[in.Lang]
		if !present || existing != in.Intro {
			return &MirrorGateError{Field: FieldWorkIntros + " (" + in.Lang + ")", WorkID: entityID}
		}
		delete(have, in.Lang)
	}
	// Whatever is left in `have` was dropped by this patch — also a change.
	for lang := range have {
		return &MirrorGateError{Field: FieldWorkIntros + " (" + lang + ")", WorkID: entityID}
	}
	return nil
}
