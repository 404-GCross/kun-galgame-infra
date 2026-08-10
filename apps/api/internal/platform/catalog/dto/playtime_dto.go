package dto

import "api/internal/platform/catalog/model"

const (
	PlaytimeStatusWordPlaying  = "playing"
	PlaytimeStatusWordFinished = "finished"
	PlaytimeStatusWordDropped  = "dropped"
	PlaytimeStatusWordOnHold   = "on_hold"
)

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

type PlaytimeReportBody struct {
	Minutes      int     `json:"minutes" minimum:"0" maximum:"60000" doc:"Absolute cumulative playtime in MINUTES (never a delta). Ceiling 60000 (1000 hours)"`
	Status       string  `json:"status,omitempty" enum:"playing,finished,dropped,on_hold" doc:"Defaults to playing. ONLY finished reports feed the public aggregate"`
	LastPlayedAt *string `json:"last_played_at,omitempty" format:"date-time" doc:"When this client last saw the game running (RFC 3339). Optional"`
}

type PlaytimeRecordResponse struct {
	WorkID       int64   `json:"work_id"`
	Minutes      int     `json:"minutes"`
	Status       string  `json:"status"`
	LastPlayedAt *string `json:"last_played_at"`
	ClientID     string  `json:"client_id"`
	UpdatedAt    string  `json:"updated_at"`
	ResolvedFrom string  `json:"resolved_from,omitempty"`
}

type PlaytimeMineResponse struct {
	Items  []PlaytimeRecordResponse `json:"items"`
	Cursor *string                  `json:"cursor"`
}

type PlaytimeSelfResponse struct {
	WorkID       int64   `json:"work_id"`
	Minutes      int     `json:"minutes"`
	Status       string  `json:"status"`
	LastPlayedAt *string `json:"last_played_at"`
	Clients      int     `json:"clients"`
}

type PlaytimeBatchBody struct {
	Items []PlaytimeBatchItem `json:"items" minItems:"1" maxItems:"200" doc:"Up to 200 reports. Each item is accepted or rejected on its own — a bad item never fails the batch"`
}

type PlaytimeBatchItem struct {
	WorkID     int64  `json:"work_id,omitempty" doc:"Catalog work id. Omit to address by source/external_id instead"`
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

type PlaytimeBatchResult struct {
	Index  int    `json:"index"`
	Status string `json:"status" enum:"ok,not_found,rejected"`
	WorkID int64  `json:"work_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

type PlaytimeBatchResponse struct {
	Accepted int                   `json:"accepted"`
	Refused  int                   `json:"refused"`
	Results  []PlaytimeBatchResult `json:"results"`
}
