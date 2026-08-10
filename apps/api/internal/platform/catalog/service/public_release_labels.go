// public_release_labels.go — per-EDITION company attribution on the public
// face (wave 200).
//
// The storage half of the wave moved the fact to where it is true
// (catalog_release_label, see model.CatalogReleaseLabel for the worked example);
// this is the half that lets anybody see it. Without it the table is written and
// never read, and a work page still shows one flat pile of companies.
//
// Two rules it inherits rather than re-decides:
//
//   - l.deleted_at IS NULL. The edge is NOT the authority on whether the label
//     still exists — read_labels.go's comment spells out why (an edge survives
//     its label being merged away, and projecting it anyway renders the
//     merged-away twin beside the survivor).
//   - the collapse is publicWorkLabels, shared verbatim with the work grain, so
//     "one entry per company, capacities in kinds[]" cannot mean two things on
//     two faces.
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
)

// releaseLabelsFor loads the company chips for a set of releases in ONE query,
// keyed by release id. A release with no attribution is absent from the map;
// callers render [] for it.
//
// work_count is NOT filled here — it is a taxonomy aggregate over a different
// edge, and the callers batch it across the whole record / page together with
// their work-level chips (see fillWorkLabelCounts).
func (s *PublicService) releaseLabelsFor(ctx context.Context, releaseIDs []int64) (map[int64][]dto.PublicWorkLabel, error) {
	out := make(map[int64][]dto.PublicWorkLabel, len(releaseIDs))
	if len(releaseIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ReleaseID   int64  `gorm:"column:release_id"`
		LabelID     int64  `gorm:"column:label_id"`
		DisplayName string `gorm:"column:display_name"`
		LabelKind   int16  `gorm:"column:label_kind"`
		Kind        int16  `gorm:"column:kind"`
		Lang        string `gorm:"column:lang"`
		LogoHash    string `gorm:"column:logo_hash"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT rl.release_id, rl.label_id, l.display_name, l.kind AS label_kind,
			rl.kind AS kind, l.lang, l.logo_hash
		FROM catalog_release_label rl
		JOIN catalog_label l ON l.id = rl.label_id AND l.deleted_at IS NULL
		WHERE rl.release_id IN ?
		ORDER BY rl.release_id, rl.kind, l.display_name`, releaseIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	grouped := make(map[int64][]LabelAttribution, len(releaseIDs))
	for _, r := range rows {
		grouped[r.ReleaseID] = append(grouped[r.ReleaseID], LabelAttribution{
			LabelID: r.LabelID, DisplayName: r.DisplayName, LabelKind: r.LabelKind,
			Kind: r.Kind, Lang: r.Lang, LogoHash: r.LogoHash,
		})
	}
	for id, attrs := range grouped {
		out[id] = publicWorkLabels(attrs)
	}
	return out, nil
}

// attachReleaseLabels fills labels[] on an already-built releases[] block, in
// one query for the whole block. Every release ends up with a non-nil slice —
// the wire promise is [] not null.
func (s *PublicService) attachReleaseLabels(ctx context.Context, releases []dto.PublicRelease) error {
	if len(releases) == 0 {
		return nil
	}
	ids := make([]int64, len(releases))
	for i, r := range releases {
		ids[i] = r.ID
	}
	byRelease, err := s.releaseLabelsFor(ctx, ids)
	if err != nil {
		return err
	}
	for i := range releases {
		if labels := byRelease[releases[i].ID]; labels != nil {
			releases[i].Labels = labels
		} else {
			releases[i].Labels = []dto.PublicWorkLabel{}
		}
	}
	return nil
}
