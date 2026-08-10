package handler

import (
	"context"
	"net/http"

	"api/internal/platform/trust/dto"
	"api/internal/platform/trust/model"
	"api/internal/platform/trust/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

func (s *AdminServer) registerPolicies(api huma.API) {
	tags := []string{"trust-admin"}
	const note = "Platform staff with trust.term_manage (admin/ren) only. A null override means the site inherits the platform default."

	huma.Register(api, huma.Operation{OperationID: "listTrustSitePolicies", Method: http.MethodGet, Path: "/api/v1/admin/trust/site-policies",
		Summary: "List per-site moderation postures, with the platform defaults they inherit from", Tags: tags,
		Description: note}, s.listSitePolicies)
	huma.Register(api, huma.Operation{OperationID: "upsertTrustSitePolicy", Method: http.MethodPut, Path: "/api/v1/admin/trust/site-policies/{site}",
		Summary: "Write a site's moderation posture wholesale (omitted fields are CLEARED back to the platform default)", Tags: tags,
		Description: note}, s.upsertSitePolicy)
}

func (s *AdminServer) requirePolicyManage(ctx context.Context) *houseError {
	if he := s.requireUnrestricted(ctx); he != nil {
		return he
	}
	return s.requireTermManage(ctx)
}

type sitePoliciesOutput struct {
	Body Envelope[dto.SitePoliciesResponse]
}

func (s *AdminServer) listSitePolicies(ctx context.Context, _ *struct{}) (*sitePoliciesOutput, error) {
	if he := s.requirePolicyManage(ctx); he != nil {
		return nil, he
	}
	rows, err := s.policies.List()
	if err != nil {
		return nil, mapAdminErr("list site policies", err)
	}
	d := s.policies.Defaults()
	return &sitePoliciesOutput{Body: okEnvelope(dto.SitePoliciesResponse{
		Policies: toSitePolicyViews(rows),
		Defaults: dto.PlatformDefaultsView{
			ScanMode:           d.ScanMode,
			SampleRate:         d.SampleRate,
			AggregateThreshold: d.AggregateThreshold,
			AutoHideEnabled:    d.AutoHideEnabled,
		},
	})}, nil
}

type upsertSitePolicyInput struct {
	Site string `path:"site" doc:"the tenant site key (kungal / moyu / letmoe …)"`
	Body dto.UpsertSitePolicyRequest
}
type sitePolicyOutput struct {
	Body Envelope[dto.SitePolicyView]
}

func (s *AdminServer) upsertSitePolicy(ctx context.Context, in *upsertSitePolicyInput) (*sitePolicyOutput, error) {
	if he := s.requirePolicyManage(ctx); he != nil {
		return nil, he
	}
	if in.Site == "" {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "site is required")
	}
	if in.Body.ScanMode != nil && *in.Body.ScanMode != model.ScanModeShadow && *in.Body.ScanMode != model.ScanModeLive {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "scan_mode must be 0 (shadow) or 1 (live)")
	}
	if in.Body.SampleRate != nil && (*in.Body.SampleRate < 0 || *in.Body.SampleRate > service.MaxScanSampleRate()) {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam,
			"sample_rate must be between 0 and the platform cap (human review is a fixed-capacity queue)")
	}

	row := &model.TrustSitePolicy{
		Site: in.Site, ScanMode: in.Body.ScanMode, SampleRate: in.Body.SampleRate,
		FlagThreshold: in.Body.FlagThreshold, AggregateThreshold: in.Body.AggregateThreshold,
		AutoHideEnabled: in.Body.AutoHideEnabled, Note: in.Body.Note,
	}
	if err := s.policies.Upsert(row); err != nil {
		return nil, mapAdminErr("upsert site policy", err)
	}
	return &sitePolicyOutput{Body: okEnvelope(toSitePolicyView(*row))}, nil
}

func toSitePolicyViews(rows []model.TrustSitePolicy) []dto.SitePolicyView {
	out := make([]dto.SitePolicyView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSitePolicyView(r))
	}
	return out
}

func toSitePolicyView(r model.TrustSitePolicy) dto.SitePolicyView {
	return dto.SitePolicyView{
		Site: r.Site, ScanMode: r.ScanMode, SampleRate: r.SampleRate,
		FlagThreshold: r.FlagThreshold, AggregateThreshold: r.AggregateThreshold,
		AutoHideEnabled: r.AutoHideEnabled, Note: r.Note,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
