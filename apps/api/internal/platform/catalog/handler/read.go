package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// registerRead wires the S2S read surface (step 18): anchor read-through,
// credits-by-work, entity search. All Basic-authed like the other S2S ops; the
// read face imposes no site binding (step 16 semantics).
func (s *S2SServer) registerRead(api huma.API) {
	tags := []string{"catalog-s2s"}
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogWorkByAnchor", Method: http.MethodGet, Path: "/api/v1/catalog/works/by-anchor",
		Summary: "Read a work through one of its external anchors (work- or release-level)", Tags: tags,
	}, s.workByAnchor)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogWorkCredits", Method: http.MethodGet, Path: "/api/v1/catalog/works/{id}/credits",
		Summary: "List a work's credits grouped by role", Tags: tags,
	}, s.workCredits)
	huma.Register(api, huma.Operation{
		OperationID: "searchCatalogEntities", Method: http.MethodGet, Path: "/api/v1/catalog/search/entities",
		Summary: "Search catalog entities (credit names / characters / labels)", Tags: tags,
	}, s.searchEntities)
	huma.Register(api, huma.Operation{
		OperationID: "searchCatalogWorks", Method: http.MethodGet, Path: "/api/v1/catalog/works/search",
		Summary: "Search works by title (NFKC-folded substring) for the product-side create picker", Tags: tags,
	}, s.searchWorks)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogStats", Method: http.MethodGet, Path: "/api/v1/catalog/stats",
		Summary: "Dashboard counts for the internal data browser (one round-trip)", Tags: tags,
	}, s.getStats)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogWorkByID", Method: http.MethodGet, Path: "/api/v1/catalog/works/{id}",
		Summary: "Read a work by catalog id (same bundle as by-anchor)", Tags: tags,
	}, s.workByID)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogLabelWorks", Method: http.MethodGet, Path: "/api/v1/catalog/labels/{id}/works",
		Summary: "Works attributed to a label (circle→works reverse, paginated)", Tags: tags,
	}, s.labelWorks)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogNameWorks", Method: http.MethodGet, Path: "/api/v1/catalog/names/{id}/works",
		Summary: "Works a credited name worked on (name→works reverse; sibling names + roles)", Tags: tags,
	}, s.nameWorks)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogCharacterWorks", Method: http.MethodGet, Path: "/api/v1/catalog/characters/{id}/works",
		Summary: "Works a character appears in (roster edges ∪ voice credits) with kind + voice names", Tags: tags,
	}, s.characterWorks)
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogCharacterByID", Method: http.MethodGet, Path: "/api/v1/catalog/characters/{id}",
		Summary: "Read a character's identity + aliases by catalog id", Tags: tags,
	}, s.characterByID)
}

// ---- by-anchor ----

type byAnchorInput struct {
	Source     string `query:"source" minLength:"1" doc:"Source key (dlsite/vndb/bangumi/erogamespace/…), validated against the source registry"`
	ExternalID string `query:"external_id" minLength:"1" doc:"The id within that source (e.g. a DLsite RJ number, a VNDB v-id)"`
}

type byAnchorOutput struct {
	Body Envelope[dto.WorkByAnchorResponse]
}

func (s *S2SServer) workByAnchor(ctx context.Context, in *byAnchorInput) (*byAnchorOutput, error) {
	detail, err := s.read.WorkByAnchor(ctx, in.Source, in.ExternalID)
	if err != nil {
		return nil, workDetailErr(err)
	}
	return &byAnchorOutput{Body: okEnvelope(buildWorkResponse(detail))}, nil
}

// ---- work by id (internal browser drill-down; same bundle) ----

type workByIDInput struct {
	ID int64 `path:"id" doc:"Catalog work id"`
}

func (s *S2SServer) workByID(ctx context.Context, in *workByIDInput) (*byAnchorOutput, error) {
	detail, err := s.read.WorkByID(ctx, in.ID)
	if err != nil {
		return nil, workDetailErr(err)
	}
	return &byAnchorOutput{Body: okEnvelope(buildWorkResponse(detail))}, nil
}

