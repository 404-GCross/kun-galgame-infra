// Package wikirescue lands the data-layer-retirement W0 wave
// (refs/plans/10-data-layer-retirement/00-charter.md, refs/proj/121): the
// one-shot rescue of every wiki-family row that has NO upstream to regenerate
// it from, executed before the galgame_* table family is dropped.
//
// The charter's core principle is that the wiki tables are overwhelmingly
// VNDB-derived and can be discarded wholesale; this package moves the
// exceptions — hand-curated engines, user-uploaded screenshots, user-entered
// links, three families of hand-written descriptions, the unprojected official
// tail, the user-original works, and the gid→work address map.
//
// Steps j..l extend that last idea (A2-0, refs/proj/127): the official, engine
// and tag id spaces are addressable today only by joining a wiki table, so they
// are filed into catalog_external_ref before the join disappears.
//
// Steps m..o are the W2 image-byte retirement (refs/proj/128,
// refs/plans/10-data-layer-retirement/01-w2-image-bytes.md) and invert the
// charter's discard rule for one reason: the wiki cover and screenshot tables are
// the ONLY thing referencing 174,843 image-service hashes, and an unreferenced
// hash is one GC cycle away from having its bytes deleted. The rows are
// regenerable; the bytes are not. So m and n project both media tables WHOLE, and
// o gives the catalog site an ownership row for every hash the catalog face now
// references, which is what lets the existing catalog reference ping take over
// from the wiki one. None of the three touches the galgame_wiki image client.
//
// Steps p..t are the W1-pre read-time-bridge nativization (refs/proj/140,
// refs/plans/10-data-layer-retirement/02-w1pre-bridge-nativization.md) and are a
// different KIND of step: MIRRORS, not fills. The catalog read face used to bridge
// a claimed work's titles / intros / tags / ratings / popularity / display flag out of the wiki
// tables at read time; these steps materialize those projections verbatim so the
// face can read one lane for every work and the tables can be dropped. Because a
// mirror must also drop what the wiki dropped, they own a real set-diff — insert,
// update AND delete — inside a declared ownership scope. See mirror.go.
//
// Three rules every step obeys:
//
//   - FILL-MISSING, NEVER OVERWRITE (a..o). Every write is ON CONFLICT DO
//     NOTHING, so a second pass writes zero rows. That is the acceptance
//     criterion, not an optimization: these steps run against production once, and
//     a re-run must be provably inert. The mirror steps p..t reach the same
//     acceptance criterion by converging: a second pass decides zero changes.
//   - ANCHORED IS THE DENOMINATOR. A wiki row only projects when its galgame
//     row carries a catalog_work_id. Unanchored rows are PARKED to a JSON
//     artifact with their full original text, never silently dropped — the
//     charter accepts parking as a legitimate outcome.
//   - WORK-FACE WRITES TOUCH. Anything landing on a work's facet tables bumps
//     catalog_work.updated_at through repository.TouchWorks in the SAME
//     transaction, so the public changes feed sees it. Only works that really
//     received a row are touched (TouchWorks' documented contract).
//
// Pool discipline mirrors internal/jobs/galgametouch: galgame_* reads run on
// the galgame handle, catalog_* writes on the catalog handle. The two families
// sharing one physical database is a deployment posture, not a code contract.
package wikirescue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
)

// sourceKeyWiki is the catalog_source key every row this wave writes is
// attributed to. Resolved to an id at run time rather than hardcoded — the
// seed owns the number (orglabels' resolveSourceID pattern).
const sourceKeyWiki = "galgame_wiki"

// sourceKeyWeb is the generic external-webpage source the link step falls back
// to. Seeded by this same wave (seed.sources), so an unresolvable key here
// means migrate-catalog has not been run yet.
const sourceKeyWeb = "web"

// langZhHans is the BCP-47 tag every rescued description is filed under. The
// wiki's description fields are Simplified Chinese by editorial convention and
// carry no per-row language marker.
const langZhHans = "zh-Hans"

// paramChunk bounds one multi-row INSERT's bind parameters. Postgres caps a
// statement at 65,535 parameters; the per-step column count divides into this.
const paramChunk = 60000

// Opts configures one run.
type Opts struct {
	// Apply switches from planning to writing. Dry run is the default
	// everywhere in this repo's one-shot tools.
	Apply bool
	// Step selects one lettered step, a comma-separated selection ("m,n,o"), or
	// "all" for the charter order. See ParseSteps.
	Step string
	// ArtifactDir receives the parked-row JSON files. Empty disables parking
	// output (the counts are still reported).
	ArtifactDir string
}

