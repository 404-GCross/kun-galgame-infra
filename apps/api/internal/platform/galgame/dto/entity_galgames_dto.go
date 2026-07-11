package dto

// Entity reverse-lookup reads (step 20): an official / tag's self-description
// plus a page of the galgames under it — the "what else did this circle make /
// what else carries this tag" face. Modeled after the catalog reverse-lookup
// endpoints (labels/names/characters → works): the head makes the page
// self-sufficient on direct navigation; the galgames reuse the shared
// GalgameBrief (id + names + cover + content gate), so a consumer resolves each
// item to its own game / read-through page.

// EntityHead is a taxonomy entity's own identity (name + category + aliases),
// returned so the entity page renders its title without a second lookup.
type EntityHead struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases"`
}

// OfficialGalgamesResponse is an official's self-description + a page of its
// published galgames (offset-paginated, SFW-gated). Official is always present
// (404 on a missing id).
type OfficialGalgamesResponse struct {
	Official EntityHead     `json:"official"`
	Galgames []GalgameBrief `json:"galgames"`
	Total    int64          `json:"total"`
}

// TagGalgamesResponse is a tag's self-description + a page of the galgames that
// carry it (offset-paginated, SFW-gated). Tag is always present (404 on a miss).
type TagGalgamesResponse struct {
	Tag      EntityHead     `json:"tag"`
	Galgames []GalgameBrief `json:"galgames"`
	Total    int64          `json:"total"`
}
