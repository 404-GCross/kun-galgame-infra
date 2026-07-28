// Package orglabels lands the E2 org/label wave (refs/proj/83): it anchors the
// catalog_label registry to the three upstream org sources (VNDB producers /
// Bangumi companies+groups / erogamespace brands) and enriches the anchored
// labels with intros, aliases and external links.
//
// Doctrine (refs/proj/83 裁定):
//
//   - The aggregation subject is catalog_label; catalog_org is NOT built this
//     wave. An anchor is a catalog_external_ref(entity_type=label) row.
//   - Anchoring is STRUCTURE-FIRST: for a source org O with catalog-work set
//     W(O) and a label L with work set W(L), s=|W(O)∩W(L)| grades the link
//     (s≥2 exact / s==1+name exact / s==0+name probable). Name equality is the
//     NFKC fold, computed IN-SQL (lower(normalize(x,NFKC))) so it is
//     byte-identical to the *_norm generated columns (bgmtype4gated precedent).
//   - A source org with works but no matching label mints a new label (+ self
//     anchor + imported revision + work_label edges). VNDB type=in (persons)
//     NEVER mint a label — that would smuggle the frozen person-identity
//     resolution into the label space.
//
// Writes are fill-missing + idempotent: every anchor goes through
// InsertRefIfAbsent (ON CONFLICT DO NOTHING), and an org whose external_id is
// already anchored is skipped, so a second --apply run writes zero.
package orglabels

import (
	"fmt"
	"regexp"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Source ids — pinned by the catalog_source seed (registry.go / seed.go). The
// job resolves them by key at runtime too (resolveSourceID); these mirror the
// seeded values for the three org sources and the three link targets.
const (
	sourceVNDB    int16 = 2
	sourceBangumi int16 = 3
	sourceDlsite  int16 = 4
	sourceEG      int16 = 5

	sourceOfficialSite int16 = 9
	sourceTwitter      int16 = 10
	sourceCien         int16 = 14
)

// matched_by rule ids (rule:<src>-<entity>-<action> convention, importer.go).
const (
	ruleVNDBCoworks    = "rule:vndb-org-coworks"
	ruleVNDBCoworkName = "rule:vndb-org-cowork-name"
	ruleVNDBNameOnly   = "rule:vndb-org-name-only"
	ruleVNDBNew        = "rule:vndb-org-new"

	ruleBGMCoworks    = "rule:bangumi-org-coworks"
	ruleBGMCoworkName = "rule:bangumi-org-cowork-name"
	ruleBGMNameOnly   = "rule:bangumi-org-name-only"
	ruleBGMNew        = "rule:bangumi-org-new"

	ruleEGCoworks    = "rule:eg-org-coworks"
	ruleEGCoworkName = "rule:eg-org-cowork-name"
	ruleEGNameOnly   = "rule:eg-org-name-only"
	ruleEGNew        = "rule:eg-org-new"
)

// ruleSet bundles the four matched_by strings for one source so the core
// grader stays source-agnostic.
type ruleSet struct {
	coworks, coworkName, nameOnly, newLabel string
}

// srcKey maps a source id to its registry key (for FieldProvenance / logs).
func srcKey(source int16) string {
	switch source {
	case sourceVNDB:
		return "vndb"
	case sourceBangumi:
		return "bangumi"
	case sourceEG:
		return "erogamespace"
	default:
		return fmt.Sprintf("source-%d", source)
	}
}

// Opts configures an anchor or enrich run.
type Opts struct {
	Apply     bool
	DSN       string // catalog DSN — REQUIRED (also hosts src_vndb / src_bangumi)
	EGDSN     string // erogamespace DSN — defaults to DSN with dbname=erogamespace
	DlsiteDSN string // dlsite DSN — cien facet only (hosts cien_profiles, read-only)
	Source    string // anchor: vndb | bangumi | eg | all
	Facet     string // enrich: intro | alias | link | all | cien
	Limit     int    // cap orgs processed per source (0 = all); debugging aid
}

// openGorm opens a silent-logger GORM handle (enrich-precedent convention).
func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
}

var reDBName = regexp.MustCompile(`dbname=\S+`)

// deriveEGDSN swaps the dbname of a key-value catalog DSN to erogamespace. The
// EG source tables (brands / games) live in their own database on the same
// server, never in the catalog database.
func deriveEGDSN(catalogDSN string) (string, error) {
	if !reDBName.MatchString(catalogDSN) {
		return "", fmt.Errorf("cannot derive eg dsn: no dbname= in catalog dsn")
	}
	return reDBName.ReplaceAllString(catalogDSN, "dbname=erogamespace"), nil
}

// openPools opens the catalog pool and, when needed, the erogamespace pool.
func openPools(opts Opts, needEG bool) (catalog, eg *gorm.DB, err error) {
	if opts.DSN == "" {
		return nil, nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess the target database")
	}
	catalog, err = openGorm(opts.DSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open catalog pool: %w", err)
	}
	if needEG {
		egDSN := opts.EGDSN
		if egDSN == "" {
			if egDSN, err = deriveEGDSN(opts.DSN); err != nil {
				return nil, nil, err
			}
		}
		eg, err = openGorm(egDSN)
		if err != nil {
			return nil, nil, fmt.Errorf("open erogamespace pool: %w", err)
		}
	}
	return catalog, eg, nil
}

// resolveSourceID looks up a source id by key, failing loudly if the seed is
// missing (never silently guess a vocabulary id).
func resolveSourceID(db *gorm.DB, key string) (int16, error) {
	var id int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("resolve source %q: %w", key, err)
	}
	if id == 0 {
		return 0, fmt.Errorf("source %q not seeded — run migrate-catalog first", key)
	}
	return id, nil
}

// nowUTC is a package seam so provenance timestamps are stable in tests.
var nowUTC = func() time.Time { return time.Now().UTC() }
