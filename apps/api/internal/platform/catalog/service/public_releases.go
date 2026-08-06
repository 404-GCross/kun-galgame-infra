// public_releases.go — the RELEASE-GRAIN new-releases timeline on the frozen
// /v1/catalog public projection (wave 174).
//
// ── why a second dated lane next to the calendar ─────────────────────────────
//
// The calendar (public_calendar.go) is a WORK-grain surface: it places each
// work by its EARLIEST dated release and shows it exactly once, in one month.
// That is the right shape for 新作月表 — "which titles come out in June" — and
// it is deliberately blind to everything that happens to a title afterwards: a
// Switch port, a Steam re-edition, a Chinese localisation are all releases of a
// work whose earliest date was years ago, so the calendar can never surface
// them at all.
//
// This lane is the other half: EVERY dated release row is its own item, ordered
// by its own date. A port shows up on the day the port ships. The two lanes
// share the work population predicate, the olang / nsfw / content_limit gates
// and the ETag pipeline, so a consumer that renders one renders the other.
//
// ── the population ───────────────────────────────────────────────────────────
//
// A release is in the timeline when its work is in the public fetchable set
// (galgame, status live, not soft-deleted) and the release itself is not
// soft-deleted and carries a date known to at least the MONTH (released_y AND
// released_m). Year-only and undated releases are deliberately absent: a feed
// entry whose position is "somewhere in 2019" cannot be ordered against a real
// date, and those rows already have their homes on the calendar's pending / tba
// buckets. Same composed ordinal as everywhere else (releaseOrd), so this lane,
// the calendar and the works list's release_date can never disagree.
//
// ── cost ─────────────────────────────────────────────────────────────────────
//
// Zero migrations. Measured against the 2026-08 prod snapshot (168,643 rows in
// the ungated population): 21.9 ms for the date_desc first page, 37.5 ms for
// date_asc, 24.5 ms for a mid-feed keyset resume, 20.5 ms for the meta query.
// The ordinal is a top-N heapsort over a hash join, not an index scan — well
// inside budget, so no expression index was added for it.
package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
)

// Cursor lanes of the two sort directions. Each is its own lane token so a
// cursor minted while reading forwards can never be replayed backwards
// (decodePublicCursor pins it) — the positions mean opposite things.
const (
	releaseFeedLaneDesc = "releases-date-desc"
	releaseFeedLaneAsc  = "releases-date-asc"
)

// ReleaseFeedSortDateDesc / ReleaseFeedSortDateAsc are the two public sort
// tokens. Exported because the handler owns the closed-vocabulary 400.
const (
	ReleaseFeedSortDateDesc = "date_desc"
	ReleaseFeedSortDateAsc  = "date_asc"
)

// ReleaseKindFromKey is the inverse of releaseKindKey — the CLOSED public kind
// vocabulary, used by the handler to 400 an unknown ?kind= token. Exported for
// exactly that reason: the strings a caller may send must be the strings the
// items print, and one function owning both directions is what keeps them so.
func ReleaseKindFromKey(k string) (int16, bool) {
	switch k {
	case "default":
		return model.ReleaseKindDefault, true
	case "digital":
		return model.ReleaseKindDigital, true
	case "physical":
		return model.ReleaseKindPhysical, true
	case "trial":
		return model.ReleaseKindTrial, true
	case "patch":
		return model.ReleaseKindPatch, true
	default:
		return 0, false
	}
}

// DefaultReleaseFeedKinds is what an OMITTED ?kind= resolves to: the FULL
// RELEASE kinds only (default / digital / physical), i.e. trial and patch are
// excluded.
//
// 发售动态 means "a thing came out". A demo and a translation patch are real
// release rows and stay addressable — kind=trial,patch fetches them, and
// kind=default,digital,physical,trial,patch is the true unfiltered feed — but
// they are not what a "new releases" surface is asking for, and on the vndb
// lane they outnumber the releases they belong to. The default therefore
// EXCLUDES them rather than making every consumer remember to.
var DefaultReleaseFeedKinds = []int16{
	model.ReleaseKindDefault, model.ReleaseKindDigital, model.ReleaseKindPhysical,
}

