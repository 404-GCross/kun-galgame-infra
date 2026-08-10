package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/jobs/userplaytime"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED)")
	minReporters := flag.Int("min-reporters", 0, "distinct finishers required to publish a work's median (0 = the model's threshold)")
	exclude := flag.String("exclude-client", "", "comma-separated OAuth client ids whose reports do not count toward the public number")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := userplaytime.Run(context.Background(), userplaytime.Opts{
		DSN: *dsn, Apply: *apply, MinReporters: *minReporters,
		ExcludeClients: splitCSV(*exclude),
	})
	if err != nil {
		slog.Error("aggregate-user-playtime failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== aggregate-user-playtime %s ===\n", mode(*apply))
	fmt.Printf("eligible=%d written=%d unchanged=%d deleted=%d errors=%d\n",
		st.Eligible, st.Written, st.Unchanged, st.Deleted, st.Errors)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY RUN"
}
