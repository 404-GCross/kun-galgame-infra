package handler

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

type UserReadServer struct{ read *service.ReadService }

func RegisterUserReadOps(api huma.API, read *service.ReadService) {
	s := &UserReadServer{read: read}
	tags := []string{"catalog-user"}

	huma.Register(api, huma.Operation{
		OperationID: "listCatalogWorkCoversUser", Method: http.MethodGet,
		Path:    UserPrefix + "/works/{id}/covers",
		Summary: "This work's covers with their advisory best-cover tallies, each carrying whether the BEARER TOKEN'S OWN USER voted for it. There is no uid parameter: the viewer is the token. Cross-site open like every catalog read — the tenant fence is a write-side rule",
		Tags:    tags,
	}, s.covers)
}

type userWorkCoversInput struct {
	ID int64 `path:"id" minimum:"1" doc:"Catalog work id"`
}

type userWorkCoversOutput struct {
	Body Envelope[dto.UserWorkCoversResponse]
}

func (s *UserReadServer) covers(ctx context.Context, in *userWorkCoversInput) (*userWorkCoversOutput, error) {
	uid, _, he := userActor(ctx)
	if he != nil {
		return nil, he
	}
	detail, err := s.read.WorkByID(ctx, in.ID, 0)
	if err != nil {
		if stderrors.Is(err, service.ErrWorkNotFound) {
			return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
		}
		slog.Error("catalog user covers", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}

	resp := dto.UserWorkCoversResponse{
		WorkID: in.ID, Covers: make([]dto.UserWorkCover, 0, len(detail.Covers)),
	}
	if len(detail.Covers) == 0 {
		return &userWorkCoversOutput{Body: okEnvelope(resp)}, nil
	}
	coverIDs := make([]int64, 0, len(detail.Covers))
	for _, cv := range detail.Covers {
		coverIDs = append(coverIDs, cv.ID)
	}
	votes, err := s.read.CoverVotes(ctx, coverIDs, uid)
	if err != nil {
		slog.Error("catalog user cover votes", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	for _, cv := range detail.Covers {
		tally := votes[cv.ID]
		resp.Covers = append(resp.Covers, dto.UserWorkCover{
			ID: cv.ID, ImageHash: cv.ImageHash, VoteCount: tally.Count, Voted: tally.Voted,
		})
	}
	return &userWorkCoversOutput{Body: okEnvelope(resp)}, nil
}
