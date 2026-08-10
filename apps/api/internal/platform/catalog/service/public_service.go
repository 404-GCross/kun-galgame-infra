package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
	"api/pkg/imageclient"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// NextMoe open-API public projection (step 03). These methods map the internal
// catalog graph to the FROZEN public DTOs (dto.PublicCatalog*), reusing the S2S
// read + resolve services — the S2S/admin face is untouched. Two invariants ride
// on every projection: probable/related anchors NEVER surface (exact-only,
// 硬红线) and content_rating=r18 works are fully hidden in Phase 1 (裁定 6). The
// public fetchable set (裁定 1) is galgame non-stub works: only those are
// independently GET-able; cross-media ends still appear as briefs.
type PublicService struct {
	db      *gorm.DB
	read    *ReadService
	resolve *ResolveService
	mediums map[int16]string // medium id → key (seeded, tiny, loaded once)
	sources map[int16]string // source id → PUBLIC key (facet provenance, wave 104)
	cdnBase string           // image CDN base — covers/screenshots render complete URLs, never hashes
	// imageMeta / metaCache are the A2-1a cover enrichment (dimensions +
	// thumbhash, and the orientation evidence the cover-slot picker needs).
	// Both nil = enrichment off, which degrades gracefully (see WithImageMeta).
	imageMeta ImageMetaFunc
	metaCache *imageMetaCache
	// worksSearch backs the works PRODUCT search face (A2-1d). nil = the face
	// is unavailable (ErrSearchUnavailable); see WithWorksSearch.
	worksSearch *catsearch.Indexer
}

// NewPublicService builds the public projection over the catalog DB, reusing the
// already-constructed read + resolve services.
func NewPublicService(db *gorm.DB, read *ReadService, resolve *ResolveService, cdnBase string) *PublicService {
	s := &PublicService{db: db, read: read, resolve: resolve,
		mediums: map[int16]string{}, sources: map[int16]string{}, cdnBase: cdnBase}
	// Mediums are a tiny seeded vocabulary; load once at construction. A failure
	// leaves the map empty (briefs carry an empty medium) rather than blocking the
	// service — the rows are always seeded in practice.
	var rows []struct {
		ID  int16
		Key string
	}
	if err := db.Raw(`SELECT id, key FROM catalog_medium`).Scan(&rows).Error; err == nil {
		for _, r := range rows {
			s.mediums[r.ID] = r.Key
		}
	}
	var srcRows []struct {
		ID  int16
		Key string
	}
	if err := db.Raw(`SELECT id, key FROM catalog_source`).Scan(&srcRows).Error; err == nil {
		for _, r := range srcRows {
			s.sources[r.ID] = publicSourceKey(r.Key)
		}
	}
	return s
}

// galgameMediumID is the medium the Phase-1 fetchable set is scoped to (裁定 1).
const galgameMediumID int16 = 1

// PublicInclude selects the optional (heavy) blocks of a work/entity record.
type PublicInclude struct {
	Relations bool
	Credits   bool
	Works     bool
}

// ParsePublicInclude resolves the comma-separated include token set. Unknown
// tokens are ignored.
func ParsePublicInclude(raw string) PublicInclude {
	var inc PublicInclude
	for _, tok := range strings.Split(raw, ",") {
		switch strings.TrimSpace(tok) {
		case "relations":
			inc.Relations = true
		case "credits":
			inc.Credits = true
		case "works":
			inc.Works = true
		}
	}
	return inc
}

// ─────────────────────────── lookup (killer) ───────────────────────────

// publicLookupTypes is the CLOSED `type` vocabulary of the reverse-lookup face:
// token → the entity family the external id is resolved against. Deliberately
// narrower than the resolve/redirects entity vocabulary — person / org / release
// are not independently addressable on the public face, so they are not
// lookup-able either.
var publicLookupTypes = map[string]int16{
	"work":      model.EntityTypeWork,
	"name":      model.EntityTypeCreditName,
	"character": model.EntityTypeCharacter,
	"label":     model.EntityTypeLabel,
}

// ParsePublicLookupType resolves a lookup `type` token to its entity family plus
// the canonical token echoed back on the response; an absent token means work
// (the original contract, unchanged). ok=false → the token is outside the closed
// vocabulary and BOTH faces answer 400 — note the deliberate contrast with an
// unknown SOURCE, which stays a miss because sources are an open registry.
func ParsePublicLookupType(raw string) (entityType int16, key string, ok bool) {
	key = strings.TrimSpace(raw)
	if key == "" {
		key = "work"
	}
	entityType, ok = publicLookupTypes[key]
	return entityType, key, ok
}

// Lookup resolves an external id to its catalog work via an EXACT anchor only
// (probable is an unverified hypothesis and never crosses the public face).
// found=false → 404 when the source is unknown, no exact anchor exists, or the
// resolved work is r18 (hidden). external_id is normalized per source (裁定 3).
func (s *PublicService) Lookup(ctx context.Context, source, externalID string, nsfw bool) (dto.PublicLookupData, bool, error) {
	return s.LookupTyped(ctx, source, externalID, model.EntityTypeWork, nsfw)
}

// LookupTyped resolves an external id against ONE entity family (the `type`
// parameter, already parsed to its constant). work keeps the release-anchor
// fallback and answers with the work brief + claim pointer; name / character /
// label take a single-entity-type exact anchor and then delegate to that
// entity's own detail projection with the heavy blocks off — so each family
// inherits its established visibility rules by construction instead of growing a
// second, drifting copy of them here.
func (s *PublicService) LookupTyped(ctx context.Context, source, externalID string, entityType int16, nsfw bool) (dto.PublicLookupData, bool, error) {
	if entityType == model.EntityTypeWork {
		brief, err := s.lookupBrief(ctx, source, externalID, nsfw)
		if err != nil || brief == nil {
			return dto.PublicLookupData{}, false, err
		}
		return dto.PublicLookupData{Work: brief, ClaimedBy: brief.ClaimedBy}, true, nil
	}

	id, err := s.lookupEntityID(ctx, source, externalID, entityType)
	if err != nil || id == 0 {
		return dto.PublicLookupData{}, false, err
	}
	switch entityType {
	case model.EntityTypeCreditName:
		rec, found, err := s.Name(ctx, id, false, nsfw, 0, 0)
		if err != nil || !found {
			return dto.PublicLookupData{}, false, err
		}
		return dto.PublicLookupData{Name: &rec}, true, nil
	case model.EntityTypeCharacter:
		rec, found, err := s.Character(ctx, id, false, nsfw, 0, 0, 0)
		if err != nil || !found {
			return dto.PublicLookupData{}, false, err
		}
		return dto.PublicLookupData{Character: &rec}, true, nil
	case model.EntityTypeLabel:
		rec, found, err := s.Label(ctx, id, false, nsfw, 0, 0)
		if err != nil || !found {
			return dto.PublicLookupData{}, false, err
		}
		return dto.PublicLookupData{Label: &rec}, true, nil
	default:
		return dto.PublicLookupData{}, false, nil
	}
}