func workDetailErr(err error) error {
	if stderrors.Is(err, service.ErrWorkNotFound) {
		return apiErr(http.StatusNotFound, errors.ErrNotFound)
	}
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

// buildWorkResponse maps a service WorkDetail to the wire DTO (shared by the
// by-anchor and by-id read-throughs).
func buildWorkResponse(detail *service.WorkDetail) dto.WorkByAnchorResponse {
	resp := dto.WorkByAnchorResponse{
		Work: dto.WorkCore{
			ID: detail.Work.ID, MediumID: detail.Work.MediumID, DisplayName: detail.Work.DisplayName,
			OLang: detail.Work.OLang, ContentRating: detail.Work.ContentRating, Status: detail.Work.Status,
		},
		// Pre-size to non-nil so an empty (bare / freshly-minted) work serializes
		// `[]` rather than `null` — a consumer that does `titles.length` on the
		// projection must never see a null slice (docs/proj/16 #3).
		Titles:     make([]dto.WorkTitle, 0, len(detail.Titles)),
		Releases:   make([]dto.ReleaseBrief, 0, len(detail.Releases)),
		Labels:     make([]dto.WorkLabel, 0, len(detail.Labels)),
		Refs:       make([]dto.WorkRef, 0, len(detail.Refs)),
		Characters: make([]dto.WorkCharacter, 0, len(detail.Characters)),
		// Intro pre-sized non-nil so a work with no intro (or a claimed work whose
		// galgame body has none — strict XOR) serializes `[]`, not `null`.
		Intro: make([]dto.WorkIntro, 0, len(detail.Intros)),
		// Covers pre-sized non-nil so a work with no cover (or a claimed work whose
		// galgame body has none — strict XOR) serializes `[]`, not `null`.
		Covers: make([]dto.WorkCover, 0, len(detail.Covers)),
		// Screenshots pre-sized non-nil so a work with no screenshot (or a claimed
		// work whose galgame body has none — strict XOR) serializes `[]`, not `null`.
		Screenshots: make([]dto.WorkScreenshot, 0, len(detail.Screenshots)),
	}
	if detail.Work.Site != nil {
		resp.Work.Site = *detail.Work.Site
	}
	if detail.Work.ProductWorkID != nil {
		resp.Work.ProductWorkID = *detail.Work.ProductWorkID
	}
	for _, t := range detail.Titles {
		wt := dto.WorkTitle{Lang: t.Lang, Title: t.Title, Kind: t.Kind}
		if t.Latin != nil {
			wt.Latin = *t.Latin
		}
		resp.Titles = append(resp.Titles, wt)
	}
	for _, rd := range detail.Releases {
		rb := dto.ReleaseBrief{ID: rd.Release.ID, Kind: rd.Release.Kind}
		rb.ReleasedY, rb.ReleasedM, rb.ReleasedD = derefI16(rd.Release.ReleasedY), derefI16(rd.Release.ReleasedM), derefI16(rd.Release.ReleasedD)
		for _, a := range rd.Anchors {
			rb.Anchors = append(rb.Anchors, dto.AnchorRef{Source: a.Source, ExternalID: a.ExternalID, LinkKind: a.LinkKind, MatchedBy: a.MatchedBy})
		}
		resp.Releases = append(resp.Releases, rb)
	}
	for _, l := range detail.Labels {
		resp.Labels = append(resp.Labels, dto.WorkLabel{
			LabelID: l.LabelID, DisplayName: l.DisplayName, LabelKind: l.LabelKind, Kind: l.Kind,
		})
	}
	for _, rf := range detail.Refs {
		wr := dto.WorkRef{Source: rf.Source, ExternalID: rf.ExternalID, Level: "work"}
		if rf.EntityType == model.EntityTypeRelease {
			wr.Level, wr.ReleaseID = "release", rf.ReleaseID
		}
		resp.Refs = append(resp.Refs, wr)
	}
	for _, c := range detail.Characters {
		wc := dto.WorkCharacter{
			CharacterID: c.CharacterID, DisplayName: c.DisplayName, Latin: derefStr(c.Latin),
			Gender: derefI16(c.Gender), Kind: c.Kind, Spoiler: c.Spoiler, ImageHash: derefStr(c.ImageHash),
			// va pre-sized non-nil so a roster-only character (no VA) serializes
			// `[]`, not `null` (docs/proj/16 #3).
			Va: make([]dto.WorkCharacterVA, 0, len(c.Va)),
		}
		for _, v := range c.Va {
			wc.Va = append(wc.Va, dto.WorkCharacterVA{CreditNameID: v.CreditNameID, Name: v.Name})
		}
		resp.Characters = append(resp.Characters, wc)
	}
	for _, in := range detail.Intros {
		resp.Intro = append(resp.Intro, dto.WorkIntro{Lang: in.Lang, Intro: in.Intro, SourceID: in.SourceID})
	}
	for _, cv := range detail.Covers {
		resp.Covers = append(resp.Covers, dto.WorkCover{
			ImageHash: cv.ImageHash, Kind: cv.Kind, PortraitPinned: cv.PortraitPinned,
			SortOrder: cv.SortOrder, Sexual: cv.Sexual, Violence: cv.Violence, SourceID: cv.SourceID,
		})
	}
	for _, sh := range detail.Screenshots {
		resp.Screenshots = append(resp.Screenshots, dto.WorkScreenshot{
			ImageHash: sh.ImageHash, Caption: sh.Caption,
			SortOrder: sh.SortOrder, Sexual: sh.Sexual, Violence: sh.Violence, SourceID: sh.SourceID,
		})
	}
	return resp
}

// ---- credits ----

type creditsInput struct {
	ID int64 `path:"id" doc:"Catalog work id"`
}

type creditsOutput struct {
	Body Envelope[dto.WorkCreditsResponse]
}

func (s *S2SServer) workCredits(ctx context.Context, in *creditsInput) (*creditsOutput, error) {
	rows, err := s.read.WorkCredits(ctx, in.ID)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	// Groups pre-sized to non-nil so a credit-less work serializes `[]`, not
	// `null` (docs/proj/16 #3).
	resp := dto.WorkCreditsResponse{WorkID: in.ID, Groups: make([]dto.CreditGroup, 0)}
	// rows are ordered by role_id; group consecutive rows.
	var cur *dto.CreditGroup
	for _, r := range rows {
		if cur == nil || cur.RoleID != r.RoleID {
			resp.Groups = append(resp.Groups, dto.CreditGroup{
				RoleID: r.RoleID, RoleKey: r.RoleKey, RoleName: firstNonEmpty(r.RoleNameCN, r.RoleNameJA, r.RoleKey),
			})
			cur = &resp.Groups[len(resp.Groups)-1]
		}
		item := dto.CreditItem{CreditNameID: r.CreditNameID, Name: r.Name, Lang: r.Lang, Note: r.Note}
		if r.Latin != nil {
			item.Latin = *r.Latin
		}
		if r.CharacterID != nil {
			item.CharacterID = *r.CharacterID
		}
		if r.CharacterNM != nil {
			item.Character = *r.CharacterNM
		}
		if r.SourceKey != nil {
			item.Source = *r.SourceKey
		}
		cur.Credits = append(cur.Credits, item)
	}
	return &creditsOutput{Body: okEnvelope(resp)}, nil
}

// ---- work title search (product-side create picker) ----

type searchWorksInput struct {
	Q string `query:"q" minLength:"1" doc:"Title search text (NFKC-folded substring match)"`
	// -1 = no filter (Huma cannot express optional scalars as pointers).
	MediumID int16 `query:"medium_id" default:"-1" doc:"Filter to one medium; -1 = all"`
	Limit    int   `query:"limit" default:"20" doc:"Max hits (capped at 50)"`
}

type searchWorksOutput struct {
	Body Envelope[dto.WorkSearchResponse]
}

func (s *S2SServer) searchWorks(ctx context.Context, in *searchWorksInput) (*searchWorksOutput, error) {
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	hits, err := s.read.SearchWorks(ctx, in.Q, in.MediumID, limit)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	// Pre-size non-nil so an empty result serializes `[]`, not `null`.
	resp := dto.WorkSearchResponse{Items: make([]dto.WorkSearchHit, 0, len(hits))}
	for _, h := range hits {
		resp.Items = append(resp.Items, dto.WorkSearchHit{
			WorkID: h.WorkID, DisplayName: h.DisplayName, MediumID: h.MediumID,
			ContentRating: h.ContentRating, Status: h.Status, Site: h.Site, DlsiteID: h.DlsiteID,
		})
	}
	return &searchWorksOutput{Body: okEnvelope(resp)}, nil
}

// ---- entity search ----

type searchInput struct {
	Q      string `query:"q" doc:"Search text; empty returns the most-credited entities"`
	Type   string `query:"type" enum:"names,characters,labels" doc:"Which entity index to search"`
	Locale string `query:"locale" enum:"zh,ja,en" doc:"UI locale; the server pins the query language (client-supplied Meili locales are never accepted)"`
	Limit  int    `query:"limit" default:"20" doc:"Max hits (capped at 20)"`
}

type searchOutput struct {
	Body Envelope[dto.EntitySearchResponse]
}

func (s *S2SServer) searchEntities(ctx context.Context, in *searchInput) (*searchOutput, error) {
	uid, ok := catsearch.IndexForType(in.Type)
	if !ok {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "type must be one of names|characters|labels")
	}
	limit := in.Limit
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	res, err := s.search.SearchEntities(ctx, uid, in.Q, catsearch.LocalesForUI(in.Locale), limit)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	resp := dto.EntitySearchResponse{Total: res.Total}
	for _, d := range res.Hits {
		resp.Items = append(resp.Items, dto.EntitySearchHit{
			ID: d.ID, EntityType: d.EntityType, Name: d.Name(), Latin: d.Latin,
			Sources: d.Sources, Popularity: d.Popularity, Kind: d.Kind, PersonID: d.PersonID,
		})
	}
	return &searchOutput{Body: okEnvelope(resp)}, nil
}

