package dto

import "time"

type EditActor struct {
	UserID        int64    `json:"user_id" minimum:"1" doc:"Product-side user id (the shared identity space)"`
	Roles         []string `json:"roles,omitempty" doc:"The user's roles as the product's JWT asserts them"`
	TrustTier     int16    `json:"trust_tier,omitempty" minimum:"0" maximum:"4" doc:"Trust tier TL0-TL4 (doc 18)"`
	IsEntityOwner bool     `json:"is_entity_owner,omitempty" doc:"Product-asserted entity ownership (owner-review overlays)"`
}

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

type UserEditAmendRequest struct {
	Set   map[string]any `json:"set,omitempty" doc:"Field-key → corrected value (change or add)"`
	Unset []string       `json:"unset,omitempty" doc:"Field keys to reject from the patch"`
	Note  string         `json:"note,omitempty" maxLength:"2000"`
}

type UserEditDecisionRequest struct {
	Note string `json:"note,omitempty" maxLength:"2000" doc:"Merge note / decline reason (kept on the proposal)"`
}

type UserEditRevertRequest struct {
	EntityType string `json:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `json:"entity_id" minimum:"1"`
	ToSeq      int    `json:"to_seq" minimum:"1" doc:"Target revision seq to restore"`
	Note       string `json:"note,omitempty" maxLength:"2000"`
}

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
	Total int64              `json:"total"`
}

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

	LegacyAction string `json:"legacy_action,omitempty" doc:"Original pre-engine action word (migrated rows only)"`
	LegacyNote   string `json:"legacy_note,omitempty" doc:"Old-wire revision note (migrated rows only)"`
	LegacyMinor  bool   `json:"legacy_minor,omitempty" doc:"Old-wire minor-edit flag (migrated rows only)"`
	LegacyID     *int64 `json:"legacy_id,omitempty" doc:"Source pre-engine revision row id (migrated rows only)"`
}

type EditRevisionListResponse struct {
	Items []EditRevisionView `json:"items"`
}

type EditRevertResponse struct {
	Proposal EditProposalView `json:"proposal"`
	Revision EditRevisionView `json:"revision"`
}

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

type EditSnapshotResponse struct {
	EntityType string         `json:"entity_type"`
	EntityID   int64          `json:"entity_id"`
	Values     map[string]any `json:"values"`
}

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

type EditConflictInfo struct {
	Conflicts []string `json:"conflicts"`
}
