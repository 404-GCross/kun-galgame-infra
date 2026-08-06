package dto

import "time"

// Editing-engine S2S face DTOs (E0). BFF posture (doc 21 §2.7/§3): the
// product backend authenticates its user locally and ASSERTS the actor
// identity here over the Basic-authed S2S channel — exactly the community
// S2S convention. All body fields are written out explicitly (Huma
// anonymous-embed trap).

// EditActor is the asserted end-user identity a policy context is built
// from: roles feed the permission resolver, trust_tier the trusted rules,
// is_entity_owner the OwnerReview overlay capability (E3b).
type EditActor struct {
	UserID    int64    `json:"user_id" minimum:"1" doc:"Product-side user id (the shared identity space)"`
	Roles     []string `json:"roles,omitempty" doc:"The user's roles as the product's JWT asserts them"`
	TrustTier int16    `json:"trust_tier,omitempty" minimum:"0" maximum:"4" doc:"Trust tier TL0-TL4 (doc 18)"`
	// IsEntityOwner asserts that this user owns the entity the operation
	// targets (what ownership means is the product's business — e.g. the
	// galgame row's creator uid). Feeds OwnerReview-enabled policies only.
	IsEntityOwner bool `json:"is_entity_owner,omitempty" doc:"Product-asserted entity ownership (owner-review overlays)"`
}

// EditProposalCreateRequest files a proposal (or a direct edit — the engine
// automerges when every field's policy allows it).
type EditProposalCreateRequest struct {
	EntityType string         `json:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64          `json:"entity_id" minimum:"1"`
	Site       string         `json:"site" minLength:"1" doc:"Filing tenant; must equal the client's catalog_site binding"`
	Patch      map[string]any `json:"patch" doc:"Field-key → new-value document (registered keys only)"`
	Note       string         `json:"note,omitempty" maxLength:"2000"`
	Actor      EditActor      `json:"actor"`
}

// UserEditProposalCreateRequest is EditProposalCreateRequest as the USER-TOKEN
// face accepts it (wave 177): the same proposal, minus `actor` and minus `site`.
// Both are derived server-side from the verified token — the proposer from its
// `id` claim and its role union, the tenant from the token client's
// catalog_site binding — so there is deliberately no wire field for either.
// Written out explicitly rather than embedded (Huma anonymous-embed trap), and
// kept a separate type rather than a variant of the S2S one precisely so no
// future field can be added to both by accident.
type UserEditProposalCreateRequest struct {
	EntityType string         `json:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64          `json:"entity_id" minimum:"1"`
	Patch      map[string]any `json:"patch" doc:"Field-key → new-value document (registered keys only)"`
	Note       string         `json:"note,omitempty" maxLength:"2000"`
}

type EditProposalCreateResponse struct {
	Proposal EditProposalView  `json:"proposal"`
	Merged   bool              `json:"merged" doc:"true when the direct-edit sugar landed the patch immediately"`
	Revision *EditRevisionView `json:"revision,omitempty" doc:"The produced revision when merged"`
}

// EditAmendRequest appends a maintainer patch delta to an open proposal.
type EditAmendRequest struct {
	Set   map[string]any `json:"set,omitempty" doc:"Field-key → corrected value (change or add)"`
	Unset []string       `json:"unset,omitempty" doc:"Field keys to reject from the patch"`
	Note  string         `json:"note,omitempty" maxLength:"2000"`
	Actor EditActor      `json:"actor"`
}

// EditDecisionRequest merges or declines an open proposal.
type EditDecisionRequest struct {
	Note  string    `json:"note,omitempty" maxLength:"2000" doc:"Merge note / decline reason (kept on the proposal)"`
	Actor EditActor `json:"actor"`
}

// EditWithdrawRequest withdraws the actor's own open proposal.
type EditWithdrawRequest struct {
	Actor EditActor `json:"actor"`
}