// LookupBatch resolves up to len(pairs) external ids, preserving order; a miss /
// hidden entity yields null blocks in its slot (裁定 3), and every item echoes
// the RESOLVED type token. The caller caps the count and rejects unknown type
// tokens with a 400 — an unknown token reaching here can only miss.
func (s *PublicService) LookupBatch(ctx context.Context, pairs []dto.PublicLookupPair, nsfw bool) ([]dto.PublicLookupBatchItem, error) {
	out := make([]dto.PublicLookupBatchItem, 0, len(pairs))
	for _, p := range pairs {
		entityType, key, ok := ParsePublicLookupType(p.Type)
		item := dto.PublicLookupBatchItem{Source: p.Source, ExternalID: p.ExternalID, Type: key}
		if ok {
			data, found, err := s.LookupTyped(ctx, p.Source, p.ExternalID, entityType, nsfw)
			if err != nil {
				return nil, err
			}
			if found {
				item.Work, item.ClaimedBy = data.Work, data.ClaimedBy
				item.Name, item.Character, item.Label = data.Name, data.Character, data.Label
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// lookupEntityID is the single-family exact-anchor resolver behind the non-work
// lookup types: source key → source id → the exact ref OF THAT entity type.
// 0 = miss. There is no release-style fallback — that indirection is specific to
// works (a SKU anchor standing in for its work).
func (s *PublicService) lookupEntityID(ctx context.Context, source, externalID string, entityType int16) (int64, error) {
	db := s.db.WithContext(ctx)

	var srcID int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = ?`, registrySourceKey(source)).Scan(&srcID).Error; err != nil {
		return 0, err
	}
	if srcID == 0 {
		return 0, nil // unknown source key
	}
	// Verbatim id — deliberately NOT normalizeExternalID, whose vndb "v" prefix
	// rule is work-specific. The registry stores vndb characters as c123 and
	// labels as p123 but staff as BARE numbers, so there is no bare-number form
	// to complete here: prefixing would make every vndb non-work anchor miss.
	ext := strings.TrimSpace(externalID)
	if ext == "" {
		return 0, nil
	}

	var entityID int64
	if err := db.Raw(`SELECT entity_id FROM catalog_external_ref
		WHERE source_id = ? AND external_id = ? AND link_kind = ? AND entity_type = ? LIMIT 1`,
		srcID, ext, model.LinkKindExact, entityType).Scan(&entityID).Error; err != nil {
		return 0, err
	}
	return entityID, nil
}

// lookupBrief is the shared exact-anchor resolver: source key → source id →
// exact ref (work- or release-level) → owning work → public brief (nil on any
// miss or an r18 work).
func (s *PublicService) lookupBrief(ctx context.Context, source, externalID string, nsfw bool) (*dto.PublicWorkBrief, error) {
	db := s.db.WithContext(ctx)

	var srcID int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = ?`, registrySourceKey(source)).Scan(&srcID).Error; err != nil {
		return nil, err
	}
	if srcID == 0 {
		return nil, nil // unknown source key
	}
	ext := normalizeExternalID(source, externalID)
	if ext == "" {
		return nil, nil
	}

	var ref struct {
		EntityType int16
		EntityID   int64
	}
	// EXACT tier only (link_kind = 0). Work-level anchor wins a tie over a
	// release-level one (entity_type ASC).
	if err := db.Raw(`SELECT entity_type, entity_id FROM catalog_external_ref
		WHERE source_id = ? AND external_id = ? AND link_kind = ? AND entity_type IN (?, ?)
		ORDER BY entity_type ASC LIMIT 1`,
		srcID, ext, model.LinkKindExact, model.EntityTypeWork, model.EntityTypeRelease).Scan(&ref).Error; err != nil {
		return nil, err
	}
	if ref.EntityID == 0 {
		return nil, nil
	}
	workID := ref.EntityID
	if ref.EntityType == model.EntityTypeRelease {
		if err := db.Raw(`SELECT work_id FROM catalog_release WHERE id = ?`, ref.EntityID).Scan(&workID).Error; err != nil {
			return nil, err
		}
	}
	briefs, err := s.loadWorkBriefs(ctx, []int64{workID}, nsfw)
	if err != nil {
		return nil, err
	}
	return briefs[workID], nil // nil when r18 / not found (loadWorkBriefs drops r18)
}

// ─────────────────────────── work detail ───────────────────────────

// WorkDetail projects one work to the frozen v1 record. found=false → 404 when
// the id is unknown OR outside the fetchable set (galgame, live). The Phase-1
// blanket r18 gate (裁定 6) became CALLER-CONTROLLED in wave 104: r18 works are
// served only with nsfw=1 (the API is r18-capable and the consumer decides —
// default hidden keeps existing consumers bit-identical). relations / credits
// stay include-gated; every aggregation facet (popularity / ratings / tags /
// playtimes / series / platforms / intro / covers / screenshots / characters /
// labels / releases) is always present (wave 104 全量开放).
// spoilers is the tag/edge spoiler ceiling (0-2, default 0 = safe), the same
// parameter shape characters/{id} already uses: at 0 the record contains no
// spoiler-flagged tag at all, so a consumer that ignores the axis is safe by
// construction and the default response stays byte-identical to A2-1d's.
func (s *PublicService) WorkDetail(ctx context.Context, id int64, inc PublicInclude, nsfw bool, spoilers int16) (dto.PublicCatalogWork, bool, error) {
	detail, err := s.read.WorkByID(ctx, id, spoilers)
	if err != nil {
		if stderrors.Is(err, ErrWorkNotFound) {
			return dto.PublicCatalogWork{}, false, nil
		}
		return dto.PublicCatalogWork{}, false, err
	}
	w := detail.Work
	if w.MediumID != galgameMediumID || w.Status != model.WorkStatusLive {
		return dto.PublicCatalogWork{}, false, nil
	}
	if !nsfw && isR18(w.ContentRating) {
		return dto.PublicCatalogWork{}, false, nil
	}

	// The display axis (A2-R5) for this record AND every brief it embeds, in ONE
	// batched wiki-body read: the work itself, its series siblings and — when
	// asked for — its relation ends. Every one of them renders a claimed_by.
	subjects := []claimSubject{{WorkID: w.ID}}
	for _, sb := range detail.SeriesSiblings {
		subjects = append(subjects, claimSubject{WorkID: sb.WorkID})
	}
	if inc.Relations {
		for _, r := range detail.Relations {
			subjects = append(subjects, claimSubject{WorkID: r.OtherID})
		}
	}
	limits, err := s.read.loadDisplayNSFW(ctx, subjects)
	if err != nil {
		return dto.PublicCatalogWork{}, false, err
	}

	rec := dto.PublicCatalogWork{
		ID:            w.ID,
		Medium:        s.mediumKey(w.MediumID),
		DisplayName:   w.DisplayName,
		OLang:         w.OLang,
		ContentRating: contentRatingKey(w.ContentRating),
		ReleaseDate:   earliestReleaseDate(detail.Releases),
		Titles:        publicTitles(detail.Titles),
		Refs:          publicRefs(detail.Refs),
		ClaimedBy:     claimedBy(w.Site, w.ProductWorkID, w.ClaimState, limits[w.ID], w.ContentRating),
		Created:       w.CreatedAt.UTC().Format(time.RFC3339),
		Updated:       w.UpdatedAt.UTC().Format(time.RFC3339),
	}
	s.attachWorkFacets(ctx, &rec, detail, nsfw, limits[w.ID], spoilers)
	// Before attachWorkChipCounts, which stamps work_count onto these chips
	// together with the work-level ones (one aggregate, not two).
	if err = s.attachReleaseLabels(ctx, rec.Releases); err != nil {
		return dto.PublicCatalogWork{}, false, err
	}
	if rec.Engines, err = s.workEngines(ctx, id); err != nil {
		return dto.PublicCatalogWork{}, false, err
	}
	if err = s.attachWorkChipCounts(ctx, &rec, nsfw); err != nil {
		return dto.PublicCatalogWork{}, false, err
	}
	if rec.Links, err = s.workLinks(ctx, id); err != nil {
		return dto.PublicCatalogWork{}, false, err
	}
	rec.SeriesSiblings = s.publicSeriesSiblings(detail.SeriesSiblings, nsfw, limits)
	if inc.Relations {
		rec.Relations = s.publicRelations(detail.Relations, nsfw, limits)
	}
	if inc.Credits {
		groups, err := s.workCredits(ctx, id)
		if err != nil {
			return dto.PublicCatalogWork{}, false, err
		}
		rec.Credits = groups
	}
	return rec, true, nil
}

// publicRelations projects the detail's relation edges (loaded by ReadService,
// wave 104) to the public shape, dropping r18 ends unless nsfw. A dropped end's
// identity stays reachable via lookup once the caller opts in.
func (s *PublicService) publicRelations(rels []WorkRelationRow, nsfw bool, limits map[int64]bool) []dto.PublicRelation {
	out := make([]dto.PublicRelation, 0, len(rels))
	for _, r := range rels {
		if !nsfw && isR18(r.ContentRating) {
			continue
		}
		out = append(out, dto.PublicRelation{
			RelationType: r.Key, Phrase: r.Phrase,
			Work: dto.PublicWorkBrief{
				ID: r.OtherID, Medium: s.mediumKey(r.MediumID), DisplayName: r.DisplayName,
				ContentRating: contentRatingKey(r.ContentRating),
				ClaimedBy:     claimedBy(r.Site, r.ProductWorkID, r.ClaimState, limits[r.OtherID], r.ContentRating),
			},
		})
	}
	return out
}

// attachWorkFacets projects every aggregation facet onto the public record
// (wave 104 全量开放): source keys not ids, CDN URLs not hashes, string
// vocabularies not enum ints. Every block is always present ([] when empty).
// ctx carries the one batched image_service lookup that enriches covers AND
// screenshots with width/height/thumbhash (A2-1a, extended to screenshots by
// A2-1b); an unwired lookup or an unknown hash simply omits the three keys.
// nsfw reaches only cover_slots: covers[] publishes every row with its flags
// and lets the consumer choose, but a SLOT is a rendering decision, so it obeys
// the same sfw rule the list face applies.
// attachWorkFacets projects every non-include facet of a loaded detail onto the
// public record. displayNSFW is the SUBJECT work's editorial display flag (not
// the caller's age gate) — the cover slots need it to decide whether sexual art
// may represent a work at all.
func (s *PublicService) attachWorkFacets(ctx context.Context, rec *dto.PublicCatalogWork, detail *WorkDetail, nsfw, displayNSFW bool, spoilers int16) {
	rec.Releases = make([]dto.PublicRelease, 0, len(detail.Releases))
	for _, rd := range detail.Releases {
		r := rd.Release
		pr := dto.PublicRelease{
			ID: r.ID, Kind: releaseKindKey(r.Kind), Date: releaseDate(r),
			Title: derefStrPub(r.Title), Lang: derefStrPub(r.Lang),
			Platform: derefStrPub(r.Platform), Platforms: publicPlatformsFromExtra(r.Extra),
			Refs: make([]dto.PublicCatalogRef, 0, len(rd.Anchors)),
		}
		for _, a := range rd.Anchors {
			if a.LinkKind != model.LinkKindExact {
				continue // probable/related never cross the public face
			}
			pr.Refs = append(pr.Refs, dto.PublicCatalogRef{Source: publicSourceKey(a.Source), ExternalID: a.ExternalID})
		}
		rec.Releases = append(rec.Releases, pr)
	}
	rec.Popularity = make([]dto.PublicPopularity, 0, len(detail.Popularity))
	for _, p := range detail.Popularity {
		rec.Popularity = append(rec.Popularity, dto.PublicPopularity{
			Source: s.sourceKey(p.SourceID), Metric: popularityMetricKey(p.Metric), Value: p.Value,
		})
	}
	rec.Ratings = make([]dto.PublicRating, 0, len(detail.Ratings))
	for _, r := range detail.Ratings {
		rec.Ratings = append(rec.Ratings, dto.PublicRating{
			Source: s.sourceKey(r.SourceID), Score: r.Score, VoteCount: r.VoteCount, Rank: r.Rank,
		})
	}
	rec.Tags = make([]dto.PublicTag, 0, len(detail.Tags))
	for _, t := range detail.Tags {
		// Defence in depth: the loader already filtered to the ceiling, but a
		// spoiler tag leaking onto a default response is the one failure this
		// axis exists to prevent, so the projection re-checks it.
		if t.Spoiler > spoilers {
			continue
		}
		pt := dto.PublicTag{
			Name: t.Name, Count: t.Count, Source: s.sourceKey(t.SourceID),
			Spoiler: t.Spoiler, Sexual: t.Sexual,
		}
		if t.CanonicalID != nil {
			pt.CanonicalID = *t.CanonicalID
		}
		if t.Tier != nil {
			pt.Tier = tagTierKey(*t.Tier)
		}
		if t.Kind != nil {
			pt.Kind = tagKindKey(*t.Kind)
		}
		rec.Tags = append(rec.Tags, pt)
	}
	rec.Playtimes = make([]dto.PublicPlaytime, 0, len(detail.Playtimes))
	for _, p := range detail.Playtimes {
		rec.Playtimes = append(rec.Playtimes, dto.PublicPlaytime{
			Source: s.sourceKey(p.SourceID), Minutes: p.Minutes, VoteCount: p.VoteCount,
		})
	}
	rec.Series = make([]dto.PublicSeries, 0, len(detail.Series))
	for _, se := range detail.Series {
		rec.Series = append(rec.Series, dto.PublicSeries{
			ID: se.ID, Name: se.Name, Source: s.sourceKey(se.SourceID), MemberCount: se.MemberCount,
		})
	}
	rec.Platforms = make([]dto.PublicPlatform, 0, len(detail.Platforms))
	for _, p := range detail.Platforms {
		rec.Platforms = append(rec.Platforms, dto.PublicPlatform{Platform: p.Platform, Source: s.sourceKey(p.SourceID)})
	}
	rec.Intro = make([]dto.PublicWorkIntro, 0, len(detail.Intros))
	for _, in := range detail.Intros {
		rec.Intro = append(rec.Intro, dto.PublicWorkIntro{
			Lang: in.Lang, Intro: in.Intro, Source: s.sourceKey(in.SourceID), Machine: in.Machine,
		})
	}
	rec.Covers = make([]dto.PublicCover, 0, len(detail.Covers))
	// ONE image_service batch for the record's whole image set — covers and
	// screenshots share it (A2-1b).
	imgMeta := s.workMediaMetaFor(ctx, detail.Covers, detail.Screenshots)
	for _, c := range detail.Covers {
		url := s.imageURL(c.ImageHash)
		if url == "" {
			continue // never a bare hash on the public face
		}
		pc := dto.PublicCover{
			URL: url, Kind: c.Kind, PortraitPinned: c.PortraitPinned,
			Sexual: c.Sexual, Violence: c.Violence, Source: s.sourceKey(c.SourceID),
		}
		if m, ok := imgMeta[c.ImageHash]; ok {
			pc.Width, pc.Height, pc.Thumbhash = m.Width, m.Height, m.Thumbhash
		}
		rec.Covers = append(rec.Covers, pc)
	}
	// Same picker, same batch as the list face — one policy, two faces.
	//
	// nsfw is the AGE gate, and every consumer holds it open just to see that
	// r18 works exist, so it cannot be the whole permission to render sexual
	// ART: on production 223 works declared display-safe (display_nsfw=false)
	// were handed a sexual-flagged hero by it. The IMAGERY question is the
	// editorial display axis, and both must say yes.
	rec.CoverSlots = s.pickCoverSlots(detail.Covers, imgMeta, nsfw && displayNSFW)
	rec.Screenshots = make([]dto.PublicScreenshot, 0, len(detail.Screenshots))
	for _, sc := range detail.Screenshots {
		url := s.imageURL(sc.ImageHash)
		if url == "" {
			continue
		}
		ps := dto.PublicScreenshot{
			URL: url, Caption: sc.Caption, Sexual: sc.Sexual, Violence: sc.Violence, Source: s.sourceKey(sc.SourceID),
		}
		if m, ok := imgMeta[sc.ImageHash]; ok {
			ps.Width, ps.Height, ps.Thumbhash = m.Width, m.Height, m.Thumbhash
		}
		rec.Screenshots = append(rec.Screenshots, ps)
	}
	rec.Characters = make([]dto.PublicRosterCharacter, 0, len(detail.Characters))
	for _, ch := range detail.Characters {
		pc := dto.PublicRosterCharacter{
			ID: ch.CharacterID, Name: ch.DisplayName, Latin: derefStrPub(ch.Latin),
			Kind: rosterKindKey(ch.Kind), Spoiler: ch.Spoiler,
			Voices: make([]dto.PublicRosterVoice, 0, len(ch.Va)),
		}
		if ch.ImageHash != nil {
			pc.Image = s.imageURL(*ch.ImageHash)
		}
		if ch.FigureHash != nil {
			pc.Figure = s.imageURL(*ch.FigureHash)
		}
		for _, v := range ch.Va {
			pc.Voices = append(pc.Voices, dto.PublicRosterVoice{ID: v.CreditNameID, Name: v.Name})
		}
		rec.Characters = append(rec.Characters, pc)
	}
	// Same collapser as the list face — one entry per company, capacities in
	// kinds[]. Two projections of the same edges must not disagree about how
	// many companies a work has.
	rec.Labels = publicWorkLabels(detail.Labels)
	if rec.Labels == nil {
		rec.Labels = []dto.PublicWorkLabel{}
	}
}

// imageURL renders a complete CDN URL from an image hash ("" when no hash or no
// CDN base — the public face never emits a bare hash).
func (s *PublicService) imageURL(hash string) string {
	if hash == "" || s.cdnBase == "" {
		return ""
	}
	return imageclient.MainURL(s.cdnBase, hash, "webp")
}

// sourceKey maps a source id to its PUBLIC key ("" when unknown).
func (s *PublicService) sourceKey(id int16) string {
	return s.sources[id]
}

// resolveSourceIDs turns a comma-separated source= parameter into registry ids.
// filter=false means the caller passed nothing and wants no gate at all; an
// empty ids slice with filter=true means every token was unrecognized, which
// the caller answers with an empty page (OPEN vocabulary — see SeriesList).
// Both the public and the registry spelling of a key are accepted, matching
// what the lookup face already does.
func (s *PublicService) resolveSourceIDs(param string) (ids []int16, filter bool) {
	param = strings.TrimSpace(param)
	if param == "" {
		return nil, false
	}
	seen := map[int16]bool{}
	for tok := range strings.SplitSeq(param, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		want := publicSourceKey(registrySourceKey(tok))
		for id, key := range s.sources {
			if key == want && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	return ids, true
}

// popularityMetricKey projects the PopularityMetric* vocabulary to stable
// public string keys (the contract never leaks enum ints).
func popularityMetricKey(m int16) string {
	switch m {
	case model.PopularityMetricDownloads:
		return "downloads"
	case model.PopularityMetricWishlist:
		return "wishlist"
	case model.PopularityMetricReviews:
		return "reviews"
	case model.PopularityMetricBgmWish:
		return "bgm_wish"
	case model.PopularityMetricBgmCollect:
		return "bgm_collect"
	case model.PopularityMetricBgmDoing:
		return "bgm_doing"
	case model.PopularityMetricBgmOnHold:
		return "bgm_on_hold"
	case model.PopularityMetricBgmDropped:
		return "bgm_dropped"
	default:
		return fmt.Sprintf("metric_%d", m)
	}
}

func rosterKindKey(k int16) string {
	switch k {
	case model.WorkCharacterKindMain:
		return "main"
	case model.WorkCharacterKindSecondary:
		return "secondary"
	case model.WorkCharacterKindAppears:
		return "appears"
	default:
		return "unknown"
	}
}

func releaseKindKey(k int16) string {
	switch k {
	case model.ReleaseKindDigital:
		return "digital"
	case model.ReleaseKindPhysical:
		return "physical"
	case model.ReleaseKindTrial:
		return "trial"
	case model.ReleaseKindPatch:
		return "patch"
	default:
		return "default"
	}
}

// releaseDate renders one release's fuzzy date as partial ISO
// (YYYY[-MM[-DD]]); nil without a year.
func releaseDate(r model.CatalogRelease) *string {
	if r.ReleasedY == nil {
		return nil
	}
	d := fmt.Sprintf("%04d", *r.ReleasedY)
	if r.ReleasedM != nil && *r.ReleasedM > 0 {
		d = fmt.Sprintf("%s-%02d", d, *r.ReleasedM)
		if r.ReleasedD != nil && *r.ReleasedD > 0 {
			d = fmt.Sprintf("%s-%02d", d, *r.ReleasedD)
		}
	}
	return &d
}

// publicPlatformsFromExtra parses extra.platforms (the step-76/96 write shape);
// absent/malformed → nil (omitempty).
func publicPlatformsFromExtra(extra datatypes.JSON) []string {
	if len(extra) == 0 {
		return nil
	}
	var e struct {
		Platforms []string `json:"platforms"`
	}
	if err := json.Unmarshal(extra, &e); err != nil {
		return nil
	}
	return e.Platforms
}

// workCredits projects a work's credits (reusing the S2S read query), grouping
// consecutive rows by role and whitelisting fields (note / provenance stripped,
// 裁定 7; source published since the 2026-08-07 carve-out).
func (s *PublicService) workCredits(ctx context.Context, workID int64) ([]dto.PublicCreditGroup, error) {
	rows, err := s.read.WorkCredits(ctx, workID)
	if err != nil {
		return nil, err
	}
	groups := make([]dto.PublicCreditGroup, 0)
	var cur *dto.PublicCreditGroup
	for _, r := range rows {
		if cur == nil || cur.RoleKey != r.RoleKey {
			groups = append(groups, dto.PublicCreditGroup{
				RoleKey:  r.RoleKey,
				RoleName: firstNonEmptyPub(r.RoleNameCN, r.RoleNameJA, r.RoleKey),
			})
			cur = &groups[len(groups)-1]
		}
		item := dto.PublicCreditItem{ID: r.CreditNameID, Name: r.Name, Lang: r.Lang}
		if r.Latin != nil {
			item.Latin = *r.Latin
		}
		if r.CharacterID != nil {
			item.CharacterID = *r.CharacterID
		}
		if r.CharacterNM != nil {
			item.Character = *r.CharacterNM
		}
		if r.LabelID != nil {
			item.LabelID = *r.LabelID
		}
		if r.LabelNM != nil {
			item.Label = *r.LabelNM
		}
		if r.SourceKey != nil {
			item.Source = publicSourceKey(*r.SourceKey)
		}
		cur.Credits = append(cur.Credits, item)
	}
	return groups, nil
}

// ─────────────────────────── names / characters / labels ───────────────────────────

// Name projects a credited identity (GET /v1/catalog/names/{id}; {id} is a
// credit-name id) reusing ReadService.NameWorks — which implements the hidden
// credit_name→person link doctrine (裁定 6). found=false → 404. credits (works +
// roles) are attached only when included; r18 works are dropped and offset
// paging is over the unfiltered query so nothing is skipped.
func (s *PublicService) Name(ctx context.Context, id int64, withCredits, nsfw bool, limit, offset int) (dto.PublicName, bool, error) {
	res, err := s.read.NameWorks(ctx, id, limit, offset)
	if err != nil {
		return dto.PublicName{}, false, err
	}
	if res.Head == nil {
		return dto.PublicName{}, false, nil
	}
	p := dto.PublicName{
		ID:          res.Head.ID,
		DisplayName: res.Head.Name,
		Lang:        res.Head.Lang,
		Latin:       derefStrPub(res.Head.Latin),
		Siblings:    make([]dto.PublicSiblingName, 0, len(res.Siblings)),
		// The person block (wave 172). NameWorks has already applied the
		// link-visibility gate to all five, so this copies them verbatim.
		PhotoHash: res.Head.PhotoHash,
		Gender:    res.Head.Gender,
		BirthY:    res.Head.BirthY,
		BirthM:    res.Head.BirthM,
		BirthD:    res.Head.BirthD,
		// links is always present, [] when the person has none — or when there
		// is no visible person at all (see the gate below).
		Links: []dto.PublicPersonLink{},
	}
	if res.Head.PersonID != nil {
		p.PersonID = *res.Head.PersonID
		// The person's web presence rides the SAME gate as the rest of the
		// person block: NameWorks nils PersonID out when the credit_name→person
		// link is hidden, so an unset id means there is nothing to publish here.
		if p.Links, err = s.personLinks(ctx, p.PersonID); err != nil {
			return dto.PublicName{}, false, err
		}
	}
	for _, sib := range res.Siblings {
		p.Siblings = append(p.Siblings, dto.PublicSiblingName{
			ID:          sib.ID,
			DisplayName: sib.Name,
			Lang:        sib.Lang,
			Latin:       derefStrPub(sib.Latin),
		})
	}
	if p.Aliases, p.Localized, err = s.nameAliases(ctx, id); err != nil {
		return dto.PublicName{}, false, err
	}
	// p.PersonID is 0 unless NameWorks published a visible person link above,
	// which is exactly the gate the person intro lane must ride.
	if p.Intros, err = s.nameIntros(ctx, id, p.PersonID); err != nil {
		return dto.PublicName{}, false, err
	}
	if p.Refs, err = s.entityRefs(ctx, model.EntityTypeCreditName, id); err != nil {
		return dto.PublicName{}, false, err
	}
	if withCredits {
		briefs, err := s.claimEnrich(ctx, res.Works)
		if err != nil {
			return dto.PublicName{}, false, err
		}
		p.Credits = make([]dto.PublicNameCredit, 0, len(res.Works))
		for _, w := range res.Works {
			b := s.briefFromRow(w.Brief, briefs[w.Brief.WorkID], nsfw)
			if b == nil {
				continue // r18 work (nsfw off) — drop
			}
			row := dto.PublicNameCredit{Work: *b, Roles: make([]dto.PublicNameRole, 0, len(w.Roles))}
			for _, r := range w.Roles {
				pr := dto.PublicNameRole{RoleKey: r.RoleKey, RoleName: firstNonEmptyPub(r.RoleNameCN, r.RoleNameJA, r.RoleKey)}
				if r.CharacterID != nil {
					pr.CharacterID = *r.CharacterID
				}
				if r.CharacterNM != nil {
					pr.Character = *r.CharacterNM
				}
				row.Roles = append(row.Roles, pr)
			}
			p.Credits = append(p.Credits, row)
		}
		p.NextOffset = nextOffset(len(res.Works), limit, offset)
	}
	return p, true, nil
}

// Character projects a character (GET /v1/catalog/characters/{id}) reusing
// ReadService.CharacterWorks. works (appears-in + voices) are include-gated.
func (s *PublicService) Character(ctx context.Context, id int64, withWorks, nsfw bool, spoilers int16, limit, offset int) (dto.PublicCharacter, bool, error) {
	res, err := s.read.CharacterWorks(ctx, id, limit, offset)
	if err != nil {
		return dto.PublicCharacter{}, false, err
	}
	if res.Head == nil {
		return dto.PublicCharacter{}, false, nil
	}
	ch := dto.PublicCharacter{
		ID:          res.Head.ID,
		DisplayName: res.Head.DisplayName,
		Lang:        res.Head.Lang,
		Latin:       derefStrPub(res.Head.Latin),
	}
	if ch.Aliases, ch.Localized, err = s.characterAliases(ctx, id); err != nil {
		return dto.PublicCharacter{}, false, err
	}
	if ch.Traits, err = s.characterTraits(ctx, id, spoilers, nsfw); err != nil {
		return dto.PublicCharacter{}, false, err
	}
	if ch.Refs, err = s.entityRefs(ctx, model.EntityTypeCharacter, id); err != nil {
		return dto.PublicCharacter{}, false, err
	}
	if ch.Intros, err = s.characterIntros(ctx, id); err != nil {
		return dto.PublicCharacter{}, false, err
	}
	var art struct {
		ImageHash  *string `gorm:"column:image_hash"`
		FigureHash *string `gorm:"column:figure_hash"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT image_hash, figure_hash FROM catalog_character WHERE id = ?`, id).Scan(&art).Error; err != nil {
		return dto.PublicCharacter{}, false, err
	}
	if art.ImageHash != nil {
		ch.Image = s.imageURL(*art.ImageHash)
	}
	if art.FigureHash != nil {
		ch.Figure = s.imageURL(*art.FigureHash)
	}
	if withWorks {
		briefs, err := s.claimEnrichCharacter(ctx, res.Works)
		if err != nil {
			return dto.PublicCharacter{}, false, err
		}
		ch.Works = make([]dto.PublicCharacterWork, 0, len(res.Works))
		for _, w := range res.Works {
			b := s.briefFromRow(w.Brief, briefs[w.Brief.WorkID], nsfw)
			if b == nil {
				continue
			}
			row := dto.PublicCharacterWork{Work: *b, Voices: make([]dto.PublicVoiceName, 0, len(w.Voices))}
			for _, v := range w.Voices {
				row.Voices = append(row.Voices, dto.PublicVoiceName{
					ID: v.CreditNameID, Name: v.Name, Lang: v.Lang, Latin: derefStrPub(v.Latin),
				})
			}
			ch.Works = append(ch.Works, row)
		}
		ch.NextOffset = nextOffset(len(res.Works), limit, offset)
	}
	return ch, true, nil
}

// Label projects a label (GET /v1/catalog/labels/{id}) reusing
// ReadService.LabelWorks. works (attributed) are include-gated.
func (s *PublicService) Label(ctx context.Context, id int64, withWorks, nsfw bool, limit, offset int) (dto.PublicLabel, bool, error) {
	head, items, _, err := s.read.LabelWorks(ctx, id, limit, offset)
	if err != nil {
		return dto.PublicLabel{}, false, err
	}
	if head == nil {
		return dto.PublicLabel{}, false, nil
	}
	l := dto.PublicLabel{
		ID: head.ID, DisplayName: head.DisplayName, Kind: labelKindKey(head.Kind), Lang: head.Lang,
		LogoHash: head.LogoHash,
	}
	// work_count is the browse lane's number for this one row (A2-1e) — the
	// SAME nsfw-aware, live-claim aggregate, so a detail page and the list it was
	// reached from can never disagree.
	counts, err := s.workCountsFor(ctx, labelWorkEdge, []int64{id}, nsfw)
	if err != nil {
		return dto.PublicLabel{}, false, err
	}
	l.WorkCount = counts[id]
	// …and the roll-up companion (wave 199): what a holding company would show
	// if it followed its own imprint arrows. A SECOND number, disjoint from the
	// first — never folded in.
	if l.ImprintWorkCount, err = s.imprintWorkCount(ctx, id, nsfw); err != nil {
		return dto.PublicLabel{}, false, err
	}
	// intros / links / aliases are part of the base record (not include-gated).
	if l.Aliases, l.Localized, err = s.labelAliases(ctx, id); err != nil {
		return dto.PublicLabel{}, false, err
	}
	if l.Intros, err = s.labelIntros(ctx, id); err != nil {
		return dto.PublicLabel{}, false, err
	}
	if l.Links, err = s.labelLinks(ctx, id); err != nil {
		return dto.PublicLabel{}, false, err
	}
	if l.Relations, err = s.labelRelations(ctx, id); err != nil {
		return dto.PublicLabel{}, false, err
	}
	if l.Refs, err = s.entityRefs(ctx, model.EntityTypeLabel, id); err != nil {
		return dto.PublicLabel{}, false, err
	}
	if withWorks {
		ids := make([]int64, 0, len(items))
		for _, w := range items {
			ids = append(ids, w.WorkID)
		}
		claims, err := s.claimedByFor(ctx, ids)
		if err != nil {
			return dto.PublicLabel{}, false, err
		}
		l.Works = make([]dto.PublicLabelWork, 0, len(items))
		for _, w := range items {
			if !nsfw && isR18(w.ContentRating) {
				continue
			}
			l.Works = append(l.Works, dto.PublicLabelWork{
				Work: dto.PublicWorkBrief{
					ID: w.WorkID, Medium: s.mediumKey(w.MediumID), DisplayName: w.DisplayName,
					ContentRating: contentRatingKey(w.ContentRating), ClaimedBy: claims[w.WorkID],
				},
				Kind: workLabelKindKey(w.Kind),
			})
		}
		l.NextOffset = nextOffset(len(items), limit, offset)
	}
	return l, true, nil
}

// labelIntros loads a label's multilingual intros, merged to one element per
// language (lowest source_id wins — the step-65 intro merge), lang ASC. source
// is the catalog_source key (public-face convention, never the numeric id).
// Empty → [].
func (s *PublicService) labelIntros(ctx context.Context, labelID int64) ([]dto.PublicLabelIntro, error) {
	var rows []struct {
		Lang       string `gorm:"column:lang"`
		Intro      string `gorm:"column:intro"`
		Source     string `gorm:"column:source"`
		Provenance int16  `gorm:"column:provenance"`
	}
	// provenance ASC puts SOURCE rows (0) ahead of machine translations (1)
	// within a language — a source row always wins (the step-75 rule).
	if err := s.db.WithContext(ctx).Raw(`
		SELECT i.lang, i.intro, src.key AS source, i.provenance
		FROM catalog_label_intro i JOIN catalog_source src ON src.id = i.source_id
		WHERE i.label_id = ? ORDER BY i.lang, i.provenance, i.source_id`, labelID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicLabelIntro, 0, len(rows))
	seenLang := map[string]bool{}
	for _, r := range rows {
		if seenLang[r.Lang] {
			continue // a higher-priority row already claimed this language
		}
		seenLang[r.Lang] = true
		out = append(out, dto.PublicLabelIntro{Lang: r.Lang, Intro: r.Intro, Source: r.Source, Machine: r.Provenance == 1})
	}
	return out, nil
}

// labelAliases loads a label's alternate spellings (A2-1e) as a flat, ordered,
// deduplicated string list, plus the wave-191 per-locale map. The display name
// is excluded from the flat list — it is already the record's display_name and
// repeating it in aliases[] would make every consumer filter it out again — but
// deliberately KEPT in the map; see public_names.go for why the two projections
// differ. Empty → [] and {}, never nil.
func (s *PublicService) labelAliases(ctx context.Context, labelID int64) ([]string, map[string]dto.PublicLocalizedName, error) {
	rows, err := s.entityAliases(ctx, labelAliasSource, labelID)
	if err != nil {
		return nil, nil, err
	}
	return flatAliases(rows), localizedNames(rows), nil
}

// characterAliases is labelAliases for characters. Until wave 191 the character
// face published no aliases at all, so the bangumi lane's zh-Hans character
// names — by far the largest localized-name population in the catalog — were
// unreachable through the public API.
func (s *PublicService) characterAliases(ctx context.Context, characterID int64) ([]string, map[string]dto.PublicLocalizedName, error) {
	rows, err := s.entityAliases(ctx, characterAliasSource, characterID)
	if err != nil {
		return nil, nil, err
	}
	return flatAliases(rows), localizedNames(rows), nil
}

// labelLinks projects a label's non-identity web-presence links from its
// entity_type=3, link_kind=related refs (refs/proj/89 ruling 3). The
// exact/probable identity anchors are excluded at the query level — identity
// anchors and web links never cross, a hard red line. Each external id is
// rendered to an absolute URL per source; an unknown related source (a future
// addition) has no template and is skipped, never guessed.
//
// dead_at IS NULL is the upstream-liveness predicate every user-facing
// external-id projection carries (see CatalogExternalRef.DeadAt): a row whose
// upstream entry has been observed absent renders as a 404. Only the VNDB
// anchor audit writes that column today and it never touches related rows, so
// this is a no-op right now — it is here so the liveness rule holds for the
// whole face rather than for the one lane that happens to have a writer, and
// personLinks / workLinks below carry it for the same reason.
// Deterministic (source_id, external_id); empty → [].
func (s *PublicService) labelLinks(ctx context.Context, labelID int64) ([]dto.PublicLabelLink, error) {
	var rows []struct {
		Source     string `gorm:"column:source"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT src.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id = ? AND r.link_kind = ? AND r.dead_at IS NULL
		ORDER BY r.source_id, r.external_id`,
		model.EntityTypeLabel, labelID, model.LinkKindRelated).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicLabelLink, 0, len(rows))
	for _, r := range rows {
		if url, ok := relatedLinkURL(r.Source, r.ExternalID); ok {
			out = append(out, dto.PublicLabelLink{Source: publicSourceKey(r.Source), URL: url})
		}
	}
	return out, nil
}

// labelRelations projects a label's corporate-structure neighbourhood (wave
// 186) from catalog_label_relation. The graph is stored MIRRORED — every fact
// is present under both endpoints with the inverse relation code — so this
// query is a plain `WHERE label_id = ?` and NEVER inverts anything: what it
// finds filed under the label is what the label's page says.
//
// A relation code with no public spelling is dropped rather than rendered as a
// number (model.LabelRelationKey is the one vocabulary). The other end is
// joined for its display name and soft-deleted labels are excluded — a merged-
// away label must not surface as a structural fact. Deterministic
// (relation, name, id); empty → [].
func (s *PublicService) labelRelations(ctx context.Context, labelID int64) ([]dto.PublicLabelRelation, error) {
	var rows []struct {
		ID       int64  `gorm:"column:id"`
		Name     string `gorm:"column:display_name"`
		Relation int16  `gorm:"column:relation"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT other.id, other.display_name, r.relation
		FROM catalog_label_relation r
		JOIN catalog_label other ON other.id = r.other_label_id AND other.deleted_at IS NULL
		WHERE r.label_id = ?
		ORDER BY r.relation, other.display_name, other.id`, labelID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicLabelRelation, 0, len(rows))
	for _, r := range rows {
		key, ok := model.LabelRelationKey[r.Relation]
		if !ok {
			continue
		}
		out = append(out, dto.PublicLabelRelation{ID: r.ID, Name: r.Name, Relation: key})
	}
	return out, nil
}

// personLinks projects a PERSON's non-identity web-presence links from its
// entity_type=0, link_kind=related refs — labelLinks' query with the person
// discriminator, through the same shared template table. The exact/probable
// identity anchors are excluded at the query level (identity anchors and web
// links never cross). Deterministic (source_id, external_id); empty → [].
func (s *PublicService) personLinks(ctx context.Context, personID int64) ([]dto.PublicPersonLink, error) {
	var rows []struct {
		Source     string `gorm:"column:source"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT src.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id = ? AND r.link_kind = ? AND r.dead_at IS NULL
		ORDER BY r.source_id, r.external_id`,
		model.EntityTypePerson, personID, model.LinkKindRelated).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicPersonLink, 0, len(rows))
	for _, r := range rows {
		if url, ok := relatedLinkURL(r.Source, r.ExternalID); ok {
			out = append(out, dto.PublicPersonLink{Source: publicSourceKey(r.Source), URL: url})
		}
	}
	return out, nil
}

// workEngines projects a work's engine attributions (A2-1e). Deterministic by
// name so two reads of the same work render the same order. Empty → [].
func (s *PublicService) workEngines(ctx context.Context, workID int64) ([]dto.PublicWorkEngine, error) {
	var rows []struct {
		ID   int64  `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT e.id, e.name FROM catalog_work_engine we
		JOIN catalog_engine e ON e.id = we.engine_id
		WHERE we.work_id = ? ORDER BY e.name, e.id`, workID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicWorkEngine, 0, len(rows))
	for _, r := range rows {
		out = append(out, dto.PublicWorkEngine{ID: r.ID, Name: r.Name})
	}
	return out, nil
}

// workLinks projects a work's NON-IDENTITY external links from its
// entity_type=work, link_kind=related refs (doc 126 D6) — the wiki's
// user-submitted link table as the retirement wave absorbed it: URL bytes only,
// platform-curated, no user attribution (the wiki's user_id was deliberately
// never carried across, so the "hide links by banned authors" rule has no input
// here and is gone by design, not by oversight).
//
// The exact/probable identity anchors are excluded at the query level — the
// same hard boundary labelLinks draws. Deterministic (source_id, external_id);
// empty → [].
func (s *PublicService) workLinks(ctx context.Context, workID int64) ([]dto.PublicWorkLink, error) {
	var rows []struct {
		Source     string `gorm:"column:source"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT src.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id = ? AND r.link_kind = ? AND r.dead_at IS NULL
		ORDER BY r.source_id, r.external_id`,
		model.EntityTypeWork, workID, model.LinkKindRelated).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicWorkLink, 0, len(rows))
	for _, r := range rows {
		if url, ok := relatedLinkURL(r.Source, r.ExternalID); ok {
			out = append(out, dto.PublicWorkLink{Source: publicSourceKey(r.Source), URL: url})
		}
	}
	return out, nil
}

// relatedLinkURL renders a link_kind=related external id to its absolute URL.
// It is the ONE template table of the public face: works, labels and persons
// all store their web presence in the same catalog_external_ref rows under the
// same source keys, so a per-face copy of these templates is not a variation
// point — it is a drift bug waiting to happen. (It was one: the label face
// carried three of these six templates and silently dropped every steam / pixiv
// / web row a label actually had.)
//
// The templates are the exact inverses of the parsers that mint these rows
// (internal/jobs/wikirescue/link.go for the work lane, the E2b org/label
// enrichment for the label lane), which is why they are safe to apply rather
// than guesses:
//
//	web            the external_id IS the full URL (that is the whole point of
//	               the `web` source — a link whose host we do not model)
//	official_site  a host+path with the scheme and trailing slash stripped
//	twitter        a bare lowercase handle
//	cien           the numeric ci-en creator id
//	steam          the numeric app id
//	pixiv          the numeric user id
//
// dlsite and dmm are deliberately ABSENT: their storefront URLs are
// section-dependent (dlsite maniax/home/pro/soft; dmm digital/dlsoft), and the
// registry stores only the bare code, so any single template would be a guess
// that 404s for part of the population. Those two anchors stay reachable as
// data through the source key; the face never invents an address for them. An
// unknown source is skipped for the same reason.
func relatedLinkURL(source, externalID string) (string, bool) {
	switch source {
	case "web":
		return externalID, true
	case "official_site":
		return "https://" + externalID, true
	case "twitter":
		return "https://x.com/" + externalID, true
	case "cien":
		return "https://ci-en.dlsite.com/creator/" + externalID, true
	case "steam":
		return "https://store.steampowered.com/app/" + externalID, true
	case "pixiv":
		return "https://www.pixiv.net/users/" + externalID, true
	default:
		return "", false
	}
}

// ─────────────────────────── shared brief helpers ───────────────────────────

type workBriefRow struct {
	ID            int64
	MediumID      int16
	DisplayName   string
	ContentRating int16
	Status        int16
	Site          *string
	ProductWorkID *int64
	ClaimState    *int16 `gorm:"column:claim_state"`
	// DisplayNSFW is the claimed work's editorial display flag — the display
	// axis's authority (A2-R5), read off the row itself since the W1-pre wave
	// nativized it (refs/proj/140 §5b).
	DisplayNSFW bool `gorm:"column:display_nsfw"`
}

// loadWorkBriefs loads public briefs for a set of work ids, keyed by id. r18
// works are OMITTED from the map (they must never surface, 裁定 6). Raw SQL
// bypasses GORM soft-delete, so deleted_at is filtered explicitly.
func (s *PublicService) loadWorkBriefs(ctx context.Context, ids []int64, nsfw bool) (map[int64]*dto.PublicWorkBrief, error) {
	if len(ids) == 0 {
		return map[int64]*dto.PublicWorkBrief{}, nil
	}
	var rows []workBriefRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT w.id, w.medium_id, w.display_name, w.content_rating, w.status, w.site, w.product_work_id, w.claim_state,
		       w.display_nsfw
		FROM catalog_work w
		WHERE w.id IN ? AND w.deleted_at IS NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]*dto.PublicWorkBrief, len(rows))
	for _, r := range rows {
		if !nsfw && isR18(r.ContentRating) {
			continue
		}
		out[r.ID] = &dto.PublicWorkBrief{
			ID: r.ID, Medium: s.mediumKey(r.MediumID), DisplayName: r.DisplayName,
			ContentRating: contentRatingKey(r.ContentRating),
			ClaimedBy:     claimedBy(r.Site, r.ProductWorkID, r.ClaimState, r.DisplayNSFW, r.ContentRating),
		}
	}
	return out, nil
}

