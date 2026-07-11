package dto

// Read-surface DTOs (step 18, D-01): the S2S read face for product-side
// consumers. Every field is explicitly named (no anonymous embeds — Huma drops
// them silently).

// --- by-anchor ---

// WorkByAnchorResponse is the full read-through of a work reached via one of
// its external anchors (work- or release-level).
type WorkByAnchorResponse struct {
	Work     WorkCore       `json:"work"`
	Titles   []WorkTitle    `json:"titles"`
	Releases []ReleaseBrief `json:"releases"`
	Labels   []WorkLabel    `json:"labels"`
	// Refs is the flat cross-source identity projection: every EXACT external
	// ref of this work, work-level and release-level in one list, for rendering
	// external links (DLsite/EG/…) and the cross-source identity chain.
	// probable/related tiers are deliberately excluded — they are review-lane
	// internal state and never cross the S2S face.
	Refs []WorkRef `json:"refs"`
}

// WorkRef is one exact external anchor of a work, flattened across the
// work/release levels. release-level refs carry the owning release id so a
// consumer can tie the identity to a specific SKU.
type WorkRef struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	Level      string `json:"level" doc:"work | release"`
	ReleaseID  int64  `json:"release_id,omitempty" doc:"present when level=release"`
}

// WorkCore is the work's identity + claim state.
type WorkCore struct {
	ID            int64  `json:"id"`
	MediumID      int16  `json:"medium_id"`
	DisplayName   string `json:"display_name"`
	OLang         string `json:"olang"`
	ContentRating int16  `json:"content_rating" doc:"0=all_ages 1=sensitive 2=r18"`
	Status        int16  `json:"status" doc:"0=live 1=stub 2=merged"`
	// Site is the claiming tenant key; empty = unclaimed (R2 registry row).
	Site          string `json:"site,omitempty"`
	ProductWorkID int64  `json:"product_work_id,omitempty"`
}

// WorkTitle is one title row (official / alias / abbreviation / search_hint).
type WorkTitle struct {
	Lang  string `json:"lang"`
	Title string `json:"title"`
	Latin string `json:"latin,omitempty"`
	Kind  int16  `json:"kind" doc:"0=official 1=alias 2=abbreviation 3=search_hint"`
}

// ReleaseBrief is one release (SKU) with its own anchors and fuzzy date.
type ReleaseBrief struct {
	ID        int64       `json:"id"`
	Kind      int16       `json:"kind" doc:"0=default 1=digital 2=physical 3=trial 4=patch"`
	ReleasedY int16       `json:"released_y,omitempty"`
	ReleasedM int16       `json:"released_m,omitempty"`
	ReleasedD int16       `json:"released_d,omitempty"`
	Anchors   []AnchorRef `json:"anchors"`
}

// AnchorRef is one external identity anchor. MatchedBy is the rule string that
// asserted it, surfaced verbatim for provenance.
type AnchorRef struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	LinkKind   int16  `json:"link_kind" doc:"0=exact 1=probable 2=related"`
	MatchedBy  string `json:"matched_by,omitempty"`
}

// WorkLabel is one attribution edge (organizational responsibility, distinct
// from an authorship credit).
type WorkLabel struct {
	LabelID     int64  `json:"label_id"`
	DisplayName string `json:"display_name"`
	LabelKind   int16  `json:"label_kind" doc:"the label's own kind (0=game_brand … 4=doujin_circle …)"`
	Kind        int16  `json:"kind" doc:"attribution nature: 0=circle 1=publisher 2=developer 3=brand"`
}

// --- credits ---

// WorkCreditsResponse is a work's credits grouped by role.
type WorkCreditsResponse struct {
	WorkID int64         `json:"work_id"`
	Groups []CreditGroup `json:"groups"`
}

// CreditGroup is all credits sharing one role.
type CreditGroup struct {
	RoleID   int64        `json:"role_id"`
	RoleKey  string       `json:"role_key"`
	RoleName string       `json:"role_name"`
	Credits  []CreditItem `json:"credits"`
}

// CreditItem is one credited name (orphan names appear as-is — no person layer).
type CreditItem struct {
	CreditNameID int64  `json:"credit_name_id"`
	Name         string `json:"name"`
	Lang         string `json:"lang"`
	Latin        string `json:"latin,omitempty"`
	CharacterID  int64  `json:"character_id,omitempty"`
	Character    string `json:"character,omitempty"`
	Note         string `json:"note,omitempty"`
	Source       string `json:"source,omitempty"`
}

// --- work title search (step 18: product-side upstream-first create flow) ---

// WorkSearchResponse is a page of title-search hits (lightweight brief).
type WorkSearchResponse struct {
	Items []WorkSearchHit `json:"items"`
}

// WorkSearchHit is one work matched by title, projected for the product-side
// create picker: identity + claim state + its first DLsite anchor (if any). It
// is deliberately light — the picker only needs to identify + annotate a hit;
// the full read-through bundle is fetched by-anchor/by-id on selection.
type WorkSearchHit struct {
	WorkID        int64  `json:"work_id"`
	DisplayName   string `json:"display_name"`
	MediumID      int16  `json:"medium_id"`
	ContentRating int16  `json:"content_rating" doc:"0=all_ages 1=sensitive 2=r18"`
	Status        int16  `json:"status" doc:"0=live 1=stub 2=merged"`
	// Site is the claiming tenant key; empty = unclaimed (an R2 registry row).
	Site string `json:"site,omitempty"`
	// DlsiteID is the work's first DLsite workno anchor (empty when it has none) —
	// the product side auto-fills its dlsite_id from this to seed reconciliation.
	DlsiteID string `json:"dlsite_id,omitempty"`
}

// --- entity search ---

// EntitySearchResponse is a page of entity hits.
type EntitySearchResponse struct {
	Items []EntitySearchHit `json:"items"`
	Total int64             `json:"total"`
}

// EntitySearchHit is one projected entity (name / character / label).
type EntitySearchHit struct {
	ID         string   `json:"id" doc:"prefixed: n{id} / c{id} / b{id}"`
	EntityType string   `json:"entity_type"`
	Name       string   `json:"name"`
	Latin      string   `json:"latin,omitempty"`
	Sources    []string `json:"sources"`
	Popularity float64  `json:"popularity"`
	Kind       *int16   `json:"kind,omitempty" doc:"label kind (labels only)"`
	PersonID   *int64   `json:"person_id,omitempty" doc:"credit-name → person link (names only; absent = orphan)"`
}