// EditRevertRequest restores an entity's registered fields to a historical
// revision through the same merge path (a NEW revision; history untouched).
type EditRevertRequest struct {
	EntityType string    `json:"entity_type" minLength:"1"`
	EntityID   int64     `json:"entity_id" minimum:"1"`
	ToSeq      int       `json:"to_seq" minimum:"1" doc:"Target revision seq to restore"`
	Site       string    `json:"site" minLength:"1"`
	Note       string    `json:"note,omitempty" maxLength:"2000"`
	Actor      EditActor `json:"actor"`
}

// The user-token face's request shapes (wave 178). Each is its S2S sibling
// minus every identity field — `actor` everywhere, plus `site` on revert — for
// the same reason UserEditProposalCreateRequest is: on that face the uid comes
// from the token's `id` claim and the tenant from the token client's
// catalog_site, so a wire field for either would only be a place to lie. All
// fields are written out explicitly (Huma anonymous-embed trap) and each is a
// separate type rather than a variant of the S2S one, so no future field can be
// added to both by accident.

// UserEditAmendRequest appends a patch delta to an open proposal as the token's
// own user (requires the review rule on every touched field).
type UserEditAmendRequest struct {
	Set   map[string]any `json:"set,omitempty" doc:"Field-key → corrected value (change or add)"`
	Unset []string       `json:"unset,omitempty" doc:"Field keys to reject from the patch"`
	Note  string         `json:"note,omitempty" maxLength:"2000"`
}

// UserEditDecisionRequest merges or declines an open proposal as the token's
// own user. A note and nothing else — the decider is the token.
type UserEditDecisionRequest struct {
	Note string `json:"note,omitempty" maxLength:"2000" doc:"Merge note / decline reason (kept on the proposal)"`
}

// UserEditRevertRequest restores an entity's registered fields to a historical
// revision as the token's own user. No `site`: the tenant whose policy overlay
// applies is the token client's binding.
type UserEditRevertRequest struct {
	EntityType string `json:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `json:"entity_id" minimum:"1"`
	ToSeq      int    `json:"to_seq" minimum:"1" doc:"Target revision seq to restore"`
	Note       string `json:"note,omitempty" maxLength:"2000"`
}

// EditProposalView is the wire shape of a proposal. Status is a string
// (open/merged/declined/withdrawn) for the schema-driven UI.
type EditProposalView struct {
	ID              int64          `json:"id"`
	EntityFamily    string         `json:"entity_family"`
	EntityType      string         `json:"entity_type"`
	EntityID        int64          `json:"entity_id"`
	BaseRevisionSeq int            `json:"base_revision_seq"`
	Patch           map[string]any `json:"patch"`
	EffectivePatch  map[string]any `json:"effective_patch,omitempty" doc:"patch ⊕ amendments (detail endpoint only)"`
	ProposerUID     int64          `json:"proposer_uid"`
	Note            string         `json:"note"`
	Site            string         `json:"site"`
	Status          string         `json:"status"`
	DecidedByUID    *int64         `json:"decided_by_uid,omitempty"`
	DecidedAt       *time.Time     `json:"decided_at,omitempty"`
	DecisionNote    string         `json:"decision_note,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`

	Amendments []EditAmendmentView `json:"amendments,omitempty" doc:"Seq-ordered (detail endpoint only)"`
}

type EditAmendmentView struct {
	ID         int64          `json:"id"`
	Seq        int            `json:"seq"`
	Set        map[string]any `json:"set,omitempty"`
	Unset      []string       `json:"unset,omitempty"`
	AmenderUID int64          `json:"amender_uid"`
	Note       string         `json:"note"`
	CreatedAt  time.Time      `json:"created_at"`
}

type EditProposalListResponse struct {
	Items []EditProposalView `json:"items"`
	// Total is how many proposals the SAME filter matches, ignoring limit
	// (wave 162). A contribution statistic — "edits merged", a creator
	// eligibility threshold — is a number, and counting a capped page answers
	// 50 forever. Additive: pre-existing callers reading only items are
	// byte-identical.
	Total int64 `json:"total"`
}

