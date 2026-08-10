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