// claimEnrich loads claimed_by pointers for the works in a name→works result.
func (s *PublicService) claimEnrich(ctx context.Context, works []NameWorkDetail) (map[int64]*dto.PublicClaimedBy, error) {
	ids := make([]int64, 0, len(works))
	for _, w := range works {
		ids = append(ids, w.Brief.WorkID)
	}
	return s.claimedByFor(ctx, ids)
}

// claimEnrichCharacter loads claimed_by pointers for the works in a
// character→works result.
func (s *PublicService) claimEnrichCharacter(ctx context.Context, works []CharacterWorkDetail) (map[int64]*dto.PublicClaimedBy, error) {
	ids := make([]int64, 0, len(works))
	for _, w := range works {
		ids = append(ids, w.Brief.WorkID)
	}
	return s.claimedByFor(ctx, ids)
}

// briefFromRow builds a public brief from an S2S WorkBriefRow (which lacks
// product_work_id) plus a supplementary claimed_by. Returns nil for an r18 work.
func (s *PublicService) briefFromRow(b WorkBriefRow, claim *dto.PublicClaimedBy, nsfw bool) *dto.PublicWorkBrief {
	if !nsfw && isR18(b.ContentRating) {
		return nil
	}
	return &dto.PublicWorkBrief{
		ID: b.WorkID, Medium: s.mediumKey(b.MediumID), DisplayName: b.DisplayName,
		ContentRating: contentRatingKey(b.ContentRating), ClaimedBy: claim,
	}
}

