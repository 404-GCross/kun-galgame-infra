package personphotos

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// resolveSourceID looks the bangumi source up BY KEY rather than hardcoding its
// id, so a rehearsal database seeded with different auto-increment values still
// works. An unseeded registry is a hard error: id 0 would quietly match nothing
// and the run would report a healthy zero-candidate forecast.
func resolveSourceID(ctx context.Context, db *gorm.DB) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKey).Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("resolve %s source: %w", sourceKey, err)
	}
	if id == 0 {
		return 0, fmt.Errorf("registry not seeded (%s source not found in catalog_source)", sourceKey)
	}
	return id, nil
}

// candidate is one person to give a photo, plus the field_provenance document
// the write path has to merge into. Carrying the provenance along with the row
// avoids a second SELECT per person inside the upload pool.
type candidate struct {
	PersonID        int64          `gorm:"column:person_id"`
	ExternalID      string         `gorm:"column:external_id"`
	FieldProvenance datatypes.JSON `gorm:"column:field_provenance"`
}

// loadCandidates resolves live persons with no photo that carry an EXACT
// bangumi identity anchor:
//
//	catalog_person(deleted_at IS NULL, photo_hash = '')
//	  → catalog_external_ref(entity_type=person, source_id=bangumi,
//	      link_kind=exact, external_id = bare numeric upstream id)
//
// Nothing looser qualifies — not a probable link, not a related one. A guessed
// link would put a stranger's face on this person's page.
//
// An empty photo_hash is the idempotency filter: a re-run skips filled persons
// before any byte is read.
//
// DISTINCT ON keeps ONE anchor per person (lowest external_id) in case a person
// carries several bangumi anchors; slicing a one-row-per-person list keeps
// offset/limit chunking obviously correct. Ordering by person id (then external
// id) is stable across runs.
func loadCandidates(ctx context.Context, db *gorm.DB, sourceID int16) ([]candidate, error) {
	var out []candidate
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (p.id)
		       p.id AS person_id, r.external_id AS external_id, p.field_provenance AS field_provenance
		FROM catalog_person p
		JOIN catalog_external_ref r ON r.entity_id = p.id
		     AND r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
		WHERE p.deleted_at IS NULL AND p.photo_hash = ''
		ORDER BY p.id, r.external_id`,
		model.EntityTypePerson, sourceID, model.LinkKindExact).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// writeIDs writes the distinct external ids whose bytes are NOT in the mirror
// yet — one per line, sorted, so re-running a dry run produces a byte-identical
// worklist. That is precisely the crawler's ids file: ids already mirrored are
// omitted so a resumed fetch does not re-hit the upstream API for bytes sitting
// on disk. Returns how many ids were written.
func writeIDs(path string, cands []candidate, m *mirror) (int, error) {
	seen := map[string]bool{}
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		if seen[c.ExternalID] || m.has(c.ExternalID) {
			continue
		}
		seen[c.ExternalID] = true
		ids = append(ids, c.ExternalID)
	}
	sort.Strings(ids)
	body := ""
	if len(ids) > 0 {
		body = strings.Join(ids, "\n") + "\n"
	}
	return len(ids), os.WriteFile(path, []byte(body), 0o644)
}
