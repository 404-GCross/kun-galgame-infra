package releasemeta

import (
	"context"

	"gorm.io/gorm"
)

// mirrorBatch is the shared IN-list chunk size for mirror reads.
const mirrorBatch = 1000

// dlDate is one DLsite mirror regist_date, pre-split. nil fields = the mirror
// row exists but regist_date IS NULL ("DLsite never published a date").
type dlDate struct {
	y, m, d *int
}

// loadDlsiteDates batch-loads regist_date trios for the referenced worknos.
// The timestamptz is rendered AT TIME ZONE 'Asia/Shanghai' — the wall clock
// the mirror ingest recorded and the step-55/56 importers (process-local
// splitDate on a +08 box) already projected into catalog rows, so re-derived
// dates are byte-identical to the imported ones; surveyed: zero hour-23 rows,
// so this equals the JST calendar date on every row. Extracting explicitly
// also makes the run independent of any session TimeZone setting.
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

// loadDlsiteAges batch-loads age_category ('1' general / '2' r15 / '3' adult;
// NULL/” = unpublished) for the referenced worknos.
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

// loadEgSelldays batch-loads the EG mirror's sellday text ('YYYY-MM-DD';
// surveyed: always non-NULL and full-ISO, with 2050-01-01 as the TBA
// placeholder — the year gate rejects it).
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

// loadWikiAgeLimits batch-loads galgame.age_limit (surveyed live values:
// 'r18' / 'all') for the referenced wiki galgame ids.
func loadWikiAgeLimits(ctx context.Context, wikiDB *gorm.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	type row struct {
		ID       int64   `gorm:"column:id"`
		AgeLimit *string `gorm:"column:age_limit"`
	}
	for start := 0; start < len(ids); start += mirrorBatch {
		end := min(start+mirrorBatch, len(ids))
		var batch []row
		if err := wikiDB.WithContext(ctx).Table("galgame").
			Select("id, age_limit").
			Where("id IN ?", ids[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			limit := ""
			if r.AgeLimit != nil {
				limit = *r.AgeLimit
			}
			out[r.ID] = limit
		}
	}
	return out, nil
}
