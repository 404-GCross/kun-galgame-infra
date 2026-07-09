package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"

	"api/internal/platform/community/dto"
	"api/internal/platform/community/model"
	"api/internal/platform/community/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
	"gorm.io/datatypes"
)

const defaultPageLimit = 50

// Server holds the community S2S operation dependencies.
type Server struct {
	threads   *service.ThreadService
	posts     *service.PostService
	reactions *service.ReactionService
	feedback  *service.FeedbackService
	flags     *service.FlagService
}

// Setup builds the community S2S Huma API over the Fiber app. S2SAuth is applied
// by the caller as path-scoped Fiber middleware BEFORE this. Callable with nil
// services for spec export (handlers are never invoked then).
func Setup(app *fiber.App, threads *service.ThreadService, posts *service.PostService, reactions *service.ReactionService, feedback *service.FeedbackService, flags *service.FlagService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Community Service", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(S2SBridge)

	s := &Server{threads: threads, posts: posts, reactions: reactions, feedback: feedback, flags: flags}
	s.register(api)
	return api
}

func (s *Server) register(api huma.API) {
	read := []string{"community-read"}
	write := []string{"community-write"}

	huma.Register(api, huma.Operation{OperationID: "resolveComments", Method: http.MethodPost, Path: "/api/v1/community/comments/resolve",
		Summary: "Get-or-create the comments thread for an anchor and return its first page of posts", Tags: read}, s.resolveComments)
	huma.Register(api, huma.Operation{OperationID: "listThreads", Method: http.MethodGet, Path: "/api/v1/community/threads",
		Summary: "List a site's threads of a kind (keyset, newest activity first)", Tags: read}, s.listThreads)
	huma.Register(api, huma.Operation{OperationID: "getThread", Method: http.MethodGet, Path: "/api/v1/community/threads/{id}",
		Summary: "Get a thread with a page of posts", Tags: read}, s.getThread)
	huma.Register(api, huma.Operation{OperationID: "listPosts", Method: http.MethodGet, Path: "/api/v1/community/threads/{id}/posts",
		Summary: "List a thread's posts (keyset by post_number)", Tags: read}, s.listPosts)

	huma.Register(api, huma.Operation{OperationID: "openTopic", Method: http.MethodPost, Path: "/api/v1/community/topics",
		Summary: "Open a board topic with its opening post", Tags: write}, s.openTopic)
	huma.Register(api, huma.Operation{OperationID: "openFeedback", Method: http.MethodPost, Path: "/api/v1/community/feedback",
		Summary: "Open a feedback thread with its opening post", Tags: write}, s.openFeedback)
	huma.Register(api, huma.Operation{OperationID: "reply", Method: http.MethodPost, Path: "/api/v1/community/threads/{id}/posts",
		Summary: "Reply to a thread", Tags: write}, s.reply)
	huma.Register(api, huma.Operation{OperationID: "toggleReaction", Method: http.MethodPost, Path: "/api/v1/community/posts/{id}/reaction",
		Summary: "Toggle a reaction on a post", Tags: write}, s.toggleReaction)
	huma.Register(api, huma.Operation{OperationID: "submitFlag", Method: http.MethodPost, Path: "/api/v1/community/posts/{id}/flag",
		Summary: "Report a post", Tags: write}, s.submitFlag)
	huma.Register(api, huma.Operation{OperationID: "setFeedbackStatus", Method: http.MethodPost, Path: "/api/v1/community/feedback/{id}/status",
		Summary: "Set a feedback thread's status and official response", Tags: write}, s.setFeedbackStatus)
	huma.Register(api, huma.Operation{OperationID: "mergeFeedback", Method: http.MethodPost, Path: "/api/v1/community/feedback/{id}/merge",
		Summary: "Merge a duplicate feedback thread into another (reversible)", Tags: write}, s.mergeFeedback)
}

// --- read ------------------------------------------------------------------

type resolveCommentsInput struct{ Body dto.CommentsResolveRequest }
type threadWithPostsOutput struct {
	Body Envelope[dto.ThreadWithPosts]
}

