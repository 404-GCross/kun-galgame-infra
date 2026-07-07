package model

// This file centralizes every catalog enum constant. These are CODE-COUPLED
// state machines (doc 17 R1 boundary note): unlike the registry vocabularies,
// adding a value here always ships with new code, so smallint constants —
// not registry rows — are the right form.
//
// All values are persisted; NEVER renumber an existing constant.

// Entity types — the discriminator values of the polymorphic infrastructure
// tables (redirect / entity_usage / revision) and, later, external_ref.
// Work (5) and release (6) tables arrive in step 04; the constants are pinned
// now so the ID space never shifts.
const (
	EntityTypePerson     int16 = 0
	EntityTypeCreditName int16 = 1
	EntityTypeOrg        int16 = 2
	EntityTypeLabel      int16 = 3
	EntityTypeCharacter  int16 = 4
	EntityTypeWork       int16 = 5
	EntityTypeRelease    int16 = 6
)

// Gender values for person/character. The columns are nullable: NULL means
// unknown/unset — there is deliberately no 0 constant.
const (
	GenderMale   int16 = 1
	GenderFemale int16 = 2
	GenderOther  int16 = 3
)

// CreditName kinds — how this credited identity relates to the person.
const (
	CreditNameKindMain            int16 = 0 // the person's primary credited name
	CreditNameKindPenName         int16 = 1 // alternate pen name, publicly the same person
	CreditNameKindDistinctPersona int16 = 2 // deliberately separated persona (裏名義 etc.)
	CreditNameKindFormerName      int16 = 3 // no longer used name
)

// CreditName link visibility — visibility of the credit_name→person LINK
// (doc 10 invariant 9 / §10). Hidden: the link stays visible to the data
// layer and admin surfaces, but public person aggregations, "same person"
// grouping in search, and public API responses never expose it — the credit
// name appears as an independent identity. Policy ownership (editorial
// guideline, not schema): linking two names publicly requires a public
// source; R18↔all-ages name links default to hidden unless the person has
// publicly self-identified; takedown requests hide/unlink via a fast path.
const (
	LinkVisibilityPublic int16 = 0
	LinkVisibilityHidden int16 = 1
)

// Alias kinds — writing variants of one identity (NOT new identities; a new
// identity is a new credit_name row).
const (
	AliasKindTranslation     int16 = 0 // translated form of the name
	AliasKindSpellingVariant int16 = 1 // transliteration/spelling variant
	AliasKindSearchHint      int16 = 2 // findability only, never displayed
)

// Label kinds. Constants, not a registry (reviewer ruling in refs/proj/03):
// the set is small and every new kind ships with code; promote to a registry
// table if it ever starts growing uncontrolled.
const (
	LabelKindGameBrand    int16 = 0 // game brand (ブランド)
	LabelKindBunko        int16 = 1 // bunko / book imprint
	LabelKindPublisher    int16 = 2 // publisher
	LabelKindAnimeStudio  int16 = 3 // anime studio
	LabelKindDoujinCircle int16 = 4 // doujin circle (同人サークル)
	LabelKindGroup        int16 = 5 // group/unit (组合, e.g. Bangumi person type 3)
)

// WorkLabelKind — the attribution nature of a catalog_work_label edge:
// organizational responsibility for a work (who published/made it), which is
// DISTINCT from an authorship credit (catalog_credit, "who performed a role").
// Step 14 registered maker labels but deferred this edge for want of a real
// consumer; step 18's read surface is that consumer. 0/1 are in use; 2/3 are
// reserved for when developer/brand attributions gain a source.
const (
	WorkLabelKindCircle    int16 = 0 // doujin circle (同人サークル)
	WorkLabelKindPublisher int16 = 1 // publisher / label
	WorkLabelKindDeveloper int16 = 2 // reserved: the studio that made it
	WorkLabelKindBrand     int16 = 3 // reserved: the brand it shipped under
)

