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
// Three rules every step obeys:
//
//   - FILL-MISSING, NEVER OVERWRITE. Every write is ON CONFLICT DO NOTHING, so
//     a second pass writes zero rows. That is the acceptance criterion, not an
//     optimization: these steps run against production once, and a re-run must
//     be provably inert.
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
	// Step selects one lettered step (a..l) or "all" for the charter order.
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
	// Note carries a step-specific remark for the report.
	Note string `json:"note,omitempty"`
}

// Runner holds the two pools and the resolved source ids for a run.
type Runner struct {
	galgame  *gorm.DB
	catalog  *gorm.DB
	opts     Opts
	wikiSrc  int16
	webSrc   int16
	artifact string
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
	return &Runner{galgame: galgame, catalog: catalog, opts: opts, wikiSrc: wikiSrc, webSrc: webSrc, artifact: opts.ArtifactDir}, nil
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
// the gid map, only exist today as a joinable wiki table.
var steps = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}

// Run dispatches the selected step(s) and returns one Stats per step executed.
func (r *Runner) Run(ctx context.Context) ([]Stats, error) {
	want := strings.ToLower(strings.TrimSpace(r.opts.Step))
	selected := steps
	if want != "all" && want != "" {
		if !contains(steps, want) {
			return nil, fmt.Errorf("unknown step %q (want one of %s or \"all\")", want, strings.Join(steps, ","))
		}
		selected = []string{want}
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
