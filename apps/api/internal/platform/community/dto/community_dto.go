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
	// OpeningStatus / OpeningAuthorID project the opening post (post_number=1)
	// into the per-site thread LIST so an embed can hide an opening post that
	// must not leak its title: a held (TL0 first-post) opening post is visible
	// only to its author, a self-deleted (tombstoned) one is gone. The thread's
	// own status stays open in both cases, so the list cannot filter on it.
	// Populated only on the list read; nil elsewhere (a thread detail already
	// carries the opening post in its posts page). PostStatus* constants.
	OpeningStatus   *int16 `json:"opening_status,omitempty"`
	OpeningAuthorID *int64 `json:"opening_author_id,omitempty"`
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
	// EditedByModerator reports whether the LATEST edit was a mod-actor edit
	// (docs/proj/17 decision 3), so a site can label "edited (moderation)"
	// distinctly from an author self-edit.
	EditedByModerator bool      `json:"edited_by_moderator,omitempty" doc:"true when the latest edit was a mod-actor edit (as_moderator)"`
	CreatedAt         time.Time `json:"created_at"`
}

// --- requests --------------------------------------------------------------

// CommentsResolveRequest resolves (get-or-create) the single comments thread for
// an anchor and returns its first page of posts — the embed read-first-screen.
type CommentsResolveRequest struct {
	AnchorKind    int16  `json:"anchor_kind" doc:"0=board 1=site_game 2=site_resource 3=catalog_work 4=catalog_person"`
	AnchorID      string `json:"anchor_id"`
	ContentRating int16  `json:"content_rating" doc:"0=all 1=r15 2=r18 (inherited from the anchor)"`
}

// PostsResolveRequest hydrates a batch of posts by id — the forum like-tab read
// (it stores only community post ids and needs their content back). ids is capped
// at 100 (422 over) and de-duplicated preserving first-seen order.
type PostsResolveRequest struct {
	IDs []int64 `json:"ids" doc:"post ids to hydrate (max 100; deduped; only visible posts return)"`
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

// EditPostRequest edits a post's body. author_id is the acting user and must
// match the post's author unless as_moderator is set — the mod-actor variant
// (the calling site declares the actor is one of ITS moderators; community
// trusts the assertion like every other BFF-supplied identity and audit-logs the
// action). The body is re-sanitized on write and edited_at is stamped.
type EditPostRequest struct {
	AuthorID    int64  `json:"author_id" doc:"the acting user (the post author, or the moderator when as_moderator)"`
	Body        string `json:"body" doc:"new markdown source; re-cooked + sanitized on write"`
	AsModerator bool   `json:"as_moderator,omitempty" doc:"mod-actor variant: skip the author match; the site vouches author_id is its moderator"`
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

// PostThreadContext is the minimal render context of a post's thread, projected
// into the cross-thread by-author list so the consuming site can render a jump
// link (title for the label, anchor for the URL) without a per-post round trip.
// thread_id is the same value as the embedded post's, repeated for convenience.
type PostThreadContext struct {
	ThreadID   int64   `json:"thread_id"`
	Title      *string `json:"title,omitempty" doc:"thread title (NULL for a comments thread)"`
	AnchorKind int16   `json:"anchor_kind" doc:"0=board 1=site_game 2=site_resource 3=catalog_work 4=catalog_person"`
	AnchorID   string  `json:"anchor_id"`
}

// AuthorPostView is one of an author's posts plus its thread render context — the
// unit of the by-author profile list. The existing PostView is reused verbatim
// (nested under post); no new field semantics are invented.
type AuthorPostView struct {
	Post   PostView          `json:"post"`
	Thread PostThreadContext `json:"thread"`
}

// AuthorPostsResponse is a keyset page of an author's visible posts.
type AuthorPostsResponse struct {
	Posts      []AuthorPostView `json:"posts"`
	NextCursor string           `json:"next_cursor,omitempty" doc:"post id to pass as after for the next (older) page; empty = last page"`
}

// PostsResolveResponse is the batch by-id hydration result: the VISIBLE posts
// among the requested ids, each with its thread render context, in REQUEST ORDER
// (post-dedupe). Hidden/deleted/cross-site/unknown ids are silently absent — the
// caller diffs the request set against the returned set to render placeholders
// (no per-id status is leaked). The view is the same AuthorPostView reused by the
// by-author list; no new field semantics are invented.
type PostsResolveResponse struct {
	Posts []AuthorPostView `json:"posts"`
}

// AuthorStat is one author's visible-post count.
type AuthorStat struct {
	AuthorID     int64 `json:"author_id"`
	VisiblePosts int64 `json:"visible_posts"`
}

// AuthorStatsResponse is the batch visible-post count. There is exactly one entry
// per requested id (unknown ids report 0), in request order.
type AuthorStatsResponse struct {
	Stats []AuthorStat `json:"stats"`
}

// PurgeResponse reports the effect of a compliance purge. A second purge of the
// same author on the same site reports zero for both (idempotent).
type PurgeResponse struct {
	PostsPurged      int64 `json:"posts_purged" doc:"posts tombstoned + content-scrubbed this run"`
	ReactionsDeleted int64 `json:"reactions_deleted" doc:"reaction rows the author left that were deleted this run"`
}

// PostResponse wraps a single created post (reply).
type PostResponse struct {
	Post PostView `json:"post"`
}

// ReactionToggleResponse reports the post-toggle state plus the post's context
// (author + thread/anchor), which the reaction flow resolves anyway for the
// trust tallies. The context lets the consuming site fan out its like
// notification — recipient (author_id) and jump target (thread/anchor) — without
// a second round trip.
type ReactionToggleResponse struct {
	Added      bool   `json:"added"`
	AuthorID   int64  `json:"author_id" doc:"the post's author (the like-notification recipient)"`
	ThreadID   int64  `json:"thread_id"`
	AnchorKind int16  `json:"anchor_kind"`
	AnchorID   string `json:"anchor_id"`
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

// ReviewItemView is a moderation-queue item. ThreadID / AuthorID are projected
// from the subject post (list read) so the consuming site can deep-link the
// thread and resolve the author without a per-item round trip; absent when the
// item has no post.
type ReviewItemView struct {
	ID        int64  `json:"id"`
	Site      string `json:"site,omitempty"`
	PostID    *int64 `json:"post_id,omitempty"`
	ThreadID  *int64 `json:"thread_id,omitempty" doc:"the subject post's thread (deep-link target)"`
	AuthorID  *int64 `json:"author_id,omitempty" doc:"the subject post's author"`
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
