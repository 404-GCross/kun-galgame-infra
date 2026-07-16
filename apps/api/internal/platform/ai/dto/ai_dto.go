// Package dto holds the wire types of the AI-gateway S2S face. Bodies are named
// structs with explicit fields (Huma does not expand anonymous embeds, charter
// ruling 8); the tenant `site` is never on the wire — it is derived from the
// authenticated client's catalog_site binding.
package dto

// ModerateTextRequest is a moderate-text scan submission. site is NOT here (it
// is derived from the S2S client binding). This is an async scan surface — the
// caller has already published the content; moderate-text never gates a sync path.
type ModerateTextRequest struct {
	Text     string `json:"text" doc:"the UGC text to scan"`
	AuthorID *int64 `json:"author_id,omitempty" doc:"optional author global id (attribution/repeat-offender signal); NOT the tenant — the tenant is derived from the client binding"`
}

// ModerateTextResponse is the route verdict. On any degraded path (upstream
// unavailable / over budget / env empty) Flagged is false and Degraded is true
// — fail-open: the gateway never blocks on moderation.
type ModerateTextResponse struct {
	Route      string   `json:"route" doc:"the semantic route served (v0: moderate-text)"`
	Flagged    bool     `json:"flagged" doc:"true = upstream flagged the text; always false on a degraded (fail-open) path"`
	Categories []string `json:"categories,omitempty" doc:"upstream category labels when flagged"`
	Score      *float32 `json:"score,omitempty" doc:"upstream confidence in [0,1] when provided"`
	Channel    string   `json:"channel" doc:"the upstream channel/model that scored; '' when degraded"`
	Degraded   bool     `json:"degraded" doc:"true = upstream unavailable / over budget / env empty; result is fail-open (flagged:false)"`
}
