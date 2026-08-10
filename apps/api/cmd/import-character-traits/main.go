package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/chartraits"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; hosts src_vndb)")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := chartraits.Run(context.Background(), chartraits.Opts{Apply: *apply, DSN: *dsn})
	if err != nil {
		slog.Error("import-character-traits failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-character-traits %s ===\n", mode(*apply))
	fmt.Printf("vocab: total=%d written=%d unchanged=%d\n", st.VocabTotal, st.VocabWritten, st.VocabUnchanged)
	fmt.Printf("edges: total=%d added=%d deleted=%d\n", st.EdgesTotal, st.EdgesAdded, st.EdgesDeleted)
	fmt.Printf("links: seen=%d written=%d unchanged=%d\n", st.LinksSeen, st.LinksWritten, st.LinksUnchanged)
	fmt.Printf("errors=%d\n", st.Errors)
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