// claimedByFor batch-loads the claim pointers for a set of work ids (only claimed
// rows are returned). ONE query: the editorial flag that decides the display axis
// is a column on the row (it was a LEFT JOIN into the wiki body until the W1-pre
// wave nativized it, refs/proj/140 §5b).
func (s *PublicService) claimedByFor(ctx context.Context, ids []int64) (map[int64]*dto.PublicClaimedBy, error) {
	if len(ids) == 0 {
		return map[int64]*dto.PublicClaimedBy{}, nil
	}
	var rows []struct {
		ID            int64
		Site          string
		ProductWorkID int64
		ClaimState    *int16 `gorm:"column:claim_state"`
		ContentRating int16  `gorm:"column:content_rating"`
		// DisplayNSFW is the editorial display flag (see public_display_limit.go);
		// only a galgame_wiki claim ever carries true.
		DisplayNSFW bool `gorm:"column:display_nsfw"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT w.id, w.site, w.product_work_id, w.claim_state, w.content_rating, w.display_nsfw
		FROM catalog_work w
		WHERE w.id IN ? AND w.site IS NOT NULL AND w.product_work_id IS NOT NULL AND w.deleted_at IS NULL`,
		ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]*dto.PublicClaimedBy, len(rows))
	for _, r := range rows {
		// The query already pinned site / product_work_id non-NULL, so the shared
		// projection can only return one of the three claimed states here.
		out[r.ID] = &dto.PublicClaimedBy{
			Site: r.Site, WorkID: r.ProductWorkID,
			State:        model.ClaimStateKey(&r.Site, &r.ProductWorkID, r.ClaimState),
			ContentLimit: model.DisplayLimitKey(&r.Site, &r.ProductWorkID, r.DisplayNSFW, r.ContentRating),
		}
	}
	return out, nil
}

