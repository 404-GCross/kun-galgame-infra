package service

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

// NextMoe open-API public projection (step 02). These methods map the internal
// galgame graph to the FROZEN public aggregate DTO (dto.PublicGalgame /
// PublicGalgameItem), reusing the existing repository, scores, and image-meta
// paths — the internal read face is untouched. Every projection is
// published-only + content_limit-gated; on the sfw face NSFW-rated images are
// dropped (裁定 2).

// PublicInclude selects the optional (heavy) blocks of the detail record. The
// default is all-false → the thin, cache-friendly aggregate.
//
// W1a add-only tokens: links / screenshots / series / meta gate the new detail
// blocks; tag_refs / official_refs / engine_refs enrich the taxonomy block's
// rich-reference sub-keys and are meaningful ONLY together with taxonomy (a ref
// token without taxonomy is a silent no-op, so include=taxonomy alone stays
// byte-identical).
type PublicInclude struct {
	Intro    bool
	Scores   bool
	Covers   bool
	Taxonomy bool

	Links        bool
	Screenshots  bool
	Series       bool
	Meta         bool
	TagRefs      bool
	OfficialRefs bool
	EngineRefs   bool
	Contributors bool
}

// ParsePublicInclude resolves the comma-separated `include` query token set.
// Unknown tokens are ignored.
func ParsePublicInclude(raw string) PublicInclude {
	var inc PublicInclude
	for _, tok := range strings.Split(raw, ",") {
		switch strings.TrimSpace(tok) {
		case "intro":
			inc.Intro = true
		case "scores":
			inc.Scores = true
		case "covers":
			inc.Covers = true
		case "taxonomy":
			inc.Taxonomy = true
		case "links":
			inc.Links = true
		case "screenshots":
			inc.Screenshots = true
		case "series":
			inc.Series = true
		case "meta":
			inc.Meta = true
		case "tag_refs":
			inc.TagRefs = true
		case "official_refs":
			inc.OfficialRefs = true
		case "engine_refs":
			inc.EngineRefs = true
		case "contributors":
			inc.Contributors = true
		}
	}
	return inc
}

// publicListLimits clamps a requested list/batch limit to [1, max] with a default.
func publicClampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}

// PublicDetail projects one published galgame to the aggregate record. Returns
// found=false (→ 404) when the id is unknown, unpublished, or hidden by the
// content_limit gate. Also returns the galgame's updated timestamp for the ETag.
// Read-only: unlike the internal detail path it does NOT increment the view
// counter (public API traffic must not mutate wiki state).
func (s *GalgameService) PublicDetail(ctx context.Context, id int, inc PublicInclude, contentLimit string, withRatings bool) (dto.PublicGalgame, bool, time.Time, error) {
	g, err := s.galgameRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return dto.PublicGalgame{}, false, time.Time{}, nil
		}
		return dto.PublicGalgame{}, false, time.Time{}, err
	}
	if g.Status != model.GalgameStatusPublished {
		return dto.PublicGalgame{}, false, time.Time{}, nil
	}
	if contentLimit != "" && g.ContentLimit != contentLimit {
		return dto.PublicGalgame{}, false, time.Time{}, nil
	}
	// Fill cover dims + thumbhash (same enrichment the internal detail uses).
	s.enrichGalgameImages(ctx, g)
	// Score meta anchors refs.erogamescape + attribution + the optional scores block.
	sm, err := s.galgameRepo.LoadScoreMeta(ctx, id)
	if err != nil {
		return dto.PublicGalgame{}, false, time.Time{}, err
	}
	// include=series needs the series' published-galgame count (one extra count
	// query, detail-only — never a per-item list cost).
	var seriesCount int
	if inc.Series && g.Series != nil && g.SeriesID != nil {
		if n, cErr := s.galgameRepo.SeriesGalgameCount(ctx, *g.SeriesID); cErr == nil {
			seriesCount = int(n)
		}
	}
	return s.projectDetail(g, sm, inc, contentLimit, seriesCount, withRatings), true, g.Updated.Time(), nil
}

