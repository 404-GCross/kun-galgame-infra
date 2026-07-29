package repository

import (
	"context"
	"strings"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GalgameRepository handles galgame data access on kun_galgame_wiki
type GalgameRepository struct {
	db *gorm.DB
}

// NewGalgameRepository creates a new GalgameRepository
func NewGalgameRepository(db *gorm.DB) *GalgameRepository {
	return &GalgameRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions
func (r *GalgameRepository) DB() *gorm.DB {
	return r.db
}

// FindByID finds a galgame by ID with all relations
func (r *GalgameRepository) FindByID(ctx context.Context, id int) (*model.Galgame, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Preload("Alias").
		Preload("Series").
		Preload("Contributor").
		Preload("Link").
		// Inject the same `galgame_count` (aliased as `cnt`) subquery
		// that the dedicated GET /official, GET /engine, and GET /tag
		// list endpoints use, so detail-embedded officials / engines /
		// tags carry the count without consumers (kungal, moyu) having
		// to issue N follow-up requests to learn "this 会社 has how
		// many galgames?". The count filters galgame.status = 0 to
		// match what the list and per-official detail pages display —
		// drafts imported by sync-vndb stay excluded.
		Preload("Tag.Tag", func(db *gorm.DB) *gorm.DB {
			return db.
				Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
				Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id")
		}).
		Preload("Official.Official", func(db *gorm.DB) *gorm.DB {
			return db.
				Select("galgame_official.*, COALESCE(oc.cnt, 0) AS cnt").
				Joins("LEFT JOIN (SELECT r.official_id, COUNT(*) AS cnt FROM galgame_official_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.official_id) oc ON oc.official_id = galgame_official.id")
		}).
		Preload("Official.Official.Alias").
		Preload("Engine.Engine", func(db *gorm.DB) *gorm.DB {
			return db.
				Select("galgame_engine.*, COALESCE(ec.cnt, 0) AS cnt").
				Joins("LEFT JOIN (SELECT r.engine_id, COUNT(*) AS cnt FROM galgame_engine_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.engine_id) ec ON ec.engine_id = galgame_engine.id")
		}).
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Preload("Screenshot", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		First(&galgame, id).Error
	if err != nil {
		return nil, err
	}
	// Populate the derived EffectiveBannerHash via the model-layer helper
	// so List / ListMine / FindByID share one implementation of the
	// "pinned cover" rule.
	model.PopulateEffectiveBanner(&galgame)
	// Portrait pin (vertical banner) — same loaded Cover list, distinct flag.
	model.PopulateEffectivePortrait(&galgame)
	return &galgame, nil
}

// ExistsByVNDBID checks if a galgame with the given VNDB ID exists
func (r *GalgameRepository) ExistsByVNDBID(ctx context.Context, vndbID string) (bool, int, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Select("id").
		Where("vndb_id = ?", vndbID).
		First(&galgame).Error
	if err == gorm.ErrRecordNotFound {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, galgame.ID, nil
}

// List returns a paginated list of galgames.
//
// contentLimit follows the canonical content-filter contract (see
// pkg/utils.ParseContentLimit): "sfw" / "nsfw" → WHERE filter, "" → no
// filter. Handlers resolve the default for missing query params.
//
// releasedFrom / releasedTo are inclusive bounds on galgame.release_date.
// Zero `time.Time` on either side = no filter on that side. The column
// has a btree index (declared in the model via gorm tag), so range
// scans are O(log N) — safe to expose to anonymous traffic.
// galgames with release_date IS NULL are excluded when either bound is
// set (SQL `>=` / `<=` on NULL is UNKNOWN → row drops out, matching
// the user-visible contract: "filter to 2024 = only games we know are
// in 2024").
//
// releasedMonths is an optional set of calendar months (1-12); when
// non-empty it adds `EXTRACT(MONTH FROM release_date) IN (...)` — an
// orthogonal AND filter on top of the year range. Non-sargable, but it
// only rechecks rows the year-range scan already narrowed. NULL
// release_date drops out here too (EXTRACT(NULL) → NULL → not IN).
func (r *GalgameRepository) List(ctx context.Context, page, limit int, sortField, sortOrder, search, contentLimit string, releasedFrom, releasedTo time.Time, releasedMonths []int) (items []model.Galgame, total int64, err error) {
	defer func() {
		for i := range items {
			model.PopulateEffectiveBanner(&items[i])
		}
	}()

	query := r.db.WithContext(ctx).Model(&model.Galgame{}).Where("status = 0")

	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}

	// Date filter — compare against the `date`-typed column with a
	// formatted YYYY-MM-DD string. PG's `date >= 'YYYY-MM-DD'` uses the
	// btree index; passing time.Time directly would cause an implicit
	// timestamptz coercion that drops index access.
	if !releasedFrom.IsZero() {
		query = query.Where("release_date >= ?", releasedFrom.Format("2006-01-02"))
	}
	if !releasedTo.IsZero() {
		query = query.Where("release_date <= ?", releasedTo.Format("2006-01-02"))
	}
	if len(releasedMonths) > 0 {
		query = query.Where("EXTRACT(MONTH FROM release_date)::int IN ?", releasedMonths)
	}

	if search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where(
			"LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ?",
			like, like, like, like,
		)
	}

	query.Count(&total)

	// Whitelist allowed sort fields (snake_case column names only).
	// SQL-injection-safe: the value is keyed into this map, never
	// interpolated raw — an unknown field falls back to "created".
	allowedSortFields := map[string]bool{
		"created":              true,
		"updated":              true,
		"view":                 true,
		"resource_update_time": true,
		"release_date":         true,
	}
	if !allowedSortFields[sortField] {
		sortField = "created"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}
	order := sortField + " " + sortOrder
	// release_date is the only NULLable sort column. Default PG ordering
	// puts NULLs FIRST on DESC — which would float every undated game to
	// the top of a "newest first" list (bad UX). Force NULLS LAST so
	// dated games always lead, undated tail. Other sort columns are
	// NOT NULL (have defaults) so this clause never matters for them.
	if sortField == "release_date" {
		order += " NULLS LAST"
	}
	// Unique tiebreaker: append the primary key as the final sort column so rows
	// that tie on the chosen sort field keep a deterministic order. Without it,
	// tied rows fall back to physical heap order, which a DB restore / VACUUM
	// FULL / pg_repack reshuffles — the exact tie-order noise the wiki-retirement
	// DB move (kun_galgame_wiki → kun_catalog) exposed. id follows sortOrder for
	// an intuitive secondary sort (same direction as the primary).
	order += ", id " + sortOrder

	err = query.
		Order(order).
		Offset((page-1)*limit).
		Limit(limit).
		Preload("Tag.Tag").
		Preload("Official.Official").
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Find(&items).Error

	return items, total, err
}

// briefColumns is the projection shared by the lightweight brief queries
// (FindByIDs / FindByIDsAny / FindByIDsWithViewer) — one source of truth for the
// GalgameBrief shape. detailColumns adds the heavier intro / release_date the
// detail view needs.
const briefColumns = "id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, " +
	"banner, content_limit, status, user_id, resource_update_time, original_language, age_limit"

const detailColumns = briefColumns +
	", intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw, release_date, release_date_tba, catalog_work_id"

// galgameVisibilityScope is the shared row-visibility filter for the brief /
// detail batch queries: published (status=0) plus, when a viewer is given, that
// viewer's own pending/declined (3/4) rows; with the optional content_limit
// filter. One source of truth so the visibility rule lives in a single place.
func galgameVisibilityScope(viewerUserID int, contentLimit string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if viewerUserID > 0 {
			db = db.Where("status = 0 OR (status IN (3, 4) AND user_id = ?)", viewerUserID)
		} else {
			db = db.Where("status = 0")
		}
		if contentLimit != "" {
			db = db.Where("content_limit = ?", contentLimit)
		}
		return db
	}
}

// FindByIDs finds published galgames by a list of IDs (lightweight, no
// relations). status is selected so the brief can carry it through, though it's
// always 0 on this path. contentLimit follows ParseContentLimit ("" = no filter).
func (r *GalgameRepository) FindByIDs(ctx context.Context, ids []int, contentLimit string) ([]model.Galgame, error) {
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select(briefColumns).
		Where("id IN ?", ids).
		Scopes(galgameVisibilityScope(0, contentLimit)).
		// Deterministic id order: the batch endpoints re-key by id on the caller
		// side (they never promised input-id order), but an explicit ORDER BY id
		// keeps the response byte-stable instead of leaking physical heap order —
		// which a DB restore reshuffles (wiki-retirement tie-order fix).
		Order("id ASC").
		Find(&galgames).Error
	return galgames, err
}

// FindByIDsAny is like FindByIDs but does NOT filter by status. Used by
// internal services (e.g. MessageService.enrich) that already authorize the
// audience and need the row regardless of state. Public API must use
// FindByIDs or FindByIDsWithViewer instead.
func (r *GalgameRepository) FindByIDsAny(ctx context.Context, ids []int) ([]model.Galgame, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select(briefColumns).
		Where("id IN ?", ids).
		Order("id ASC"). // deterministic order (see FindByIDs)
		Find(&galgames).Error
	return galgames, err
}

// GalgameMetaRow is the minimal ownership + lifecycle projection of a galgame
// (A2-1e area B): who submitted it and what state it is in. Nothing else — this
// is deliberately NOT a brief, and it is not renderable content.
type GalgameMetaRow struct {
	GID    int64 `gorm:"column:id" json:"gid"`
	UserID int64 `gorm:"column:user_id" json:"user_id"`
	Status int16 `gorm:"column:status" json:"status"`
}

// FindMetaByIDs returns the ownership meta of the given galgames, STATUS-BLIND
// and deterministic by id.
//
// Status-blind is the point, not an oversight. Its consumer is the forum's edit
// lane, whose owner assertion currently rides the anonymous published-only batch
// read: for any entry in status {2,3,4} that read returns nothing, the assertion
// degrades to "not the owner", and the true owner is locked out of editing,
// reverting and reviewing their own unpublished entry. An ownership question
// must be answerable for rows the PUBLIC face will not show — which is exactly
// why this lives on the credentialed /internal face and returns no content.
//
// Missing ids are simply absent from the result (a deleted / never-existing
// galgame is not an error).
func (r *GalgameRepository) FindMetaByIDs(ctx context.Context, ids []int64) ([]GalgameMetaRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []GalgameMetaRow
	err := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Select("id, user_id, status").
		Where("id IN ?", ids).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// FindByIDsWithViewer is like FindByIDs but additionally returns entries
// in status {3, 4} where the viewer is the submitter. Used by
// GET /galgame/batch when the caller authenticates with a user JWT.
//
// viewerUserID == 0 falls back to FindByIDs (public visibility).
//
// contentLimit applies the same way as FindByIDs: "" = no filter.
// The viewer's own pending/declined drafts are NOT exempted from the
// filter — if you submit an NSFW draft and your client requests
// content_limit=sfw, you won't see it in batch results either (consistent
// with how List/search behave for the same caller's data).
func (r *GalgameRepository) FindByIDsWithViewer(ctx context.Context, ids []int, viewerUserID int, contentLimit string) ([]model.Galgame, error) {
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select(briefColumns).
		Where("id IN ?", ids).
		Scopes(galgameVisibilityScope(viewerUserID, contentLimit)).
		Order("id ASC"). // deterministic order (see FindByIDs)
		Find(&galgames).Error
	return galgames, err
}

// FindDetailByIDs is FindByIDsWithViewer with the extra columns the detail view
// needs (intro_* + release_date). Heavier than the brief query — only the
// view=detail batch path uses it; the brief path stays narrow. Safe for empty.
func (r *GalgameRepository) FindDetailByIDs(ctx context.Context, ids []int, viewerUserID int, contentLimit string) ([]model.Galgame, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select(detailColumns).
		Where("id IN ?", ids).
		Scopes(galgameVisibilityScope(viewerUserID, contentLimit)).
		Order("id ASC"). // deterministic order (see FindByIDs)
		Find(&galgames).Error
	return galgames, err
}

// OfficialNames returns {galgame_id → [maker names]} for the given ids,
// resolving the galgame_official_relation → galgame_official join. Used by the
// detail view to render "由 <maker> 制作". Safe for empty input.
func (r *GalgameRepository) OfficialNames(ctx context.Context, ids []int) (map[int][]string, error) {
	out := make(map[int][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		GalgameID int
		Name      string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("galgame_official_relation AS rel").
		Select("rel.galgame_id AS galgame_id, o.name AS name").
		Joins("JOIN galgame_official AS o ON o.id = rel.official_id").
		Where("rel.galgame_id IN ?", ids).
		Order("rel.galgame_id, o.name").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.GalgameID] = append(out[rw.GalgameID], rw.Name)
	}
	return out, nil
}

// PinnedCoverHashes returns the {galgame_id → image_hash} mapping of the
// pinned cover (sort_order=0) for each id. Galgames with no covers yet
// are absent from the map. Used by BatchGet to populate
// GalgameBrief.EffectiveBannerHash without per-row preloading.
//
// Safe for empty input — returns an empty map, no query.
func (r *GalgameRepository) PinnedCoverHashes(ctx context.Context, ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		GalgameID int
		ImageHash string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&model.GalgameCover{}).
		Select("galgame_id, image_hash").
		Where("galgame_id IN ? AND sort_order = 0", ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GalgameID] = r.ImageHash
	}
	return out, nil
}