func (s *Server) resolveComments(ctx context.Context, in *resolveCommentsInput) (*threadWithPostsOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	thread, err := s.threads.GetOrCreateCommentsThread(ctx, service.CommentsThreadParams{
		Site: site, AnchorKind: in.Body.AnchorKind, AnchorID: in.Body.AnchorID,
		ContentRating: in.Body.ContentRating, ActorID: 0,
	})
	if err != nil {
		return nil, mapErr("resolve comments", err)
	}
	posts, err := s.posts.ListPosts(thread.ID, 0, defaultPageLimit)
	if err != nil {
		return nil, mapErr("list posts", err)
	}
	views := toPostViews(posts)
	return &threadWithPostsOutput{Body: okEnvelope(dto.ThreadWithPosts{
		Thread: toThreadView(thread), Posts: views, NextCursor: postsPageCursor(views, defaultPageLimit),
	})}, nil
}

type listThreadsInput struct {
	Kind   int16  `query:"kind" doc:"0=topic 1=comments 2=feedback"`
	Cursor string `query:"cursor" doc:"opaque cursor from the previous page"`
	Limit  int    `query:"limit" doc:"page size (max 100, default 50)"`
}
type threadListOutput struct {
	Body Envelope[dto.ThreadListResponse]
}

func (s *Server) listThreads(ctx context.Context, in *listThreadsInput) (*threadListOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	cursor, err := decodeThreadCursor(in.Cursor)
	if err != nil {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "malformed cursor")
	}
	limit := clampLimit(in.Limit)
	threads, err := s.threads.ListBySite(site, in.Kind, cursor, limit)
	if err != nil {
		return nil, mapErr("list threads", err)
	}
	return &threadListOutput{Body: okEnvelope(dto.ThreadListResponse{
		Threads: toThreadViews(threads), NextCursor: threadsPageCursor(threads, limit),
	})}, nil
}

type threadPostsInput struct {
	ID    int64 `path:"id"`
	After int32 `query:"after" doc:"post_number to read after (0 = from the top)"`
	Limit int   `query:"limit"`
}

func (s *Server) getThread(ctx context.Context, in *threadPostsInput) (*threadWithPostsOutput, error) {
	thread, err := s.threads.Get(in.ID)
	if err != nil {
		return nil, mapErr("get thread", err)
	}
	if thread == nil {
		return nil, apiErr(http.StatusNotFound, errors.ErrNotFound)
	}
	limit := clampLimit(in.Limit)
	posts, err := s.posts.ListPosts(in.ID, in.After, limit)
	if err != nil {
		return nil, mapErr("list posts", err)
	}
	views := toPostViews(posts)
	return &threadWithPostsOutput{Body: okEnvelope(dto.ThreadWithPosts{
		Thread: toThreadView(thread), Posts: views, NextCursor: postsPageCursor(views, limit),
	})}, nil
}

type postListOutput struct {
	Body Envelope[dto.PostListResponse]
}

func (s *Server) listPosts(ctx context.Context, in *threadPostsInput) (*postListOutput, error) {
	limit := clampLimit(in.Limit)
	posts, err := s.posts.ListPosts(in.ID, in.After, limit)
	if err != nil {
		return nil, mapErr("list posts", err)
	}
	views := toPostViews(posts)
	return &postListOutput{Body: okEnvelope(dto.PostListResponse{
		Posts: views, NextCursor: postsPageCursor(views, limit),
	})}, nil
}

// --- write -----------------------------------------------------------------

type openTopicInput struct{ Body dto.OpenTopicRequest }
type threadOutput struct {
	Body Envelope[dto.ThreadResponse]
}

func (s *Server) openTopic(ctx context.Context, in *openTopicInput) (*threadOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	thread, post, err := s.threads.OpenTopic(ctx, service.OpenThreadParams{
		Site: site, AuthorID: in.Body.AuthorID, AnchorKind: model.AnchorKindBoard, AnchorID: in.Body.AnchorID,
		Title: in.Body.Title, ContentRating: in.Body.ContentRating, BodyRaw: in.Body.Body,
		HeaderImageHashes: hashesJSON(in.Body.HeaderImageHashes),
	})
	if err != nil {
		return nil, mapErr("open topic", err)
	}
	pv := toPostView(post)
	return &threadOutput{Body: okEnvelope(dto.ThreadResponse{Thread: toThreadView(thread), Post: &pv})}, nil
}

type openFeedbackInput struct{ Body dto.OpenFeedbackRequest }

func (s *Server) openFeedback(ctx context.Context, in *openFeedbackInput) (*threadOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	thread, post, err := s.threads.OpenFeedback(ctx, service.OpenThreadParams{
		Site: site, AuthorID: in.Body.AuthorID, AnchorKind: in.Body.AnchorKind, AnchorID: in.Body.AnchorID,
		Title: in.Body.Title, ContentRating: in.Body.ContentRating, BodyRaw: in.Body.Body,
	})
	if err != nil {
		return nil, mapErr("open feedback", err)
	}
	pv := toPostView(post)
	return &threadOutput{Body: okEnvelope(dto.ThreadResponse{Thread: toThreadView(thread), Post: &pv})}, nil
}

