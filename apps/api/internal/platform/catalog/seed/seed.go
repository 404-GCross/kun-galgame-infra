// Package seed owns the catalog registry vocabulary data (doc 17 R1) and its
// idempotent write path. Two kinds of seeds:
//
//   - hand-written rows (media, sources, relation types) pinned in this file;
//   - generated rows (the unified role vocabulary + the bangumi role map),
//     derived from the bangumicommon snapshot by seed/gen and CHECKED IN under
//     data/ — the migrate path reads the artifacts, never re-derives them.
//
// Write semantics: upsert by primary key, updating display fields only
// (names, phrases, notes). Seeds never DELETE registry rows and never touch
// is_deprecated — retirement is a manual act.
package seed

import (
	"embed"
	"fmt"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//go:embed data/roles.gen.yaml data/bangumi_role_map.gen.yaml
var dataFS embed.FS

// bangumiSourceID must match the catalog_source seed row below.
const bangumiSourceID int16 = 3

// media rows — ids/keys pinned by refs/proj/02 T3(a).
func media() []model.CatalogMedium {
	return []model.CatalogMedium{
		{ID: 1, Key: "galgame", NameCN: "Galgame"},
		{ID: 2, Key: "manga", NameCN: "漫画"},
		{ID: 3, Key: "novel", NameCN: "小说"},
		{ID: 4, Key: "anime", NameCN: "动画"},
		{ID: 5, Key: "asmr", NameCN: "ASMR"},
		{ID: 6, Key: "doujin_game", NameCN: "同人游戏"},
		{ID: 7, Key: "music", NameCN: "音乐"},
	}
}

// sources rows — ids/keys/trust tiers pinned by refs/proj/02 T3(a).
// URL templates stay NULL until the per-entity URL shapes are designed.
func sources() []model.CatalogSource {
	return []model.CatalogSource{
		{ID: 1, Key: "user", TrustTier: 0, Note: "manual curation, not an import source"},
		{ID: 2, Key: "vndb", TrustTier: 1},
		{ID: 3, Key: "bangumi", TrustTier: 1},
		{ID: 4, Key: "dlsite", TrustTier: 0},
		{ID: 5, Key: "erogamespace", TrustTier: 1},
		{ID: 6, Key: "anilist", TrustTier: 1},
		{ID: 7, Key: "mal", TrustTier: 1},
		{ID: 8, Key: "steam", TrustTier: 2},
		{ID: 9, Key: "official_site", TrustTier: 2},
		{ID: 10, Key: "twitter", TrustTier: 2},
		{ID: 11, Key: "pixiv", TrustTier: 2},
	}
}

// relationTypes rows — ids/keys/phrases pinned by refs/proj/02 T3(a).
// Work domain ids grow from 1, entity domain from 20 (sparse gap on purpose).
func relationTypes() []model.CatalogRelationType {
	return []model.CatalogRelationType{
		{ID: 1, Key: "adaptation_of", Domain: model.RelationDomainWork, ForwardPhrase: "改编自", ReversePhrase: "被改编为"},
		{ID: 2, Key: "sequel_of", Domain: model.RelationDomainWork, ForwardPhrase: "是…的续作", ReversePhrase: "有续作"},
		{ID: 3, Key: "side_story_of", Domain: model.RelationDomainWork, ForwardPhrase: "是…的外传", ReversePhrase: "有外传"},
		{ID: 4, Key: "fandisc_of", Domain: model.RelationDomainWork, ForwardPhrase: "是…的 Fandisc", ReversePhrase: "有 Fandisc"},
		{ID: 5, Key: "collects", Domain: model.RelationDomainWork, ForwardPhrase: "收录", ReversePhrase: "被收录于"},
		{ID: 6, Key: "remake_of", Domain: model.RelationDomainWork, ForwardPhrase: "重制自", ReversePhrase: "被重制为"},
		{ID: 7, Key: "same_series", Domain: model.RelationDomainWork, ForwardPhrase: "同系列", ReversePhrase: "同系列", IsSymmetric: true},
		{ID: 8, Key: "same_setting", Domain: model.RelationDomainWork, ForwardPhrase: "同世界观", ReversePhrase: "同世界观", IsSymmetric: true},
		{ID: 9, Key: "crossover_with", Domain: model.RelationDomainWork, ForwardPhrase: "联动", ReversePhrase: "联动", IsSymmetric: true},

		{ID: 20, Key: "imprint_of", Domain: model.RelationDomainEntity, ForwardPhrase: "是…旗下的厂牌/文库", ReversePhrase: "拥有厂牌/文库"},
		{ID: 21, Key: "renamed_from", Domain: model.RelationDomainEntity, ForwardPhrase: "前身为", ReversePhrase: "后改名为"},
		{ID: 22, Key: "subsidiary_of", Domain: model.RelationDomainEntity, ForwardPhrase: "是…的子公司", ReversePhrase: "有子公司"},
		{ID: 23, Key: "member_of", Domain: model.RelationDomainEntity, ForwardPhrase: "是…的成员", ReversePhrase: "有成员"},
	}
}

// loadGeneratedRoles reads the checked-in artifacts into registry rows.
func loadGeneratedRoles() ([]model.CatalogRole, []model.CatalogSourceRoleMap, error) {
	var rolesDoc struct {
		Roles []RoleSeed `yaml:"roles"`
	}
	if err := unmarshalData("data/roles.gen.yaml", &rolesDoc); err != nil {
		return nil, nil, err
	}
	var mapDoc struct {
		Mappings []RoleMapSeed `yaml:"mappings"`
	}
	if err := unmarshalData("data/bangumi_role_map.gen.yaml", &mapDoc); err != nil {
		return nil, nil, err
	}
	if len(rolesDoc.Roles) == 0 || len(mapDoc.Mappings) == 0 {
		return nil, nil, fmt.Errorf("catalog seed: generated artifacts are empty — regenerate via seed/gen")
	}

	roles := make([]model.CatalogRole, len(rolesDoc.Roles))
	for i, r := range rolesDoc.Roles {
		roles[i] = model.CatalogRole{
			ID: r.ID, Key: r.Key, Category: r.Category,
			NameCN: r.NameCN, NameJA: r.NameJA, NameEN: r.NameEN,
		}
	}
	mappings := make([]model.CatalogSourceRoleMap, len(mapDoc.Mappings))
	for i, m := range mapDoc.Mappings {
		mappings[i] = model.CatalogSourceRoleMap{
			SourceID: bangumiSourceID, SourceRole: m.SourceRole,
			RoleID: m.RoleID, Note: m.Note,
		}
	}
	return roles, mappings, nil
}

func unmarshalData(name string, out any) error {
	raw, err := dataFS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("catalog seed: read embedded %s: %w", name, err)
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("catalog seed: parse %s: %w", name, err)
	}
	return nil
}

