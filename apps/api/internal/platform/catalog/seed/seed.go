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

// Source ids referenced by the seed maps (must match the catalog_source rows).
const (
	vndbSourceID    int16 = 2
	bangumiSourceID int16 = 3
	dlsiteSourceID  int16 = 4
	egSourceID      int16 = 5
)

// Role ids used by hand-added roles + the EG map. Hand-added roles live in the
// reserved 1-99 id band (the generated bangumi vocabulary starts at 100), so
// they never collide with a regenerated artifact and the drift test is
// unaffected.
const (
	roleVoiceActor int64 = 1 // 声優 — Bangumi models VA as a character relation,
	// NOT a staff position, so the 246-position vocabulary has no VA role; both
	// Bangumi person_character and EG appearance_actors/shubetu=5 need one.
	roleOtherStaff int64 = 2 // その他 — the wide bucket for EG shubetu=7.

	// Reserved-band slots for the three VNDB staff roles the generated Bangumi
	// (anime) vocabulary has no faithful position for. refs/proj/80 拍板: give
	// them pinned reserved-band ids and absorb the ~10,950 previously-skipped
	// credits, rather than force-fit or drop them.
	roleTranslator int64 = 3 // 翻译 / 翻訳 / Translator (VNDB `translator`).
	roleEditor     int64 = 4 // 编辑 / 編集 / Editor (VNDB `editor`). Its Key is
	// "text-editor", NOT "editor": the generated vocabulary already pins key
	// "editor" (id 177, 剪辑 = film cutting) and catalog_role.key is UNIQUE, so
	// the reserved slot takes a distinct key while keeping the 编辑 display names.
	roleQA int64 = 5 // QA / QA / QA (VNDB `qa`).

	// EG shubetu → existing catalog_role ids (kun-erogamespace-api docs/07:
	// 1原画 2シナリオ 3音楽 4キャラデザ 5声優 6歌手 7その他).
	roleIllustration    int64 = 184
	roleScenario        int64 = 247
	roleMusic           int64 = 209
	roleCharacterDesign int64 = 145
	roleVocal           int64 = 286

	// EG song-credit tables → existing generated-vocabulary role ids
	// (refs/proj/84). singers reuse roleVocal (286, above); these three are the
	// 作词 / 作曲 / 编曲 positions. All four reuse existing roles — zero new
	// vocabulary rows.
	roleLyric    int64 = 199 // 作词
	roleComposer int64 = 158 // 作曲
	roleArrange  int64 = 115 // 编曲

	// roleDirector is the generated-vocabulary "director" position (key
	// "director", 导演 / Director), the best-fit slot for VNDB's `director`
	// staff role.
	roleDirector int64 = 173
)

// handRoles are hand-pinned roles the import needs that the generated Bangumi
// staff-position vocabulary lacks (see the reserved-band note above).
func handRoles() []model.CatalogRole {
	return []model.CatalogRole{
		{ID: roleVoiceActor, Key: "voice-actor", Category: "cast", NameCN: "声优", NameJA: "声優", NameEN: "Voice Actor"},
		{ID: roleOtherStaff, Key: "other-staff", Category: "other", NameCN: "其他", NameJA: "その他", NameEN: "Other Staff"},
		{ID: roleTranslator, Key: "translator", Category: "other", NameCN: "翻译", NameJA: "翻訳", NameEN: "Translator"},
		{ID: roleEditor, Key: "text-editor", Category: "other", NameCN: "编辑", NameJA: "編集", NameEN: "Editor"},
		{ID: roleQA, Key: "qa", Category: "other", NameCN: "QA", NameJA: "QA", NameEN: "QA"},
	}
}

// egRoleMap pins the erogamespace shubetu → catalog_role mapping (source_role =
// the shubetu integer as text). Many-to-one and deliberately broad where the
// EG category is coarser than the vocabulary (原画→illustration, 歌手→vocal);
// その他 goes to the wide other-staff bucket rather than being dropped.
func egRoleMap() []model.CatalogSourceRoleMap {
	m := map[string]int64{
		"1": roleIllustration, "2": roleScenario, "3": roleMusic, "4": roleCharacterDesign,
		"5": roleVoiceActor, "6": roleVocal, "7": roleOtherStaff,
	}
	out := make([]model.CatalogSourceRoleMap, 0, len(m))
	for sr, rid := range m {
		out = append(out, model.CatalogSourceRoleMap{SourceID: egSourceID, SourceRole: sr, RoleID: rid})
	}
	return out
}

