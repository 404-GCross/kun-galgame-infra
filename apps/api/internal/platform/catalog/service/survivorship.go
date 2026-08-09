package service

import (
	"encoding/json"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// v0 merge survivorship (doc 10 §6.2-1). Default rule per field: the target
// keeps non-empty values; empty target fields fill from the source. The
// proposal's field_resolution ({"<field>": "source"}) overrides the default
// per field — EXCEPT that a user-owned target value (field_provenance first
// entry source == "user") is never overwritten by a non-user source value
// (doc 10 invariant 6). The configurable survivorship ENGINE (priority
// table driven) is a later step; this is only the merge-internal default.

type fieldResolution map[string]string

func parseResolution(raw datatypes.JSON) (fieldResolution, error) {
	res := fieldResolution{}
	if len(raw) == 0 {
		return res, nil
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse field_resolution: %w", err)
	}
	return res, nil
}

// firstProvSource returns the owning source key of a field per the R8
// provenance array semantics ({"field": [{"source": ...}, ...]}, latest
// writer first); "" when the field has no provenance.
func firstProvSource(prov datatypes.JSON, field string) string {
	if len(prov) == 0 {
		return ""
	}
	var doc map[string][]struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(prov, &doc); err != nil {
		return ""
	}
	entries := doc[field]
	if len(entries) == 0 {
		return ""
	}
	return entries[0].Source
}

// fieldMerger accumulates the survivorship decisions of one merge.
type fieldMerger struct {
	res     fieldResolution
	srcProv datatypes.JSON
	dstProv datatypes.JSON
	updates map[string]any // column updates to apply on the target
	changed map[string]any // changed_fields for the merged_target revision
}

func newFieldMerger(res fieldResolution, srcProv, dstProv datatypes.JSON) *fieldMerger {
	return &fieldMerger{res: res, srcProv: srcProv, dstProv: dstProv,
		updates: map[string]any{}, changed: map[string]any{}}
}

// take decides whether the source value replaces the target's for a field:
// default fill-if-empty; field_resolution "source" forces it — unless the
// user-protection rule blocks the overwrite.
func (m *fieldMerger) take(field string, targetEmpty, sourceEmpty bool) bool {
	if sourceEmpty {
		return false
	}
	if !targetEmpty {
		if m.res[field] != "source" {
			return false
		}
		if firstProvSource(m.dstProv, field) == "user" && firstProvSource(m.srcProv, field) != "user" {
			return false
		}
	}
	return true
}

func (m *fieldMerger) str(field, column, dstVal, srcVal string) {
	if m.take(field, dstVal == "", srcVal == "") {
		m.updates[column] = srcVal
		m.changed[field] = srcVal
	}
}

func mergerPtr[T any](m *fieldMerger, field, column string, dstVal, srcVal *T) {
	if m.take(field, dstVal == nil, srcVal == nil) {
		m.updates[column] = *srcVal
		m.changed[field] = *srcVal
	}
}

// trio handles a fuzzy-date column trio as ONE field (mixing source and
// target date parts would produce dates neither side asserted).
func (m *fieldMerger) trio(field, prefix string, dst [3]*int16, src [3]*int16) {
	dstEmpty := dst[0] == nil && dst[1] == nil && dst[2] == nil
	srcEmpty := src[0] == nil && src[1] == nil && src[2] == nil
	if m.take(field, dstEmpty, srcEmpty) {
		m.updates[prefix+"_y"], m.updates[prefix+"_m"], m.updates[prefix+"_d"] = src[0], src[1], src[2]
		m.changed[field] = map[string]*int16{"y": src[0], "m": src[1], "d": src[2]}
	}
}

// apply writes the accumulated updates (plus merged provenance for the
// changed fields) onto the target row.
func (m *fieldMerger) apply(tx *gorm.DB, table string, targetID int64) error {
	if len(m.updates) == 0 {
		return nil
	}
	if prov, err := mergeProvenance(m.dstProv, m.srcProv, m.changed); err == nil && prov != nil {
		m.updates["field_provenance"] = prov
	} else if err != nil {
		return err
	}
	// Table() hands GORM no model, so its auto-update-time convention cannot
	// fire — without this the survivor would keep a stale updated_at after the
	// merge rewrote its fields. now() is the transaction timestamp, the same
	// instant every other statement of this merge stamps.
	m.updates["updated_at"] = gorm.Expr("now()")
	return tx.Table(table).Where("id = ?", targetID).Updates(m.updates).Error
}

// mergeProvenance copies the provenance lists of the fields that changed
// from the source document into the target's, so a filled field keeps its
// origin story.
func mergeProvenance(dstProv, srcProv datatypes.JSON, changed map[string]any) (datatypes.JSON, error) {
	if len(changed) == 0 {
		return nil, nil
	}
	dst := map[string]json.RawMessage{}
	if len(dstProv) > 0 {
		if err := json.Unmarshal(dstProv, &dst); err != nil {
			return nil, fmt.Errorf("parse target provenance: %w", err)
		}
	}
	src := map[string]json.RawMessage{}
	if len(srcProv) > 0 {
		if err := json.Unmarshal(srcProv, &src); err != nil {
			return nil, fmt.Errorf("parse source provenance: %w", err)
		}
	}
	for field := range changed {
		if entry, ok := src[field]; ok {
			dst[field] = entry
		}
	}
	raw, err := json.Marshal(dst)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(raw), nil
}

// applySurvivorship runs the v0 field rules for one merge and returns the
// changed-field map (the merged_target revision's changed_fields).
func applySurvivorship(tx *gorm.DB, entityType int16, src, dst int64, resolutionRaw datatypes.JSON) (map[string]any, error) {
	res, err := parseResolution(resolutionRaw)
	if err != nil {
		return nil, err
	}

	switch entityType {
	case model.EntityTypePerson:
		var s, d model.CatalogPerson
		if err := loadPair(tx, &s, &d, src, dst); err != nil {
			return nil, err
		}
		m := newFieldMerger(res, s.FieldProvenance, d.FieldProvenance)
		m.str("display_name", "display_name", d.DisplayName, s.DisplayName)
		m.str("description", "description", d.Description, s.Description)
		mergerPtr(m, "gender", "gender", d.Gender, s.Gender)
		m.trio("birth", "birth", [3]*int16{d.BirthY, d.BirthM, d.BirthD}, [3]*int16{s.BirthY, s.BirthM, s.BirthD})
		// The source's primary name is about to become the target's own
		// name (rehang), so the pointer stays valid.
		mergerPtr(m, "primary_credit_name_id", "primary_credit_name_id", d.PrimaryCreditNameID, s.PrimaryCreditNameID)
		return m.changed, m.apply(tx, "catalog_person", dst)

	case model.EntityTypeCreditName:
		var s, d model.CatalogCreditName
		if err := loadPair(tx, &s, &d, src, dst); err != nil {
			return nil, err
		}
		m := newFieldMerger(res, s.FieldProvenance, d.FieldProvenance)
		mergerPtr(m, "latin", "latin", d.Latin, s.Latin)
		m.str("lang", "lang", d.Lang, s.Lang)
		m.str("note", "note", d.Note, s.Note)
		return m.changed, m.apply(tx, "catalog_credit_name", dst)

	case model.EntityTypeLabel:
		var s, d model.CatalogLabel
		if err := loadPair(tx, &s, &d, src, dst); err != nil {
			return nil, err
		}
		m := newFieldMerger(res, s.FieldProvenance, d.FieldProvenance)
		m.str("display_name", "display_name", d.DisplayName, s.DisplayName)
		mergerPtr(m, "latin", "latin", d.Latin, s.Latin)
		m.str("lang", "lang", d.Lang, s.Lang)
		m.str("note", "note", d.Note, s.Note)
		// A brand logo is expensive signal, exactly like a character portrait
		// (see image_hash below): it costs a mirror + upload to obtain and the
		// merge is the one moment it can be silently lost. Fill-if-empty keeps
		// the survivor's own logo and adopts the source's when the survivor
		// is bare.
		m.str("logo_hash", "logo_hash", d.LogoHash, s.LogoHash)
		return m.changed, m.apply(tx, "catalog_label", dst)

	case model.EntityTypeCharacter:
		var s, d model.CatalogCharacter
		if err := loadPair(tx, &s, &d, src, dst); err != nil {
			return nil, err
		}
		m := newFieldMerger(res, s.FieldProvenance, d.FieldProvenance)
		m.str("display_name", "display_name", d.DisplayName, s.DisplayName)
		mergerPtr(m, "latin", "latin", d.Latin, s.Latin)
		m.str("lang", "lang", d.Lang, s.Lang)
		mergerPtr(m, "gender", "gender", d.Gender, s.Gender)
		m.str("description", "description", d.Description, s.Description)
		mergerPtr(m, "instance_of", "instance_of", d.InstanceOf, s.InstanceOf)
		// A portrait is expensive signal — never drop it in a merge. The
		// fill-if-empty default keeps the survivor's own hash when it has one
		// and adopts the source's when the survivor is bare (step 49; the
		// dedup batch also prefers a portrait-bearing survivor, so this is the
		// belt-and-suspenders that also covers hand-driven merges).
		mergerPtr(m, "image_hash", "image_hash", d.ImageHash, s.ImageHash)
		return m.changed, m.apply(tx, "catalog_character", dst)

	case model.EntityTypeWork:
		var s, d model.CatalogWork
		if err := loadPair(tx, &s, &d, src, dst); err != nil {
			return nil, err
		}
		m := newFieldMerger(res, s.FieldProvenance, d.FieldProvenance)
		m.str("display_name", "display_name", d.DisplayName, s.DisplayName)
		m.str("olang", "olang", d.OLang, s.OLang)
		changed := m.changed
		if err := m.apply(tx, "catalog_work", dst); err != nil {
			return nil, err
		}
		// Claim transfer is a unit: when the target is unclaimed and the
		// source is claimed, the claim moves (source first — the unique
		// index holds both rows).
		if d.Site == nil && s.Site != nil {
			if err := tx.Exec(`UPDATE catalog_work SET site = NULL, product_work_id = NULL, updated_at = now() WHERE id = ?`, src).Error; err != nil {
				return nil, err
			}
			if err := tx.Exec(`UPDATE catalog_work SET site = ?, product_work_id = ?, status = ?, updated_at = now() WHERE id = ?`,
				*s.Site, s.ProductWorkID, s.Status, dst).Error; err != nil {
				return nil, err
			}
			changed["claim"] = map[string]any{"site": *s.Site, "product_work_id": s.ProductWorkID}
		}
		return changed, nil
	}
	return nil, fmt.Errorf("catalog survivorship: unsupported entity type %d", entityType)
}

// loadPair fetches source and target rows (soft-deleted rows included —
// merge operates below the soft-delete veil).
func loadPair[T any](tx *gorm.DB, src, dst *T, srcID, dstID int64) error {
	if err := tx.Unscoped().First(src, srcID).Error; err != nil {
		return fmt.Errorf("load source %d: %w", srcID, err)
	}
	if err := tx.Unscoped().First(dst, dstID).Error; err != nil {
		return fmt.Errorf("load target %d: %w", dstID, err)
	}
	return nil
}
