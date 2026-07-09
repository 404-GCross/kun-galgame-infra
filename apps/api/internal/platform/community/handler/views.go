package handler

import (
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/community/dto"
	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
)

func toThreadView(t *model.CommunityThread) dto.ThreadView {
	return dto.ThreadView{
		ID: t.ID, Site: t.Site, Kind: t.Kind, AnchorKind: t.AnchorKind, AnchorID: t.AnchorID,
		Title: t.Title, ContentRating: t.ContentRating, Status: t.Status,
		FbStatus: t.FbStatus, FbResponse: t.FbResponse, AnswerPostID: t.AnswerPostID, MergedIntoID: t.MergedIntoID,
		PostsCount: t.PostsCount, ParticipantsCount: t.ParticipantsCount, HighestPostNumber: t.HighestPostNumber,
		LastPostedAt: t.LastPostedAt, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
	}
}

func toPostView(p *model.CommunityPost) dto.PostView {
	return dto.PostView{
		ID: p.ID, ThreadID: p.ThreadID, PostNumber: p.PostNumber,
		RootPostID: p.RootPostID, ReplyToPostID: p.ReplyToPostID, TargetUserID: p.TargetUserID,
		AuthorID: p.AuthorID, ContentRaw: p.ContentRaw, ContentHTML: p.ContentHTML,
		ContentRating: p.ContentRating, Status: p.Status, EditedAt: p.EditedAt, CreatedAt: p.CreatedAt,
	}
}

func toPostViews(posts []model.CommunityPost) []dto.PostView {
	out := make([]dto.PostView, len(posts))
	for i := range posts {
		out[i] = toPostView(&posts[i])
	}
	return out
}

func toThreadViews(threads []model.CommunityThread) []dto.ThreadView {
	out := make([]dto.ThreadView, len(threads))
	for i := range threads {
		out[i] = toThreadView(&threads[i])
	}
	return out
}

// --- thread-list keyset cursor (opaque base64 of the sort tuple) -----------
// Wire format: base64("<lastPostedUnixNano | 'n'>:<id>"); 'n' marks the NULL
// activity tail. Produced and parsed only here.

func encodeThreadCursor(t *model.CommunityThread) string {
	head := "n"
	if t.LastPostedAt != nil {
		head = strconv.FormatInt(t.LastPostedAt.UnixNano(), 10)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(head + ":" + strconv.FormatInt(t.ID, 10)))
}

func decodeThreadCursor(s string) (repository.ThreadCursor, error) {
	if s == "" {
		return repository.ThreadCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repository.ThreadCursor{}, err
	}
	head, idStr, ok := strings.Cut(string(raw), ":")
	if !ok {
		return repository.ThreadCursor{}, stderrors.New("bad cursor arity")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return repository.ThreadCursor{}, err
	}
	if head == "n" {
		return repository.ThreadCursor{LastPostedNull: true, ID: id}, nil
	}
	nano, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return repository.ThreadCursor{}, err
	}
	return repository.ThreadCursor{LastPosted: time.Unix(0, nano), ID: id}, nil
}

// postsPageCursor returns the "after post_number" cursor for the next page, or
// "" when the page was not full (last page). The post_number IS the cursor.
func postsPageCursor(posts []dto.PostView, limit int) string {
	if len(posts) < limit || len(posts) == 0 {
		return ""
	}
	return fmt.Sprintf("%d", posts[len(posts)-1].PostNumber)
}

// threadsPageCursor returns the opaque cursor for the next thread page, or "".
func threadsPageCursor(threads []model.CommunityThread, limit int) string {
	if len(threads) < limit || len(threads) == 0 {
		return ""
	}
	return encodeThreadCursor(&threads[len(threads)-1])
}