// PinnedPortraitHashes returns the {galgame_id → image_hash} mapping of the
// pinned PORTRAIT cover (portrait_pinned = true) for each id. Galgames with no
// pinned portrait are absent from the map. The batch/view=detail sibling of
// PinnedCoverHashes; populates GalgameDetailBrief.EffectivePortraitHash without
// preloading the whole cover set. Safe for empty input.
func (r *GalgameRepository) PinnedPortraitHashes(ctx context.Context, ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		GalgameID int
		ImageHash string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&model.GalgameCover{}).
		Select("galgame_id, image_hash").
		Where("galgame_id IN ? AND portrait_pinned", ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GalgameID] = r.ImageHash
	}
	return out, nil
}

// ListMine returns galgames the user has submitted matching one of the
// given statuses. Used by GET /galgame/mine.
func (r *GalgameRepository) ListMine(ctx context.Context, userID int, statuses []int, page, limit int) (items []model.Galgame, total int64, err error) {
	defer func() {
		for i := range items {
			model.PopulateEffectiveBanner(&items[i])
		}
	}()

	q := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ?", userID).
		Where("status IN ?", statuses)

	q.Count(&total)

	err = q.Order("updated DESC, id DESC"). // id tiebreaker → deterministic ties
						Offset((page-1)*limit).
						Limit(limit).
						Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Find(&items).Error
	return items, total, err
}

