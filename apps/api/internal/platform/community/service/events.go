package service

// Event kinds emitted at flow transitions (doc 11 §7). The service only EMITS;
// delivery + aggregation ("N people replied") is the notification layer's job.
const (
	EventPostCreated = "post.created"
	// EventPostEdited fires after a post's body is successfully rewritten
	// (PostService.Edit). It carries the same fields as post.created
	// (ThreadID/PostID/ActorID). The step-04 scanning sink consumes it to re-scan
	// the edited raw body; it is a no-op under NoopSink / the forwarding sink.
	EventPostEdited            = "post.edited"
	EventReplyToYou            = "reply.to_you"
	EventMention               = "mention"
	EventFeedbackStatusChanged = "feedback.status_changed"
	// EventFlagThreshold fires when a post's accumulated report weight crosses
	// the auto-hide threshold (doc 11 §7) — the notification layer surfaces it
	// to moderators.
	EventFlagThreshold = "flag.threshold"
	// EventReviewEnqueued / EventReviewApproved / EventReviewRejected are the
	// step-03 trust-forwarding signals: a review item was created (forward it to
	// the trust inbox) or a local moderator decided (resolve the forwarded item).
	// ReviewItemID carries the community_review_item id; the forwarding sink turns
	// these into off-request trust S2S calls (a no-op under NoopSink).
	EventReviewEnqueued = "review.enqueued"
	EventReviewApproved = "review.approved"
	EventReviewRejected = "review.rejected"
)

// Event is a domain event.
type Event struct {
	Kind         string
	ThreadID     int64
	PostID       int64
	ActorID      int64
	TargetID     int64 // recipient for reply.to_you / mention; 0 when N/A
	ReviewItemID int64 // community_review_item id for the review.* forwarding events
}

// EventSink receives domain events. v0 delivery is a no-op (章程 ruling 2): the
// interface is stood up now so the flows have their emit points; the SSE/
// notification layer swaps in a real sink later without touching the services.
type EventSink interface{ Emit(Event) }

// NoopSink discards events — the v0 default.
type NoopSink struct{}

// Emit implements EventSink.
func (NoopSink) Emit(Event) {}
