package catalogsync

import (
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// rejectionSet is the preloaded negative-knowledge index (doc 17 R7): every
// human-rejected (entity, source, external_id) pairing that a re-run MUST NOT
// re-assert. It is consulted by every ref-writing phase BEFORE the write — a
// hit is skipped and counted, never written. The table is small (one row per
// human rejection), so a single up-front SELECT into memory is cheap and keeps
// the hot loops allocation-free.
//
// Scope: this gates CROSS-SOURCE match assertions (a wiki/eg/bangumi identity
// claim on a work). Self-identity import anchors (rule:*-import — a row whose
// existence IS the identity) are never rejectable and never consulted; see
// WorkService.ClaimWork.
type rejectionSet map[rejectionKey]struct{}

type rejectionKey struct {
	entityType int16
	entityID   int64
	sourceID   int16
	externalID string
}

// Has reports whether (entity, source, external_id) is recorded negative
// knowledge. A nil/empty set answers false for everything — the exact
// zero-rejection behavior that keeps a fresh database's runs byte-identical.
func (s rejectionSet) Has(entityType int16, entityID int64, sourceID int16, externalID string) bool {
	_, ok := s[rejectionKey{entityType, entityID, sourceID, externalID}]
	return ok
}

// loadRejections reads the whole rejection table into a set, once per run.
func loadRejections(catalog *gorm.DB) (rejectionSet, error) {
	var rows []model.CatalogMatchRejection
	if err := catalog.Select("entity_type", "entity_id", "source_id", "external_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	set := make(rejectionSet, len(rows))
	for _, r := range rows {
		set[rejectionKey{r.EntityType, r.EntityID, r.SourceID, r.ExternalID}] = struct{}{}
	}
	return set, nil
}