// Stats is one step's ledger. Planned counts rows the step WANTED to write;
// Written counts rows that actually landed (ON CONFLICT DO NOTHING makes the
// difference visible), and a converged re-run reports Planned>0 Written=0.
type Stats struct {
	Step string `json:"step"`
	// Source is how many rows the step read from the wiki side.
	Source int `json:"source"`
	// Anchored is how many of those had a catalog_work/label/series target.
	Anchored int `json:"anchored"`
	// Parked is how many were written to the artifact instead.
	Parked int `json:"parked"`
	// Planned is how many INSERTs were built, Written how many landed.
	Planned int `json:"planned"`
	Written int `json:"written"`
	// Touched is how many catalog_work rows had updated_at bumped.
	Touched int `json:"touched"`
	// Created counts newly minted entities (labels in G, works in H).
	Created int `json:"created"`
	// Linked counts bridge-column write-backs (G/H anchor repair).
	Linked int `json:"linked"`

	// The MIRROR ledger (steps p..t, refs/proj/140). Insert/Update/Delete/Already
	// are the DECIDED set-diff and read the same in dry and apply, so a dry run is
	// an exact forecast; Written is the rows a --apply run really changed
	// (inserted + updated + deleted). A converged re-run reports
	// Insert=Update=Delete=0 and Already = the whole desired set.
	Insert  int `json:"insert,omitempty"`
	Update  int `json:"update,omitempty"`
	Delete  int `json:"delete,omitempty"`
	Already int `json:"already,omitempty"`
	// Skipped counts desired rows a step declined to write because the row's key
	// is held by another owner (step q: an intromt machine row).
	Skipped int `json:"skipped,omitempty"`
	// Vocab counts vocabulary-column corrections outside the work facets (step r:
	// catalog_tag.sexual).
	Vocab int `json:"vocab,omitempty"`
	// Scalar counts work-SCALAR column corrections — a column on catalog_work
	// itself, where a mirror can only update (step q: display_nsfw).
	Scalar int `json:"scalar,omitempty"`

	// Note carries a step-specific remark for the report.
	Note string `json:"note,omitempty"`
}

// Runner holds the two pools and the resolved source ids for a run.
type Runner struct {
	galgame *gorm.DB
	catalog *gorm.DB
	// images is the kun_images pool, attached by WithImages and needed only by
	// the image-usage step; nil for every other run.
	images   *gorm.DB
	opts     Opts
	wikiSrc  int16
	webSrc   int16
	artifact string
	// now is the run's single clock: every row a mirror step writes carries the
	// same created_at/updated_at, so one run is one watermark on the changes feed.
	now time.Time
}

// New wires a runner and resolves the source-registry ids it needs. It fails
// loudly when a source key is missing rather than defaulting to 0 — an
// unseeded key means migrate-catalog has not run, and writing source_id=0
// would silently mis-attribute every row.
func New(galgame, catalog *gorm.DB, opts Opts) (*Runner, error) {
	wikiSrc, err := resolveSourceID(catalog, sourceKeyWiki)
	if err != nil {
		return nil, err
	}
	webSrc, err := resolveSourceID(catalog, sourceKeyWeb)
	if err != nil {
		return nil, err
	}
	return &Runner{
		galgame: galgame, catalog: catalog, opts: opts,
		wikiSrc: wikiSrc, webSrc: webSrc, artifact: opts.ArtifactDir,
		now: time.Now().UTC(),
	}, nil
}

// resolveSourceID looks a catalog_source key up by key (orglabels' pattern).
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

// steps is the charter's execution order. A is the schema step and belongs to
// cmd/migrate-catalog; A's DATA migration is the "a" step here. j..l are the
// A2-0 registrar rescue (refs/proj/127): the three taxonomy id maps that, like
// the gid map, only exist today as a joinable wiki table. m..o are the W2
// image-byte retirement (refs/proj/128) and are normally selected explicitly
// ("--step m,n,o"), since a..l are already converged in production.
//
// p..t are the W1-pre read-time-bridge nativization (refs/proj/140): the MIRROR
// steps that move the title / intro (+ display flag) / tag / rating / popularity facets of a
// CLAIMED work out of the wiki tables and into the catalog's own, so the read face
// can stop bridging. p,q,r,s are idempotent followers and are the ones the daily
// chain runs ("--step p,q,r,s"); t is a ONE-SHOT adoption and must not be re-run
// (see stepMetaAdopt).
var steps = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t"}

// Run dispatches the selected step(s) and returns one Stats per step executed.
func (r *Runner) Run(ctx context.Context) ([]Stats, error) {
	selected, err := ParseSteps(r.opts.Step)
	if err != nil {
		return nil, err
	}
	out := make([]Stats, 0, len(selected))
	for _, s := range selected {
		st, err := r.runStep(ctx, s)
		if err != nil {
			return out, fmt.Errorf("step %s: %w", s, err)
		}
		out = append(out, st)
	}
	return out, nil
}

