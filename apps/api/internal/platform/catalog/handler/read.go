package handler

import (
	"context"
	stderrors "errors"
	"net/http"

	"api/internal/platform/catalog/dto"
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
		if stderrors.Is(err, service.ErrWorkNotFound) {
			return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
		}
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}

	resp := dto.WorkByAnchorResponse{
		Work: dto.WorkCore{
			ID: detail.Work.ID, MediumID: detail.Work.MediumID, DisplayName: detail.Work.DisplayName,
			OLang: detail.Work.OLang, ContentRating: detail.Work.ContentRating, Status: detail.Work.Status,
		},
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
			rb.Anchors = append(rb.Anchors, dto.AnchorRef{Source: a.Source, ExternalID: a.ExternalID, LinkKind: a.LinkKind})
		}
		resp.Releases = append(resp.Releases, rb)
	}
	for _, l := range detail.Labels {
		resp.Labels = append(resp.Labels, dto.WorkLabel{
			LabelID: l.LabelID, DisplayName: l.DisplayName, LabelKind: l.LabelKind, Kind: l.Kind,
		})
	}
	return &byAnchorOutput{Body: okEnvelope(resp)}, nil
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
	resp := dto.WorkCreditsResponse{WorkID: in.ID}
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

// --- small helpers ---

func derefI16(p *int16) int16 {
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
