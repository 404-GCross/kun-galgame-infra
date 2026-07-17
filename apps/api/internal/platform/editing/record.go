package editing

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// This file hosts the entity-birth record (E2a) and the actor context the
// engine hands to family Apply closures.
//
// RecordCreated is NOT a second write chain: it lands no patch and applies no
// field — the entity row is created by the family's own product write path
// (charter ruling 4 boundary), and this appends the seq=1 "created" revision
// that anchors the entity's history (the action the E0 vocabulary reserved).
// Every later write still flows through the single merge path.

// actorCtxKey carries the acting user's id into family Apply closures, so a
// family can attribute apply-level side rows (e.g. galgame link ownership)
// without widening the ApplyFunc signature. 0 = unattributed/system.
type actorCtxKey struct{}

func withActor(ctx context.Context, uid int64) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, uid)
}

// ActorFromContext returns the acting user's id inside an Apply closure
// (0 when the write carries no actor — e.g. a direct engine-tool write).
func ActorFromContext(ctx context.Context) int64 {
	uid, _ := ctx.Value(actorCtxKey{}).(int64)
	return uid
}

// RecordCreated appends the seq=1 action=created revision for an entity the
// product write path just created: snapshot = the full registered-field state,
// changedFields = the keys the creation populated (the caller computes them —
// "populated" is family semantics, e.g. galgame's delta-vs-empty-snapshot).
// It refuses to run on an entity that already has revisions.
func (e *Engine) RecordCreated(ctx context.Context, entityType string, entityID int64, actor PolicyContext, changedFields []string) (*Revision, error) {
	spec, err := e.resolveSpec(entityType)
	if err != nil {
		return nil, err
	}
	for _, key := range changedFields {
		if _, ok := spec.Field(key); !ok {
			return nil, &UnknownFieldError{Key: key}
		}
	}
	snapshot, err := spec.LoadSnapshot(withActor(ctx, actor.UserID), entityID)
	if err != nil {
		return nil, err
	}
	rawSnapshot, err := encodeJSON(snapshot)
	if err != nil {
		return nil, err
	}
	if changedFields == nil {
		changedFields = []string{}
	}
	rawChanged, err := encodeJSON(changedFields)
	if err != nil {
		return nil, err
	}
	var rev *Revision
	err = e.db.WithContext(ctx).Transaction(func(etx *gorm.DB) error {
		seq, err := maxRevisionSeq(etx, spec, entityID)
		if err != nil {
			return err
		}
		if seq != 0 {
			return fmt.Errorf("editing: %s/%d already has revisions (seq %d) — RecordCreated is birth-only", spec.Type, entityID, seq)
		}
		rev = &Revision{
			EntityFamily: spec.Family, EntityType: spec.Type, EntityID: entityID,
			Seq: 1, Action: ActionCreated,
			ChangedFields: rawChanged, Snapshot: rawSnapshot,
			ActorUID: actor.UserID, Site: actor.Site,
		}
		// The (entity_ref, seq) unique index turns a concurrent birth record
		// into a hard error instead of forked history.
		return etx.Create(rev).Error
	})
	if err != nil {
		return nil, err
	}
	return rev, nil
}