// ReleaseFeedFilter is the timeline's request shape: the population gates (all
// of which ride in the ETag key) plus the sort direction and the include=
// projection selector for the attached work block.
type ReleaseFeedFilter struct {
	// NSFW / OLang / DisplayLimits are the PARENT-WORK gates, word for word the
	// calendar's — one nsfw, one olang and one content_limit mean one thing
	// across the whole public face.
	NSFW          bool
	OLang         PublicOLang
	DisplayLimits []string
	// Kinds is the release-kind set; empty resolves to DefaultReleaseFeedKinds
	// at query time (never to "no gate" — the trial/patch exclusion IS the
	// default). Values are model.ReleaseKind* constants.
	Kinds []int16
	// Langs is the RELEASE-language set, matched over COALESCE(r.lang, w.olang).
	// Empty = no gate at all (the parameter's default and its explicit `all`
	// both land here) — unlike OLang, whose zero value is the curated family.
	Langs []string
	// Official gates the vndb `official` flag; nil = no gate. See
	// releaseFeedSource for the absent-key ruling.
	Official *bool
	// Platform is the release's primary platform code; "" = no gate. OPEN
	// vocabulary, so an unknown code is an empty feed, never a 400.
	Platform string
	// DateFrom / DateTo are composed ordinals (y*10000+m*100+d), inclusive;
	// 0 = unbounded.
	DateFrom int64
	DateTo   int64
	// Sort is one of the ReleaseFeedSort* tokens; anything else (including "")
	// degrades to date_desc, the feed reading.
	Sort    string
	Include WorksListInclude
}

// lane is the filter's cursor lane token.
func (f ReleaseFeedFilter) lane() string {
	if f.Sort == ReleaseFeedSortDateAsc {
		return releaseFeedLaneAsc
	}
	return releaseFeedLaneDesc
}

// kinds resolves the effective release-kind set (see DefaultReleaseFeedKinds).
func (f ReleaseFeedFilter) kinds() []int16 {
	if len(f.Kinds) > 0 {
		return f.Kinds
	}
	return DefaultReleaseFeedKinds
}

// PopulationKey is the ETag discriminator of the whole population.
//
// EVERY gate that changes membership is in here, for the reason the calendar's
// key spells out: two different populations must never share a cache validator,
// their (count, max created_at, max id) triples can coincide by accident, and a
// validator that ignored a gate would serve one caller's feed to another on the
// first If-None-Match. The SORT is deliberately absent — reversing the order
// does not change the SET, and both directions of one population legitimately
// share a validator.
func (f ReleaseFeedFilter) PopulationKey() string {
	gate := "sfw"
	if f.NSFW {
		gate = "nsfw"
	}
	limits := "all"
	if len(f.DisplayLimits) > 0 {
		limits = strings.Join(f.DisplayLimits, "+")
	}
	kinds := make([]string, 0, len(f.kinds()))
	for _, k := range f.kinds() {
		kinds = append(kinds, strconv.FormatInt(int64(k), 10))
	}
	langs := "all"
	if len(f.Langs) > 0 {
		langs = strings.Join(f.Langs, "+")
	}
	official := "any"
	if f.Official != nil {
		official = strconv.FormatBool(*f.Official)
	}
	return strings.Join([]string{
		gate, f.OLang.Key(), limits, strings.Join(kinds, "+"), langs, official,
		f.Platform, strconv.FormatInt(f.DateFrom, 10), strconv.FormatInt(f.DateTo, 10),
	}, "-")
}

// ReleaseFeedMeta returns the timeline's (row count, newest created_at, highest
// release id) over the WHOLE filtered set — the cheap ETag basis, so a matching
// If-None-Match can 304 without ever loading (and enriching) a page.
//
// catalog_release carries created_at but no updated_at, so unlike the calendar
// this lane cannot fold a single freshness stamp. It folds THREE numbers
// instead: the count moves when a row enters or leaves the population (an edit
// that dates a previously undated release, a soft delete, a work leaving the
// live set), max(created_at) moves when a row is imported, and max(id) breaks
// the one tie those two share — a same-second insert paired with a delete. An
// in-place date EDIT of an existing row still moves the count only if it
// crosses the month-precision threshold, which is the known and accepted floor
// of a cheap validator: s-maxage is 60 seconds, not a day.
func (s *PublicService) ReleaseFeedMeta(ctx context.Context, f ReleaseFeedFilter) (int64, time.Time, int64, error) {
	from, where, args := releaseFeedSource(f)
	var row struct {
		N          int64     `gorm:"column:n"`
		MaxCreated time.Time `gorm:"column:max_created"`
		MaxID      int64     `gorm:"column:max_id"`
	}
	q := `SELECT count(*) AS n,
			coalesce(max(r.created_at), to_timestamp(0)) AS max_created,
			coalesce(max(r.id), 0) AS max_id ` +
		from + ` WHERE ` + strings.Join(where, " AND ")
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&row).Error; err != nil {
		return 0, time.Time{}, 0, err
	}
	return row.N, row.MaxCreated, row.MaxID, nil
}