// ---- stats (dashboard) ----

type statsOutput struct {
	Body Envelope[dto.CatalogStats]
}

func (s *S2SServer) getStats(ctx context.Context, _ *struct{}) (*statsOutput, error) {
	o, err := s.stats.Overview(ctx)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	st := dto.CatalogStats{
		Works: dto.WorksMatrix{Total: o.WorksTotal},
		Entities: dto.EntityCounts{
			Persons: o.Entities.Persons, CreditNames: o.Entities.CreditNames,
			OrphanCreditNames: o.Entities.OrphanNames, Orgs: o.Entities.Orgs,
			Labels: o.Entities.Labels, Characters: o.Entities.Characters,
		},
		Queues: dto.QueueLevels{ProbableRefs: o.ProbableRefs, Rejections: o.Rejections},
	}
	for _, c := range o.WorksCells {
		st.Works.Cells = append(st.Works.Cells, dto.WorksCell{MediumID: c.MediumID, Claimed: c.Claimed, Status: c.Status, Count: c.Count})
	}
	for _, c := range o.Credits {
		st.CreditsBySource = append(st.CreditsBySource, dto.KeyCount{Key: c.Key, Count: c.Count})
	}
	for _, c := range o.Attributions {
		st.AttributionsByKind = append(st.AttributionsByKind, dto.KindCount{Kind: c.Kind, Count: c.Count})
	}
	for _, c := range o.Anchors {
		st.AnchorsBySourceTier = append(st.AnchorsBySourceTier, dto.AnchorTierCell{Source: c.Source, LinkKind: c.LinkKind, Count: c.Count})
	}
	for _, c := range o.Candidates {
		st.Queues.CandidatesByStatus = append(st.Queues.CandidatesByStatus, dto.StatusCount{Status: c.Status, Count: c.Count})
	}
	for _, c := range o.Proposals {
		st.Queues.ProposalsByStatus = append(st.Queues.ProposalsByStatus, dto.StatusCount{Status: c.Status, Count: c.Count})
	}
	for _, c := range o.LLMBid {
		st.LLMBidVerdicts = append(st.LLMBidVerdicts, dto.KeyCount{Key: c.Key, Count: c.Count})
	}
	for _, f := range o.Freshness {
		st.SourceFreshness = append(st.SourceFreshness, dto.SourceFreshness{Source: f.Source, LatestRef: f.LatestRef})
	}
	return &statsOutput{Body: okEnvelope(st)}, nil
}

