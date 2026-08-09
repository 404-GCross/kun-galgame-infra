// playtime_dto.go — the wire shapes of the playtime report face.
//
// Status crosses the wire as a WORD, not as the int16 the column stores. A
// client author reading `"status": "finished"` in a log knows what happened;
// `"status": 1` sends them to a table in a document they have not read. The
// codec below is the only place the two spellings meet.
package dto

import "api/internal/platform/catalog/model"

// Playtime status words, the vocabulary the wire accepts and returns.
const (
	PlaytimeStatusWordPlaying  = "playing"
	PlaytimeStatusWordFinished = "finished"
	PlaytimeStatusWordDropped  = "dropped"
	PlaytimeStatusWordOnHold   = "on_hold"
)

// PlaytimeStatusFromWord maps a wire word onto the stored code. ok=false is an
// unknown word, which the face refuses rather than coercing to a default —
// silently filing "compleded" as `playing` would corrupt the one status the
// public aggregate reads.
func PlaytimeStatusFromWord(word string) (int16, bool) {
	switch word {
	case PlaytimeStatusWordPlaying:
		return model.PlaytimeStatusPlaying, true
	case PlaytimeStatusWordFinished:
		return model.PlaytimeStatusFinished, true
	case PlaytimeStatusWordDropped:
		return model.PlaytimeStatusDropped, true
	case PlaytimeStatusWordOnHold:
		return model.PlaytimeStatusOnHold, true
	}
	return 0, false
}

// PlaytimeStatusWord maps a stored code back onto its wire word. An
// unrecognized code renders as "playing" — the read side has no way to refuse,
// and the neutral word is the least misleading thing to say about a row whose
// status a future migration has not taught this build about yet.
func PlaytimeStatusWord(code int16) string {
	switch code {
	case model.PlaytimeStatusFinished:
		return PlaytimeStatusWordFinished
	case model.PlaytimeStatusDropped:
		return PlaytimeStatusWordDropped
	case model.PlaytimeStatusOnHold:
		return PlaytimeStatusWordOnHold
	}
	return PlaytimeStatusWordPlaying
}

// PlaytimeReportBody is what a client sends for one work.
type PlaytimeReportBody struct {
	// Minutes is the ABSOLUTE cumulative total this client has observed, not
	// a delta since the last call. Re-sending the same number changes nothing.
	Minutes int `json:"minutes" minimum:"0" maximum:"60000" doc:"Absolute cumulative playtime in MINUTES (never a delta). Ceiling 60000 (1000 hours)"`
	// Status defaults to playing when omitted: a client that only tracks a
	// running total is a legitimate client, and "playing" is the honest thing
	// to say about a total with no completion signal attached.
	Status string `json:"status,omitempty" enum:"playing,finished,dropped,on_hold" doc:"Defaults to playing. ONLY finished reports feed the public aggregate"`
	// LastPlayedAt is optional; omit it if the client does not track sessions.
	LastPlayedAt *string `json:"last_played_at,omitempty" format:"date-time" doc:"When this client last saw the game running (RFC 3339). Optional"`
}

// PlaytimeRecordResponse is one stored report as the face returns it.
type PlaytimeRecordResponse struct {
	WorkID       int64   `json:"work_id"`
	Minutes      int     `json:"minutes"`
	Status       string  `json:"status"`
	LastPlayedAt *string `json:"last_played_at"`
	// ClientID is the reporting application — present so a multi-app user's
	// sync leg can tell its own rows from its sibling's.
	ClientID  string `json:"client_id"`
	UpdatedAt string `json:"updated_at"`
	// ResolvedFrom is set ONLY on the by-ref write leg, echoing the external
	// id that resolved to this work (e.g. "vndb:v17"). A client should cache
	// the work_id it gets back and stop paying for the lookup.
	ResolvedFrom string `json:"resolved_from,omitempty"`
}

// PlaytimeMineResponse is the sync leg's page. Cursor is the last row's
// updated_at, to be handed straight back as ?updated_since= — null when the
// page is empty, which means "you are caught up".
type PlaytimeMineResponse struct {
	Items  []PlaytimeRecordResponse `json:"items"`
	Cursor *string                  `json:"cursor"`
}

// PlaytimeSelfResponse is the caller's own folded playtime on ONE work — the
// shape a rating form asks for. Null `playtime` means the user has never
// reported on this work (a 200, not a 404: having no playtime is an answer).
type PlaytimeSelfResponse struct {
	WorkID       int64   `json:"work_id"`
	Minutes      int     `json:"minutes"`
	Status       string  `json:"status"`
	LastPlayedAt *string `json:"last_played_at"`
	// Clients is how many of the user's applications reported here, so a UI
	// can say "from 2 apps" instead of implying a single authority.
	Clients int `json:"clients"`
}

// PlaytimeBatchBody is the library-sync write: a manager's whole shelf in one
// call. Items may address a work by id OR by external ref; exactly one of the
// two forms per item.
type PlaytimeBatchBody struct {
	Items []PlaytimeBatchItem `json:"items" minItems:"1" maxItems:"200" doc:"Up to 200 reports. Each item is accepted or rejected on its own — a bad item never fails the batch"`
}

// PlaytimeBatchItem is one line of a batch write.
type PlaytimeBatchItem struct {
	// WorkID addresses the work directly. Leave 0 to use the ref form.
	WorkID int64 `json:"work_id,omitempty" doc:"Catalog work id. Omit to address by source/external_id instead"`
	// Source + ExternalID address it by an anchor the client already holds
	// (vndb / dlsite / getchu / bangumi …). Only EXACT anchors resolve.
	Source     string `json:"source,omitempty" doc:"External source key (vndb, dlsite, getchu, bangumi, …). Use WITH external_id when work_id is omitted"`
	ExternalID string `json:"external_id,omitempty" doc:"The id this game carries on that source (e.g. v17, RJ01234)"`

	// The three value fields are spelled out rather than embedded: Huma does
	// not expand an anonymous embedded struct into the body schema, so an
	// embedded PlaytimeReportBody would compile, validate, and silently
	// publish a contract with no minutes field in it.
	Minutes      int     `json:"minutes" minimum:"0" maximum:"60000" doc:"Absolute cumulative playtime in MINUTES (never a delta)"`
	Status       string  `json:"status,omitempty" enum:"playing,finished,dropped,on_hold" doc:"Defaults to playing"`
	LastPlayedAt *string `json:"last_played_at,omitempty" format:"date-time" doc:"When this client last saw the game running (RFC 3339). Optional"`
}

// PlaytimeBatchResult is one line's outcome. Status is "ok", "not_found" (the
// work or the ref did not resolve) or "rejected" (the values were refused);
// Error carries the human detail for the latter two.
type PlaytimeBatchResult struct {
	Index  int    `json:"index"`
	Status string `json:"status" enum:"ok,not_found,rejected"`
	WorkID int64  `json:"work_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// PlaytimeBatchResponse reports per-item outcomes plus the two counts a client
// wants for its progress bar.
type PlaytimeBatchResponse struct {
	Accepted int                   `json:"accepted"`
	Refused  int                   `json:"refused"`
	Results  []PlaytimeBatchResult `json:"results"`
}
