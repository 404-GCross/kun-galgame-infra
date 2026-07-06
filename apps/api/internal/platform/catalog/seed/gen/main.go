// Command gen regenerates the checked-in role vocabulary artifacts
// (seed/data/roles.gen.yaml + seed/data/bangumi_role_map.gen.yaml) from the
// embedded bangumicommon snapshot. Run from apps/api:
//
//	go run ./internal/platform/catalog/seed/gen
//
// Review the diff before committing — role IDs are frozen once generated and
// changes must stay additive (the drift test in the seed package fails if the
// artifacts don't match the generation logic).
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"api/internal/platform/catalog/seed"
)

func main() {
	outDir := flag.String("out", "internal/platform/catalog/seed/data", "output directory")
	flag.Parse()

	generated, err := seed.GenerateBangumiRoles()
	if err != nil {
		log.Fatalf("generate roles: %v", err)
	}
	rolesYAML, err := seed.RenderRolesYAML(generated.Roles)
	if err != nil {
		log.Fatalf("render roles: %v", err)
	}
	mapYAML, err := seed.RenderRoleMapYAML(generated.Mappings)
	if err != nil {
		log.Fatalf("render role map: %v", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "roles.gen.yaml"), rolesYAML, 0o644); err != nil {
		log.Fatalf("write roles.gen.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "bangumi_role_map.gen.yaml"), mapYAML, 0o644); err != nil {
		log.Fatalf("write bangumi_role_map.gen.yaml: %v", err)
	}
	log.Printf("generated %d roles, %d mappings", len(generated.Roles), len(generated.Mappings))
}
