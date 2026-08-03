// Package dbtest decides, in one place, what a DB-backed suite does when it has
// no database to run against.
//
// The two failure modes it sits between are both real and both have bitten this
// repo:
//
//   - Skipping silently. A suite whose TestMain exits 0 when it cannot reach a
//     database still prints `ok` for the package, so a whole DB-backed suite can
//     stop running and CI stays green. Nothing tells you; the only tell is the
//     runtime (~1s instead of ~5s).
//   - Failing unconditionally. The `unit` job in .github/workflows/test.yml runs
//     `go test ./...` with NO service containers on purpose, and its contract is
//     that service-gated tests skip cleanly. A suite that hard-fails on a missing
//     DSN turns that always-green gate permanently red, which is what happened
//     between 2026-08-02 and 2026-08-03.
//
// The distinction that resolves it is not "is a DSN present" but "was this run
// SUPPOSED to have a database". Only the caller of `go test` knows that, so it
// says so: the integration job sets REQUIRE_DB_TESTS=1, and a missing DSN there
// is a misconfigured job, not an absent service.
//
// Deliberately no default DSN. Falling back to a hard-coded localhost database
// is how a suite silently adopts whichever database happens to be listening —
// including one another track is mid-run against.
package dbtest

import (
	"fmt"
	"os"
	"testing"
)

// RequireEnv names the variable a caller sets to assert "this run has a
// database; treat its absence as a failure".
const RequireEnv = "REQUIRE_DB_TESTS"

// DSN returns the assigned test database DSN and whether the suite should run.
//
// ok=false means the DSN is unset and this run was not promised one — the caller
// should skip. When REQUIRE_DB_TESTS is set, an unset DSN never returns: it is a
// broken CI job or a mis-launched local run, and reporting `ok` for a suite that
// did not execute is worse than stopping.
func DSN() (dsn string, ok bool) {
	dsn = os.Getenv("TEST_DATABASE_DSN")
	if dsn != "" {
		return dsn, true
	}
	if os.Getenv(RequireEnv) != "" {
		fmt.Fprintf(os.Stderr,
			"FAIL: %s is set but TEST_DATABASE_DSN is empty — this run was supposed to have a database\n",
			RequireEnv)
		os.Exit(2)
	}
	return "", false
}

// SkipMain is the TestMain half: it reports the skip on stderr and exits 0.
// Package-level suites call it when DSN reports ok=false.
func SkipMain(suite string) {
	fmt.Fprintf(os.Stderr,
		"SKIP: TEST_DATABASE_DSN unset — %s not run (set %s=1 to make this a failure)\n",
		suite, RequireEnv)
	os.Exit(0)
}

// Skip is the per-test half, for suites gated inside a test function rather than
// in TestMain.
func Skip(t *testing.T) {
	t.Helper()
	t.Skipf("TEST_DATABASE_DSN unset — DB-backed test not run (set %s=1 to make this a failure)", RequireEnv)
}
