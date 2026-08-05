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
	// LinkKind and MatchedBy are the anchor predicate: which catalog_external_ref
	// rows count as "this label IS that upstream entity" for THIS lane. The two
	// lanes differ here, and the asymmetry is deliberate — see the vars below.
	// An empty MatchedBy means "any rule with this link_kind".
	LinkKind  int16
	MatchedBy []string
}

// The two lanes. Bangumi runs FIRST in production; cien then only sees labels
// whose logo_hash is still empty (see the package doc on precedence).
//
// THE ANCHOR PREDICATE IS ASYMMETRIC, and it has to be. Bangumi label anchors
// are ordinary identity anchors, so exact is exact. Ci-en has NO exact label
// anchors in the catalog at all: every one of its 2,537 rows is
// link_kind=related, because BOTH writers file Ci-en as web presence rather
// than identity — the orglabels link facet by charter (non-identity links:
// official site, twitter, ci-en) and the Ci-en projection alongside it.
// Requiring exact here selects ZERO rows, which is precisely what the first
// acceptance run measured.
//
// Accepting those two related rules is not a relaxation of the identity bar; it
// is where the identity actually lives. Both are first-party self-declarations
// riding on an already-exact anchor:
//
//	rule:eg-cien    1,105  ErogameScape's brands.cien column on a label already
//	                       exactly anchored to that EG brand — the brand's own
//	                       statement of its own Ci-en page.
//	rule:cien-self  1,432  the Ci-en profile declaring its own DLsite maker ids,
//	                       resolved back to the label from the other end.
//
// The rules are PINNED rather than accepting related generally: `related` also
// carries official sites, twitter handles and the pixiv/dmm/steam whitelist,
// and a twitter avatar is not a label logo.
var (
	SourceBangumi = Source{
		Key: "bangumi", FileStem: "logo", UploaderSub: "system:label-logo-backfill:bangumi",
		LinkKind: model.LinkKindExact,
	}
	SourceCien = Source{
		Key: "cien", FileStem: "avatar", UploaderSub: "system:label-logo-backfill:cien",
		LinkKind: model.LinkKindRelated, MatchedBy: cienAnchorRules,
	}
)

// cienAnchorRules are the matched_by tags that make a related Ci-en link
// identity-grade for this lane. Duplicated (not imported) to keep the job
// boundary clean; they are persisted contract values and must stay
// byte-identical to orglabels.ruleEGCien / orglabels.ruleCienSelf.
var cienAnchorRules = []string{"rule:eg-cien", "rule:cien-self"}

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

// loadCandidates resolves live labels with no logo that carry an identity-grade
// anchor for this source:
//
//	catalog_label(deleted_at IS NULL, logo_hash = '')
//	  → catalog_external_ref(entity_type=label, source_id=<source>,
//	      <the lane's anchor predicate>, external_id = bare numeric upstream id)
//
// The anchor predicate is the lane's, NOT a constant — bangumi wants
// link_kind=exact (any rule), cien wants link_kind=related AND matched_by in
// ('rule:eg-cien','rule:cien-self'). See the Source vars for why the asymmetry
// is correct rather than a loosened bar; the short version is that Ci-en has no
// exact label anchors in the catalog at all, so an exact-only filter here
// selects zero rows, and the two related rules it accepts are first-party
// self-declarations riding on an already-exact anchor.
//
// What is NOT accepted is a bare probable link or `related` in general: a
// guessed link would put another company's logo on this brand's page, the one
// error a reader cannot detect and the catalog cannot self-correct.
//
// an empty logo_hash is the idempotency filter AND the precedence rule in one:
// a re-run skips filled labels before any byte is read, and the cien pass
// naturally sees only what bangumi left empty.
//
// DISTINCT ON keeps ONE anchor per label (lowest external_id) in the case a
// label carries several qualifying anchors for one source; slicing a
// one-row-per-label list keeps offset/limit chunking obviously correct.
// Ordering by label id (then external id) is stable across runs.
func loadCandidates(ctx context.Context, db *gorm.DB, sourceID int16, src Source) ([]candidate, error) {
	clause, args := anchorClause("r", sourceID, src)
	var out []candidate
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (l.id)
		       l.id AS label_id, r.external_id AS external_id, l.field_provenance AS field_provenance
		FROM catalog_label l
		JOIN catalog_external_ref r ON r.entity_id = l.id AND `+clause+`
		WHERE l.deleted_at IS NULL AND l.logo_hash = ''
		ORDER BY l.id, r.external_id`, args...).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// anchorClause builds one lane's anchor predicate for an aliased
// catalog_external_ref, so the candidate query and the audit query cannot drift
// apart on what counts as an anchor. Only the alias is interpolated — a fixed
// identifier chosen here, never user input; every value is bound. GORM expands
// a slice bound to `IN ?` into one placeholder per element.
func anchorClause(alias string, sourceID int16, src Source) (string, []any) {
	q := fmt.Sprintf(`%[1]s.entity_type = ? AND %[1]s.source_id = ? AND %[1]s.link_kind = ?`, alias)
	args := []any{model.EntityTypeLabel, sourceID, src.LinkKind}
	if len(src.MatchedBy) > 0 {
		q += fmt.Sprintf(` AND %s.matched_by IN ?`, alias)
		args = append(args, src.MatchedBy)
	}
	return q, args
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
