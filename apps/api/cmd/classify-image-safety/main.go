package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"api/pkg/logger"
)

func main() {
	mode := flag.String("mode", "", "scan | report")
	dsn := flag.String("dsn", "", "images DSN (REQUIRED for scan)")
	out := flag.String("out", "", "scan: JSONL output path (appended — resume-safe)")
	in := flag.String("in", "", "report: JSONL input path")

	limit := flag.Int("limit", 1000, "scan: images sampled")
	salt := flag.String("salt", "kungal-safety-v1", "scan: sample ordering salt")
	baseURL := flag.String("base-url", os.Getenv("KUN_IMAGE_PUBLIC_BASE_URL"), "scan: public image base URL")
	variant := flag.String("variant", "", "scan: variant name to score instead of the main image")

	omniBase := flag.String("omni-base", os.Getenv("KUN_AI_OMNI_BASE_URL"), "scan: moderations API root")
	omniToken := flag.String("omni-token", os.Getenv("KUN_AI_OMNI_TOKEN"), "scan: bearer token")
	omniModel := flag.String("omni-model", envOr("KUN_AI_OMNI_MODEL", "omni-moderation-latest"), "scan: model id")

	concurrency := flag.Int("concurrency", 8, "scan: parallel requests")
	qps := flag.Float64("qps", 8, "scan: request rate ceiling")
	flag.Parse()

	logger.Init("production")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch *mode {
	case "scan":
		err = runScan(ctx, scanOptions{
			DSN:         *dsn,
			Out:         *out,
			Limit:       *limit,
			Salt:        *salt,
			BaseURL:     *baseURL,
			Variant:     *variant,
			Concurrency: *concurrency,
			QPS:         *qps,
			Client:      newOmniImageClient(*omniBase, *omniToken, *omniModel),
		}, os.Stdout)
	case "report":
		err = runReport(*in, os.Stdout)
	default:
		err = fmt.Errorf("--mode must be scan or report")
	}
	if err != nil {
		slog.Error("classify-image-safety", "mode", *mode, "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