func (s *PublicService) mediumKey(id int16) string { return s.mediums[id] }

// ─────────────────────────── mappers ───────────────────────────

func isR18(r int16) bool { return r == model.ContentRatingR18 }

func contentRatingKey(r int16) string {
	switch r {
	case model.ContentRatingAllAges:
		return "all_ages"
	case model.ContentRatingSensitive:
		return "sensitive"
	case model.ContentRatingR18:
		return "r18"
	default:
		return "all_ages"
	}
}

// titleKindKey maps a title kind to its public string; ok=false for search_hint
// (findability-only, never displayed — dropped from the public face).
func titleKindKey(k int16) (string, bool) {
	switch k {
	case model.WorkTitleKindOfficial:
		return "official", true
	case model.WorkTitleKindAlias:
		return "alias", true
	case model.WorkTitleKindAbbreviation:
		return "abbreviation", true
	default:
		return "", false
	}
}

func labelKindKey(k int16) string {
	switch k {
	case model.LabelKindGameBrand:
		return "game_brand"
	case model.LabelKindBunko:
		return "bunko"
	case model.LabelKindPublisher:
		return "publisher"
	case model.LabelKindAnimeStudio:
		return "anime_studio"
	case model.LabelKindDoujinCircle:
		return "doujin_circle"
	case model.LabelKindGroup:
		return "group"
	default:
		return "other"
	}
}