// egMusicRoleMap pins the erogamespace song-credit tables → catalog_role mapping
// (source_role = the source table name; refs/proj/84). catalog has no song
// entity, so these project as WORK-level credits: singers→vocal, lyricists→
// lyric, composers→composer, arrangers→arrange. Table-name keys never collide
// with the shubetu-integer keys of egRoleMap.
func egMusicRoleMap() []model.CatalogSourceRoleMap {
	m := map[string]int64{
		"singers": roleVocal, "lyricists": roleLyric, "composers": roleComposer, "arrangers": roleArrange,
	}
	out := make([]model.CatalogSourceRoleMap, 0, len(m))
	for sr, rid := range m {
		out = append(out, model.CatalogSourceRoleMap{SourceID: egSourceID, SourceRole: sr, RoleID: rid})
	}
	return out
}

// dlsiteRoleMap pins the DLsite creaters[].classification → catalog_role mapping
// (source_role = the raw classification string; the voice/ASMR subset uses
// exactly these six, created_by being DLsite's catch-all → other-staff).
func dlsiteRoleMap() []model.CatalogSourceRoleMap {
	m := map[string]int64{
		"voice_by": roleVoiceActor, "illust_by": roleIllustration, "scenario_by": roleScenario,
		"music_by": roleMusic, "created_by": roleOtherStaff, "キャラデザ": roleCharacterDesign,
	}
	out := make([]model.CatalogSourceRoleMap, 0, len(m))
	for sr, rid := range m {
		out = append(out, model.CatalogSourceRoleMap{SourceID: dlsiteSourceID, SourceRole: sr, RoleID: rid})
	}
	return out
}

