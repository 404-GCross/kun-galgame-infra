package labellogos

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

// Source is one upstream lane: which catalog_source key its anchors carry, and
// what the mirrored file is called. Nothing else differs between the two, which
// is why they share one lane instead of being two binaries — a second copy of
// the candidate/idempotency logic could disagree with this one about who
// already has a logo, and then the precedence ruling would stop holding.
type Source struct {
	Key string // catalog_source.key — also the provenance source string
	// FileStem is the mirrored basename without extension. The crawler repos
	// name their own artefact: Bangumi persons carry a "logo", Ci-en creators
	// carry an "avatar".
	FileStem string
	// UploaderSub stamps a machine identity onto first_uploader_sub so the
	// backfilled image rows are traceable (there is no human uploader).
	UploaderSub string
}

// The two lanes. Bangumi runs FIRST in production; cien then only sees labels
// whose logo_hash is still empty (see the package doc on precedence).
var (
	SourceBangumi = Source{Key: "bangumi", FileStem: "logo", UploaderSub: "system:label-logo-backfill:bangumi"}
	SourceCien    = Source{Key: "cien", FileStem: "avatar", UploaderSub: "system:label-logo-backfill:cien"}
)

// ParseSource maps the --source flag to a lane. Unknown values are an error,
// never a silent default: picking the wrong lane reads the wrong filename out
// of the mirror and stamps the wrong provenance.
func ParseSource(name string) (Source, error) {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "bangumi":
		return SourceBangumi, nil
	case "cien":
		return SourceCien, nil
	case "":
		return Source{}, fmt.Errorf("--source is REQUIRED: bangumi (brand logos) or cien (creator avatars)")
	default:
		return Source{}, fmt.Errorf("unknown --source %q (want bangumi or cien)", name)
	}
}

// registry holds the catalog_source ids this lane needs, resolved BY KEY rather
// than hardcoded so a rehearsal database with different auto-increment seeds
// still works. Both ids are resolved regardless of --source: the audit set
// needs the other lane's id to find dual-anchored labels.
type registry struct {
	bangumi int16
	cien    int16
}

func (r registry) sourceID(s Source) int16 {
	if s.Key == SourceCien.Key {
		return r.cien
	}
	return r.bangumi
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumi).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'cien'`).Scan(&r.cien).Error; err != nil {
		return r, fmt.Errorf("resolve cien source: %w", err)
	}
	if r.bangumi == 0 || r.cien == 0 {
		return r, fmt.Errorf("registry not seeded (bangumi source=%d, cien source=%d)", r.bangumi, r.cien)
	}
	return r, nil
}

// candidate is one label to give a logo, plus the field_provenance document the
// write path has to merge into. Carrying the provenance along with the row
// avoids a second SELECT per label inside the upload pool.
type candidate struct {
	LabelID         int64          `gorm:"column:label_id"`
	ExternalID      string         `gorm:"column:external_id"`
	FieldProvenance datatypes.JSON `gorm:"column:field_provenance"`
}

// loadCandidates resolves live labels with no logo that carry an EXACT anchor
// for this source:
//
//	catalog_label(deleted_at IS NULL, logo_hash = '')
//	  → catalog_external_ref(entity_type=label, source_id=<source>,
//	      link_kind=exact, external_id = bare numeric upstream id)
//
// Only EXACT anchors qualify: a probable/related link is a guess, and a guessed
// link here would put another company's logo on this brand's page — the one
// error a reader cannot detect and the catalog cannot self-correct.
//
// an empty logo_hash is the idempotency filter AND the precedence rule in one:
// a re-run skips filled labels before any byte is read, and the cien pass
// naturally sees only what bangumi left empty.
//
// DISTINCT ON keeps ONE anchor per label (lowest external_id) in the
// theoretical case a label carries several exact anchors for one source; the
// anti-squatting unique index makes that near-impossible, but slicing a
// one-row-per-label list keeps offset/limit chunking obviously correct.
// Ordering by label id (then external id) is stable across runs.
func loadCandidates(ctx context.Context, db *gorm.DB, sourceID int16) ([]candidate, error) {
	var out []candidate
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (l.id)
		       l.id AS label_id, r.external_id AS external_id, l.field_provenance AS field_provenance
		FROM catalog_label l
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = l.id
			AND r.source_id = ? AND r.link_kind = ?
		WHERE l.deleted_at IS NULL AND l.logo_hash = ''
		ORDER BY l.id, r.external_id`,
		model.EntityTypeLabel, sourceID, model.LinkKindExact).Scan(&out).Error
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
