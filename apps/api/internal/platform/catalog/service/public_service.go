package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
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

// Lookup resolves an external id to its catalog work via an EXACT anchor only
// (probable is an unverified hypothesis and never crosses the public face).
// found=false → 404 when the source is unknown, no exact anchor exists, or the
// resolved work is r18 (hidden). external_id is normalized per source (裁定 3).
func (s *PublicService) Lookup(ctx context.Context, source, externalID string, nsfw bool) (dto.PublicLookupData, bool, error) {
	brief, err := s.lookupBrief(ctx, source, externalID, nsfw)
	if err != nil {
		return dto.PublicLookupData{}, false, err
	}
	if brief == nil {
		return dto.PublicLookupData{}, false, nil
	}
	return dto.PublicLookupData{Work: brief, ClaimedBy: brief.ClaimedBy}, true, nil
}

// LookupBatch resolves up to len(pairs) external ids, preserving order; a miss /
// hidden work yields a null work in its slot (裁定 3). The caller caps the count.
func (s *PublicService) LookupBatch(ctx context.Context, pairs []dto.PublicLookupPair, nsfw bool) ([]dto.PublicLookupBatchItem, error) {
	out := make([]dto.PublicLookupBatchItem, 0, len(pairs))
	for _, p := range pairs {
		item := dto.PublicLookupBatchItem{Source: p.Source, ExternalID: p.ExternalID}
		brief, err := s.lookupBrief(ctx, p.Source, p.ExternalID, nsfw)
		if err != nil {
			return nil, err
		}
		if brief != nil {
			item.Work = brief
			item.ClaimedBy = brief.ClaimedBy
		}
		out = append(out, item)
	}
	return out, nil
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
func (s *PublicService) WorkDetail(ctx context.Context, id int64, inc PublicInclude, nsfw bool) (dto.PublicCatalogWork, bool, error) {
	detail, err := s.read.WorkByID(ctx, id)
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

	rec := dto.PublicCatalogWork{
		ID:            w.ID,
		Medium:        s.mediumKey(w.MediumID),
		DisplayName:   w.DisplayName,
		OLang:         w.OLang,
		ContentRating: contentRatingKey(w.ContentRating),
		ReleaseDate:   earliestReleaseDate(detail.Releases),
		Titles:        publicTitles(detail.Titles),
		Refs:          publicRefs(detail.Refs),
		ClaimedBy:     claimedBy(w.Site, w.ProductWorkID),
		Updated:       w.UpdatedAt.UTC().Format(time.RFC3339),
	}
	s.attachWorkFacets(&rec, detail)
	if inc.Relations {
		rec.Relations = s.publicRelations(detail.Relations, nsfw)
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
func (s *PublicService) publicRelations(rels []WorkRelationRow, nsfw bool) []dto.PublicRelation {
	out := make([]dto.PublicRelation, 0, len(rels))
	for _, r := range rels {
		if !nsfw && isR18(r.ContentRating) {
			continue
		}
		out = append(out, dto.PublicRelation{
			RelationType: r.Key, Phrase: r.Phrase,
			Work: dto.PublicWorkBrief{
				ID: r.OtherID, Medium: s.mediumKey(r.MediumID), DisplayName: r.DisplayName,
				ContentRating: contentRatingKey(r.ContentRating), ClaimedBy: claimedBy(r.Site, r.ProductWorkID),
			},
		})
	}
	return out
}

// attachWorkFacets projects every aggregation facet onto the public record
// (wave 104 全量开放): source keys not ids, CDN URLs not hashes, string
// vocabularies not enum ints. Every block is always present ([] when empty).
func (s *PublicService) attachWorkFacets(rec *dto.PublicCatalogWork, detail *WorkDetail) {
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
		pt := dto.PublicTag{Name: t.Name, Count: t.Count, Source: s.sourceKey(t.SourceID)}
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
	for _, c := range detail.Covers {
		url := s.imageURL(c.ImageHash)
		if url == "" {
			continue // never a bare hash on the public face
		}
		rec.Covers = append(rec.Covers, dto.PublicCover{
			URL: url, Kind: c.Kind, PortraitPinned: c.PortraitPinned,
			Sexual: c.Sexual, Violence: c.Violence, Source: s.sourceKey(c.SourceID),
		})
	}
	rec.Screenshots = make([]dto.PublicScreenshot, 0, len(detail.Screenshots))
	for _, sc := range detail.Screenshots {
		url := s.imageURL(sc.ImageHash)
		if url == "" {
			continue
		}
		rec.Screenshots = append(rec.Screenshots, dto.PublicScreenshot{
			URL: url, Caption: sc.Caption, Sexual: sc.Sexual, Violence: sc.Violence, Source: s.sourceKey(sc.SourceID),
		})
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
		for _, v := range ch.Va {
			pc.Voices = append(pc.Voices, dto.PublicRosterVoice{ID: v.CreditNameID, Name: v.Name})
		}
		rec.Characters = append(rec.Characters, pc)
	}
	rec.Labels = make([]dto.PublicWorkLabel, 0, len(detail.Labels))
	for _, l := range detail.Labels {
		rec.Labels = append(rec.Labels, dto.PublicWorkLabel{
			ID: l.LabelID, DisplayName: l.DisplayName,
			LabelKind: labelKindKey(l.LabelKind), Kind: workLabelKindKey(l.Kind),
		})
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
// consecutive rows by role and whitelisting fields (note / provenance / source
// stripped, 裁定 7).
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
		ID:       res.Head.ID,
		Name:     nameBuckets(res.Head.Lang, res.Head.Name),
		Latin:    derefStrPub(res.Head.Latin),
		Siblings: make([]dto.PublicSiblingName, 0, len(res.Siblings)),
	}
	if res.Head.PersonID != nil {
		p.PersonID = *res.Head.PersonID
	}
	for _, sib := range res.Siblings {
		p.Siblings = append(p.Siblings, dto.PublicSiblingName{
			ID: sib.ID, Name: nameBuckets(sib.Lang, sib.Name), Latin: derefStrPub(sib.Latin),
		})
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
		ID:    res.Head.ID,
		Name:  nameBuckets(res.Head.Lang, res.Head.DisplayName),
		Latin: derefStrPub(res.Head.Latin),
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
	var imgHash *string
	if err := s.db.WithContext(ctx).Raw(`SELECT image_hash FROM catalog_character WHERE id = ?`, id).Scan(&imgHash).Error; err != nil {
		return dto.PublicCharacter{}, false, err
	}
	if imgHash != nil {
		ch.Image = s.imageURL(*imgHash)
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
	l := dto.PublicLabel{ID: head.ID, DisplayName: head.DisplayName, Kind: labelKindKey(head.Kind)}
	// intros / links are part of the base record (not include-gated).
	if l.Intros, err = s.labelIntros(ctx, id); err != nil {
		return dto.PublicLabel{}, false, err
	}
	if l.Links, err = s.labelLinks(ctx, id); err != nil {
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
		Lang   string `gorm:"column:lang"`
		Intro  string `gorm:"column:intro"`
		Source string `gorm:"column:source"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT i.lang, i.intro, src.key AS source
		FROM catalog_label_intro i JOIN catalog_source src ON src.id = i.source_id
		WHERE i.label_id = ? ORDER BY i.lang, i.source_id`, labelID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicLabelIntro, 0, len(rows))
	seenLang := map[string]bool{}
	for _, r := range rows {
		if seenLang[r.Lang] {
			continue // a lower source_id already claimed this language
		}
		seenLang[r.Lang] = true
		out = append(out, dto.PublicLabelIntro{Lang: r.Lang, Intro: r.Intro, Source: r.Source})
	}
	return out, nil
}

// labelLinks projects a label's non-identity web-presence links from its
// entity_type=3, link_kind=related refs (refs/proj/89 ruling 3). The
// exact/probable identity anchors are excluded at the query level — identity
// anchors and web links never cross, a hard red line. Each external id is
// rendered to an absolute URL per source; an unknown related source (a future
// addition) has no template and is skipped, never guessed.
// Deterministic (source_id, external_id); empty → [].
func (s *PublicService) labelLinks(ctx context.Context, labelID int64) ([]dto.PublicLabelLink, error) {
	var rows []struct {
		Source     string `gorm:"column:source"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT src.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id = ? AND r.link_kind = ?
		ORDER BY r.source_id, r.external_id`,
		model.EntityTypeLabel, labelID, model.LinkKindRelated).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicLabelLink, 0, len(rows))
	for _, r := range rows {
		if url, ok := labelLinkURL(r.Source, r.ExternalID); ok {
			out = append(out, dto.PublicLabelLink{Source: r.Source, URL: url})
		}
	}
	return out, nil
}

// labelLinkURL renders a related-link external id to its absolute URL per
// source. E2b stores official_site scheme- and trailing-slash-stripped (so an
// https:// prefix is exact), twitter as a bare lowercase handle, and ci-en as
// the numeric creator id. An unknown source returns ok=false (no template — the
// URL is never guessed).
func labelLinkURL(source, externalID string) (string, bool) {
	switch source {
	case "official_site":
		return "https://" + externalID, true
	case "twitter":
		return "https://x.com/" + externalID, true
	case "cien":
		return "https://ci-en.dlsite.com/creator/" + externalID, true
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
		SELECT id, medium_id, display_name, content_rating, status, site, product_work_id
		FROM catalog_work WHERE id IN ? AND deleted_at IS NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]*dto.PublicWorkBrief, len(rows))
	for _, r := range rows {
		if !nsfw && isR18(r.ContentRating) {
			continue
		}
		out[r.ID] = &dto.PublicWorkBrief{
			ID: r.ID, Medium: s.mediumKey(r.MediumID), DisplayName: r.DisplayName,
			ContentRating: contentRatingKey(r.ContentRating), ClaimedBy: claimedBy(r.Site, r.ProductWorkID),
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
// rows are returned).
func (s *PublicService) claimedByFor(ctx context.Context, ids []int64) (map[int64]*dto.PublicClaimedBy, error) {
	if len(ids) == 0 {
		return map[int64]*dto.PublicClaimedBy{}, nil
	}
	var rows []struct {
		ID            int64
		Site          string
		ProductWorkID int64
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, site, product_work_id FROM catalog_work
		WHERE id IN ? AND site IS NOT NULL AND product_work_id IS NOT NULL AND deleted_at IS NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]*dto.PublicClaimedBy, len(rows))
	for _, r := range rows {
		out[r.ID] = &dto.PublicClaimedBy{Site: r.Site, WorkID: r.ProductWorkID}
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

// publicTitles projects the S2S title rows, dropping search_hint titles.
func publicTitles(titles []model.CatalogWorkTitle) []dto.PublicCatalogTitle {
	out := make([]dto.PublicCatalogTitle, 0, len(titles))
	for _, t := range titles {
		kind, ok := titleKindKey(t.Kind)
		if !ok {
			continue
		}
		pt := dto.PublicCatalogTitle{Lang: t.Lang, Title: t.Title, Kind: kind}
		if t.Latin != nil {
			pt.Latin = *t.Latin
		}
		out = append(out, pt)
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
// (both set = claimed).
func claimedBy(site *string, productWorkID *int64) *dto.PublicClaimedBy {
	if site == nil || *site == "" || productWorkID == nil {
		return nil
	}
	return &dto.PublicClaimedBy{Site: *site, WorkID: *productWorkID}
}

// nameBuckets places a name into its single language bucket by the row's lang
// (catalog imports default ” to Japanese).
func nameBuckets(lang, name string) dto.PublicNameBuckets {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return dto.PublicNameBuckets{Zh: name}
	case strings.HasPrefix(lang, "ja"), lang == "":
		return dto.PublicNameBuckets{Ja: name}
	default:
		return dto.PublicNameBuckets{Other: name}
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
		GroupName    *string
		Sexual       bool
		SpoilerLevel int16
		Lie          bool
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT t.id, t.name, g.name AS group_name,
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
			ID: r.ID, Name: r.Name, Group: derefStrPub(r.GroupName),
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
		Lang     string
		Intro    string
		SourceID int16 `gorm:"column:source_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT lang, intro, source_id
		FROM catalog_character_intro WHERE character_id = ?
		ORDER BY lang, source_id`, characterID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicCharacterIntro, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Lang] {
			continue
		}
		seen[r.Lang] = true
		out = append(out, dto.PublicCharacterIntro{Lang: r.Lang, Intro: r.Intro, Source: s.sourceKey(r.SourceID)})
	}
	return out, nil
}