func workLabelKindKey(k int16) string {
	switch k {
	case model.WorkLabelKindCircle:
		return "circle"
	case model.WorkLabelKindPublisher:
		return "publisher"
	case model.WorkLabelKindDeveloper:
		return "developer"
	case model.WorkLabelKindBrand:
		return "brand"
	default:
		return "other"
	}
}

// publicTitles projects the merged title rows (bridged for a claimed work,
// native for a bodyless one — read_titles.go), dropping search_hint titles.
func publicTitles(titles []WorkTitleRow) []dto.PublicCatalogTitle {
	out := make([]dto.PublicCatalogTitle, 0, len(titles))
	for _, t := range titles {
		kind, ok := titleKindKey(t.Kind)
		if !ok {
			continue
		}
		out = append(out, dto.PublicCatalogTitle{Lang: t.Lang, Title: t.Title, Latin: t.Latin, Kind: kind})
	}
	return out
}

// publicSourceKey maps a registry source key to its public wire key. The
// registry (and the wider codebase) spells the ErogameScape source
// "erogamespace" — a frozen internal key; the PUBLIC contract uses the site's
// real spelling, matching the galgame face's refs/attribution keys.
func publicSourceKey(registry string) string {
	if registry == "erogamespace" {
		return "erogamescape"
	}
	return registry
}

