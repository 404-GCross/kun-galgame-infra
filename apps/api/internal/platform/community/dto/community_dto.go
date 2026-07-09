// Package dto holds the wire types of the community S2S face (doc 11 §5.1 embed
// protocol). Bodies are named structs with explicit fields (Huma does not
// expand anonymous embeds); the tenant `site` is never on the wire — it is
// derived from the authenticated client's binding.
package dto

import "time"

// ThreadView is a thread as returned to embeds/aggregators.
type ThreadView struct {
	ID                int64      `json:"id"`
	Site              string     `json:"site"`
	Kind              int16      `json:"kind"`
	AnchorKind        int16      `json:"anchor_kind"`
	AnchorID          string     `json:"anchor_id"`
	Title             *string    `json:"title,omitempty"`
	HeaderImageHashes []string   `json:"header_image_hashes,omitempty" doc:"Header image hashes as accepted at topic creation; empty for none"`
	ContentRating     int16      `json:"content_rating"`
	Status            int16      `json:"status"`
	FbStatus          *int16     `json:"fb_status,omitempty"`
	FbResponse        *string    `json:"fb_response,omitempty"`
	AnswerPostID      *int64     `json:"answer_post_id,omitempty"`
	MergedIntoID      *int64     `json:"merged_into_id,omitempty"`
	PostsCount        int32      `json:"posts_count"`
	ParticipantsCount int32      `json:"participants_count"`
	HighestPostNumber int32      `json:"highest_post_number"`
	LastPostedAt      *time.Time `json:"last_posted_at,omitempty"`
	CreatedBy         int64      `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
}

// PostView is a post as returned to embeds. Both the raw markdown (for the
// editor) and the cooked HTML (for display) are included.
type PostView struct {
	ID            int64      `json:"id"`
	ThreadID      int64      `json:"thread_id"`
	PostNumber    int32      `json:"post_number"`
	RootPostID    *int64     `json:"root_post_id,omitempty"`
	ReplyToPostID *int64     `json:"reply_to_post_id,omitempty"`
	TargetUserID  *int64     `json:"target_user_id,omitempty"`
	AuthorID      int64      `json:"author_id"`
	ContentRaw    string     `json:"content_raw"`
	ContentHTML   string     `json:"content_html"`
	ContentRating int16      `json:"content_rating"`
	Status        int16      `json:"status"`
	EditedAt      *time.Time `json:"edited_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// --- requests --------------------------------------------------------------

// CommentsResolveRequest resolves (get-or-create) the single comments thread for
// an anchor and returns its first page of posts — the embed read-first-screen.
type CommentsResolveRequest struct {
	AnchorKind    int16  `json:"anchor_kind" doc:"0=board 1=site_game 2=site_resource 3=catalog_work 4=catalog_person"`
	AnchorID      string `json:"anchor_id"`
	ContentRating int16  `json:"content_rating" doc:"0=all 1=r15 2=r18 (inherited from the anchor)"`
}

// OpenTopicRequest opens a board topic (kind=0) with its opening post.
type OpenTopicRequest struct {
	AuthorID          int64    `json:"author_id"`
	AnchorID          string   `json:"anchor_id" doc:"board id"`
	Title             string   `json:"title"`
	ContentRating     int16    `json:"content_rating"`
	Body              string   `json:"body" doc:"markdown source of the opening post"`
	HeaderImageHashes []string `json:"header_image_hashes,omitempty"`
}

// OpenFeedbackRequest opens a feedback thread (kind=2) with its opening post.
type OpenFeedbackRequest struct {
	AuthorID      int64  `json:"author_id"`
	AnchorKind    int16  `json:"anchor_kind"`
	AnchorID      string `json:"anchor_id"`
	Title         string `json:"title"`
	ContentRating int16  `json:"content_rating"`
	Body          string `json:"body"`
}

// ReplyRequest appends a post to a thread.
type ReplyRequest struct {
	AuthorID      int64  `json:"author_id"`
	Body          string `json:"body"`
	RootPostID    *int64 `json:"root_post_id,omitempty"`
	ReplyToPostID *int64 `json:"reply_to_post_id,omitempty"`
	TargetUserID  *int64 `json:"target_user_id,omitempty"`
}

// EditPostRequest edits a post's body (author only). author_id is the acting
// user and must match the post's author; the body is re-sanitized on write and
// edited_at is stamped.
type EditPostRequest struct {
	AuthorID int64  `json:"author_id"`
	Body     string `json:"body" doc:"new markdown source; re-cooked + sanitized on write"`
}

// ReactionToggleRequest flips a user's reaction on a post.
type ReactionToggleRequest struct {
	UserID int64 `json:"user_id"`
	Kind   int16 `json:"kind" doc:"reaction kind (0=like)"`
}

// FlagRequest reports a post.
type FlagRequest struct {
	FlaggerID int64   `json:"flagger_id"`
	Reason    *int16  `json:"reason,omitempty"`
	Note      *string `json:"note,omitempty"`
}

// FeedbackStatusRequest sets a feedback thread's status + official response.
type FeedbackStatusRequest struct {
	FbStatus    int16   `json:"fb_status"`
	ResponderID int64   `json:"responder_id"`
	Response    *string `json:"response,omitempty"`
}

// FeedbackMergeRequest merges a duplicate feedback thread into another.
type FeedbackMergeRequest struct {
	IntoID int64 `json:"into_id"`
}

// --- responses -------------------------------------------------------------

// ThreadWithPosts is the read-first-screen / thread-detail payload.
type ThreadWithPosts struct {
	Thread     ThreadView `json:"thread"`
	Posts      []PostView `json:"posts"`
	NextCursor string     `json:"next_cursor,omitempty" doc:"post_number to pass as after for the next page; empty = last page"`
}

// ThreadListResponse is a keyset page of threads.
type ThreadListResponse struct {
	Threads    []ThreadView `json:"threads"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// PostListResponse is a keyset page of posts.
type PostListResponse struct {
	Posts      []PostView `json:"posts"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// ThreadResponse wraps a single thread (open topic/feedback, reply parent).
type ThreadResponse struct {
	Thread ThreadView `json:"thread"`
	Post   *PostView  `json:"post,omitempty" doc:"the opening post, for open-topic/feedback"`
}

// PostResponse wraps a single created post (reply).
type PostResponse struct {
	Post PostView `json:"post"`
}

// ReactionToggleResponse reports the post-toggle state.
type ReactionToggleResponse struct {
	Added bool `json:"added"`
}

// OKResponse is a bare acknowledgement for state-change endpoints.
type OKResponse struct {
	OK bool `json:"ok"`
}

// --- trust / moderation ----------------------------------------------------

// ActivityReceiptRequest is one batch of a user's reading-behavior activity a
// site BFF reports (deltas; likes are counted by the reaction flow, not here).
type ActivityReceiptRequest struct {
	UserID        int64 `json:"user_id"`
	TopicsEntered int32 `json:"topics_entered"`
	PostsRead     int32 `json:"posts_read"`
	ReadTimeS     int32 `json:"read_time_s"`
	DaysVisited   int32 `json:"days_visited"`
	// WindowActiveDays is the site-computed "days active in the last 100"; when
	// present it drives the TL3 promote/demote decision.
	WindowActiveDays *int32 `json:"window_active_days,omitempty"`
}

// SetBoostRequest declares a starter boost decided at the consuming site (it
// holds the IdP claims: account age / creator / staff).
type SetBoostRequest struct {
	UserID int64 `json:"user_id"`
	Boost  int16 `json:"boost" doc:"0=none 1=veteran 2=creator 3=staff"`
}

// TrustView is a user's trust state.
type TrustView struct {
	UserID                  int64  `json:"user_id"`
	Level                   int16  `json:"level"`
	TopicsEntered           *int32 `json:"topics_entered,omitempty"`
	PostsRead               *int32 `json:"posts_read,omitempty"`
	ReadTimeS               *int32 `json:"read_time_s,omitempty"`
	DaysVisited             *int32 `json:"days_visited,omitempty"`
	LikesGiven              *int32 `json:"likes_given,omitempty"`
	LikesReceived           *int32 `json:"likes_received,omitempty"`
	FlagsAgreed             *int32 `json:"flags_agreed,omitempty"`
	FlagsDisagreed          *int32 `json:"flags_disagreed,omitempty"`
	FirstPostsHeldRemaining int32  `json:"first_posts_held_remaining"`
	GrantedBoost            *int16 `json:"granted_boost,omitempty"`
}

// ReviewItemView is a moderation-queue item.
type ReviewItemView struct {
	ID        int64  `json:"id"`
	Site      string `json:"site,omitempty"`
	PostID    *int64 `json:"post_id,omitempty"`
	Source    *int16 `json:"source,omitempty" doc:"0=flags 1=first_post_hold 2=suspect_words 3=external"`
	Status    int16  `json:"status" doc:"0=pending 1=approved 2=rejected"`
	DecidedBy *int64 `json:"decided_by,omitempty"`
}

// ReviewListResponse is a page of pending queue items.
type ReviewListResponse struct {
	Items []ReviewItemView `json:"items"`
}

// ReviewDecisionRequest carries the operator id of an approve/reject.
type ReviewDecisionRequest struct {
	DecidedBy int64 `json:"decided_by"`
}
