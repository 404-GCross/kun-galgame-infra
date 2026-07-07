package service

import (
	"context"
	stderrors "errors"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// ReadService backs the S2S read face (step 18, D-01): anchor read-through and
// credits-by-work. Pure reads over the catalog DB; transport-agnostic (the
// handler maps these to DTOs).
type ReadService struct{ db *gorm.DB }

func NewReadService(db *gorm.DB) *ReadService { return &ReadService{db: db} }

// ErrWorkNotFound is returned when an anchor resolves to no work.
var ErrWorkNotFound = stderrors.New("catalog: no work for anchor")

// WorkDetail is the anchor read-through result.
type WorkDetail struct {
	Work     model.CatalogWork
	Titles   []model.CatalogWorkTitle
	Releases []ReleaseDetail
	Labels   []LabelAttribution
}

// ReleaseDetail is a release plus its anchors.
type ReleaseDetail struct {
	Release model.CatalogRelease
	Anchors []AnchorDetail
}

// AnchorDetail is one external anchor with the source key resolved.
type AnchorDetail struct {
	Source     string
	ExternalID string
	LinkKind   int16
}

// LabelAttribution is one work↔label edge with the label denormalized.
type LabelAttribution struct {
	LabelID     int64
	DisplayName string
	LabelKind   int16
	Kind        int16 // attribution edge kind
}

// WorkByAnchor resolves a work via any of its external anchors (work- or
// release-level) and loads its titles, releases (with anchors) and label
// attributions. A release anchor traces back to its work; a lower link_kind
// (exact before probable) and a work-level anchor win ties.
func (s *ReadService) WorkByAnchor(ctx context.Context, sourceKey, externalID string) (*WorkDetail, error) {
	db := s.db.WithContext(ctx)

	var srcID int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKey).Scan(&srcID).Error; err != nil {
		return nil, err
	}
	if srcID == 0 {
		return nil, ErrWorkNotFound // unknown source key == no such anchor
	}

	var ref struct {
		EntityType int16
		EntityID   int64
	}
	if err := db.Raw(`SELECT entity_type, entity_id FROM catalog_external_ref
		WHERE source_id = ? AND external_id = ? AND entity_type IN (?, ?)
		ORDER BY link_kind ASC, entity_type ASC LIMIT 1`,
		srcID, externalID, model.EntityTypeWork, model.EntityTypeRelease).Scan(&ref).Error; err != nil {
		return nil, err
	}
	if ref.EntityID == 0 {
		return nil, ErrWorkNotFound
	}

	workID := ref.EntityID
	if ref.EntityType == model.EntityTypeRelease {
		if err := db.Raw(`SELECT work_id FROM catalog_release WHERE id = ?`, ref.EntityID).Scan(&workID).Error; err != nil {
			return nil, err
		}
	}

	var work model.CatalogWork
	if err := db.First(&work, workID).Error; err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkNotFound
		}
		return nil, err
	}

	detail := &WorkDetail{Work: work}
	if err := db.Where("work_id = ?", workID).Order("kind, lang").Find(&detail.Titles).Error; err != nil {
		return nil, err
	}

	var releases []model.CatalogRelease
	if err := db.Where("work_id = ?", workID).Order("id").Find(&releases).Error; err != nil {
		return nil, err
	}
	anchorsByRelease := map[int64][]AnchorDetail{}
	if len(releases) > 0 {
		relIDs := make([]int64, len(releases))
		for i, r := range releases {
			relIDs[i] = r.ID
		}
		var arows []struct {
			EntityID   int64
			Source     string
			ExternalID string
			LinkKind   int16
		}
		if err := db.Raw(`SELECT r.entity_id, s.key AS source, r.external_id, r.link_kind
			FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
			WHERE r.entity_type = ? AND r.entity_id IN ?
			ORDER BY r.link_kind, s.key`, model.EntityTypeRelease, relIDs).Scan(&arows).Error; err != nil {
			return nil, err
		}
		for _, a := range arows {
			anchorsByRelease[a.EntityID] = append(anchorsByRelease[a.EntityID],
				AnchorDetail{Source: a.Source, ExternalID: a.ExternalID, LinkKind: a.LinkKind})
		}
	}
	for _, r := range releases {
		detail.Releases = append(detail.Releases, ReleaseDetail{Release: r, Anchors: anchorsByRelease[r.ID]})
	}

	if err := db.Raw(`SELECT wl.label_id, l.display_name, l.kind AS label_kind, wl.kind AS kind
		FROM catalog_work_label wl JOIN catalog_label l ON l.id = wl.label_id
		WHERE wl.work_id = ? ORDER BY wl.kind, l.display_name`, workID).Scan(&detail.Labels).Error; err != nil {
		return nil, err
	}
	return detail, nil
}

// CreditRow is one credit joined with its role, name, character and source.
type CreditRow struct {
	RoleID       int64
	RoleKey      string
	RoleNameCN   string
	RoleNameJA   string
	CreditNameID int64
	Name         string
	Lang         string
	Latin        *string
	CharacterID  *int64
	CharacterNM  *string
	Note         string
	SourceKey    *string
}

// WorkCredits loads a work's credits ordered by role then source then name.
// Orphan credit names are returned as-is (no person layer). The caller groups
// consecutive rows by role.
func (s *ReadService) WorkCredits(ctx context.Context, workID int64) ([]CreditRow, error) {
	var rows []CreditRow
	err := s.db.WithContext(ctx).Raw(`SELECT
		c.role_id, ro.key AS role_key, ro.name_cn AS role_name_cn, ro.name_ja AS role_name_ja,
		cn.id AS credit_name_id, cn.name, cn.lang, cn.latin,
		c.character_id, ch.display_name AS character_nm, c.note, src.key AS source_key
		FROM catalog_credit c
		JOIN catalog_role ro ON ro.id = c.role_id
		JOIN catalog_credit_name cn ON cn.id = c.credit_name_id
		LEFT JOIN catalog_character ch ON ch.id = c.character_id
		LEFT JOIN catalog_source src ON src.id = c.source_id
		WHERE c.work_id = ?
		ORDER BY c.role_id ASC, src.key ASC NULLS LAST, cn.id ASC`, workID).Scan(&rows).Error
	return rows, err
}