// projectDetail assembles the aggregate record from a fully-loaded galgame + its
// score meta. include-gated blocks are attached only when requested.
// seriesCount is the resolved series member count (0 unless include=series).
// withRatings gates the per-image sexual/violence levels (W1c; see detailImages).
func (s *GalgameService) projectDetail(g *model.Galgame, sm repository.ScoreMeta, inc PublicInclude, contentLimit string, seriesCount int, withRatings bool) dto.PublicGalgame {
	dropNSFW := contentLimit == "sfw"

	rec := dto.PublicGalgame{
		ID:               g.ID,
		Names:            publicNames(g),
		Aliases:          aliasNames(g),
		ReleaseDate:      fmtPublicDate(g.ReleaseDate),
		ReleaseDateTBA:   g.ReleaseDateTBA,
		OriginalLanguage: g.OriginalLanguage,
		AgeLimit:         g.AgeLimit,
		Refs:             publicRefs(g, sm),
		Images:           s.detailImages(g, inc.Covers, inc.Screenshots, dropNSFW, withRatings),
		CatalogWorkID:    g.CatalogWorkID,
		Updated:          fmtPublicTS(g.Updated),
		Attribution:      publicAttribution(g, sm),
	}
	if inc.Intro {
		intro := dto.PublicIntro{
			ZhCN: nullIfEmptyPub(g.IntroZhCN),
			EnUS: nullIfEmptyPub(g.IntroEnUS),
			JaJP: nullIfEmptyPub(g.IntroJaJP),
			ZhTW: nullIfEmptyPub(g.IntroZhTW),
		}
		rec.Intro = &intro
	}
	if inc.Scores {
		sc := buildGalgameScores(sm)
		rec.Scores = &sc
	}
	if inc.Taxonomy {
		tax := publicTaxonomy(g)
		// Rich-reference sub-keys are add-only inside the taxonomy block: each
		// appears only when its own token is also requested, so include=taxonomy
		// alone stays byte-identical.
		if inc.TagRefs {
			refs := publicTagRefs(g)
			tax.TagRefs = &refs
		}
		if inc.OfficialRefs {
			refs := publicOfficialRefs(g)
			tax.OfficialRefs = &refs
		}
		if inc.EngineRefs {
			refs := publicEngineRefs(g)
			tax.EngineRefs = &refs
		}
		rec.Taxonomy = tax
	}
	if inc.Links {
		links := publicLinks(g)
		rec.Links = &links
	}
	if inc.Series && g.Series != nil {
		rec.Series = &dto.PublicSeriesRef{ID: g.Series.ID, Name: g.Series.Name, GalgameCount: seriesCount}
	}
	if inc.Meta {
		m := buildPublicMeta(g)
		rec.Meta = &m
	}
	if inc.Contributors {
		c := publicContributors(g)
		rec.Contributors = &c
	}
	return rec
}

// publicContributors projects the preloaded contributor relation to the curated
// public shape ({user_id}). Always non-nil so include=contributors on a
// contributor-less game emits [] rather than null. Order is the preloaded order.
func publicContributors(g *model.Galgame) []dto.PublicContributor {
	out := make([]dto.PublicContributor, 0, len(g.Contributor))
	for i := range g.Contributor {
		out = append(out, dto.PublicContributor{UserID: g.Contributor[i].UserID})
	}
	return out
}