// vndbRoleMap pins the VNDB vn_staff.role → catalog_role mapping (source_role =
// the raw role string; refs/proj/73 + 80). All ten VNDB staff roles map: seven
// onto generated-vocabulary slots (art→原画, songs→声乐 vocals, staff→the wide
// その他 bucket, ...). translator / editor / qa historically had NO faithful
// slot in the 246-position Bangumi (anime) vocabulary (its "editor" is 剪辑,
// film cutting, a different craft) and were left UNMAPPED — they are now carried
// by the reserved-band roles 3 / 4 / 5 (refs/proj/80 拍板: absorb the ~10,950
// credits rather than drop them).
func vndbRoleMap() []model.CatalogSourceRoleMap {
	m := map[string]int64{
		"scenario":   roleScenario,        // 247 シナリオ
		"art":        roleIllustration,    // 184 原画 / イラスト
		"chardesign": roleCharacterDesign, // 145 キャラクターデザイン
		"music":      roleMusic,           // 209 音楽
		"songs":      roleVocal,           // 286 声乐 (vocals / theme-song performers)
		"director":   roleDirector,        // 173 監督
		"staff":      roleOtherStaff,      // 2   その他 (VNDB's miscellaneous bucket)
		"translator": roleTranslator,      // 3   翻译 (reserved-band slot, refs/proj/80)
		"editor":     roleEditor,          // 4   编辑
		"qa":         roleQA,              // 5   QA
	}
	out := make([]model.CatalogSourceRoleMap, 0, len(m))
	for sr, rid := range m {
		out = append(out, model.CatalogSourceRoleMap{SourceID: vndbSourceID, SourceRole: sr, RoleID: rid})
	}
	return out
}

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
		// curated is the FIRST-PARTY HUMAN lane: the source_id every facet row
		// a human edit writes carries, and the one mechanism separating human
		// writes from importer writes on the multi-valued facets (03 定案 §0).
		//
		// It was seeded as `galgame_wiki` from step 52 until wave 161, when the
		// last mirror step retired and the wiki product it was named after
		// ceased to exist. Only the LABEL moved; id 12 is untouched, because
		// 60k+ works' intros / tags / covers / screenshots are already filed
		// under it and a fresh id would split one lane in two and force every
		// read face to know both (03 §1 / §9-3).
		//
		// Renaming a registry KEY is a wire-visible act — source_keys[] on the
		// public face renders it, and a consumer that resolves gids through
		// this key sees an empty bridge if it only knows the old spelling. See
		// the dual-read note on curatedSourceKeys in service/read_service.go.
		{ID: 12, Key: "curated", TrustTier: 0, Note: "first-party curated/human lane (was galgame_wiki until wave 161)"},
		// upscale is the first-party DERIVED cover source: galgame_cover rows whose
		// source='upscale' are AI-upscaled portrait covers produced inside the
		// galgame wiki. The cover bridge (step 53, refs/proj/51 §8.C) maps that
		// source text to this catalog_source id so a bridged upscaled cover carries
		// honest provenance. First-party derivation → trust_tier 0.
		{ID: 13, Key: "upscale", TrustTier: 0, Note: "first-party AI-upscaled cover derivation (galgame_cover.source='upscale')"},
		// cien (ci-en.net) is a creator-support / subscription platform — a
		// NON-IDENTITY external link only (link_kind=related), same tier as the
		// other web-presence sources (official_site/twitter/pixiv). Added for the
		// org/label link facet (refs/proj/83 E2b §3).
		{ID: 14, Key: "cien", TrustTier: 2, Note: "cien creator-support platform (ci-en.net)"},
		// dmm (dlsoft.dmm.co.jp / dmm.co.jp) is a storefront: work-level refs
		// imported from the EG mirror's typed dmm column land as PROBABLE
		// (community-maintained cross-reference, the same R8 middle tier as
		// EG's vndb column — refs/proj/91). Same tier as steam (8).
		{ID: 15, Key: "dmm", TrustTier: 2, Note: "DMM storefront (EG cross-reference lane, step 91)"},
		// web is the generic external-webpage catch-all: a link whose host has
		// no dedicated source row (an official site, a fan page, a storefront we
		// do not model). Its external_id is the FULL URL rather than a
		// site-native id, so it can never anchor an identity — rows are
		// link_kind=related only, and the lowest trust tier says so. Added by
		// the data-layer-retirement wave (refs/plans/10 W0), which rescues the
		// wiki family's 5,410 hand-entered links.
		{ID: 16, Key: "web", TrustTier: 2, Note: "generic external web page (external_id = full URL, related links only)"},
		// Getchu is a Japanese retailer whose product pages carry the one facet
		// no other upstream we ingest covers at scale: a structured character
		// roster (name / furigana / CV / 身長・スリーサイズ・血液型・誕生日 /
		// profile prose / portrait). Trust tier 1, not 2: the anchors are not
		// guessed from titles — they come from VNDB's own curated getchu
		// extlink on a release we already anchored EXACT, so a getchu ref
		// asserts identity as strongly as the vndb ref it rides on
		// (refs/proj/167 §1). Crawler: ../kun-getchu-api.
		{ID: 17, Key: "getchu", TrustTier: 1, Note: "Getchu.com retailer pages (character rosters, story text, sample CG; anchored via VNDB extlinks)"},
		// derived is the MACHINE INFERENCE lane (wave 184): rows nothing
		// upstream published and no human wrote, computed from facts the
		// catalog already holds. Its first (and so far only) product is the
		// series builder, which clusters connected components over the
		// series-ish work relation edges and materializes each as a
		// catalog_series with external_id "comp:<min member work id>".
		//
		// Its own source id, not curated's and not the asserting upstream's,
		// for two reasons. Provenance: a derived grouping is exactly as strong
		// as the inference that produced it, and filing it under vndb would
		// claim VNDB published a series it never did. Ownership: the human edit
		// face's curatedOnly guard already refuses to touch a non-curated
		// series, so a separate id makes "the builder is the only writer here"
		// true by construction rather than by convention — a human who wants a
		// different grouping curates one on lane 12, where the human always wins.
		//
		// trust_tier 1, level with the curated upstreams: the inference is
		// deterministic and rides on edges those upstreams asserted, but it is
		// still an inference, so it does not sit at first-party tier 0.
		{ID: 18, Key: "derived", TrustTier: 1, Note: "first-party machine inference over catalog facts (wave 184 series builder)"},
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
		// Symmetric character/setting-variation family (step 30, mapped from
		// Bangumi game-domain relations 4007/4009/4010). same_setting (8) already
		// covers "same world, different characters"; these complete the family
		// with the three variations Bangumi distinguishes but our step-02
		// vocabulary had not needed until the relation import surfaced them.
		{ID: 10, Key: "shares_character", Domain: model.RelationDomainWork, ForwardPhrase: "角色出演", ReversePhrase: "角色出演", IsSymmetric: true},
		{ID: 11, Key: "alternative_setting", Domain: model.RelationDomainWork, ForwardPhrase: "不同世界观", ReversePhrase: "不同世界观", IsSymmetric: true},
		{ID: 12, Key: "alternative_version", Domain: model.RelationDomainWork, ForwardPhrase: "不同演绎", ReversePhrase: "不同演绎", IsSymmetric: true},

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
// catalog_source additionally refreshes `key` — see the note at its call.
func Run(db *gorm.DB) error {
	roles, roleMap, err := loadGeneratedRoles()
	if err != nil {
		return err
	}
	// Hand-pinned roles (reserved band) + the EG shubetu map join the generated
	// vocabulary in the same idempotent upserts.
	roles = append(roles, handRoles()...)
	roleMap = append(roleMap, egRoleMap()...)
	roleMap = append(roleMap, egMusicRoleMap()...)
	roleMap = append(roleMap, dlsiteRoleMap()...)
	roleMap = append(roleMap, vndbRoleMap()...)

	if err := upsert(db, "catalog_medium", media(), []string{"id"}, []string{"name_cn"}); err != nil {
		return err
	}
	// `key` joins `note` in the refreshed set at wave 161. A source's key is
	// its public NAME (the public face renders it in source_keys[]), and this
	// slice is declared the single write path for the registry vocabulary — a
	// key that could only be corrected by hand-written SQL would mean the two
	// disagree with no mechanism to converge. The concrete need is source 12,
	// renamed galgame_wiki → curated; the id never moves.
	if err := upsert(db, "catalog_source", sources(), []string{"id"}, []string{"note", "key"}); err != nil {
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
	if err := upsert(db, "catalog_platform", platforms(), []string{"id"}, []string{"display_name"}); err != nil {
		return err
	}
	return nil
}

// platforms rows — the VNDB platform code vocabulary (step 96, refs/proj/96).
// Ids are seed-owned, keys alphabetical; the 48 codes are the full distinct
// set observed in src_vndb.releases_platforms (win 117,552 … fm8 8).
func platforms() []model.CatalogPlatform {
	return []model.CatalogPlatform{
		{ID: 1, Key: "and", DisplayName: "Android"},
		{ID: 2, Key: "bdp", DisplayName: "Blu-ray Player"},
		{ID: 3, Key: "dos", DisplayName: "DOS"},
		{ID: 4, Key: "drc", DisplayName: "Dreamcast"},
		{ID: 5, Key: "dvd", DisplayName: "DVD Player"},
		{ID: 6, Key: "fm7", DisplayName: "FM-7"},
		{ID: 7, Key: "fm8", DisplayName: "FM-8"},
		{ID: 8, Key: "fmt", DisplayName: "FM Towns"},
		{ID: 9, Key: "gba", DisplayName: "Game Boy Advance"},
		{ID: 10, Key: "gbc", DisplayName: "Game Boy Color"},
		{ID: 11, Key: "ios", DisplayName: "iOS"},
		{ID: 12, Key: "lin", DisplayName: "Linux"},
		{ID: 13, Key: "mac", DisplayName: "macOS"},
		{ID: 14, Key: "mob", DisplayName: "Mobile (feature phone)"},
		{ID: 15, Key: "msx", DisplayName: "MSX"},
		{ID: 16, Key: "n3d", DisplayName: "Nintendo 3DS"},
		{ID: 17, Key: "nds", DisplayName: "Nintendo DS"},
		{ID: 18, Key: "nes", DisplayName: "Famicom"},
		{ID: 19, Key: "oth", DisplayName: "Other"},
		{ID: 20, Key: "p88", DisplayName: "PC-88"},
		{ID: 21, Key: "p98", DisplayName: "PC-98"},
		{ID: 22, Key: "pce", DisplayName: "PC Engine"},
		{ID: 23, Key: "pcf", DisplayName: "PC-FX"},
		{ID: 24, Key: "ps1", DisplayName: "PlayStation"},
		{ID: 25, Key: "ps2", DisplayName: "PlayStation 2"},
		{ID: 26, Key: "ps3", DisplayName: "PlayStation 3"},
		{ID: 27, Key: "ps4", DisplayName: "PlayStation 4"},
		{ID: 28, Key: "ps5", DisplayName: "PlayStation 5"},
		{ID: 29, Key: "psp", DisplayName: "PlayStation Portable"},
		{ID: 30, Key: "psv", DisplayName: "PlayStation Vita"},
		{ID: 31, Key: "sat", DisplayName: "Sega Saturn"},
		{ID: 32, Key: "scd", DisplayName: "Sega Mega-CD"},
		{ID: 33, Key: "sfc", DisplayName: "Super Famicom"},
		{ID: 34, Key: "smd", DisplayName: "Mega Drive"},
		{ID: 35, Key: "sw2", DisplayName: "Nintendo Switch 2"},
		{ID: 36, Key: "swi", DisplayName: "Nintendo Switch"},
		{ID: 37, Key: "tdo", DisplayName: "3DO"},
		{ID: 38, Key: "vnd", DisplayName: "VNDS"},
		{ID: 39, Key: "web", DisplayName: "Web Browser"},
		{ID: 40, Key: "wii", DisplayName: "Wii"},
		{ID: 41, Key: "win", DisplayName: "Windows"},
		{ID: 42, Key: "wiu", DisplayName: "Wii U"},
		{ID: 43, Key: "x1s", DisplayName: "Sharp X1"},
		{ID: 44, Key: "x68", DisplayName: "Sharp X68000"},
		{ID: 45, Key: "xb1", DisplayName: "Xbox"},
		{ID: 46, Key: "xb3", DisplayName: "Xbox 360"},
		{ID: 47, Key: "xbo", DisplayName: "Xbox One"},
		{ID: 48, Key: "xxs", DisplayName: "Xbox Series X/S"},
	}
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