// releaseFeedRow is one raw timeline row: the release columns, its composed
// ordinal, the is_first discriminator, and the parent work's registry columns
// (the works-list base item is built from those, batched per page).
type releaseFeedRow struct {
	ID        int64
	Kind      int16
	Title     *string
	Lang      *string
	Platform  *string
	ReleasedY *int16 `gorm:"column:released_y"`
	ReleasedM *int16 `gorm:"column:released_m"`
	ReleasedD *int16 `gorm:"column:released_d"`
	Extra     datatypes.JSON
	Ord       int64 `gorm:"column:ord"`
	IsFirst   bool  `gorm:"column:is_first"`

	WorkID      int64 `gorm:"column:work_id"`
	MediumID    int16
	DisplayName string
	// Explicit column tag: GORM snake-cases the field to o_lang, which matches
	// no result column (the W1 works-list trap, fixed in 0dce1f36).
	OLang         string `gorm:"column:olang"`
	ContentRating int16
	Site          *string
	ProductWorkID *int64
	ClaimState    *int16 `gorm:"column:claim_state"`
	UpdatedAt     time.Time
}

// ReleaseFeed serves one keyset page of the timeline. A malformed or
// cross-direction cursor is ErrBadCursor (the handler maps it to 400).
func (s *PublicService) ReleaseFeed(ctx context.Context, f ReleaseFeedFilter, cursor string, limit int) (dto.PublicReleaseFeedData, error) {
	lane := f.lane()
	cur, err := decodePublicCursor(cursor, lane)
	if err != nil {
		return dto.PublicReleaseFeedData{}, err
	}
	// Defensive only — the handler is the wire authority (it 400s a bad limit).
	limit = clampBrowseLimit(limit)

	from, where, args := releaseFeedSource(f)
	ord := releaseOrd("r")
	if cur.ID > 0 {
		// The tiebreak is id ASC in BOTH directions, so the descending lane's
		// keyset is a MIXED-direction comparison and cannot use the row-value
		// form the works list and the calendar use ((a,b) > (?,?) requires both
		// columns to move the same way). It is written out as the explicit OR
		// instead; the ordinal is sorted, not indexed, so nothing is lost.
		if lane == releaseFeedLaneAsc {
			where = append(where, "(("+ord+") > ? OR (("+ord+") = ? AND r.id > ?))")
		} else {
			where = append(where, "(("+ord+") < ? OR (("+ord+") = ? AND r.id > ?))")
		}
		args = append(args, cur.Ord, cur.Ord, cur.ID)
	}
	dir := "DESC"
	if lane == releaseFeedLaneAsc {
		dir = "ASC"
	}
	args = append(args, limit)

	// is_first is a correlated scalar in the SELECT list on purpose: Postgres
	// evaluates it only for the rows that survive ORDER BY + LIMIT (measured: 20
	// index probes on idx_catalog_release_work_id per page), so the port
	// discriminator costs one page's worth of lookups, not one per candidate.
	q := `SELECT r.id, r.kind, r.title, r.lang, r.platform,
			r.released_y, r.released_m, r.released_d, r.extra,
			` + ord + ` AS ord,
			(` + ord + `) = (SELECT min(` + releaseOrd("r2") + `) FROM catalog_release r2
				WHERE r2.work_id = r.work_id AND r2.deleted_at IS NULL
				  AND r2.released_y IS NOT NULL AND r2.released_m IS NOT NULL) AS is_first,
			w.id AS work_id, w.medium_id, w.display_name, w.olang, w.content_rating,
			w.site, w.product_work_id, w.claim_state, w.updated_at ` +
		from + ` WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY ord ` + dir + `, r.id ASC LIMIT ?`

	var rows []releaseFeedRow
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicReleaseFeedData{}, err
	}
	items, err := s.buildReleaseFeedItems(ctx, rows, f)
	if err != nil {
		return dto.PublicReleaseFeedData{}, err
	}
	out := dto.PublicReleaseFeedData{Items: items}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		nc := encodePublicCursor(publicCursor{Sort: lane, ID: last.ID, Ord: last.Ord})
		out.NextCursor = &nc
	}
	return out, nil
}

