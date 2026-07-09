package service

// Event kinds emitted at flow transitions (doc 11 §7). The service only EMITS;
// delivery + aggregation ("N people replied") is the notification layer's job.
const (
	EventPostCreated           = "post.created"
	EventReplyToYou            = "reply.to_you"
	EventMention               = "mention"
	EventFeedbackStatusChanged = "feedback.status_changed"
)

// Event is a domain event.
type Event struct {
	Kind     string
	ThreadID int64
	PostID   int64
	ActorID  int64
	TargetID int64 // recipient for reply.to_you / mention; 0 when N/A
}

// EventSink receives domain events. v0 delivery is a no-op (章程 ruling 2): the
// interface is stood up now so the flows have their emit points; the SSE/
// notification layer swaps in a real sink later without touching the services.
type EventSink interface{ Emit(Event) }

// NoopSink discards events — the v0 default.
type NoopSink struct{}

// Emit implements EventSink.
func (NoopSink) Emit(Event) {}
