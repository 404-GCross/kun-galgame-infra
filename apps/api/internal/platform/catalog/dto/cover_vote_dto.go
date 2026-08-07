package dto

// Wire types of the best-cover vote face (wave 175).
//
// A ballot has no request body at all: the voter is the bearer token's user and
// the tenant is that token client's catalog_site, so there is nothing left for a
// caller to state. The retired S2S pair took both from the wire.

// CoverVoteResponse is the tally after the write, so a caller re-renders without
// a second read.
type CoverVoteResponse struct {
	CoverID int64 `json:"cover_id"`
	// VoteCount is the cover's total AFTER this write. Advisory: it orders
	// nothing server-side.
	VoteCount int64 `json:"vote_count" doc:"the cover's advisory vote total after this write"`
	// Voted is this actor's resulting state on this cover (true after a vote,
	// false after a withdrawal).
	Voted bool `json:"voted" doc:"the acting user's resulting state on this cover"`
}

// UserWorkCover is one cover of a work as the USER-TOKEN plane's tally read
// returns it (wave 180). It is the projection of WorkCover a vote UI actually
// consumes — the address, the bytes, the count and this viewer's own state —
// and nothing else: sort_order, the portrait pin and the content flags belong
// to the work detail read, which a gallery has already loaded by the time it
// asks for tallies.
type UserWorkCover struct {
	ID        int64  `json:"id" doc:"catalog_work_cover row id (the vote endpoints' cover id)"`
	ImageHash string `json:"image_hash"`
	// VoteCount is advisory, exactly as on the work detail: it orders nothing
	// server-side and never touches the editorial pins.
	VoteCount int `json:"vote_count" doc:"advisory best-cover votes on this cover"`
	// Voted is stated here and nowhere else: WorkCover (the S2S work detail)
	// carries only the count, because it has no verified viewer to answer for.
	// On this face a viewer is never absent — the token IS the viewer.
	Voted bool `json:"voted" doc:"true if the token user's ballot is on this cover"`
}

// UserWorkCoversResponse is the tally read's payload: every cover of one work,
// in the work detail's order.
type UserWorkCoversResponse struct {
	WorkID int64           `json:"work_id"`
	Covers []UserWorkCover `json:"covers"`
}