// detailImages builds the images block from a galgame whose Cover / Screenshot
// lists are loaded and dims-enriched. banner/portrait are the effective pins;
// covers[] is the full set (only under include=covers); screenshots[] is the
// gallery set (only under include=screenshots). On the sfw face every NSFW-rated
// image (sexual>0 OR violence>0) is dropped from every set.
// withRatings gates the per-image sexual/violence levels on covers[]/screenshots[]
// (W1c): true only when the resolved credential is nsfw-capable (same gate as the
// three-state content_limit), so a third-party (no scope) response stays
// byte-frozen. The screenshot caption is ungated (always emitted when non-empty).
// The banner/portrait pins never carry ratings/caption.
func (s *GalgameService) detailImages(g *model.Galgame, withCovers, withScreenshots, dropNSFW, withRatings bool) dto.PublicImages {
	byHash := make(map[string]*model.GalgameCover, len(g.Cover))
	for i := range g.Cover {
		byHash[g.Cover[i].ImageHash] = &g.Cover[i]
	}
	pick := func(hash *string) *dto.PublicImage {
		if hash == nil {
			return nil
		}
		c := byHash[*hash]
		if c == nil || (dropNSFW && (c.Sexual > 0 || c.Violence > 0)) {
			return nil
		}
		return s.publicImage(c.ImageHash, ImageMeta{Width: c.Width, Height: c.Height, Thumbhash: c.Thumbhash}, "")
	}
	imgs := dto.PublicImages{
		Banner:   pick(g.EffectiveBannerHash),
		Portrait: pick(g.EffectivePortraitHash),
	}
	if withCovers {
		covers := make([]dto.PublicImage, 0, len(g.Cover))
		for i := range g.Cover {
			c := &g.Cover[i]
			if dropNSFW && (c.Sexual > 0 || c.Violence > 0) {
				continue
			}
			if img := s.publicImage(c.ImageHash, ImageMeta{Width: c.Width, Height: c.Height, Thumbhash: c.Thumbhash}, c.Kind); img != nil {
				if withRatings {
					sx, vl := int(c.Sexual), int(c.Violence)
					img.Sexual, img.Violence = &sx, &vl
				}
				covers = append(covers, *img)
			}
		}
		imgs.Covers = covers
	}
	if withScreenshots {
		shots := make([]dto.PublicImage, 0, len(g.Screenshot))
		for i := range g.Screenshot {
			sc := &g.Screenshot[i]
			if dropNSFW && (sc.Sexual > 0 || sc.Violence > 0) {
				continue
			}
			if img := s.publicImage(sc.ImageHash, ImageMeta{Width: sc.Width, Height: sc.Height, Thumbhash: sc.Thumbhash}, ""); img != nil {
				img.Caption = sc.Caption // ungated; omitempty drops the empty string
				if withRatings {
					sx, vl := int(sc.Sexual), int(sc.Violence)
					img.Sexual, img.Violence = &sx, &vl
				}
				shots = append(shots, *img)
			}
		}
		imgs.Screenshots = shots
	}
	return imgs
}

// PublicList returns a keyset-paginated page of thin items. sort is "id"
// (default, id ASC) or "release_date" (release_date DESC NULLS LAST, id DESC).
// An invalid/mismatched cursor is treated as the first page.
func (s *GalgameService) PublicList(ctx context.Context, sort, cursor string, limit int, contentLimit string, inc PublicItemInclude) (dto.PublicListData, error) {
	limit = publicClampLimit(limit, 20, 100)
	fetch := limit + 1 // one extra row probes "is there a next page?"

	var rows []model.Galgame
	var err error
	byReleaseDate := sort == "release_date"
	if byReleaseDate {
		hasCur, d, dNull, id := decodeReleaseCursor(cursor)
		rows, err = s.galgameRepo.PublicListByReleaseDate(ctx, hasCur, d, dNull, id, fetch, contentLimit)
	} else {
		rows, err = s.galgameRepo.PublicListByID(ctx, decodeIDCursor(cursor), fetch, contentLimit)
	}
	if err != nil {
		return dto.PublicListData{}, err
	}

	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		last := &rows[len(rows)-1]
		var nc string
		if byReleaseDate {
			nc = encodeReleaseCursor(last.ReleaseDate, last.ID)
		} else {
			nc = encodeIDCursor(last.ID)
		}
		next = &nc
	}

	items := s.thinItems(ctx, rows, contentLimit, inc)
	return dto.PublicListData{Items: items, NextCursor: next}, nil
}

