package labellogos

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// auditPair is one label that carries BOTH a Bangumi and a Ci-en exact anchor.
//
// These are the ONLY labels where the bangumi > cien ruling is a real choice.
// Everywhere else exactly one source has a picture, so "bangumi first" decides
// nothing and cannot be wrong. Here two candidate images exist and the run
// order silently discards one of them — so this is the set a human reviews
// (charter §纪律: 抽 30 人审优先序) to confirm that a curated brand logo really
// does beat a creator's self-chosen avatar. Producing it costs one query and
// makes the ruling falsifiable instead of merely stated.
//
// LogoHash is carried so a review after the bangumi pass can see which labels
// already took the bangumi image, and Source names which lane wrote it.
type auditPair struct {
	LabelID     int64
	DisplayName string
	BangumiID   string
	CienID      string
	LogoHash    string
}

// collectAudit finds every live label anchored EXACTly to both sources. Run
// independently of --source and before any write, so the falsification set
// never depends on the pass being audited.
func collectAudit(ctx context.Context, db *gorm.DB, reg registry) ([]auditPair, error) {
	var rows []struct {
		LabelID     int64  `gorm:"column:label_id"`
		DisplayName string `gorm:"column:display_name"`
		BangumiID   string `gorm:"column:bangumi_id"`
		CienID      string `gorm:"column:cien_id"`
		LogoHash    string `gorm:"column:logo_hash"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (l.id)
		       l.id AS label_id, l.display_name AS display_name,
		       b.external_id AS bangumi_id, c.external_id AS cien_id,
		       l.logo_hash AS logo_hash
		FROM catalog_label l
		JOIN catalog_external_ref b ON b.entity_type = ? AND b.entity_id = l.id
			AND b.source_id = ? AND b.link_kind = ?
		JOIN catalog_external_ref c ON c.entity_type = ? AND c.entity_id = l.id
			AND c.source_id = ? AND c.link_kind = ?
		WHERE l.deleted_at IS NULL
		ORDER BY l.id, b.external_id, c.external_id`,
		model.EntityTypeLabel, reg.bangumi, model.LinkKindExact,
		model.EntityTypeLabel, reg.cien, model.LinkKindExact).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load dual-anchored labels: %w", err)
	}
	out := make([]auditPair, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditPair{
			LabelID: r.LabelID, DisplayName: r.DisplayName,
			BangumiID: r.BangumiID, CienID: r.CienID, LogoHash: r.LogoHash,
		})
	}
	return out, nil
}

// writeAudit dumps the falsification set as CSV for an offline image compare.
func writeAudit(path string, pairs []auditPair) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"label_id", "display_name", "bangumi_id", "cien_id", "logo_hash"}); err != nil {
		return err
	}
	for _, p := range pairs {
		if err := w.Write([]string{
			strconv.FormatInt(p.LabelID, 10), p.DisplayName, p.BangumiID, p.CienID, p.LogoHash,
		}); err != nil {
			return err
		}
	}
	return w.Error()
}