type replyInput struct {
	ID   int64 `path:"id"`
	Body dto.ReplyRequest
}
type postOutput struct {
	Body Envelope[dto.PostResponse]
}

func (s *Server) reply(ctx context.Context, in *replyInput) (*postOutput, error) {
	post, err := s.posts.Reply(ctx, service.ReplyParams{
		ThreadID: in.ID, AuthorID: in.Body.AuthorID, BodyRaw: in.Body.Body,
		RootPostID: in.Body.RootPostID, ReplyToPostID: in.Body.ReplyToPostID, TargetUserID: in.Body.TargetUserID,
	})
	if err != nil {
		return nil, mapErr("reply", err)
	}
	return &postOutput{Body: okEnvelope(dto.PostResponse{Post: toPostView(post)})}, nil
}

type toggleReactionInput struct {
	ID   int64 `path:"id"`
	Body dto.ReactionToggleRequest
}
type reactionOutput struct {
	Body Envelope[dto.ReactionToggleResponse]
}

func (s *Server) toggleReaction(ctx context.Context, in *toggleReactionInput) (*reactionOutput, error) {
	added, err := s.reactions.Toggle(ctx, in.ID, in.Body.UserID, in.Body.Kind)
	if err != nil {
		return nil, mapErr("toggle reaction", err)
	}
	return &reactionOutput{Body: okEnvelope(dto.ReactionToggleResponse{Added: added})}, nil
}

type flagInput struct {
	ID   int64 `path:"id"`
	Body dto.FlagRequest
}
type okOutput struct {
	Body Envelope[dto.OKResponse]
}

func (s *Server) submitFlag(ctx context.Context, in *flagInput) (*okOutput, error) {
	if err := s.flags.Submit(ctx, in.ID, in.Body.FlaggerID, in.Body.Reason, in.Body.Note); err != nil {
		return nil, mapErr("submit flag", err)
	}
	return &okOutput{Body: okEnvelope(dto.OKResponse{OK: true})}, nil
}

type feedbackStatusInput struct {
	ID   int64 `path:"id"`
	Body dto.FeedbackStatusRequest
}

func (s *Server) setFeedbackStatus(ctx context.Context, in *feedbackStatusInput) (*okOutput, error) {
	if err := s.feedback.SetStatus(ctx, in.ID, in.Body.FbStatus, in.Body.ResponderID, in.Body.Response); err != nil {
		return nil, mapErr("set feedback status", err)
	}
	return &okOutput{Body: okEnvelope(dto.OKResponse{OK: true})}, nil
}

type feedbackMergeInput struct {
	ID   int64 `path:"id"`
	Body dto.FeedbackMergeRequest
}

func (s *Server) mergeFeedback(ctx context.Context, in *feedbackMergeInput) (*okOutput, error) {
	if err := s.feedback.Merge(ctx, in.ID, in.Body.IntoID); err != nil {
		return nil, mapErr("merge feedback", err)
	}
	return &okOutput{Body: okEnvelope(dto.OKResponse{OK: true})}, nil
}

// --- helpers ---------------------------------------------------------------

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return defaultPageLimit
	}
	return limit
}

func hashesJSON(hashes []string) datatypes.JSON {
	if len(hashes) == 0 {
		return nil
	}
	b, err := json.Marshal(hashes)
	if err != nil {
		return nil
	}
	return datatypes.JSON(b)
}

// mapErr translates a service error into the house error envelope.
func mapErr(op string, err error) *houseError {
	var sandbox *service.SandboxError
	switch {
	case stderrors.As(err, &sandbox):
		return apiErrMsg(http.StatusTooManyRequests, errors.ErrOperationFailed, "sandbox limit: "+sandbox.Reason)
	case stderrors.Is(err, service.ErrThreadNotFound):
		return apiErr(http.StatusNotFound, errors.ErrNotFound)
	case stderrors.Is(err, service.ErrThreadNotOpen):
		return apiErrMsg(http.StatusConflict, errors.ErrOperationFailed, "thread is not open")
	case stderrors.Is(err, service.ErrNotFeedback):
		return apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "thread is not a feedback thread")
	default:
		slog.Error("community "+op, "err", err)
		return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
}
