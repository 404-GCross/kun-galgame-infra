package handler

import (
	"context"
	"net/http"

	"api/internal/platform/community/dto"
	"api/internal/platform/community/repository"
	"api/pkg/errors"
)

// Batch by-id post hydration (step 11: the infra face for forum's 06a like-tab
// cutover — the like table stores only community post ids and needs their content
// back). The read derives the tenant from the client's site binding and resolves
// through PostService, which enforces the site-scope at the repository layer — so
// a client bound to site A can never hydrate site B's posts (they resolve absent,
// indistinguishable from hidden/deleted/unknown; no per-id status leaks).

type resolvePostsInput struct{ Body dto.PostsResolveRequest }
type resolvePostsOutput struct {
	Body Envelope[dto.PostsResolveResponse]
}

func (s *Server) resolvePosts(ctx context.Context, in *resolvePostsInput) (*resolvePostsOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	ids, he := dedupePostIDs(in.Body.IDs)
	if he != nil {
		return nil, he
	}
	rows, err := s.posts.ResolvePosts(site, ids)
	if err != nil {
		return nil, mapErr("resolve posts", err)
	}
	return &resolvePostsOutput{Body: okEnvelope(dto.PostsResolveResponse{
		Posts: orderResolvedPosts(rows, ids),
	})}, nil
}

// dedupePostIDs enforces the batch cap (more than 100 ids -> 422, the raw request
// count as in parseAuthorIDs) and de-duplicates preserving first-seen order; an
// empty request stays an empty list (a no-op resolve). Unlike the stats query the
// ids arrive already typed (JSON []int64), so there is no malformed-id 400 path.
func dedupePostIDs(ids []int64) ([]int64, *houseError) {
	if len(ids) > 100 {
		return nil, apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, "too many ids (max 100)")
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// orderResolvedPosts re-projects the resolved rows into request order (post-
// dedupe): the DB returns the visible in-tenant subset in arbitrary order, so
// index it by post id and walk the requested ids, emitting only those that
// resolved. Ids that were hidden/deleted/cross-site/unknown are simply skipped
// (silently absent), so the caller diffs request-set vs response-set for
// placeholders.
func orderResolvedPosts(rows []repository.AuthorPostRow, ids []int64) []dto.AuthorPostView {
	byID := make(map[int64]*repository.AuthorPostRow, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	out := make([]dto.AuthorPostView, 0, len(rows))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			out = append(out, authorPostView(row))
		}
	}
	return out
}