// CheckSubmitQuota counts submissions made today by this user.
// Caller compares against the configured per-day limit (default 5).
func (r *GalgameRepository) CountSubmissionsToday(ctx context.Context, userID int) (int64, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var cnt int64
	err := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status IN (3, 4) AND created >= ?", userID, dayStart).
		Count(&cnt).Error
	return cnt, err
}

// FindForUpdate selects a galgame with row-level locking. Used in claim/patch
// to avoid race conditions between concurrent admins/users.
func (r *GalgameRepository) FindForUpdate(tx *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// Create creates a galgame record
func (r *GalgameRepository) Create(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Create(galgame).Error
}

// Update updates a galgame record
func (r *GalgameRepository) Update(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Save(galgame).Error
}

// IncrementView increments the view count.
//
// UpdateColumn (not Update) on purpose: Update would trip GORM's autoUpdateTime
// and bump `updated` on every page view, making a merely-VIEWED galgame look
// "recently updated" — polluting the updated sort/feed, cross-site freshness
// signals, and any cache keyed on `updated`. UpdateColumn writes only `view`
// (and skips hooks), so a view stays a view.
func (r *GalgameRepository) IncrementView(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id = ?", id).
		UpdateColumn("view", gorm.Expr("view + 1")).Error
}

// GetUserStats returns aggregated galgame statistics for a user.
//
// Every Count propagates its error: a swallowed DB failure would leave the
// counter at a stale value and return HTTP 200 with fabricated zeros, which
// the frontend renders as authoritative. Any failure now surfaces as a 500.
func (r *GalgameRepository) GetUserStats(ctx context.Context, userID int) (*dto.UserGalgameStats, error) {
	var stats dto.UserGalgameStats

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	count := func(q *gorm.DB) (int, error) {
		var c int64
		if err := q.Count(&c).Error; err != nil {
			return 0, err
		}
		return int(c), nil
	}

	var err error
	if stats.GalgameCreated, err = count(r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0", userID)); err != nil {
		return nil, err
	}
	if stats.GalgameCreatedToday, err = count(r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0 AND created >= ?", userID, todayStart)); err != nil {
		return nil, err
	}
	// CREATED ∪ EDITED, matching ListContributedByUser's set (minus the
	// status=0 filter — this stat is intentionally all-status). Counting only
	// galgame_contributor here would undercount by the ~35% of titles whose
	// creator was never written as a contributor (migrated/synced entries), so
	// the badge could read 0 while the contributed list shows the user's own
	// creations. OR galgame.user_id closes that gap.
	if stats.GalgameContributed, err = count(r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? OR id IN (SELECT galgame_id FROM galgame_contributor WHERE user_id = ?)", userID, userID)); err != nil {
		return nil, err
	}
	if stats.RevisionCount, err = count(r.db.WithContext(ctx).Model(&model.GalgameRevision{}).
		Where("user_id = ?", userID)); err != nil {
		return nil, err
	}
	if stats.PRSubmitted, err = count(r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ?", userID)); err != nil {
		return nil, err
	}
	if stats.PRMerged, err = count(r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 1", userID)); err != nil {
		return nil, err
	}
	if stats.PRDeclined, err = count(r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 2", userID)); err != nil {
		return nil, err
	}
	if stats.PRPending, err = count(r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 0", userID)); err != nil {
		return nil, err
	}

	return &stats, nil
}

// ListPublishedByUser returns a user's PUBLISHED galgames (status=0), newest
// first, paginated — the rows behind GetUserStats.GalgameCreated (same WHERE).
// Selects only the brief columns; the service maps them to GalgameBrief +
// pinned-cover hashes (PinnedCoverHashes), exactly like BatchGet.
func (r *GalgameRepository) ListPublishedByUser(ctx context.Context, userID, page, limit int, contentLimit string) (items []model.Galgame, total int64, err error) {
	q := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0", userID)
	// NSFW gating is the wiki's job (handbook §16) — filter server-side so the
	// count + rows agree and nothing NSFW leaks to a SFW viewer.
	if contentLimit != "" {
		q = q.Where("content_limit = ?", contentLimit)
	}

	q.Count(&total)

	err = q.
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, content_limit, status, user_id, resource_update_time, original_language, age_limit").
		Order("created DESC, id DESC"). // id tiebreaker → deterministic ties
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error
	return items, total, err
}

// ListContributedByUser returns the published galgames a user CONTRIBUTED to —
// defined as CREATED ∪ EDITED, newest-contribution-first, paginated. Distinct
// from ListPublishedByUser, which is CREATED-only (galgame.user_id); this is a
// superset that also includes titles the user only edited. status=0 only
// (public profile view — never leak drafts/banned) and NSFW-gated like the
// created list.
//
// Why the OR galgame.user_id (not a plain JOIN on galgame_contributor): the
// contributor row is *supposed* to be written on create too, but ~35% of
// published titles predate that invariant (migrated / VNDB-synced entries have
// galgame.user_id set with NO matching galgame_contributor row). A plain JOIN
// would silently drop a creator's own titles from their "contributed" list —
// the exact bug a user reported. The OR is robust to that gap (and to any
// future create-path miss), and only ever evaluates status=0, so the bulk
// status=2 VNDB-draft rows never enter. galgame_contributor holds at most one
// row per (galgame,user), so the LEFT JOIN can't duplicate rows.
func (r *GalgameRepository) ListContributedByUser(ctx context.Context, userID, page, limit int, contentLimit string) (items []model.Galgame, total int64, err error) {
	q := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Joins("LEFT JOIN galgame_contributor gc ON gc.galgame_id = galgame.id AND gc.user_id = ?", userID).
		Where("galgame.status = 0 AND (galgame.user_id = ? OR gc.user_id IS NOT NULL)", userID)
	if contentLimit != "" {
		q = q.Where("galgame.content_limit = ?", contentLimit)
	}

	q.Count(&total)

	err = q.
		Select("galgame.id, galgame.vndb_id, galgame.name_en_us, galgame.name_ja_jp, galgame.name_zh_cn, galgame.name_zh_tw, galgame.banner, galgame.content_limit, galgame.status, galgame.user_id, galgame.resource_update_time, galgame.original_language, galgame.age_limit").
		// newest contribution first: the contributor row's `created` when the
		// user edited it, else the galgame's own `created` (created-only).
		// galgame.id tiebreaker → deterministic order for equal timestamps.
		Order("COALESCE(gc.created, galgame.created) DESC, galgame.id DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error
	return items, total, err
}

// DraftFilters scopes the drafts list to one taxonomy entity — the
// claim-funnel modal lives on the official / tag / engine detail pages and
// shows only THAT entity's unclaimed drafts. Zero values = no filter (global
// list). Engine-scoped lists are empty by data today (engine edges are
// human-curated and drafts are untouched VNDB imports) — the parameter is
// uniform anyway so the face doesn't change if that ever changes.
type DraftFilters struct {
	OfficialID int
	TagID      int
	EngineID   int
	// OriginalLanguages narrows drafts to games whose original_language is in
	// the set (exact codes, e.g. ja-jp / zh-cn / zh-tw). Empty = no filter.
	// The 52k VNDB draft pool is ~37% English/Russian/... originals the
	// claim funnel doesn't want to surface.
	OriginalLanguages []string
}

// ListDrafts pages the unclaimed VNDB drafts (status = 2), newest first — the
// claim-funnel browser's data source (the drafts modal on kungal's entity
// pages). Only the cover preload is carried (the card needs the effective
// banner/portrait, not taxonomy); content_limit filters the same way List does.
func (r *GalgameRepository) ListDrafts(ctx context.Context, page, limit int, contentLimit string, f DraftFilters) (items []model.Galgame, total int64, err error) {
	defer func() {
		for i := range items {
			model.PopulateEffectiveBanner(&items[i])
		}
	}()

	query := r.db.WithContext(ctx).Model(&model.Galgame{}).Where("status = 2")
	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}
	if f.OfficialID > 0 {
		query = query.Where("EXISTS (SELECT 1 FROM galgame_official_relation r WHERE r.galgame_id = galgame.id AND r.official_id = ?)", f.OfficialID)
	}
	if f.TagID > 0 {
		query = query.Where("EXISTS (SELECT 1 FROM galgame_tag_relation r WHERE r.galgame_id = galgame.id AND r.tag_id = ?)", f.TagID)
	}
	if f.EngineID > 0 {
		query = query.Where("EXISTS (SELECT 1 FROM galgame_engine_relation r WHERE r.galgame_id = galgame.id AND r.engine_id = ?)", f.EngineID)
	}
	if len(f.OriginalLanguages) > 0 {
		query = query.Where("original_language IN ?", f.OriginalLanguages)
	}
	query.Count(&total)

	err = query.
		Order("created DESC, id DESC").
		Offset((page-1)*limit).
		Limit(limit).
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Find(&items).Error
	return items, total, err
}