// EditRevisionView is one row of the append-only revision log. Action is a
// string (created/merged/direct/reverted).
type EditRevisionView struct {
	ID            int64          `json:"id"`
	EntityFamily  string         `json:"entity_family"`
	EntityType    string         `json:"entity_type"`
	EntityID      int64          `json:"entity_id"`
	Seq           int            `json:"seq"`
	Action        string         `json:"action"`
	ChangedFields []string       `json:"changed_fields"`
	Snapshot      map[string]any `json:"snapshot"`
	ActorUID      int64          `json:"actor_uid"`
	AmenderUID    *int64         `json:"amender_uid,omitempty"`
	ProposalID    *int64         `json:"proposal_id,omitempty"`
	Site          string         `json:"site"`
	CreatedAt     time.Time      `json:"created_at"`

	// Migrated-history provenance (E2 transform; empty on new-era rows):
	// the original action word, the old-wire note, and the minor-edit flag.
	LegacyAction string `json:"legacy_action,omitempty" doc:"Original pre-engine action word (migrated rows only)"`
	LegacyNote   string `json:"legacy_note,omitempty" doc:"Old-wire revision note (migrated rows only)"`
	LegacyMinor  bool   `json:"legacy_minor,omitempty" doc:"Old-wire minor-edit flag (migrated rows only)"`
	// LegacyID is the migrated row's source galgame_revision id (the old
	// wire's row id; wire id = COALESCE(legacy_id, id)) — lets consumers
	// holding pre-engine revision-row ids resolve them to seqs (E3b).
	LegacyID *int64 `json:"legacy_id,omitempty" doc:"Source pre-engine revision row id (migrated rows only)"`
}

type EditRevisionListResponse struct {
	Items []EditRevisionView `json:"items"`
}

type EditRevertResponse struct {
	Proposal EditProposalView `json:"proposal"`
	Revision EditRevisionView `json:"revision"`
}

// EditFieldDiffView is one field's difference between two revisions, with
// the registry's rendering hints (empty for keys no longer registered).
type EditFieldDiffView struct {
	Key      string `json:"key"`
	Kind     string `json:"kind,omitempty"`
	DiffHint string `json:"diff_hint,omitempty"`
	From     any    `json:"from"`
	To       any    `json:"to"`
}

type EditDiffResponse struct {
	FromSeq int                 `json:"from_seq"`
	ToSeq   int                 `json:"to_seq"`
	Fields  []EditFieldDiffView `json:"fields"`
}

// EditSnapshotResponse is the entity's CURRENT registered-field values —
// the registry's LoadSnapshot view, keyed by eternal field keys (the BFF
// editor's bootstrap read; E3a).
type EditSnapshotResponse struct {
	EntityType string         `json:"entity_type"`
	EntityID   int64          `json:"entity_id"`
	Values     map[string]any `json:"values"`
}

// EditSchemaFieldView is one field of the edit-schema projection: shape +
// the CALLER's evaluated capabilities (the UI holds zero policy logic).
type EditSchemaFieldView struct {
	Key            string `json:"key"`
	Kind           string `json:"kind"`
	DiffHint       string `json:"diff_hint"`
	Deprecated     bool   `json:"deprecated,omitempty"`
	Locked         bool   `json:"locked"`
	CanPropose     bool   `json:"can_propose"`
	CanReview      bool   `json:"can_review"`
	WouldAutomerge bool   `json:"would_automerge" doc:"A proposal by this caller would merge instantly"`
}

type EditSchemaResponse struct {
	EntityType string                `json:"entity_type"`
	Fields     []EditSchemaFieldView `json:"fields"`
}

// EditConflictInfo is the 409 data payload of a rebase conflict: the fields
// changed since the proposal's base that no amendment re-adjudicated.
type EditConflictInfo struct {
	Conflicts []string `json:"conflicts"`
}
