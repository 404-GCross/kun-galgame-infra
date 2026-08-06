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
	SubjectURL  *string `json:"subject_url,omitempty" doc:"deep link to the reported content in its product context (http/https, ≤512 chars); carried by the submitter because sub-entity subjects have no page derivable from subject_id alone"`
	ReporterID  int64   `json:"reporter_id" doc:"the reporting user's global id (P0 requires login)"`
}

// ReportResponse is the intake outcome.
type ReportResponse struct {
	ReportID     int64  `json:"report_id"`
	ReviewItemID *int64 `json:"review_item_id,omitempty" doc:"set when the report opened/linked/folded a review item"`
}

// ScanRequest is an async content-scan submission. The product has ALREADY
// published the content; scan never gates a sync path (doc 18 §6). The subject is
// referenced by (subject_kind, subject_id); text carries the UGC body to score
// (capped at intake with truncation recorded).
//
// Site (step 04) is normally OMITTED — it is then derived from the client
// binding, exactly like report intake. A non-empty `site` is the forwarder relay
// path (a caller relaying for many community-backed sites through one S2S
// identity): it is allowlist-gated (KUN_TRUST_FORWARDER_CLIENT_IDS), mirroring
// the forward face, and wins over the bound site.
type ScanRequest struct {
	Site        string `json:"site,omitempty" doc:"optional tenant site (allowlist-gated relay path); omitted = derived from the client binding"`
	SubjectKind string `json:"subject_kind" doc:"registered subject kind for this site (e.g. community_post)"`
	SubjectID   string `json:"subject_id" doc:"the subject's stable id in the product"`
	Text        string `json:"text" doc:"the UGC text to scan (capped at ~8000 runes; excess is truncated and recorded)"`
	AuthorID    *int64 `json:"author_id,omitempty" doc:"optional content author's global id (attribution/repeat-offender signal); not the tenant"`
	SubjectReach *int64 `json:"subject_reach,omitempty" doc:"optional audience the content has reached so far (views or the product's nearest equivalent); ranks the review queue — omitted or 0 contributes no boost"`
}

// ScanResponse is the intake outcome (an accept-type endpoint: the row lands
// status=pending and is returned immediately, before any scoring).
type ScanResponse struct {
	ScanID    int64 `json:"scan_id"`
	Truncated bool  `json:"truncated" doc:"true = the text exceeded the cap and was truncated before storage"`
}

// CheckRequest is a synchronous Tier0 word-list check (step 05; doc 18 §6). The
// caller has content it is about to accept and asks whether the text trips the
// deterministic word list. Site (like ScanRequest) is normally OMITTED and
// derived from the client binding; a non-empty `site` is the allowlist-gated
// forwarder relay path. AuthorID is accepted for future repeat-offender
// weighting but is NOT consulted in v0.
type CheckRequest struct {
	Site     string `json:"site,omitempty" doc:"optional tenant site (allowlist-gated relay path); omitted = derived from the client binding"`
	Text     string `json:"text" doc:"the UGC text to check against the Tier0 word list"`
	AuthorID *int64 `json:"author_id,omitempty" doc:"optional content author's global id (accepted, not used in v0)"`
}

// CheckResponse is the Tier0 verdict: a decision (deny wins over hold) plus the
// matched normalized terms (safe to relay to the S2S caller).
type CheckResponse struct {
	Decision string   `json:"decision" enum:"allow,deny,hold" doc:"allow=no match; hold=only suspect terms matched (route to review); deny=a banned term matched (block)"`
	Matched  []string `json:"matched" doc:"the matched normalized terms ([] when none)"`
}

// SubjectKindsResponse lists a site's registered kinds (S2S sanity check).
type SubjectKindsResponse struct {
	Kinds []SubjectKindView `json:"kinds"`
}

// ReportReasonsResponse lists the reasons a site's report UI may offer (global
// base + the site's own extensions, non-deprecated) — the S2S feed that keeps
// product dropdowns in sync with the live taxonomy.
type ReportReasonsResponse struct {
	Reasons []ReasonView `json:"reasons"`
}

// --- views -----------------------------------------------------------------

