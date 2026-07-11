// gen-openapi emits the artifact service's OpenAPI spec (derived from the Go
// types via Huma) so it can be committed and consumed by codegen (Go/Dart) and
// registered in kungal-docs — without booting the service. See docs/artifact/10.
//
//	go run ./cmd/gen-openapi -o ../../docs/artifact/openapi.yaml            # 3.1 (canonical)
//	go run ./cmd/gen-openapi -downgrade -o ../../docs/artifact/openapi-3.0.yaml  # 3.0.3 (oapi-codegen)
//	go run ./cmd/gen-openapi -admin -o ../../docs/artifact/admin-openapi.yaml    # oauth admin API (3.1)
//	go run ./cmd/gen-openapi -galgame-calendar -o ../../docs/galgame_wiki/calendar-openapi.yaml  # wiki release calendar (3.1)
//	go run ./cmd/gen-openapi -galgame-read -o ../../docs/galgame_wiki/read-openapi.yaml          # wiki read endpoints (3.1)
//	go run ./cmd/gen-openapi -catalog -o ../../docs/catalog/openapi.yaml                # catalog S2S face (3.1)
//	go run ./cmd/gen-openapi -catalog-admin -o ../../docs/catalog/admin-openapi.yaml    # catalog review queues (3.1)
//	go run ./cmd/gen-openapi -community -o ../../docs/community/openapi.yaml             # community S2S embed face (3.1)
//	go run ./cmd/gen-openapi -trust -o ../../docs/trust/openapi.yaml                    # trust S2S intake face (3.1)
//	go run ./cmd/gen-openapi -trust-admin -o ../../docs/trust/admin-openapi.yaml        # trust admin review inbox (3.1)
package main

import (
	"flag"
	"fmt"
	"os"

	artHandler "api/internal/platform/artifact/handler"
	"api/internal/platform/artifact/service"
	catHandler "api/internal/platform/catalog/handler"
	commHandler "api/internal/platform/community/handler"
	galgameHandler "api/internal/platform/galgame/handler"
	trustHandler "api/internal/platform/trust/handler"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gofiber/fiber/v3"
)

func main() {
	out := flag.String("o", "", "output file (default: stdout)")
	downgrade := flag.Bool("downgrade", false, "emit OpenAPI 3.0.3 instead of 3.1 (for tools without 3.1 support, e.g. oapi-codegen)")
	admin := flag.Bool("admin", false, "emit the oauth-hosted admin API spec (/api/v1/admin/artifact/*) instead of the artifact service spec")
	galgameCalendar := flag.Bool("galgame-calendar", false, "emit the galgame-wiki release-calendar spec (/api/galgame/calendar*)")
	galgameRead := flag.Bool("galgame-read", false, "emit the galgame-wiki read-endpoint spec (/api/galgame/batch, …)")
	catalog := flag.Bool("catalog", false, "emit the catalog S2S spec (/api/v1/catalog/*)")
	catalogAdmin := flag.Bool("catalog-admin", false, "emit the catalog admin review-queue spec (/api/v1/admin/catalog/*)")
	community := flag.Bool("community", false, "emit the community S2S embed spec (/api/v1/community/*)")
	trust := flag.Bool("trust", false, "emit the trust S2S intake spec (/api/v1/trust/*)")
	trustAdmin := flag.Bool("trust-admin", false, "emit the trust admin review-inbox spec (/api/v1/admin/trust/*)")
	flag.Parse()

	// Build the API to derive the spec; the deps are nil / stub because Setup
	// only registers operations (handlers are never invoked here).
	app := fiber.New()
	var api huma.API
	switch {
	case *galgameRead:
		api = galgameHandler.SetupGalgameReadSpec(app)
	case *galgameCalendar:
		api = galgameHandler.SetupCalendarSpec(app)
	case *catalog:
		api = catHandler.Setup(app, nil, nil, nil, nil, nil)
	case *catalogAdmin:
		api = catHandler.SetupAdmin(app, nil, nil)
	case *community:
		api = commHandler.Setup(app, nil, nil, nil, nil, nil, nil, nil)
	case *trust:
		api = trustHandler.Setup(app, nil, nil, nil)
	case *trustAdmin:
		api = trustHandler.SetupAdmin(app, nil, nil, nil)
	case *admin:
		api = artHandler.SetupAdmin(app, artHandler.NewAdmin(nil, nil, nil, 0))
	default:
		api = artHandler.Setup(app, service.New(nil, nil, nil, service.Options{}), true)
	}

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