// registrySourceKey is the inverse for inbound lookup params: both spellings
// are accepted, the registry form is what the DB query needs.
func registrySourceKey(public string) string {
	if public == "erogamescape" {
		return "erogamespace"
	}
	return public
}

// publicRefs projects the exact-only ref list, deduplicating (source, external_id)
// across the work/release levels.
func publicRefs(refs []RefDetail) []dto.PublicCatalogRef {
	out := make([]dto.PublicCatalogRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		key := r.Source + "\x00" + r.ExternalID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, dto.PublicCatalogRef{Source: publicSourceKey(r.Source), ExternalID: r.ExternalID})
	}
	return out
}

// claimedBy builds the cross-face pointer from a work's site + product_work_id
// (both set = claimed) and stamps the claim's two axes: its visibility state
// (A2-1e / R7) and its editorial display limit (A2-R5).
//
// Both projections are the model package's — model.ClaimStateKey and
// model.DisplayLimitKey — the ONE definitions the works search index also bakes
// in, so `claim_state=live` and `content_limit=sfw` each select exactly the rows
// this function renders with that value.
//
// displayNSFW is the work's editorial display flag (catalog_work.display_nsfw, via
// loadDisplayNSFW or the row itself); false for a bodyless work, where the
// projection reads the age axis instead.
func claimedBy(site *string, productWorkID *int64, claimState *int16, displayNSFW bool, contentRating int16) *dto.PublicClaimedBy {
	state := model.ClaimStateKey(site, productWorkID, claimState)
	if state == model.ClaimStateKeyNone {
		return nil // unclaimed: claimed_by is null, not an object with a state
	}
	return &dto.PublicClaimedBy{
		Site: *site, WorkID: *productWorkID, State: state,
		ContentLimit: model.DisplayLimitKey(site, productWorkID, displayNSFW, contentRating),
	}
}

// earliestReleaseDate renders the earliest release's fuzzy date as a partial ISO
// string (YYYY / YYYY-MM / YYYY-MM-DD); nil when no release carries a year.
func earliestReleaseDate(releases []ReleaseDetail) *string {
	var bestY, bestM, bestD int16
	found := false
	for _, rd := range releases {
		r := rd.Release
		if r.ReleasedY == nil {
			continue
		}
		y := *r.ReleasedY
		m := derefI16Pub(r.ReleasedM)
		d := derefI16Pub(r.ReleasedD)
		if !found || y < bestY || (y == bestY && m < bestM) || (y == bestY && m == bestM && d < bestD) {
			bestY, bestM, bestD, found = y, m, d, true
		}
	}
	if !found {
		return nil
	}
	s := fmt.Sprintf("%04d", bestY)
	if bestM > 0 {
		s = fmt.Sprintf("%s-%02d", s, bestM)
		if bestD > 0 {
			s = fmt.Sprintf("%s-%02d", s, bestD)
		}
	}
	return &s
}

// nextOffset returns the next page offset when the underlying (unfiltered) query
// returned a full page, else nil. Paging over the unfiltered query means the r18
// display-filter never skips or duplicates a row.
func nextOffset(returned, limit, offset int) *int {
	if limit > 0 && returned == limit {
		n := offset + limit
		return &n
	}
	return nil
}

// normalizeExternalID canonicalizes an external id to its stored form. vndb ids
// are stored with the leading 'v' (galgame.vndb_id, `^v[0-9]+$`); accept input
// with or without it (裁定 3). Other sources pass through trimmed.
func normalizeExternalID(source, id string) string {
	id = strings.TrimSpace(id)
	if source == "vndb" && id != "" && !strings.HasPrefix(id, "v") {
		return "v" + id
	}
	return id
}

