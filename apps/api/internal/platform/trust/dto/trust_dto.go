// Package dto holds the wire types of the trust service faces (doc 18 §5).
// Bodies are named structs with explicit fields (Huma does not expand anonymous
// embeds); the tenant `site` is never on the S2S wire — it is derived from the
// authenticated client's binding.
package dto

import "time"

// Page is a generic offset page (mirrors the catalog admin convention).
type Page[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

// --- S2S intake ------------------------------------------------------------

// ReportRequest is a report submission (site derived from the client binding).
type ReportRequest struct {
	SubjectKind string  `json:"subject_kind" doc:"registered subject kind for this site (e.g. forum_topic)"`
	SubjectID   string  `json:"subject_id" doc:"the subject's stable id in the product"`
	ReasonKey   string  `json:"reason_key" doc:"a global or per-site reason key (abuse/spam/…)"`
	Note        *string `json:"note,omitempty"`
	Snapshot    *string `json:"snapshot,omitempty" doc:"reporter-carried content snapshot at report time"`
	ReporterID  int64   `json:"reporter_id" doc:"the reporting user's global id (P0 requires login)"`
}

// ReportResponse is the intake outcome.
type ReportResponse struct {
	ReportID     int64  `json:"report_id"`
	ReviewItemID *int64 `json:"review_item_id,omitempty" doc:"set when the report opened/linked/folded a review item"`
}

// SubjectKindsResponse lists a site's registered kinds (S2S sanity check).
type SubjectKindsResponse struct {
	Kinds []SubjectKindView `json:"kinds"`
}

// --- views -----------------------------------------------------------------

// SubjectKindView is a subject-kind registry row. The secret is never serialized;
// HasCallback reports whether a callback endpoint is configured.
type SubjectKindView struct {
	ID           int64     `json:"id"`
	Site         string    `json:"site"`
	Key          string    `json:"key"`
	CallbackURL  *string   `json:"callback_url,omitempty"`
	HasSecret    bool      `json:"has_secret"`
	IsDeprecated bool      `json:"is_deprecated"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReasonView is a reason-taxonomy row.
type ReasonView struct {
	ID           int64   `json:"id"`
	Key          string  `json:"key"`
	NameCN       string  `json:"name_cn"`
	Site         *string `json:"site,omitempty"`
	Severity     int16   `json:"severity"`
	IsDeprecated bool    `json:"is_deprecated"`
}

// ReviewItemView is a review-inbox row.
type ReviewItemView struct {
	ID              int64      `json:"id"`
	Site            string     `json:"site"`
	SubjectKind     string     `json:"subject_kind"`
	SubjectID       string     `json:"subject_id"`
	Source          int16      `json:"source" doc:"0=reports 1=ai_text 2=ai_image 3=community_forward 4=mislabel 5=manual"`
	Severity        *int16     `json:"severity,omitempty"`
	ClassifierScore *float32   `json:"classifier_score,omitempty"`
	ReportWeightSum *float32   `json:"report_weight_sum,omitempty"`
	Priority        float32    `json:"priority"`
	Status          int16      `json:"status" doc:"0=pending 1=claimed 2=actioned 3=dismissed"`
	ClaimedBy       *int64     `json:"claimed_by,omitempty"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	DecidedBy       *int64     `json:"decided_by,omitempty"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ReportView is a report row (admin detail).
type ReportView struct {
	ID           int64     `json:"id"`
	Site         string    `json:"site"`
	SubjectKind  string    `json:"subject_kind"`
	SubjectID    string    `json:"subject_id"`
	ReporterID   int64     `json:"reporter_id"`
	ReasonID     int64     `json:"reason_id"`
	Note         *string   `json:"note,omitempty"`
	Weight       float32   `json:"weight"`
	ReviewItemID *int64    `json:"review_item_id,omitempty"`
	Status       int16     `json:"status" doc:"0=received 1=linked 2=folded"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReviewItemDetail is an item plus its associated reports.
type ReviewItemDetail struct {
	Item    ReviewItemView `json:"item"`
	Reports []ReportView   `json:"reports"`
}

// DispositionView is a disposition row (dead-letter surface).
type DispositionView struct {
	ID               int64      `json:"id"`
	ReviewItemID     int64      `json:"review_item_id"`
	Action           int16      `json:"action" doc:"0=none 1=hide 2=remove 3=warn_user 4=restrict 5=escalate_idp"`
	ActedBy          int64      `json:"acted_by"`
	ReasonCode       string     `json:"reason_code"`
	Statement        *string    `json:"statement,omitempty"`
	CallbackStatus   *int16     `json:"callback_status,omitempty" doc:"0=pending 1=delivered 2=dead_letter; null=no callback"`
	CallbackAttempts int32      `json:"callback_attempts"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// --- admin requests --------------------------------------------------------

// DecideRequest resolves a review item.
type DecideRequest struct {
	Decision   string  `json:"decision" enum:"dismissed,actioned"`
	Action     *int16  `json:"action,omitempty" doc:"required for actioned: 0=none 1=hide 2=remove 3=warn_user 4=restrict 5=escalate_idp"`
	ReasonCode string  `json:"reason_code,omitempty" doc:"required for actioned"`
	Statement  *string `json:"statement,omitempty" doc:"Art.17 reason statement (optional in P0)"`
}

// CreateSubjectKindRequest registers a subject kind for a site.
type CreateSubjectKindRequest struct {
	Site           string  `json:"site" doc:"tenant site the kind belongs to"`
	Key            string  `json:"key"`
	CallbackURL    *string `json:"callback_url,omitempty"`
	CallbackSecret *string `json:"callback_secret,omitempty"`
}

// PatchSubjectKindRequest updates a subject kind (only provided fields apply).
type PatchSubjectKindRequest struct {
	CallbackURL    *string `json:"callback_url,omitempty"`
	CallbackSecret *string `json:"callback_secret,omitempty"`
	IsDeprecated   *bool   `json:"is_deprecated,omitempty"`
}

// CreateReasonRequest registers a reason (site null = global).
type CreateReasonRequest struct {
	Key      string  `json:"key"`
	NameCN   string  `json:"name_cn"`
	Site     *string `json:"site,omitempty"`
	Severity int16   `json:"severity"`
}

// PatchReasonRequest updates a reason (only provided fields apply).
type PatchReasonRequest struct {
	NameCN       *string `json:"name_cn,omitempty"`
	Severity     *int16  `json:"severity,omitempty"`
	IsDeprecated *bool   `json:"is_deprecated,omitempty"`
}

// OKResponse is a bare acknowledgement.
type OKResponse struct {
	OK bool `json:"ok"`
}