// ---- label → works (attribution reverse) ----

type labelWorksInput struct {
	ID     int64 `path:"id" doc:"Catalog label id"`
	Limit  int   `query:"limit" default:"50" doc:"Page size (capped at 50)"`
	Offset int   `query:"offset" doc:"Rows to skip"`
}

type labelWorksOutput struct {
	Body Envelope[dto.LabelWorksResponse]
}

func (s *S2SServer) labelWorks(ctx context.Context, in *labelWorksInput) (*labelWorksOutput, error) {
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	head, items, total, err := s.read.LabelWorks(ctx, in.ID, limit, offset)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	// 404 on a missing label id, aligning with names/{id}/works and
	// characters/{id}/works (step 19 finding ②: this endpoint used to return
	// 200 + label:null). The head being nil is the sole miss signal.
	if head == nil {
		return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
	}
	resp := dto.LabelWorksResponse{Total: total}
	resp.Label = &dto.LabelHead{ID: head.ID, DisplayName: head.DisplayName, Kind: head.Kind}
	for _, w := range items {
		resp.Items = append(resp.Items, dto.LabelWorkRow{
			WorkID: w.WorkID, DisplayName: w.DisplayName, MediumID: w.MediumID,
			ContentRating: w.ContentRating, Status: w.Status, Kind: w.Kind,
		})
	}
	return &labelWorksOutput{Body: okEnvelope(resp)}, nil
}

