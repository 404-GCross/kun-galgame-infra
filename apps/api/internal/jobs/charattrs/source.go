package charattrs

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// registry holds the catalog source ids resolved BY KEY (never hardcoded), so a
// rehearsal / prod DB with different auto-increment seeds still works.
type registry struct {
	vndbSource    int16
	bangumiSource int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&r.vndbSource).Error; err != nil {
		return r, fmt.Errorf("resolve vndb source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if r.vndbSource == 0 || r.bangumiSource == 0 {
		return r, fmt.Errorf("registry not seeded (vndb source=%d, bangumi source=%d)", r.vndbSource, r.bangumiSource)
	}
	return r, nil
}

// vndbCand is one character's anchor + its VNDB typed source columns. The
// current attribute state is loaded separately (preloadStates) — a flat scan
// struct, since GORM's Raw().Scan does not populate an embedded struct's
// promoted columns. DISTINCT ON keeps ONE anchor per character.
type vndbCand struct {
	EntityID   int64  `gorm:"column:entity_id"`
	ExternalID string `gorm:"column:external_id"`
	VSex       string `gorm:"column:v_sex"`
	VBlood     string `gorm:"column:v_bloodt"`
	VCup       string `gorm:"column:v_cup"`
	VBirthday  int16  `gorm:"column:v_birthday"`
	VHeight    int16  `gorm:"column:v_height"`
	VWeight    *int16 `gorm:"column:v_weight"`
	VBust      int16  `gorm:"column:v_bust"`
	VWaist     int16  `gorm:"column:v_waist"`
	VHip       int16  `gorm:"column:v_hip"`
}

// decode maps the VNDB typed columns to a proposed attrs set (sentinels and
// out-of-range values become nil).
func (v vndbCand) decode() attrs {
	m, d := vndbBirthday(v.VBirthday)
	weight := int16(0)
	if v.VWeight != nil {
		weight = *v.VWeight
	}
	return attrs{
		month:  m,
		day:    d,
		blood:  vndbBlood(v.VBlood),
		height: vndbGate(v.VHeight, minHeight, maxHeight),
		weight: vndbGate(weight, minWeight, maxWeight),
		bust:   vndbGate(v.VBust, minBWH, maxBWH),
		waist:  vndbGate(v.VWaist, minBWH, maxBWH),
		hip:    vndbGate(v.VHip, minBWH, maxBWH),
		cup:    vndbCup(v.VCup),
		gender: vndbSexGender(v.VSex),
	}
}

func loadVNDBCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]vndbCand, error) {
	query := `SELECT DISTINCT ON (c.id) c.id AS entity_id, r.external_id,
		sv.sex AS v_sex, sv.bloodt AS v_bloodt, sv.cup_size AS v_cup,
		sv.birthday AS v_birthday, sv.height AS v_height, sv.weight AS v_weight,
		sv.s_bust AS v_bust, sv.s_waist AS v_waist, sv.s_hip AS v_hip
		FROM catalog_character c
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
			AND r.source_id = ? AND r.link_kind = ?
		JOIN src_vndb.chars sv ON sv.id = r.external_id
		WHERE c.deleted_at IS NULL
		ORDER BY c.id, r.external_id`
	var out []vndbCand
	if err := db.WithContext(ctx).Raw(query, model.EntityTypeCharacter, reg.vndbSource, model.LinkKindExact).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load vndb candidates: %w", err)
	}
	return window(out, limit, offset), nil
}

// bgmCand is one character's anchor + its Bangumi infobox.
type bgmCand struct {
	EntityID   int64          `gorm:"column:entity_id"`
	ExternalID string         `gorm:"column:external_id"`
	Infobox    datatypes.JSON `gorm:"column:infobox_parsed"`
}

func loadBGMCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]bgmCand, error) {
	query := `SELECT DISTINCT ON (c.id) c.id AS entity_id, r.external_id, sb.infobox_parsed
		FROM catalog_character c
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
			AND r.source_id = ? AND r.link_kind = ?
		JOIN src_bangumi.character sb ON sb.id = r.external_id::bigint
		WHERE c.deleted_at IS NULL
		ORDER BY c.id, r.external_id::bigint`
	var out []bgmCand
	if err := db.WithContext(ctx).Raw(query, model.EntityTypeCharacter, reg.bangumiSource, model.LinkKindExact).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load bgm candidates: %w", err)
	}
	return window(out, limit, offset), nil
}

// preloadChunk keeps each state preload IN-list under the wire protocol's
// 65,535 parameter cap (the VNDB candidate set is ~160k characters).
const preloadChunk = 10000

// preloadStates loads the current attribute state of the given characters into
// a map, in chunks. A flat charState scan works cleanly where an embedded one
// does not.
func preloadStates(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]charState, error) {
	out := make(map[int64]charState, len(ids))
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		var rows []charState
		if err := db.WithContext(ctx).Raw(`SELECT
			id AS entity_id, birthday_month, birthday_day, blood_type, height_cm, weight_kg,
			bust_cm, waist_cm, hip_cm, cup, gender, extra, field_provenance
			FROM catalog_character WHERE id IN ?`, ids[start:end]).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("preload character states: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r
		}
	}
	return out, nil
}

// window applies offset/limit in Go after DISTINCT ON so they slice distinct
// characters (the dlsitemedia / entityintros discipline).
func window[T any](out []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(out) {
			return nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out
}