// SubjectKindView is a subject-kind registry row. The secret is never serialized;
// HasCallback reports whether a callback endpoint is configured.
type SubjectKindView struct {
	ID              int64     `json:"id"`
	Site            string    `json:"site"`
	Key             string    `json:"key"`
	CallbackURL     *string   `json:"callback_url,omitempty"`
	HasSecret       bool      `json:"has_secret"`
	IsDeprecated    bool      `json:"is_deprecated"`
	NotifyOnDismiss bool      `json:"notify_on_dismiss"`
	CreatedAt       time.Time `json:"created_at"`
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
	Source          int16      `json:"source" doc:"0=reports 1=ai_text 2=ai_image 3=community_forward 4=mislabel 5=manual 6=ai_sample (a clean verdict drawn at random for calibration — never enforced)"`
	Severity        *int16     `json:"severity,omitempty"`
	ClassifierScore *float32   `json:"classifier_score,omitempty"`
	ReportWeightSum *float32   `json:"report_weight_sum,omitempty"`
	SubjectReach    *int64     `json:"subject_reach,omitempty" doc:"audience the content had reached when the item opened (snapshot); absent = the product does not report it"`
	Priority        float32    `json:"priority"`
	ContextNote     *string    `json:"context_note,omitempty" doc:"non-report evidence excerpt (community forward / ai_text)"`
	Status          int16      `json:"status" doc:"0=pending 1=claimed 2=actioned 3=dismissed"`
	ClaimedBy       *int64     `json:"claimed_by,omitempty"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	DecidedBy       *int64     `json:"decided_by,omitempty"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ReportView is a report row (admin detail).
type ReportView struct {
	ID          int64   `json:"id"`
	Site        string  `json:"site"`
	SubjectKind string  `json:"subject_kind"`
	SubjectID   string  `json:"subject_id"`
	ReporterID  int64   `json:"reporter_id"`
	ReasonID    int64   `json:"reason_id"`
	Note        *string `json:"note,omitempty"`
	// SubjectSnapshot is the reporter-carried content snapshot at report time —
	// the reviewer's evidence when the subject was edited or deleted afterwards.
	SubjectSnapshot *string   `json:"subject_snapshot,omitempty"`
	SubjectURL      *string   `json:"subject_url,omitempty" doc:"submitter-carried deep link to the content in its product context"`
	Weight          float32   `json:"weight"`
	ReviewItemID    *int64    `json:"review_item_id,omitempty"`
	Status          int16     `json:"status" doc:"0=received 1=linked 2=folded"`
	CreatedAt       time.Time `json:"created_at"`
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
	Site            string  `json:"site" doc:"tenant site the kind belongs to"`
	Key             string  `json:"key"`
	CallbackURL     *string `json:"callback_url,omitempty"`
	CallbackSecret  *string `json:"callback_secret,omitempty"`
	NotifyOnDismiss *bool   `json:"notify_on_dismiss,omitempty" doc:"emit an action:0 callback on a dismissed decision (release holds); default false"`
}

// PatchSubjectKindRequest updates a subject kind (only provided fields apply).
type PatchSubjectKindRequest struct {
	CallbackURL     *string `json:"callback_url,omitempty"`
	CallbackSecret  *string `json:"callback_secret,omitempty"`
	IsDeprecated    *bool   `json:"is_deprecated,omitempty"`
	NotifyOnDismiss *bool   `json:"notify_on_dismiss,omitempty"`
}

// --- declarative subject-kind ensure (step 06) -----------------------------

// EnsureSubjectKindItem declares one subject kind in a convergence batch. The
// callback/notify fields are SPARSE: an omitted field is left as-is on an
// existing kind (never clobbering an admin-set value) and takes its create
// default (unset callback / notify_on_dismiss=false) on a new kind.
type EnsureSubjectKindItem struct {
	Key             string  `json:"key" doc:"stable subject-kind key (e.g. forum_topic)"`
	CallbackURL     *string `json:"callback_url,omitempty" doc:"enforcement callback endpoint; omit to leave unchanged / unset"`
	CallbackSecret  *string `json:"callback_secret,omitempty" doc:"per-kind HMAC signing secret; omit to leave unchanged"`
	NotifyOnDismiss *bool   `json:"notify_on_dismiss,omitempty" doc:"emit an action:0 callback on a dismissed decision; omit to leave unchanged (false on create)"`
}

// EnsureSubjectKindsRequest is the S2S declarative registration: the caller
// declares the kinds it wants and trust converges the registry to match. The
// tenant `site` is derived from the client binding (never on the wire); at most
// 50 kinds per call.
type EnsureSubjectKindsRequest struct {
	Kinds []EnsureSubjectKindItem `json:"kinds" doc:"declared subject kinds (≤50); converged per-kind, deprecated kinds are never revived"`
}

// BatchSubjectKindsRequest is the admin batch registration — the same
// convergence as the S2S ensure, but with an explicit `site` (a platform
// operator acts for any site) and gated by the registry admin permission.
type BatchSubjectKindsRequest struct {
	Site  string                  `json:"site" doc:"tenant site the kinds belong to"`
	Kinds []EnsureSubjectKindItem `json:"kinds" doc:"declared subject kinds (≤50); converged per-kind, deprecated kinds are never revived"`
}

// EnsureSubjectKindResultView is one kind's convergence verdict.
type EnsureSubjectKindResultView struct {
	Key    string `json:"key"`
	Result string `json:"result" enum:"created,updated,unchanged,deprecated_skipped" doc:"per-kind convergence outcome"`
}

// EnsureSubjectKindsResponse is the per-kind convergence outcome in request
// order (duplicated keys included).
type EnsureSubjectKindsResponse struct {
	Results []EnsureSubjectKindResultView `json:"results"`
}

// --- S2S forward (community→trust convergence, step 03) --------------------

// ForwardRequest opens/updates a community_forward review item. Unlike a report,
// `site` is on the wire here (a forwarder acts for many community-backed sites
// through one S2S identity); the KUN_TRUST_FORWARDER_CLIENT_IDS allowlist is the
// counterweight.
type ForwardRequest struct {
	Site         string   `json:"site" doc:"tenant site the subject belongs to (allowlist-gated, not from the client binding)"`
	SubjectKind  string   `json:"subject_kind" doc:"registered subject kind (e.g. community_post)"`
	SubjectID    string   `json:"subject_id"`
	Severity     *int16   `json:"severity,omitempty"`
	WeightSum    *float32 `json:"weight_sum,omitempty" doc:"accumulated signal weight (idempotent update takes the max)"`
	ContextNote  *string  `json:"context_note,omitempty" doc:"reviewer-facing evidence excerpt"`
	SubjectReach *int64   `json:"subject_reach,omitempty" doc:"optional audience the content has reached (views or nearest equivalent); ranks the review queue and re-ranks an already-open item upward as the number grows"`
	ForwarderRef *string  `json:"forwarder_ref,omitempty" doc:"caller-side trace ref (informational)"`
}

// ForwardResponse is the forward outcome.
type ForwardResponse struct {
	ReviewItemID int64 `json:"review_item_id"`
	Created      bool  `json:"created" doc:"false = an open item already existed and its signals were updated"`
}

// ForwardResolveRequest closes a forwarded item after a site moderator's local
// decision (no disposition, no callback — the enforcement already happened).
type ForwardResolveRequest struct {
	ReviewItemID int64   `json:"review_item_id"`
	Outcome      string  `json:"outcome" enum:"approved,rejected" doc:"approved→dismissed, rejected→actioned"`
	ActorRef     *string `json:"actor_ref,omitempty" doc:"site-side actor ref (recorded in the audit policy_ref)"`
}

// ForwardResolveResponse reports whether an open item was actually closed.
type ForwardResolveResponse struct {
	Closed bool `json:"closed" doc:"false = the item was already terminal (tolerated race)"`
}

// --- Tier0 terms (admin; step 05) ------------------------------------------

// TermView is a Tier0 word-list registry row. term_norm is the stored
// normalized form (the raw admin input is never persisted).
type TermView struct {
	ID           int64     `json:"id"`
	Site         *string   `json:"site,omitempty" doc:"null = global (applies to every site)"`
	TermNorm     string    `json:"term_norm"`
	Kind         int16     `json:"kind" doc:"0=suspect (hold) 1=banned (deny)"`
	Purpose      int16     `json:"purpose" doc:"0=abuse 1=compliance (exempt from precision-based retirement)"`
	Note         *string   `json:"note,omitempty"`
	IsDeprecated bool      `json:"is_deprecated"`
	CreatedAt    time.Time `json:"created_at"`
}

// TermsResponse lists ONE PAGE of Tier0 terms (admin). Total is the count
// matching the filters, not the length of Terms — the live word list runs to
// tens of thousands of entries, so the listing is always paged.
type TermsResponse struct {
	Terms []TermView `json:"terms"`
	Total int64      `json:"total"`
}

// CreateTermRequest registers a Tier0 term. `term` is the RAW word; the service
// normalizes it before storage. Site null = a global term.
type CreateTermRequest struct {
	Site *string `json:"site,omitempty" doc:"tenant site; null = global (applies to every site)"`
	Term string  `json:"term" doc:"the raw term (normalized server-side before storage)"`
	Kind int16   `json:"kind" doc:"0=suspect (hold — enqueue, don't block) 1=banned (deny — the sync check rejects)"`
	Purpose int16 `json:"purpose,omitempty" doc:"0=abuse (default; precision-prunable against the AI classifier) 1=compliance (legal/regulatory — exempt from precision pruning, since the abuse classifier does not judge that question)"`
	Note *string `json:"note,omitempty" doc:"optional operator memo"`
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