// ---- name → works (entity reverse: what a credited name worked on) ----

type nameWorksInput struct {
	ID     int64 `path:"id" doc:"Catalog credit-name id"`
	Limit  int   `query:"limit" default:"50" doc:"Page size (capped at 50)"`
	Offset int   `query:"offset" doc:"Rows to skip"`
}

type nameWorksOutput struct {
	Body Envelope[dto.NameWorksResponse]
}

func (s *S2SServer) nameWorks(ctx context.Context, in *nameWorksInput) (*nameWorksOutput, error) {
	limit, offset := pageParams(in.Limit, in.Offset)
	res, err := s.read.NameWorks(ctx, in.ID, limit, offset)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	if res.Head == nil {
		return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
	}
	resp := dto.NameWorksResponse{
		Name: dto.NameHead{
			ID:    res.Head.ID,
			Name:  langBuckets(res.Head.Lang, res.Head.Name),
			Latin: derefStr(res.Head.Latin),
			// Siblings pre-sized non-nil so no person / no siblings serializes
			// `[]`, not `null` (docs/proj/16 #3).
			Siblings: make([]dto.SiblingName, 0, len(res.Siblings)),
		},
		Items: make([]dto.NameWorkRow, 0, len(res.Works)),
		Total: res.Total,
	}
	if res.Head.PersonID != nil {
		resp.Name.PersonID = *res.Head.PersonID
	}
	for _, sib := range res.Siblings {
		resp.Name.Siblings = append(resp.Name.Siblings, dto.SiblingName{
			ID: sib.ID, Name: langBuckets(sib.Lang, sib.Name), Latin: derefStr(sib.Latin),
		})
	}
	for _, w := range res.Works {
		row := dto.NameWorkRow{Work: workBriefDTO(w.Brief), Roles: make([]dto.NameWorkRole, 0, len(w.Roles))}
		for _, r := range w.Roles {
			nr := dto.NameWorkRole{
				RoleID: r.RoleID, RoleKey: r.RoleKey,
				RoleName: firstNonEmpty(r.RoleNameCN, r.RoleNameJA, r.RoleKey),
			}
			if r.CharacterID != nil {
				nr.CharacterID = *r.CharacterID
			}
			if r.CharacterNM != nil {
				nr.Character = *r.CharacterNM
			}
			row.Roles = append(row.Roles, nr)
		}
		resp.Items = append(resp.Items, row)
	}
	return &nameWorksOutput{Body: okEnvelope(resp)}, nil
}

// ---- character → works (entity reverse: what a character appears in) ----

type characterWorksInput struct {
	ID     int64 `path:"id" doc:"Catalog character id"`
	Limit  int   `query:"limit" default:"50" doc:"Page size (capped at 50)"`
	Offset int   `query:"offset" doc:"Rows to skip"`
}

