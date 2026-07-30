// w140_flip_test.go — the A/B equivalence harness of the W1-pre bridge
// nativization (refs/proj/140 §3).
//
// The wave deleted five read-time bridges and replaced them with the catalog's own
// tables, filled by the wikirescue MIRROR steps. The acceptance question is not
// "does the native lane work" — it is "does the wire still say exactly what the
// bridge said", for the same wiki input.
//
// So the suites in this package keep their bridge-era fixtures (a wiki body, its
// aliases, its tag edges, its rating metas) and their bridge-era expectations,
// and gain ONE line: the mirror runs between the fixture and the request. Every
// assertion that still passes is an A/B equivalence result — wiki input in, frozen
// bytes out, with the real materialization in between rather than a hand-written
// look-alike of it.
//
// Where an expectation DID have to move, the wave measured the population it
// affects and recorded it as an intended change (task doc §5 报告). Two such
// changes are visible in this package:
//
//   - a claimed work's DETAIL titles[] now sorts (kind, lang) like every native
//     work's instead of the wiki's column order (TestClaimedWorkTitlesBridge);
//   - the strict XOR is gone with the bridges, so a claimed work's native facet
//     rows are no longer shadowed. Production had none of those rows (the SQL
//     parity harness over the whole claimed population reports zero mismatches on
//     all seven faces), which is exactly why the flip is byte-frozen there.
package handler

import (
	"testing"

	"api/internal/jobs/wikirescue"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mirrorWikiIntoCatalog runs the given W1-pre mirror steps over the fixture
// database, with the SAME code production runs — not a test double. steps is the
// step selection (see wikirescue.ParseSteps): a suite names the facets it
// exercises, because a step whose wiki table the suite never stubbed would fail
// on the missing relation rather than silently do nothing.
//
// Both pools are the one test database: galgame and catalog are co-located in
// kun_catalog in production too, so this is the deployed topology, and the runner
// keeps its two handles regardless.
func mirrorWikiIntoCatalog(t *testing.T, db *gorm.DB, steps string) {
	t.Helper()
	runner, err := wikirescue.New(db, db, wikirescue.Opts{Apply: true, Step: steps})
	require.NoError(t, err, "wire the mirror runner")
	stats, err := runner.Run(t.Context())
	require.NoError(t, err, "run mirror steps %q", steps)
	require.Len(t, stats, len(splitSteps(steps)), "one ledger per step")
}

// splitSteps counts the steps in a selection so the helper can assert it ran them
// all (a typo'd selection would otherwise mirror less than the suite believes).
func splitSteps(steps string) []string {
	out, err := wikirescue.ParseSteps(steps)
	if err != nil {
		return nil
	}
	return out
}