// Revision actions (doc 10 §9). merged_source/merged_target: a merge writes a
// revision on BOTH sides so neither history dangles (invariant 7).
const (
	RevisionActionCreated      int16 = 0
	RevisionActionUpdated      int16 = 1
	RevisionActionMergedSource int16 = 2
	RevisionActionMergedTarget int16 = 3
	RevisionActionSplit        int16 = 4
	RevisionActionImported     int16 = 5
	RevisionActionRedirect     int16 = 6
	RevisionActionReverted     int16 = 7
)

// Work content ratings (doc 14 three tiers; per-source mappings in doc 17 §6:
// e.g. Bangumi nsfw=true → r18, nsfw=false → NOT written — all_ages must
// never be inferred).
const (
	ContentRatingAllAges   int16 = 0
	ContentRatingSensitive int16 = 1
	ContentRatingR18       int16 = 2
)

// Work status (doc 17 R2). Stub = unclaimed AND below the metadata bar (no
// external anchor, or title+medium+date incomplete) — excluded from public
// aggregation until it graduates.
const (
	WorkStatusLive   int16 = 0
	WorkStatusStub   int16 = 1
	WorkStatusMerged int16 = 2 // merged away via redirect
)

// Work title kinds (doc 17 R2). Search hints are findability-only, never
// displayed.
const (
	WorkTitleKindOfficial     int16 = 0
	WorkTitleKindAlias        int16 = 1
	WorkTitleKindAbbreviation int16 = 2
	WorkTitleKindSearchHint   int16 = 3
)

// Release kinds (doc 17 R3). Constants for now; promote to a registry table
// if the set ever starts growing uncontrolled.
const (
	ReleaseKindDefault  int16 = 0
	ReleaseKindDigital  int16 = 1
	ReleaseKindPhysical int16 = 2
	ReleaseKindTrial    int16 = 3
	ReleaseKindPatch    int16 = 4
)

// External-ref link kinds (doc 17 R7, doc 10 invariant 5 as revised by R8).
//
//   - exact: identity assertion. Globally unique per (source, external_id,
//     entity_type) via a partial unique index — the anti-squatting line.
//     Write policy is three-layered (R8, service side): auto-exact only for
//     trust_tier=0 self-referential structural data with matched_by set;
//     community cross-references start probable; human confirmation promotes.
//   - probable: system-suggested identity, pending human confirmation.
//   - related: NON-IDENTITY link (official site, derived/reference page).
//     Related rows must NEVER participate in identity resolution,
//     aggregation, or dedup — enforce with assertions in every consumer.
const (
	LinkKindExact    int16 = 0
	LinkKindProbable int16 = 1
	LinkKindRelated  int16 = 2
)

// Match-candidate reasons (doc 10 §8 + doc 17 §4): how the pair was proposed.
const (
	CandidateReasonSharedExternalID int16 = 0 // same (source, external_id) on both entities — strong
	CandidateReasonNameNormEqual    int16 = 1 // NFKC/kana-folded names equal — medium
	CandidateReasonNameFuzzy        int16 = 2 // edit-distance/phonetic similarity — weak, conservative thresholds
	CandidateReasonImporterSuggest  int16 = 3 // ingestion pipeline heuristic
	CandidateReasonLLMSuggest       int16 = 4 // LLM suggestion (never auto-accepted, doc 17 §4)
)

// Match-candidate status. Rejected rows are kept FOREVER — deleting them
// would let the same pair resurface on every import run.
const (
	CandidateStatusPending  int16 = 0
	CandidateStatusAccepted int16 = 1 // graduated into a merge proposal
	CandidateStatusRejected int16 = 2
	CandidateStatusDeferred int16 = 3
)

// Merge-proposal status (doc 10 §6.1). approved starts the cooling-off
// window; execution is a job once execute_after passes.
const (
	ProposalStatusOpen      int16 = 0
	ProposalStatusApproved  int16 = 1
	ProposalStatusExecuted  int16 = 2
	ProposalStatusRejected  int16 = 3
	ProposalStatusWithdrawn int16 = 4
)
