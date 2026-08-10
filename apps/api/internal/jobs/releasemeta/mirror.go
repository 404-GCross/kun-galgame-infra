package releasemeta

import (
	"context"

	"gorm.io/gorm"
)

const mirrorBatch = 1000

type dlDate struct {
	y, m, d *int
}

func loadDlsiteDates(ctx context.Context, dlDB *gorm.DB, worknos []string) (map[string]dlDate, error) {
	out := make(map[string]dlDate, len(worknos))
	type row struct {
		Workno string `gorm:"column:workno"`
		Y      *int   `gorm:"column:y"`
		M      *int   `gorm:"column:m"`
		D      *int   `gorm:"column:d"`
	}
	for start := 0; start < len(worknos); start += mirrorBatch {
		end := min(start+mirrorBatch, len(worknos))
		var batch []row
		if err := dlDB.WithContext(ctx).Table("works").
			Select(`workno,
				EXTRACT(YEAR  FROM regist_date AT TIME ZONE 'Asia/Shanghai')::int AS y,
				EXTRACT(MONTH FROM regist_date AT TIME ZONE 'Asia/Shanghai')::int AS m,
				EXTRACT(DAY   FROM regist_date AT TIME ZONE 'Asia/Shanghai')::int AS d`).
			Where("workno IN ?", worknos[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			out[r.Workno] = dlDate{y: r.Y, m: r.M, d: r.D}
		}
	}
	return out, nil
}

func loadDlsiteAges(ctx context.Context, dlDB *gorm.DB, worknos []string) (map[string]string, error) {
	out := make(map[string]string, len(worknos))
	type row struct {
		Workno      string  `gorm:"column:workno"`
		AgeCategory *string `gorm:"column:age_category"`
	}
	for start := 0; start < len(worknos); start += mirrorBatch {
		end := min(start+mirrorBatch, len(worknos))
		var batch []row
		if err := dlDB.WithContext(ctx).Table("works").
			Select("workno, age_category").
			Where("workno IN ?", worknos[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			age := ""
			if r.AgeCategory != nil {
				age = *r.AgeCategory
			}
			out[r.Workno] = age
		}
	}
	return out, nil
}

func loadEgSelldays(ctx context.Context, egDB *gorm.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	type row struct {
		ID      int64   `gorm:"column:id"`
		Sellday *string `gorm:"column:sellday"`
	}
	for start := 0; start < len(ids); start += mirrorBatch {
		end := min(start+mirrorBatch, len(ids))
		var batch []row
		if err := egDB.WithContext(ctx).Table("games").
			Select("id, sellday").
			Where("id IN ?", ids[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			s := ""
			if r.Sellday != nil {
				s = *r.Sellday
			}
			out[r.ID] = s
		}
	}
	return out, nil
}
