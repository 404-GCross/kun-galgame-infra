// Package health provides the `healthcheck` subcommand used by container
// HEALTHCHECK directives.
//
// distroless runtime images ship no shell, curl, or wget, so a container
// HEALTHCHECK can't `wget localhost/health`. Instead the service binary
// probes its own HTTP health endpoint and exits 0 (healthy) / 1 (unhealthy):
//
//	HEALTHCHECK CMD ["/app", "healthcheck"]
package health

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// MaybeProbe runs the `healthcheck` subcommand and exits the process, or
// returns immediately when not invoked that way. It is a no-op unless
// os.Args[1] == "healthcheck", so it is safe to call unconditionally at the
// top of main() — before any database / cache is initialised (the probe only
// needs the already-running server, not its dependencies).
func MaybeProbe(port int, path string) {
	if len(os.Args) < 2 || os.Args[1] != "healthcheck" {
		return
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "healthcheck: unhealthy (status %d)\n", resp.StatusCode)
	os.Exit(1)
}
