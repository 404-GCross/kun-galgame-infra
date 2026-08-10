package labellogos

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"

	"gorm.io/gorm"
)

type auditPair struct {
	LabelID     int64
	DisplayName string
	BangumiID   string
	CienID      string
	LogoHash    string
}

func collectAudit(ctx context.Context, db *gorm.DB, reg registry) ([]auditPair, error) {
	var rows []struct {
		LabelID     int64  `gorm:"column:label_id"`
		DisplayName string `gorm:"column:display_name"`
		BangumiID   string `gorm:"column:bangumi_id"`
		CienID      string `gorm:"column:cien_id"`
		LogoHash    string `gorm:"column:logo_hash"`
	}
	bClause, bArgs := anchorClause("b", reg.bangumi, SourceBangumi)
	cClause, cArgs := anchorClause("c", reg.cien, SourceCien)
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (l.id)
		       l.id AS label_id, l.display_name AS display_name,
		       b.external_id AS bangumi_id, c.external_id AS cien_id,
		       l.logo_hash AS logo_hash
		FROM catalog_label l
		JOIN catalog_external_ref b ON b.entity_id = l.id AND `+bClause+`
		JOIN catalog_external_ref c ON c.entity_id = l.id AND `+cClause+`
		WHERE l.deleted_at IS NULL
		ORDER BY l.id, b.external_id, c.external_id`,
		append(bArgs, cArgs...)...).Scan(&rows).Error
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
