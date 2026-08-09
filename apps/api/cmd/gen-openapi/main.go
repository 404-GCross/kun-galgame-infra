// gen-openapi emits the artifact service's OpenAPI spec (derived from the Go
// types via Huma) so it can be committed and consumed by codegen (Go/Dart) and
// registered in kungal-docs — without booting the service. See docs/artifact/10.
//
//	go run ./cmd/gen-openapi -o ../../docs/artifact/openapi.yaml            # 3.1 (canonical)
//	go run ./cmd/gen-openapi -downgrade -o ../../docs/artifact/openapi-3.0.yaml  # 3.0.3 (oapi-codegen)
//	go run ./cmd/gen-openapi -admin -o ../../docs/artifact/admin-openapi.yaml    # oauth admin API (3.1)
//	go run ./cmd/gen-openapi -catalog -o ../../docs/catalog/openapi.yaml                # catalog S2S face (3.1)
//	go run ./cmd/gen-openapi -catalog-admin -o ../../docs/catalog/admin-openapi.yaml    # catalog review queues (3.1)
//	go run ./cmd/gen-openapi -catalog-public -o ../../docs/catalog/public-openapi.yaml  # NextMoe open-API catalog public projection (3.1)
//	go run ./cmd/gen-openapi -community -o ../../docs/community/openapi.yaml             # community S2S embed face (3.1)
//	go run ./cmd/gen-openapi -trust -o ../../docs/trust/openapi.yaml                    # trust S2S intake face (3.1)
//	go run ./cmd/gen-openapi -trust-admin -o ../../docs/trust/admin-openapi.yaml        # trust admin review inbox (3.1)
//	go run ./cmd/gen-openapi -ai -o ../../docs/ai/openapi.yaml                          # AI-gateway S2S face (3.1)
//	go run ./cmd/gen-openapi -ai-admin -o ../../docs/ai/admin-openapi.yaml              # AI-gateway usage dashboard (3.1)
package main

import (
	"flag"
	"fmt"
	"os"

	aiHandler "api/internal/platform/ai/handler"
	artHandler "api/internal/platform/artifact/handler"
	"api/internal/platform/artifact/service"
	catHandler "api/internal/platform/catalog/handler"
	commHandler "api/internal/platform/community/handler"
	trustHandler "api/internal/platform/trust/handler"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gofiber/fiber/v3"
)

func main() {
	out := flag.String("o", "", "output file (default: stdout)")
	downgrade := flag.Bool("downgrade", false, "emit OpenAPI 3.0.3 instead of 3.1 (for tools without 3.1 support, e.g. oapi-codegen)")
	admin := flag.Bool("admin", false, "emit the oauth-hosted admin API spec (/api/v1/admin/artifact/*) instead of the artifact service spec")
	// The -galgame-public target retired with the /v1/galgame face itself (wave
	// 146, 2026-07-30): the projection is delisted, so there is no spec to emit.
	catalog := flag.Bool("catalog", false, "emit the catalog S2S spec (/api/v1/catalog/*)")
	catalogAdmin := flag.Bool("catalog-admin", false, "emit the catalog admin review-queue spec (/api/v1/admin/catalog/*)")
	catalogPublic := flag.Bool("catalog-public", false, "emit the NextMoe open-API catalog public projection spec (/v1/catalog/*)")
	community := flag.Bool("community", false, "emit the community S2S embed spec (/api/v1/community/*)")
	trust := flag.Bool("trust", false, "emit the trust S2S intake spec (/api/v1/trust/*)")
	trustAdmin := flag.Bool("trust-admin", false, "emit the trust admin review-inbox spec (/api/v1/admin/trust/*)")
	ai := flag.Bool("ai", false, "emit the AI-gateway S2S spec (/api/v1/ai/*)")
	aiAdmin := flag.Bool("ai-admin", false, "emit the AI-gateway usage-dashboard spec (/api/v1/admin/ai/*)")
	flag.Parse()

	// Build the API to derive the spec; the deps are nil / stub because Setup
	// only registers operations (handlers are never invoked here).
	app := fiber.New()
	var api huma.API
	switch {
	case *catalog:
		api = catHandler.Setup(app, nil, nil, nil, nil, nil)
		// The editing-engine face (/api/v1/catalog/edit/*, E0) registers on the
		// same S2S huma.API in cmd/catalog — mirror it so the exported spec
		// matches the runtime surface (nil engine is fine for spec emission).
		catHandler.SetupEdit(api, nil, nil)
		// Same for the claim-lifecycle face (wave 155): actions + the two feeds.
		catHandler.SetupLifecycle(api, nil, nil, nil)
		// The user-token write plane (wave 176) registers on its OWN humafiber
		// API at runtime (a disjoint prefix with its own auth chain), but it is
		// the same contract document: a consumer picking between "assert the
		// actor" and "let the token be the actor" reads both here.
		catHandler.RegisterUserOps(api, nil)
		// The user plane's editing ops (wave 177-178) — every verb a human
		// performs, with the actor derived from the token. The S2S edit face
		// keeps only what a backend legitimately still needs.
		catHandler.RegisterUserEditOps(api, nil, nil)
		// The user plane's claims ops (wave 179): submit, the eight lifecycle
		// actions, and "my claims" — the S2S claims face with the submitter,
		// the tenant and the reviewer taken from the token.
		catHandler.RegisterUserClaimOps(api, nil)
		// The user plane's read op (wave 180): the work's cover tallies with the
		// viewer derived from the token. Its two sibling reads of the wave (the
		// snapshot and the proposal list) ride RegisterUserEditOps above; the
		// fourth, the multipart image upload, is spec-invisible on BOTH planes
		// exactly like its S2S twin — docs/catalog/01 §4.4 carries it instead.
		catHandler.RegisterUserReadOps(api, nil)
	case *catalogAdmin:
		api = catHandler.SetupAdmin(app, nil, nil, nil, nil)
	case *catalogPublic:
		api = catHandler.SetupCatalogPublicSpec(app)
	case *community:
		api = commHandler.Setup(app, nil, nil, nil, nil, nil, nil, nil)
	case *trust:
		api = trustHandler.Setup(app, nil, nil, nil, nil, nil)
	case *trustAdmin:
		api = trustHandler.SetupAdmin(app, nil, nil, nil, nil, nil, nil)
	case *ai:
		api = aiHandler.Setup(app, nil)
	case *aiAdmin:
		api = aiHandler.SetupAdmin(app, nil, nil)
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