// Run upserts all registry seeds. Idempotent: conflicting rows only get
// their display fields refreshed; is_deprecated and behavioral fields
// (trust_tier, domain, is_symmetric, role_id, ...) are never overwritten.
func Run(db *gorm.DB) error {
	roles, roleMap, err := loadGeneratedRoles()
	if err != nil {
		return err
	}

	if err := upsert(db, "catalog_medium", media(), []string{"id"}, []string{"name_cn"}); err != nil {
		return err
	}
	if err := upsert(db, "catalog_source", sources(), []string{"id"}, []string{"note"}); err != nil {
		return err
	}
	if err := upsert(db, "catalog_role", roles, []string{"id"}, []string{"category", "name_cn", "name_ja", "name_en"}); err != nil {
		return err
	}
	if err := upsert(db, "catalog_source_role_map", roleMap, []string{"source_id", "source_role"}, []string{"note"}); err != nil {
		return err
	}
	if err := upsert(db, "catalog_relation_type", relationTypes(), []string{"id"}, []string{"forward_phrase", "reverse_phrase"}); err != nil {
		return err
	}
	return nil
}

// upsert writes rows with ON CONFLICT (conflictCols) DO UPDATE SET
// (updateCols) — display-field refresh only, never DELETE.
func upsert[T any](db *gorm.DB, table string, rows []T, conflictCols, updateCols []string) error {
	columns := make([]clause.Column, len(conflictCols))
	for i, c := range conflictCols {
		columns[i] = clause.Column{Name: c}
	}
	res := db.Clauses(clause.OnConflict{
		Columns:   columns,
		DoUpdates: clause.AssignmentColumns(updateCols),
	}).Create(&rows)
	if res.Error != nil {
		return fmt.Errorf("catalog seed: upsert %s: %w", table, res.Error)
	}
	slog.Info("seeded registry", "table", table, "rows", len(rows), "affected", res.RowsAffected)
	return nil
}
