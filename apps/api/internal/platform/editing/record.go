package editing

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

type actorCtxKey struct{}

func withActor(ctx context.Context, uid int64) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, uid)
}

func ActorFromContext(ctx context.Context) int64 {
	uid, _ := ctx.Value(actorCtxKey{}).(int64)
	return uid
}

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
		if err := lockEntity(etx, entityType, entityID); err != nil {
			return err
		}
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
		return etx.Create(rev).Error
	})
	if err != nil {
		return nil, err
	}
	return rev, nil
}
