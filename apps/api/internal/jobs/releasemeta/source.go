package releasemeta

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the catalog registry ids this backfill needs, resolved by key
// (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works — the workratings/dlsitemedia discipline.
type registry struct {
	bangumiSource int16
	dlsiteSource  int16
	egSource      int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	for _, s := range []struct {
		key string
		dst *int16
	}{
		{"bangumi", &r.bangumiSource},
		{"dlsite", &r.dlsiteSource},
		{"erogamespace", &r.egSource},
	} {
		if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, s.key).Scan(s.dst).Error; err != nil {
			return r, fmt.Errorf("resolve %s source: %w", s.key, err)
		}
	}
	if r.bangumiSource == 0 || r.dlsiteSource == 0 || r.egSource == 0 {
		return r, fmt.Errorf("registry not seeded (bangumi=%d, dlsite=%d, erogamespace=%d)",
			r.bangumiSource, r.dlsiteSource, r.egSource)
	}
	return r, nil
}

// emptyDate is the shared fill-empty candidate predicate: released_y NULL or 0
// (0 never carries meaning — years are real or absent).
const emptyDate = `(rel.released_y IS NULL OR rel.released_y = 0)`

// singleRelease restricts a work to exactly one live release — the 1:1 stub
// shape that makes a WORK-level date (EG/bgm) project onto a release
// unambiguously.
const singleRelease = `(SELECT count(*) FROM catalog_release r2 WHERE r2.work_id = w.id AND r2.deleted_at IS NULL) = 1`

// dlDateCandidate is one empty-date release with its DLsite workno anchor.
type dlDateCandidate struct {
	ReleaseID int64  `gorm:"column:release_id"`
	WorkID    int64  `gorm:"column:work_id"`
	Workno    string `gorm:"column:workno"`
}

// loadDlDateCandidates resolves empty-date releases carrying a DLsite EXACT
// release anchor. DISTINCT ON keeps ONE workno per release (the lowest —
// deterministic; multi-anchor releases are unobserved live). No medium or
// bodyless pin: the date is a registry scalar and the anchor owns it.
func loadDlDateCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]dlDateCandidate, error) {
	var out []dlDateCandidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (rel.id) rel.id AS release_id, rel.work_id AS work_id, r.external_id AS workno
			FROM catalog_release rel
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE rel.deleted_at IS NULL AND `+emptyDate+`
			ORDER BY rel.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// egDateCandidate is one BODYLESS 1:1-stub work whose single empty-date
// release may be filled from its EG exact WORK anchors (several ids when the
// work carries >1 anchor — the lane collapses to the lowest mirror-present).
type egDateCandidate struct {
	WorkID    int64
	ReleaseID int64
	EgIDs     []int64
}

// loadEgDateCandidates resolves bodyless works carrying an EG EXACT work
// anchor whose single live release has an empty date. A non-numeric
// external_id becomes the -1 sentinel so it is counted missing_in_mirror
// rather than silently dropped (workratings convention).
func loadEgDateCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]egDateCandidate, error) {
	var rows []struct {
		WorkID     int64  `gorm:"column:work_id"`
		ReleaseID  int64  `gorm:"column:release_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT w.id AS work_id, rel.id AS release_id, r.external_id AS external_id
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			WHERE (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
				AND `+emptyDate+` AND `+singleRelease+`
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.egSource, model.LinkKindExact).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	byWork := map[int64]*egDateCandidate{}
	var order []int64
	for _, r := range rows {
		egID, err := strconv.ParseInt(r.ExternalID, 10, 64)
		if err != nil {
			egID = -1
		}
		c := byWork[r.WorkID]
		if c == nil {
			c = &egDateCandidate{WorkID: r.WorkID, ReleaseID: r.ReleaseID}
			byWork[r.WorkID] = c
			order = append(order, r.WorkID)
		}
		c.EgIDs = append(c.EgIDs, egID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]egDateCandidate, 0, len(order))
	for _, id := range order {
		sort.Slice(byWork[id].EgIDs, func(i, j int) bool { return byWork[id].EgIDs[i] < byWork[id].EgIDs[j] })
		out = append(out, *byWork[id])
	}
	return window(out, limit, offset), nil
}

// bgmDateCandidate is one 1:1-stub work (any site — claimed works' releases
// are registry rows too) with its EXACT Bangumi anchor's subject date.
type bgmDateCandidate struct {
	WorkID    int64  `gorm:"column:work_id"`
	ReleaseID int64  `gorm:"column:release_id"`
	SubjectID int64  `gorm:"column:subject_id"`
	Date      string `gorm:"column:date"`
}

// loadBgmDateCandidates resolves works carrying any EXACT Bangumi work anchor
// (every matched_by tier asserts identity) whose single live release has an
// empty date, joined to the subject's free-text date. DISTINCT ON keeps ONE
// subject per work (the lowest id — workratings precedent); the ::bigint cast
// is safe (surveyed: zero non-numeric exact bgm work anchors).
func loadBgmDateCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]bgmDateCandidate, error) {
	var out []bgmDateCandidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, rel.id AS release_id,
				sub.id AS subject_id, sub.date AS date
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			WHERE w.deleted_at IS NULL AND `+emptyDate+` AND `+singleRelease+`
			ORDER BY w.id, sub.id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// ratingCandidate is one unrated (content_rating=0) work; Site/ProductWorkID
// route the wiki lane for claimed rows.
type ratingCandidate struct {
	WorkID        int64   `gorm:"column:work_id"`
	Site          *string `gorm:"column:site"`
	ProductWorkID *int64  `gorm:"column:product_work_id"`
}

// loadRatingCandidates resolves every live work still at content_rating=0
// (0 = unrated by the step-11 ruling — the fillable "empty").
func loadRatingCandidates(ctx context.Context, db *gorm.DB, limit, offset int) ([]ratingCandidate, error) {
	var out []ratingCandidate
	if err := db.WithContext(ctx).
		Raw(`SELECT id AS work_id, site AS site, product_work_id AS product_work_id
			FROM catalog_work
			WHERE deleted_at IS NULL AND content_rating = 0
			ORDER BY id`).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// loadRatingDlsiteAnchors maps unrated works to ONE DLsite workno each via
// their release anchors (DISTINCT ON lowest workno). One query — the caller
// intersects with its windowed candidate set.
func loadRatingDlsiteAnchors(ctx context.Context, db *gorm.DB, reg registry) (map[int64]string, error) {
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Workno string `gorm:"column:workno"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.deleted_at IS NULL AND w.content_rating = 0
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.WorkID] = r.Workno
	}
	return out, nil
}

// loadRatingBgmNSFW maps unrated works to their representative EXACT Bangumi
// subject's nsfw flag (DISTINCT ON lowest subject id).
func loadRatingBgmNSFW(ctx context.Context, db *gorm.DB, reg registry) (map[int64]bool, error) {
	var rows []struct {
		WorkID int64 `gorm:"column:work_id"`
		NSFW   bool  `gorm:"column:nsfw"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, sub.nsfw AS nsfw
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.deleted_at IS NULL AND w.content_rating = 0
			ORDER BY w.id, sub.id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.WorkID] = r.NSFW
	}
	return out, nil
}
