// gen-openapi emits the artifact service's OpenAPI spec (derived from the Go
// types via Huma) so it can be committed and consumed by codegen (Go/Dart) and
// registered in kungal-docs — without booting the service. See docs/artifact/10.
//
//	go run ./cmd/gen-openapi -o ../../docs/artifact/openapi.yaml            # 3.1 (canonical)
//	go run ./cmd/gen-openapi -downgrade -o ../../docs/artifact/openapi-3.0.yaml  # 3.0.3 (oapi-codegen)
package main

import (
	"flag"
	"fmt"
	"os"

	artHandler "api/internal/platform/artifact/handler"
	"api/internal/platform/artifact/service"

	"github.com/gofiber/fiber/v3"
)

func main() {
	out := flag.String("o", "", "output file (default: stdout)")
	downgrade := flag.Bool("downgrade", false, "emit OpenAPI 3.0.3 instead of 3.1 (for tools without 3.1 support, e.g. oapi-codegen)")
	flag.Parse()

	// Build the API to derive the spec; the service deps are nil because Setup
	// only registers operations (handlers are never invoked here).
	app := fiber.New()
	svc := service.New(nil, nil, nil, service.Options{})
	api := artHandler.Setup(app, svc, true)

	var (
		b   []byte
		err error
	)
	if *downgrade {
		b, err = api.OpenAPI().DowngradeYAML()
	} else {
		b, err = api.OpenAPI().YAML()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-openapi:", err)
		os.Exit(1)
	}

	if *out == "" {
		_, _ = os.Stdout.Write(b)
		return
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-openapi:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", *out, len(b))
}
