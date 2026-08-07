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

// The user plane's one READ of the catalog itself (wave 180): a work's best-
// cover tallies, with the per-viewer `voted` flag.
//
// Wave 176 put the ballot here; the tally that ballot changes stayed on the S2S
// work detail, where the viewer is named in `?uid=` — an assertion, and the last
// place the cover-vote feature needed one. This op is that read with the uid
// removed: the viewer is the token's user, so there is no parameter to say
// otherwise and no way for a caller to ask "has SOMEONE ELSE voted for this".
//
// It is deliberately NOT tenant-fenced. Reads on this service are cross-site
// open (docs/catalog/01 §4.3) — one registry, one work, one set of covers — and
// a vote tally is public in the same sense the cover itself is. What the token
// adds is only the answer to "and did I vote", which is the caller's own fact.
//
// It reuses the S2S read face's path exactly: ReadService.WorkByID for the
// cover set, ReadService.CoverVotes for the batched tally. One query per read,
// never one per cover.

// UserReadServer holds the user plane's read dependency.
type UserReadServer struct{ read *service.ReadService }

// RegisterUserReadOps registers the user-plane read operations. Called by
// SetupUser for the runtime face and by cmd/gen-openapi on the catalog S2S spec
// API, so one contract document describes both planes. Safe with a nil service:
// spec export never invokes a handler.
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

	// Pre-sized non-nil so a work with no cover serializes `[]`, not `null`.
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
		// Absent from the map = nobody voted, which is a zero count — never a
		// reason to omit the cover.
		tally := votes[cv.ID]
		resp.Covers = append(resp.Covers, dto.UserWorkCover{
			ID: cv.ID, ImageHash: cv.ImageHash, VoteCount: tally.Count, Voted: tally.Voted,
		})
	}
	return &userWorkCoversOutput{Body: okEnvelope(resp)}, nil
}