func (r *Runner) runStep(ctx context.Context, step string) (Stats, error) {
	switch step {
	case "a":
		return r.stepEngine(ctx)
	case "b":
		return r.stepScreenshot(ctx)
	case "c":
		return r.stepLink(ctx)
	case "d":
		return r.stepTagIntro(ctx)
	case "e":
		return r.stepLabelIntro(ctx)
	case "f":
		return r.stepSeriesIntro(ctx)
	case "g":
		return r.stepOfficial(ctx)
	case "h":
		return r.stepOriginals(ctx)
	case "i":
		return r.stepGidMap(ctx)
	case "j":
		return r.stepOfficialMap(ctx)
	case "k":
		return r.stepEngineMap(ctx)
	case "l":
		return r.stepTagMap(ctx)
	case "m":
		return r.stepCoverProject(ctx)
	case "n":
		return r.stepScreenshotProject(ctx)
	case StepImageUsage:
		return r.stepImageUsage(ctx)
	case "p":
		return r.stepTitleMirror(ctx)
	case "q":
		return r.stepIntroMirror(ctx)
	case "r":
		return r.stepTagMirror(ctx)
	case "s":
		return r.stepRatingVndbMirror(ctx)
	case "t":
		return r.stepMetaAdopt(ctx)
	}
	return Stats{}, fmt.Errorf("unhandled step %q", step)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ── shared helpers ──────────────────────────────────────────────────────────

// anchorOf reads the wiki→catalog work pointer that this wave treats as the
// denominator (charter: "anchored = galgame.catalog_work_id IS NOT NULL AND
// <> 0"). Returned map omits unanchored rows entirely.
func (r *Runner) anchorMap(ctx context.Context) (map[int64]int64, error) {
	type row struct {
		ID            int64
		CatalogWorkID int64
	}
	var rows []row
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, catalog_work_id FROM galgame
		 WHERE catalog_work_id IS NOT NULL AND catalog_work_id <> 0`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load anchor map: %w", err)
	}
	m := make(map[int64]int64, len(rows))
	for _, x := range rows {
		m[x.ID] = x.CatalogWorkID
	}
	return m, nil
}

// insertReturning runs a chunked multi-row INSERT ... ON CONFLICT DO NOTHING
// and returns the values of the RETURNING column for the rows that actually
// landed. GORM cannot return non-PK columns, so this is raw SQL — the same
// reason workproducers.insertEdges and importer.insertWorkLabelEdges use it.
//
// The returned ids are what TouchWorks must be fed: touching the full planned
// set instead would shove every candidate through the changes feed on a
// converged re-run.
func insertReturning(db *gorm.DB, table string, cols []string, returning string, rows [][]any) ([]int64, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	perRow := len(cols)
	chunk := max(paramChunk/perRow, 1)
	out := make([]int64, 0, len(rows))
	for start := 0; start < len(rows); start += chunk {
		end := min(start+chunk, len(rows))
		batch := rows[start:end]
		var sb strings.Builder
		fmt.Fprintf(&sb, "INSERT INTO %s (%s) VALUES ", table, strings.Join(cols, ", "))
		args := make([]any, 0, len(batch)*perRow)
		for i, rw := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			for j := range rw {
				if j > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString("?")
			}
			sb.WriteString(")")
			args = append(args, rw...)
		}
		sb.WriteString(" ON CONFLICT DO NOTHING")
		if returning != "" {
			fmt.Fprintf(&sb, " RETURNING %s", returning)
			var got []int64
			if err := db.Raw(sb.String(), args...).Scan(&got).Error; err != nil {
				return nil, fmt.Errorf("insert into %s: %w", table, err)
			}
			out = append(out, got...)
			continue
		}
		res := db.Exec(sb.String(), args...)
		if res.Error != nil {
			return nil, fmt.Errorf("insert into %s: %w", table, res.Error)
		}
		for range res.RowsAffected {
			out = append(out, 0)
		}
	}
	return out, nil
}

// park writes the rows a step could not project to a JSON artifact, complete
// with their original text, so nothing is lost when the tables are dropped.
// Parking is a legitimate outcome per the charter, not an error path.
func (r *Runner) park(step string, payload any) error {
	if r.artifact == "" {
		return nil
	}
	if err := os.MkdirAll(r.artifact, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}
	path := filepath.Join(r.artifact, fmt.Sprintf("parked-%s.json", step))
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal parked rows: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
