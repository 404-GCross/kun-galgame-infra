package dto

// Wire types of the best-cover vote face (wave 175).
//
// The body is the SAME shape every other asserted-actor write on this service
// takes — `{"site": …, "actor": {"user_id": …}}` — because a vote is one more
// thing a product backend does on behalf of one of its users, not a new kind of
// caller. Reusing EditActor also means the uid a vote is attributed to is
// validated exactly like the uid an edit or a claim action is: the wire rejects
// a missing or non-positive user_id, and the service refuses one again (a vote
// is somebody's taste — it is never system-attributed).

// CoverVoteRequest casts or withdraws one best-cover vote.
type CoverVoteRequest struct {
	// Site is the acting tenant, enforced against the client's catalog_site
	// binding — a vote is a write, and a client writes only as itself. It is
	// provenance on the stored row, never part of the ballot's identity: the
	// same person voting from two sites still holds one vote per work.
	Site  string    `json:"site" minLength:"1" doc:"Acting tenant; must equal the client's catalog_site binding"`
	Actor EditActor `json:"actor" doc:"The end user the product backend is voting for"`
}

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