func derefStrPub(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefI16Pub(p *int16) int16 {
	if p == nil {
		return 0
	}
	return *p
}

func firstNonEmptyPub(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// characterTraits loads a character's public trait block (wave 104): links at
// or below the spoilers ceiling (0-2, default 0 = safe); sexual-family traits
// are served only with nsfw=1 (caller-controlled — the step-81 "catalog is
// r18-capable, consumers gate" doctrine made explicit at the API edge).
func (s *PublicService) characterTraits(ctx context.Context, characterID int64, spoilers int16, nsfw bool) ([]dto.PublicCharacterTrait, error) {
	var rows []struct {
		ID           int64
		Name         string
		NameZh       string `gorm:"column:name_zh"`
		GroupName    *string
		GroupNameZh  *string `gorm:"column:group_name_zh"`
		Sexual       bool
		SpoilerLevel int16
		Lie          bool
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT t.id, t.name, t.name_zh,
			g.name AS group_name, g.name_zh AS group_name_zh,
			t.sexual, l.spoiler_level, l.lie
		FROM catalog_character_trait_link l
		JOIN catalog_character_trait t ON t.id = l.trait_id
		LEFT JOIN catalog_character_trait g ON g.vndb_tid = t.group_tid
		WHERE l.character_id = ? AND l.spoiler_level <= ? AND (? OR NOT t.sexual)
		ORDER BY t.group_tid, t.gorder, t.name`, characterID, spoilers, nsfw).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicCharacterTrait, 0, len(rows))
	for _, r := range rows {
		out = append(out, dto.PublicCharacterTrait{
			ID: r.ID, Name: r.Name, NameZh: r.NameZh,
			Group: derefStrPub(r.GroupName), GroupZh: derefStrPub(r.GroupNameZh),
			Spoiler: r.SpoilerLevel, Sexual: r.Sexual, Lie: r.Lie,
		})
	}
	return out, nil
}

// characterIntros loads a character's multilingual intros, merged to one
// element per language (lowest source_id wins — the step-65 intro merge
// convention), lang ASC. Spoiler spans were stripped at import (entityintros).
// Empty → [].
func (s *PublicService) characterIntros(ctx context.Context, characterID int64) ([]dto.PublicCharacterIntro, error) {
	var rows []struct {
		Lang       string
		Intro      string
		SourceID   int16 `gorm:"column:source_id"`
		Provenance int16 `gorm:"column:provenance"`
	}
	// provenance ASC puts SOURCE rows (0) ahead of machine translations (1)
	// within a language — a source row always wins (the step-75 rule).
	if err := s.db.WithContext(ctx).Raw(`SELECT lang, intro, source_id, provenance
		FROM catalog_character_intro WHERE character_id = ?
		ORDER BY lang, provenance, source_id`, characterID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicCharacterIntro, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Lang] {
			continue
		}
		seen[r.Lang] = true
		out = append(out, dto.PublicCharacterIntro{Lang: r.Lang, Intro: r.Intro, Source: s.sourceKey(r.SourceID), Machine: r.Provenance == 1})
	}
	return out, nil
}

// nameAliases loads a credit name's alternate spellings from catalog_name_alias
// — the exact shape and query of labelAliases: the flat also-known-as list plus
// the wave-191 per-locale map. Empty → [] and {}.
//
// HEAD NAME ONLY, never the siblings'. A credit name is the alias owner
// (catalog_name_alias hangs off credit_name_id, the doc 10 invariant), and the
// face already lists the person's other public-linked credit names in
// siblings[] — each of which has its own /names/{id} record carrying its own
// aliases[]. Folding the siblings' aliases in here would attribute one
// identity's spellings to another and duplicate what the sibling record
// already answers.
//
// No link-visibility gate is needed: an alias is a spelling of THIS name, not
// an assertion about the person behind it, so it discloses nothing the head
// name does not already disclose.
//
// Search-hint rows (AliasKindSearchHint) are excluded, as in labelAliases.
func (s *PublicService) nameAliases(ctx context.Context, nameID int64) ([]string, map[string]dto.PublicLocalizedName, error) {
	rows, err := s.entityAliases(ctx, creditNameAliasSource, nameID)
	if err != nil {
		return nil, nil, err
	}
	return flatAliases(rows), localizedNames(rows), nil
}

// nameIntros assembles a credited identity's descriptions from two lanes,
// merged to one element per language.
//
// Lane 1 — the PERSON's own catalog_person_intro rows (personID > 0 only).
// These are catalog-native: a declared lang column and row-level provenance
// (0=source / 1=machine), the exact catalog_character_intro shape. personID is
// passed in ALREADY GATED by the caller — NameWorks nils the person link out
// when it is hidden, so a zero here means "no person to publish", never "look
// it up yourself". A biography rides the person_id gate for the same reason
// the photo and the birthday do (wave 172): it is a person fact, and shipping
// it under a withheld link would leak the association being withheld.
//
// Lane 2 — the wave-108 read-time bridge from the name's OWN bangumi anchor
// (src_bangumi.person.summary, lowest external_id wins when a name holds
// several). Per-name provenance: reading a name's anchored source is NOT a
// person-identity assertion (that resolution stays frozen); the E2 label-links
// doctrine applied to names. This lane is what an orphan name answers with,
// and orphans are the vast majority of credit names — it is not a fallback to
// be retired once persons fill in.
//
// Precedence per language: person source row, then bridge, then person machine
// translation. The person lane leads because it declares its language, where
// the bridge can only infer one from the script (introLang). Source beats
// machine across lanes — the step-75 rule.
//
// vndb staff descriptions are a documented follow-up lane (aid→sid mapping
// first). Empty → [].
func (s *PublicService) nameIntros(ctx context.Context, nameID, personID int64) ([]dto.PublicNameIntro, error) {
	var personRows []struct {
		Lang       string
		Intro      string
		SourceID   int16 `gorm:"column:source_id"`
		Provenance int16 `gorm:"column:provenance"`
	}
	if personID > 0 {
		// provenance ASC puts source rows ahead of machine translations within
		// a language, source_id ASC breaks the remaining ties — the step-65
		// merge convention characterIntros uses.
		if err := s.db.WithContext(ctx).Raw(`SELECT lang, intro, source_id, provenance
			FROM catalog_person_intro WHERE person_id = ?
			ORDER BY lang, provenance, source_id`, personID).Scan(&personRows).Error; err != nil {
			return nil, err
		}
	}

	var bridged []struct {
		Summary  string
		SourceID int16 `gorm:"column:source_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT p.summary, r.source_id
		FROM catalog_external_ref r
		JOIN catalog_source s ON s.id = r.source_id AND s.key = 'bangumi'
		JOIN src_bangumi.person p ON p.id::text = r.external_id
		WHERE r.entity_type = 1 AND r.entity_id = ? AND r.link_kind = 0
			AND coalesce(p.summary, '') <> ''
		ORDER BY r.external_id LIMIT 1`, nameID).Scan(&bridged).Error; err != nil {
		return nil, err
	}

	out := make([]dto.PublicNameIntro, 0, len(personRows)+len(bridged))
	seen := map[string]bool{}
	add := func(lang, intro, source string, machine bool) {
		if lang == "" || seen[lang] {
			return
		}
		seen[lang] = true
		out = append(out, dto.PublicNameIntro{Lang: lang, Intro: intro, Source: source, Machine: machine})
	}
	for _, r := range personRows {
		if r.Provenance == 0 {
			add(r.Lang, r.Intro, s.sourceKey(r.SourceID), false)
		}
	}
	for _, r := range bridged {
		add(introLang(r.Summary), r.Summary, s.sourceKey(r.SourceID), false)
	}
	for _, r := range personRows {
		if r.Provenance == 1 {
			add(r.Lang, r.Intro, s.sourceKey(r.SourceID), true)
		}
	}
	return out, nil
}

// introLang is the step-57 two-way heuristic (kana ⇒ ja, else zh-Hans) for
// bridged bangumi summaries, which carry no lang column.
func introLang(t string) string {
	for _, r := range t {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return "ja"
		}
	}
	return "zh-Hans"
}

// publicSeriesSiblings projects the transitive-closure series membership (wave
// 113) to public briefs, dropping r18 ends unless nsfw (same gate as relations).
// Always non-nil so the field serializes [].
func (s *PublicService) publicSeriesSiblings(sibs []SeriesSiblingRow, nsfw bool, limits map[int64]bool) []dto.PublicWorkBrief {
	out := make([]dto.PublicWorkBrief, 0, len(sibs))
	for _, sb := range sibs {
		if !nsfw && isR18(sb.ContentRating) {
			continue
		}
		out = append(out, dto.PublicWorkBrief{
			ID: sb.WorkID, Medium: s.mediumKey(sb.MediumID), DisplayName: sb.DisplayName,
			ContentRating: contentRatingKey(sb.ContentRating),
			ClaimedBy:     claimedBy(sb.Site, sb.ProductWorkID, sb.ClaimState, limits[sb.WorkID], sb.ContentRating),
		})
	}
	return out
}