type characterWorksOutput struct {
	Body Envelope[dto.CharacterWorksResponse]
}

func (s *S2SServer) characterWorks(ctx context.Context, in *characterWorksInput) (*characterWorksOutput, error) {
	limit, offset := pageParams(in.Limit, in.Offset)
	res, err := s.read.CharacterWorks(ctx, in.ID, limit, offset)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	if res.Head == nil {
		return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
	}
	resp := dto.CharacterWorksResponse{
		Character: dto.CharacterHead{
			ID: res.Head.ID, Name: langBuckets(res.Head.Lang, res.Head.DisplayName), Latin: derefStr(res.Head.Latin),
		},
		Items: make([]dto.CharacterWorkRow, 0, len(res.Works)),
		Total: res.Total,
	}
	for _, w := range res.Works {
		row := dto.CharacterWorkRow{
			Work: workBriefDTO(w.Brief), Kind: w.Kind, Spoiler: w.Spoiler, Voiced: w.Voiced,
			Voices: make([]dto.VoiceName, 0, len(w.Voices)),
		}
		for _, v := range w.Voices {
			row.Voices = append(row.Voices, dto.VoiceName{
				CreditNameID: v.CreditNameID, Name: v.Name, Lang: v.Lang, Latin: derefStr(v.Latin),
			})
		}
		resp.Items = append(resp.Items, row)
	}
	return &characterWorksOutput{Body: okEnvelope(resp)}, nil
}

// ---- character by id (entity detail: identity + aliases) ----

type characterByIDInput struct {
	ID int64 `path:"id" doc:"Catalog character id"`
}

type characterByIDOutput struct {
	Body Envelope[dto.CharacterDetailResponse]
}

func (s *S2SServer) characterByID(ctx context.Context, in *characterByIDInput) (*characterByIDOutput, error) {
	detail, err := s.read.CharacterByID(ctx, in.ID)
	if err != nil {
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	// 404 on a missing id, aligning with labels/{id} and the entity reverse
	// reads (step 20, 85f7f08); a nil detail is the sole miss signal.
	if detail == nil {
		return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
	}
	resp := dto.CharacterDetailResponse{
		ID: detail.ID, DisplayName: detail.DisplayName, Latin: derefStr(detail.Latin),
		Lang: detail.Lang, Gender: derefI16(detail.Gender), Description: detail.Description,
		InstanceOf: derefI64(detail.InstanceOf), ImageHash: derefStr(detail.ImageHash),
		// Aliases pre-sized non-nil so a character with no aliases serializes
		// `[]`, not `null` (docs/proj/16 #3).
		Aliases: make([]dto.CharacterAlias, 0, len(detail.Aliases)),
	}
	for _, a := range detail.Aliases {
		resp.Aliases = append(resp.Aliases, dto.CharacterAlias{
			ID: a.ID, Name: a.Name, Latin: derefStr(a.Latin), Lang: a.Lang,
			Kind: a.Kind, IsPrimaryForLocale: a.IsPrimaryForLocale,
		})
	}
	return &characterByIDOutput{Body: okEnvelope(resp)}, nil
}

// --- small helpers ---

// pageParams clamps offset-pagination inputs to the §2.7 read-face convention
// (cap 50, non-negative offset).
func pageParams(limit, offset int) (int, int) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// langBuckets places a name into its single language bucket by the row's lang
// (mirrors the search index's invariant 1: a name lives in exactly one of
// ja/zh/other; catalog imports default ” to Japanese).
func langBuckets(lang, name string) dto.NameBuckets {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return dto.NameBuckets{Zh: name}
	case strings.HasPrefix(lang, "ja"), lang == "":
		return dto.NameBuckets{Ja: name}
	default:
		return dto.NameBuckets{Other: name}
	}
}

func workBriefDTO(b service.WorkBriefRow) dto.WorkBrief {
	return dto.WorkBrief{
		WorkID: b.WorkID, DisplayName: b.DisplayName, MediumID: b.MediumID,
		ContentRating: b.ContentRating, Status: b.Status, Site: b.Site,
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefI16(p *int16) int16 {
	if p == nil {
		return 0
	}
	return *p
}

func derefI64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