// PublicBatchThin returns thin items for the given ids (published + gated),
// preserving the caller's id order (the search / favorites use case). inc drives
// the optional officials/scores expansion (batched over the whole set).
func (s *GalgameService) PublicBatchThin(ctx context.Context, ids []int, contentLimit string, inc PublicItemInclude) ([]dto.PublicGalgameItem, error) {
	rows, err := s.galgameRepo.FindByIDs(ctx, ids, contentLimit)
	if err != nil {
		return nil, err
	}
	items := s.thinItems(ctx, rows, contentLimit, inc)
	return orderItemsByIDs(items, ids), nil
}

// PublicBatchThinViewer is PublicBatchThin with viewer visibility: it also
// resolves the viewer's own pending/declined (status 3/4) rows. Used only by the
// search include_pending path to hydrate the authenticated caller's own drafts
// (which FindByIDs, published-only, would drop). contentLimit is passed through
// as-is (the caller decides whether to gate the viewer's own drafts).
func (s *GalgameService) PublicBatchThinViewer(ctx context.Context, ids []int, viewerUID int, contentLimit string, inc PublicItemInclude) ([]dto.PublicGalgameItem, error) {
	rows, err := s.galgameRepo.FindByIDsWithViewer(ctx, ids, viewerUID, contentLimit)
	if err != nil {
		return nil, err
	}
	items := s.thinItems(ctx, rows, contentLimit, inc)
	return orderItemsByIDs(items, ids), nil
}

// PublicTrackView bumps the view counter for one galgame — the /v1 detail
// track_view path (P6, gated to internal-tier callers by the handler). Fire-and-
// forget, mirroring the internal detail view bump (public traffic must not block
// on a counter write).
func (s *GalgameService) PublicTrackView(id int) {
	go func() {
		_ = s.galgameRepo.IncrementView(context.Background(), id)
	}()
}

// PublicBatchDetail returns full aggregate records (no include blocks) for the
// given ids — the batch ?view=detail variant. Reuses the per-id detail
// projection; the heavy load is bounded by the ≤100-id cap and CF-cached.
func (s *GalgameService) PublicBatchDetail(ctx context.Context, ids []int, contentLimit string) ([]dto.PublicGalgame, error) {
	out := make([]dto.PublicGalgame, 0, len(ids))
	for _, id := range ids {
		// view=detail carries no include=covers/screenshots, so no per-image
		// ratings surface — pass withRatings=false (the block is never built).
		rec, found, _, err := s.PublicDetail(ctx, id, PublicInclude{}, contentLimit, false)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, rec)
		}
	}
	return out, nil
}

// PublicChanges returns a keyset page of {id, updated} ascending by (updated,
// id). On an empty page next_cursor echoes the incoming cursor so an incremental
// sync can keep polling the same position.
func (s *GalgameService) PublicChanges(ctx context.Context, cursor string, limit int, contentLimit string) (dto.PublicChangesData, error) {
	limit = publicClampLimit(limit, 100, 500)
	hasCur, boundary, afterID := decodeChangesCursor(cursor)

	rows, err := s.galgameRepo.PublicChanges(ctx, hasCur, boundary, afterID, limit, contentLimit)
	if err != nil {
		return dto.PublicChangesData{}, err
	}

	items := make([]dto.PublicChangeItem, len(rows))
	for i := range rows {
		items[i] = dto.PublicChangeItem{ID: rows[i].ID, Updated: rows[i].Updated.UTC().Format(publicTSLayout)}
	}

	data := dto.PublicChangesData{Items: items}
	if n := len(rows); n > 0 {
		nc := encodeChangesCursor(rows[n-1].Updated, rows[n-1].ID)
		data.NextCursor = &nc
	} else if cursor != "" {
		echo := cursor
		data.NextCursor = &echo
	}
	return data, nil
}