// buildReleaseFeedItems projects one raw page to the wire items, with exactly
// two batched follow-up loads: the release-level EXACT anchors (the same
// entityRefsFor the work-detail face's releases[] rides on) and the works-list
// base items for the page's DISTINCT work ids. A page of five ports of one work
// therefore costs one work enrichment, not five.
func (s *PublicService) buildReleaseFeedItems(ctx context.Context, rows []releaseFeedRow, f ReleaseFeedFilter) ([]dto.PublicReleaseFeedItem, error) {
	if len(rows) == 0 {
		return []dto.PublicReleaseFeedItem{}, nil
	}
	releaseIDs := make([]int64, len(rows))
	src := make([]workListSourceRow, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for i, r := range rows {
		releaseIDs[i] = r.ID
		if _, dup := seen[r.WorkID]; dup {
			continue
		}
		seen[r.WorkID] = struct{}{}
		src = append(src, workListSourceRow{
			ID: r.WorkID, MediumID: r.MediumID, DisplayName: r.DisplayName, OLang: r.OLang,
			ContentRating: r.ContentRating, Site: r.Site, ProductWorkID: r.ProductWorkID,
			ClaimState: r.ClaimState, UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	refs, err := s.entityRefsFor(ctx, model.EntityTypeRelease, releaseIDs)
	if err != nil {
		return nil, err
	}
	works, err := s.enrichWorkListItems(ctx, src, f.NSFW, f.Include)
	if err != nil {
		return nil, err
	}
	byWork := make(map[int64]dto.PublicWorkListItem, len(works))
	for _, w := range works {
		byWork[w.ID] = w
	}
	out := make([]dto.PublicReleaseFeedItem, len(rows))
	for i, r := range rows {
		item := dto.PublicReleaseFeedItem{
			ID: r.ID, Kind: releaseKindKey(r.Kind), Title: derefStrPub(r.Title),
			Lang: derefStrPub(r.Lang), Platform: derefStrPub(r.Platform),
			Platforms: publicPlatformsFromExtra(r.Extra),
			IsFirst:   r.IsFirst, Work: byWork[r.WorkID],
			Refs: refs[r.ID],
		}
		if r.ReleasedY != nil {
			d := partialISOFromOrdinal(r.Ord)
			item.Date = &d
		}
		if item.Refs == nil {
			// The public face serializes [] never null.
			item.Refs = []dto.PublicCatalogRef{}
		}
		out[i] = item
	}
	return out, nil
}

// releaseFeedSource renders the FROM clause and the population predicates
// shared by the meta and page queries. FIXED internal SQL — the only caller
// input reaching it is bound as arguments.
func releaseFeedSource(f ReleaseFeedFilter) (from string, where []string, args []any) {
	from = `FROM catalog_release r JOIN catalog_work w ON w.id = r.work_id`

	// The works-list population predicate on the parent work, verbatim, plus
	// the release's own two conditions.
	where = []string{
		"r.deleted_at IS NULL", "w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?",
		"r.released_y IS NOT NULL", "r.released_m IS NOT NULL",
	}
	args = append(args, model.WorkStatusLive, galgameMediumID)

	if !f.NSFW {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	if pred, pargs := f.OLang.predicate(); pred != "" {
		where = append(where, pred)
		args = append(args, pargs...)
	}
	if pred, pargs := displayLimitWhere(f.DisplayLimits); pred != "" {
		where = append(where, pred)
		args = append(args, pargs...)
	}

	where = append(where, "r.kind IN ?")
	args = append(args, f.kinds())

	if len(f.Langs) > 0 {
		// COALESCE, not a bare r.lang: the dlsite/getchu lanes create one store
		// SKU per work and leave lang NULL, and a store SKU is by construction
		// in its work's original language. Gating on the raw column would drop
		// half the registry's releases from every lang= query. The ITEM still
		// prints the raw r.lang (empty when unrecorded) — the coalesce is a
		// matching rule, not a fabricated value.
		where = append(where, "COALESCE(r.lang, w.olang) IN ?")
		args = append(args, f.Langs)
	}
	if f.Official != nil {
		// extra->>'official' is written by the VNDB release lane only, where
		// false means "fan translation / unofficial edition". A row WITHOUT the
		// key is official: every other lane materialises store SKUs (dlsite
		// worknos, getchu product pages), which are official by construction.
		// Hence IS DISTINCT FROM rather than <> — a NULL must count as official,
		// and <> would silently drop all 159,080 keyless rows.
		if *f.Official {
			where = append(where, "r.extra->>'official' IS DISTINCT FROM 'false'")
		} else {
			where = append(where, "r.extra->>'official' = 'false'")
		}
	}
	if f.Platform != "" {
		// The works-list platform predicate's RELEASE leg, verbatim: the primary
		// platform column. (The works list unions a work-level leg on top; that
		// grain has no meaning on a release row, and extra.platforms is a
		// display list this face prints but does not filter on — same as the
		// works list.)
		where = append(where, "r.platform = ?")
		args = append(args, f.Platform)
	}
	if f.DateFrom > 0 {
		where = append(where, "("+releaseOrd("r")+") >= ?")
		args = append(args, f.DateFrom)
	}
	if f.DateTo > 0 {
		where = append(where, "("+releaseOrd("r")+") <= ?")
		args = append(args, f.DateTo)
	}
	return from, where, args
}

// ReleaseFeedETag mints the timeline's weak validator from the cheap meta
// triple. The population gates ride in the key — see PopulationKey.
func ReleaseFeedETag(populationKey string, count int64, maxCreated time.Time, maxID int64) string {
	return `W/"relfeed-` + populationKey + `-` +
		strconv.FormatInt(count, 10) + `-` +
		strconv.FormatInt(maxCreated.Unix(), 10) + `-` +
		strconv.FormatInt(maxID, 10) + `"`
}