// thinItems maps galgame rows to thin public items, filling the banner/portrait
// pins (dims-enriched) and dropping NSFW-rated pins on the sfw face. When inc
// requests officials/scores (step 07 list-level include), each block is loaded
// with ONE batched query across the whole page (裁定 2: no per-item queries).
func (s *GalgameService) thinItems(ctx context.Context, rows []model.Galgame, contentLimit string, inc PublicItemInclude) []dto.PublicGalgameItem {
	if len(rows) == 0 {
		return []dto.PublicGalgameItem{}
	}
	dropNSFW := contentLimit == "sfw"

	ids := make([]int, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	covers, _ := s.galgameRepo.PublicPinnedCovers(ctx, ids)
	portraits, _ := s.galgameRepo.PublicPinnedPortraits(ctx, ids)

	// Batched include expansions: one query per requested block for the entire
	// page (a full-page IN, never N+1). Skipped when the block is not requested.
	var officialsByID map[int][]dto.PublicOfficial
	var scoresByID map[int]repository.ScoreMeta
	var metaByID map[int]model.Galgame
	var introByID map[int]model.Galgame
	if inc.Officials {
		officialsByID, _ = s.galgameRepo.PublicOfficials(ctx, ids)
	}
	if inc.Scores {
		scoresByID, _ = s.galgameRepo.LoadScoreMetaBatch(ctx, ids)
	}
	if inc.Meta {
		metaByID, _ = s.galgameRepo.PublicMetaBatch(ctx, ids)
	}
	if inc.Intro {
		introByID, _ = s.galgameRepo.PublicIntroBatch(ctx, ids)
	}

	usable := func(m map[int]repository.PublicPinnedImage, id int) (string, bool) {
		p, ok := m[id]
		if !ok || (dropNSFW && (p.Sexual > 0 || p.Violence > 0)) {
			return "", false
		}
		return p.ImageHash, true
	}

	// One batched image-meta lookup across both pin sets.
	hashes := make([]string, 0, len(rows)*2)
	for i := range rows {
		if h, ok := usable(covers, rows[i].ID); ok {
			hashes = append(hashes, h)
		}
		if h, ok := usable(portraits, rows[i].ID); ok {
			hashes = append(hashes, h)
		}
	}
	metas := s.resolveImageMeta(ctx, hashes)

	items := make([]dto.PublicGalgameItem, len(rows))
	for i := range rows {
		g := &rows[i]
		item := dto.PublicGalgameItem{
			ID:          g.ID,
			Names:       publicNames(g),
			ReleaseDate: fmtPublicDate(g.ReleaseDate),
			AgeLimit:    g.AgeLimit,
			Updated:     fmtPublicTS(g.Updated),
		}
		if h, ok := usable(covers, g.ID); ok {
			item.Banner = s.publicImage(h, metas[h], "")
		}
		if h, ok := usable(portraits, g.ID); ok {
			item.Portrait = s.publicImage(h, metas[h], "")
		}
		if inc.Officials {
			// Present (possibly empty) whenever requested, so the key never
			// vanishes on a maker-less game — nil map entry → [].
			offs := officialsByID[g.ID]
			if offs == nil {
				offs = []dto.PublicOfficial{}
			}
			item.Officials = &offs
		}
		if inc.Scores {
			// Zero-value ScoreMeta (no anchored source) → all-null scores block,
			// same shape as GET /galgame/:gid/scores for an unanchored game.
			sc := buildGalgameScores(scoresByID[g.ID])
			item.Scores = &sc
		}
		if inc.Meta {
			// The batched meta row carries the operational scalars; a missing id
			// (should not happen — same page) falls back to the list row's own
			// fields so the key is never dropped.
			mg := g
			if row, ok := metaByID[g.ID]; ok {
				mg = &row
			}
			m := buildPublicMeta(mg)
			item.Meta = &m
		}
		if inc.Intro {
			// Same batched-row fallback discipline as meta: a missing id (should
			// not happen — same page) falls back to the list row's own intro cols.
			ig := g
			if row, ok := introByID[g.ID]; ok {
				ig = &row
			}
			intro := dto.PublicIntro{
				ZhCN: nullIfEmptyPub(ig.IntroZhCN),
				EnUS: nullIfEmptyPub(ig.IntroEnUS),
				JaJP: nullIfEmptyPub(ig.IntroJaJP),
				ZhTW: nullIfEmptyPub(ig.IntroZhTW),
			}
			item.Intro = &intro
		}
		items[i] = item
	}
	return items
}

// publicImage composes one image output. Returns nil when the hash is empty or
// no CDN base is configured (the public face never emits a bare hash).
func (s *GalgameService) publicImage(hash string, m ImageMeta, kind string) *dto.PublicImage {
	if hash == "" || s.cdnBase == "" {
		return nil
	}
	return &dto.PublicImage{
		URL:       imageclient.MainURL(s.cdnBase, hash, "webp"),
		Width:     m.Width,
		Height:    m.Height,
		Thumbhash: m.Thumbhash,
		Kind:      kind,
	}
}

// ─────────────────────────── field mappers ───────────────────────────

func publicNames(g *model.Galgame) dto.PublicNames {
	return dto.PublicNames{
		JaJP: nullIfEmptyPub(g.NameJaJP),
		ZhCN: nullIfEmptyPub(g.NameZhCN),
		ZhTW: nullIfEmptyPub(g.NameZhTW),
		EnUS: nullIfEmptyPub(g.NameEnUS),
	}
}

func aliasNames(g *model.Galgame) []string {
	out := make([]string, 0, len(g.Alias))
	for i := range g.Alias {
		out = append(out, g.Alias[i].Name)
	}
	return out
}

// publicRefs fills the per-source external-id block. vndb/bangumi come from the
// galgame row; erogamescape from the EG score meta (裁定 3).
func publicRefs(g *model.Galgame, sm repository.ScoreMeta) dto.PublicRefs {
	refs := dto.PublicRefs{VNDB: nullIfEmptyPub(g.VNDBID)}
	if g.BangumiID != nil {
		s := strconv.Itoa(*g.BangumiID)
		refs.Bangumi = &s
	}
	if sm.EG != nil {
		s := strconv.Itoa(sm.EG.EGGameID)
		refs.ErogameScape = &s
	}
	return refs
}

// publicAttribution composes the per-source attribution URLs from constant
// templates + the id; a source with no id is null. Templates are the same ones
// the scores endpoint uses (P-★ narrow slice).
func publicAttribution(g *model.Galgame, sm repository.ScoreMeta) *dto.PublicAttribution {
	att := dto.PublicAttribution{}
	if g.VNDBID != "" {
		u := vndbURLBase + g.VNDBID
		att.VNDB = &u
	}
	if g.BangumiID != nil {
		u := bangumiURLBase + strconv.Itoa(*g.BangumiID)
		att.Bangumi = &u
	}
	if sm.EG != nil {
		u := egURLBase + strconv.Itoa(sm.EG.EGGameID)
		att.ErogameScape = &u
	}
	return &att
}

// publicTaxonomy fills the name-level taxonomy block from the detail preload.
func publicTaxonomy(g *model.Galgame) *dto.PublicTaxonomy {
	tax := &dto.PublicTaxonomy{
		Tags:      make([]string, 0, len(g.Tag)),
		Officials: make([]dto.PublicOfficial, 0, len(g.Official)),
		Engines:   make([]string, 0, len(g.Engine)),
		SeriesID:  g.SeriesID,
	}
	for i := range g.Tag {
		if g.Tag[i].Tag != nil {
			tax.Tags = append(tax.Tags, g.Tag[i].Tag.Name)
		}
	}
	for i := range g.Official {
		if o := g.Official[i].Official; o != nil {
			tax.Officials = append(tax.Officials, dto.PublicOfficial{ID: o.ID, Name: o.Name})
		}
	}
	for i := range g.Engine {
		if e := g.Engine[i].Engine; e != nil {
			tax.Engines = append(tax.Engines, e.Name)
		}
	}
	return tax
}

// publicTagRefs projects the loaded tag relations to rich references (id + name
// + category + this galgame's spoiler_level for the tag).
func publicTagRefs(g *model.Galgame) []dto.PublicTagRef {
	refs := make([]dto.PublicTagRef, 0, len(g.Tag))
	for i := range g.Tag {
		if t := g.Tag[i].Tag; t != nil {
			refs = append(refs, dto.PublicTagRef{
				ID:           t.ID,
				Name:         t.Name,
				Category:     t.Category,
				SpoilerLevel: g.Tag[i].SpoilerLevel,
				GalgameCount: t.GalgameCount,
			})
		}
	}
	return refs
}

// publicOfficialRefs projects the loaded maker relations to rich references
// (id + name + category + primary language).
func publicOfficialRefs(g *model.Galgame) []dto.PublicOfficialRef {
	refs := make([]dto.PublicOfficialRef, 0, len(g.Official))
	for i := range g.Official {
		if o := g.Official[i].Official; o != nil {
			refs = append(refs, dto.PublicOfficialRef{
				ID:           o.ID,
				Name:         o.Name,
				Category:     o.Category,
				Lang:         o.Lang,
				Link:         o.Link,
				Aliases:      officialAliasNames(o.Alias),
				GalgameCount: o.GalgameCount,
			})
		}
	}
	return refs
}

// publicEngineRefs projects the loaded engine relations to rich references
// (id + name).
func publicEngineRefs(g *model.Galgame) []dto.PublicEngineRef {
	refs := make([]dto.PublicEngineRef, 0, len(g.Engine))
	for i := range g.Engine {
		if e := g.Engine[i].Engine; e != nil {
			refs = append(refs, dto.PublicEngineRef{ID: e.ID, Name: e.Name, GalgameCount: e.GalgameCount})
		}
	}
	return refs
}

// publicLinks projects the loaded links to the curated public shape
// ({id,name,link,source,user_id}), dropping only the internal source_key
// bookkeeping (user_id serves a downstream banned-author filter, W1d). Order is
// the preloaded order (id ASC).
func publicLinks(g *model.Galgame) []dto.PublicLink {
	links := make([]dto.PublicLink, 0, len(g.Link))
	for i := range g.Link {
		l := &g.Link[i]
		links = append(links, dto.PublicLink{ID: l.ID, Name: l.Name, Link: l.Link, Source: l.Source, UserID: l.UserID})
	}
	return links
}

// buildPublicMeta maps a galgame row's operational scalars to the flat meta
// block (include=meta). Values mirror the internal bridge face for the same row
// (migration parity): vndb_id is the raw stored id, timestamps are UTC RFC3339
// (null on zero), series_id / catalog_work_id null when unset.
func buildPublicMeta(g *model.Galgame) dto.PublicMeta {
	return dto.PublicMeta{
		OriginalLanguage:   g.OriginalLanguage,
		VNDBID:             g.VNDBID,
		Status:             g.Status,
		ContentLimit:       g.ContentLimit,
		ReleasePrecision:   g.ReleasePrecision,
		SeriesID:           g.SeriesID,
		CatalogWorkID:      g.CatalogWorkID,
		UserID:             g.UserID,
		ResourceUpdateTime: fmtPublicTSPtr(g.ResourceUpdateTime),
		View:               g.View,
		Created:            fmtPublicTSPtr(g.Created),
	}
}

// ─────────────────────────── formatting ───────────────────────────

const (
	publicDateLayout = "2006-01-02"
	publicTSLayout   = "2006-01-02T15:04:05Z"
)

func fmtPublicDate(d *model.Date) *string {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(publicDateLayout)
	return &s
}

func fmtPublicTS(ts model.Timestamp) string {
	t := ts.Time()
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(publicTSLayout)
}

// fmtPublicTSPtr formats a timestamp as UTC RFC3339, returning nil on the zero
// value — matching the internal model.Timestamp JSON discipline (null on zero)
// for the meta block's resource_update_time / created keys.
func fmtPublicTSPtr(ts model.Timestamp) *string {
	t := ts.Time()
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(publicTSLayout)
	return &s
}

// nullIfEmptyPub mirrors dto.nullIfEmpty for the service layer.
func nullIfEmptyPub(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orderItemsByIDs reorders items to match the requested id order, dropping ids
// with no row. Used by batch/search to honor the caller's order.
func orderItemsByIDs(items []dto.PublicGalgameItem, ids []int) []dto.PublicGalgameItem {
	byID := make(map[int]dto.PublicGalgameItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	out := make([]dto.PublicGalgameItem, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out
}

// ─────────────────────────── cursors ───────────────────────────
//
// Cursors are opaque base64url tokens. The list "id" cursor is "<id>"; the
// "release_date" cursor is "<yyyy-mm-dd|>:<id>" (empty date = the undated tail);
// the changes cursor is "<updated_unix_micro>:<id>". Micros (not millis) keep the
// keyset lossless against the timestamptz column's microsecond precision so a
// boundary row is never duplicated or skipped — the token is opaque to clients,
// so the encoding is a free implementation choice.

func encodeCursor(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeCursor(cursor string) (string, bool) {
	if cursor == "" {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func encodeIDCursor(id int) string { return encodeCursor(strconv.Itoa(id)) }

func decodeIDCursor(cursor string) int {
	s, ok := decodeCursor(cursor)
	if !ok {
		return 0
	}
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return id
}

func encodeReleaseCursor(d *model.Date, id int) string {
	dateStr := ""
	if d != nil {
		if t := time.Time(*d); !t.IsZero() {
			dateStr = t.UTC().Format(publicDateLayout)
		}
	}
	return encodeCursor(dateStr + ":" + strconv.Itoa(id))
}

func decodeReleaseCursor(cursor string) (hasCursor bool, d time.Time, dNull bool, id int) {
	s, ok := decodeCursor(cursor)
	if !ok {
		return false, time.Time{}, false, 0
	}
	dateStr, idStr, found := strings.Cut(s, ":")
	if !found {
		return false, time.Time{}, false, 0
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return false, time.Time{}, false, 0
	}
	if dateStr == "" {
		return true, time.Time{}, true, id
	}
	parsed, err := time.Parse(publicDateLayout, dateStr)
	if err != nil {
		return false, time.Time{}, false, 0
	}
	return true, parsed, false, id
}

func encodeChangesCursor(updated time.Time, id int) string {
	return encodeCursor(strconv.FormatInt(updated.UnixMicro(), 10) + ":" + strconv.Itoa(id))
}

func decodeChangesCursor(cursor string) (hasCursor bool, boundary time.Time, id int) {
	s, ok := decodeCursor(cursor)
	if !ok {
		return false, time.Time{}, 0
	}
	microStr, idStr, found := strings.Cut(s, ":")
	if !found {
		return false, time.Time{}, 0
	}
	micro, err := strconv.ParseInt(microStr, 10, 64)
	if err != nil {
		return false, time.Time{}, 0
	}
	id, err = strconv.Atoi(idStr)
	if err != nil {
		return false, time.Time{}, 0
	}
	return true, time.UnixMicro(micro).UTC(), id
}
