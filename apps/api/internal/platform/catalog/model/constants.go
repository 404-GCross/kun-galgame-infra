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
	// Tag (7) and Engine (8) are DATA-ONLY discriminators for now (A2-0, the
	// wiki registrar rescue): they exist so the wiki's tid/eid id spaces can be
	// filed in catalog_external_ref before the galgame_* tables are dropped.
	// Neither has a public read surface — the public lookup/redirect/resolve
	// vocabularies deliberately stay at the four/seven established families,
	// and extending them is A2-1's decision, not a side effect of this wave.
	// The values are pinned here so the id space never shifts.
	EntityTypeTag    int16 = 7
	EntityTypeEngine int16 = 8
)

// Gender values for person/character. The columns are nullable: NULL means
// unknown/unset — there is deliberately no 0 constant.
const (
	GenderMale   int16 = 1
	GenderFemale int16 = 2
	GenderOther  int16 = 3
)

// Blood types for a character (refs/proj/81 field PR C2). The column is
// nullable: NULL = unknown/unset — there is deliberately no 0 constant, exactly
// as with Gender. Sources: VNDB chars.bloodt (a/b/ab/o) and Bangumi 血型
// (A型/O型/…). Values outside this vocabulary — sentinels (不明/未知/？) and
// fictional types (X型/F型 etc.) — are dropped entirely, not preserved in extra
// (refs/proj/81 D1: the tail is a few hundred rows, mostly unknown-markers).
const (
	BloodTypeA  int16 = 1
	BloodTypeB  int16 = 2
	BloodTypeAB int16 = 3
	BloodTypeO  int16 = 4
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

// Alias provenance — WHO produced the spelling, on the same axis the intro
// tables carry. Orthogonal to AliasKind*, which says what KIND of variant it
// is: a translation can be either a human source's translation or a machine's,
// and the whole reason this axis exists is that the table cannot otherwise
// tell them apart afterwards.
const (
	AliasProvenanceSource  int16 = 0 // a name an upstream source or a human wrote
	AliasProvenanceMachine int16 = 1 // machine translation — never primary, never localized{}
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

// LabelRelation — the corporate-structure vocabulary of a
// catalog_label_relation edge (wave 186), read as "other_label_id is <relation>
// of label_id". Constants, not a registry, for the LabelKind reason: the set is
// small and every new value ships with code.
//
// The vocabulary is four INVERSE PAIRS, and the pairing is load-bearing: the
// graph is stored mirrored, so writing a fact means writing the edge under one
// code and its mirror under the code opposite it here.
//
//	Parent      ↔ Subsidiary   the corporate ownership axis
//	Imprint     ↔ ImprintOf    a brand operating under another label
//	Spawned     ↔ Origin       a spin-off and the label it came out of
//	SucceededBy ↔ Formerly     the succession axis (renamed / reorganized)
//
// 0 is deliberately absent: an unset relation is not a fact, and leaving the
// zero value unassigned makes a forgotten column write fail a CHECK-free table
// loudly at read time instead of silently rendering "parent".
const (
	LabelRelationParent      int16 = 1
	LabelRelationSubsidiary  int16 = 2
	LabelRelationImprint     int16 = 3
	LabelRelationImprintOf   int16 = 4
	LabelRelationSpawned     int16 = 5
	LabelRelationOrigin      int16 = 6
	LabelRelationSucceededBy int16 = 7
	LabelRelationFormerly    int16 = 8
)

// LabelRelationKey is the PUBLIC spelling of that vocabulary — the strings the
// label detail face renders in relations[].relation. It lives here, next to the
// codes, so the wire word and the stored code can never drift apart; a code
// outside the map has no public spelling and the read face drops the row rather
// than inventing one.
var LabelRelationKey = map[int16]string{
	LabelRelationParent:      "parent",
	LabelRelationSubsidiary:  "subsidiary",
	LabelRelationImprint:     "imprint",
	LabelRelationImprintOf:   "imprint_of",
	LabelRelationSpawned:     "spawned",
	LabelRelationOrigin:      "origin",
	LabelRelationSucceededBy: "succeeded_by",
	LabelRelationFormerly:    "formerly",
}

// WorkLabelKind — the attribution nature of a catalog_work_label edge:
// organizational responsibility for a work (who published/made it), which is
// DISTINCT from an authorship credit (catalog_credit, "who performed a role").
// Step 14 registered maker labels but deferred this edge for want of a real
// consumer; step 18's read surface is that consumer.
//
// ALL FOUR ARE IN USE (the "2/3 reserved" note here was true until step 100 and
// wrong for a long time after: developer 61,909 and brand 3,803 as of
// 2026-08-04).
//
// ⚠️ THIS AXIS AND catalog_label.kind ARE NOT INDEPENDENT, and a consumer that
// renders both will show one fact twice. The overlap is not a vocabulary
// accident, it is what the sources support:
//
//   - VNDB states ROLES. releases_producers carries dev/pub flags, so a vndb
//     edge is a real attribution — and the same company being BOTH developer
//     and publisher is a genuine finding, not duplication (24.7% of pairs).
//   - DLsite states only "the maker". It has no dev/pub distinction at all, so
//     importer.dlEdgeKind necessarily derives the edge kind from the maker's
//     own kind. For those edges the two axes coincide BY CONSTRUCTION: a doujin
//     circle that made a work yields edge=circle + label.kind=doujin_circle,
//     151,779 times.
//
// So a reader must not treat edge kind as adding information on top of the
// org's category unless the source actually stated a role. Splitting a
// source-stated role from a "this org is the maker" assertion would need a new
// `maker` kind — a vocabulary change with downstream consumers, deliberately
// not made here.
const (
	WorkLabelKindCircle    int16 = 0 // doujin circle (同人サークル)
	WorkLabelKindPublisher int16 = 1 // publisher / label
	WorkLabelKindDeveloper int16 = 2 // the studio that made it (vndb dev flag)
	WorkLabelKindBrand     int16 = 3 // the brand it shipped under
)

// WorkCharacterKind — the appearance strength of a catalog_work_character
// roster edge ("this character appears in this work"). 0 (unknown) is a
// MEANINGFUL value, never "unset": erogamespace has no main/supporting
// distinction on appearances, so every EG roster edge is genuinely unknown,
// and Bangumi's undocumented low-volume type tails (mob/narration/background)
// map here rather than being guessed. NOT NULL, no default (default-tag
// zero-value trap avoided).
const (
	WorkCharacterKindUnknown   int16 = 0
	WorkCharacterKindMain      int16 = 1 // Bangumi 主角
	WorkCharacterKindSecondary int16 = 2 // Bangumi 配角
	WorkCharacterKindAppears   int16 = 3 // Bangumi 客串 (guest/cameo)
)

// SeriesMemberKind — the role a work plays inside its series line
// (catalog_series_member.kind, wave 184). Unlike WorkCharacterKind, 0 here is a
// SENTINEL, not a datum: it means no ordering pass has classified this row yet,
// or the lane's classifier found no relation-edge evidence and refuses to
// guess. The derived lane (source `derived`) never leaves a member at 0 — its
// members exist BECAUSE an edge grouped them — while the dlsite / curated lanes
// keep 0 for members the edge graph says nothing about.
const (
	SeriesMemberKindUnknown    int16 = 0
	SeriesMemberKindMain       int16 = 1 // a main entry of the line
	SeriesMemberKindFandisc    int16 = 2 // fandisc_of another member
	SeriesMemberKindSideStory  int16 = 3 // side_story_of another member
	SeriesMemberKindCollection int16 = 4 // a bundle collecting other members
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
	// Deleted: the entity row was removed. Used by step 22 to tombstone an
	// auto-linked person that a detach emptied. catalog_revision has no FK to
	// the entity, so the revision (and its final snapshot) survives the row's
	// deletion as the history of record.
	RevisionActionDeleted int16 = 8
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

// Intro provenance (step 75) — whether an intro row carries the UPSTREAM
// text or a machine translation of it. It is not a source: a machine row
// reuses the source row's source_id (attribution = "translated from that
// source") and is told apart only by this column, which is what lets the read
// face prefer source text by ordering on it. The values were until now spelled
// as local constants at each writer; they are pinned here so the editing face
// and the MT lane cannot drift apart on the one column that separates them.
const (
	IntroProvenanceSource  int16 = 0
	IntroProvenanceMachine int16 = 1
)

// Claim visibility state (R7, refs/proj/135 A2-1e) — a CATALOG-OWNED
// vocabulary describing how visible the CLAIM is on the owning product face,
// NOT a copy of any product's status machine (the R2 red line: a wiki status
// value never crosses into the catalog contract).
//
// It exists because `claimed_by` alone is state-blind: a consumer that follows
// the pointer cannot tell a published entry from a draft one or from an entry
// the product has taken down, so re-anchoring on `claimed_by` would resurrect
// banned entries on a downstream site (A2-2 S1).
//
//   - live     — the claim is publicly visible on the product face;
//   - draft    — it exists but is not published yet (an editorial state);
//   - pending  — it is submitted and awaiting a curator's decision (N1);
//   - declined — a curator refused it; the submitter may revise and resubmit (N1);
//   - hidden   — the product has withdrawn it; a consumer must render neither a
//     claim badge nor any product content for it.
//
// pending/declined arrive with the editing-face nativization (refs/plans/10 §3):
// they are the natural subdivision of the SAME axis hidden already occupies —
// "how visible is this claim" — and NOT a copy of the wiki's status column,
// whose numbering they deliberately do not share. Existing rows need no
// migration: 0/1/2 keep their meaning and the column is already int16.
//
// Meaningful zero (live) → the column is NULLABLE with NO `default:` tag: NULL
// means "no claim, or not yet projected" (see CatalogWork.ClaimState), and a
// DB default would silently promote a legitimately-live row's state out of the
// INSERT (the default-tag zero-value trap).
const (
	ClaimStateLive     int16 = 0
	ClaimStateDraft    int16 = 1
	ClaimStateHidden   int16 = 2
	ClaimStatePending  int16 = 3
	ClaimStateDeclined int16 = 4
)

// The PUBLIC claim vocabulary — the six values `claimed_by.state` renders and
// the works search index's `claim_state` filters on. `none` has no column
// counterpart: it is the UNCLAIMED registry row, which the read face renders as
// `claimed_by: null`.
const (
	ClaimStateKeyNone     = "none"
	ClaimStateKeyLive     = "live"
	ClaimStateKeyDraft    = "draft"
	ClaimStateKeyPending  = "pending"
	ClaimStateKeyDeclined = "declined"
	ClaimStateKeyHidden   = "hidden"
)

// ClaimStateKey projects a registry row's claim columns onto that vocabulary.
//
// It lives in the model package because it must have exactly ONE definition:
// the read face renders it as claimed_by.state and the reindexer bakes it into
// the works index as a filterable field, and a search filter that selected a
// different set than the record it returns is the precise failure this function
// exists to make unrepresentable.
//
// The rules, in the order they apply:
//
//   - no site, or a site with no product_work_id → `none`. The second half is
//     not pedantry: claimed_by is null without a product work id (there is
//     nothing to point at), so such a row reads as unclaimed on the wire and
//     must filter as unclaimed too.
//   - claimed, column NULL → `live`. That is a claimed row no projector has
//     stamped yet, and `live` is how every consumer treated claimed_by before
//     the column existed (zero-regression semantics).
//   - 0/1/2/3/4 → live/draft/hidden/pending/declined; anything OUTSIDE the
//     vocabulary → `hidden`, the conservative choice: an unrecognized state must
//     never be published.
func ClaimStateKey(site *string, productWorkID *int64, claimState *int16) string {
	if site == nil || *site == "" || productWorkID == nil {
		return ClaimStateKeyNone
	}
	if claimState == nil {
		return ClaimStateKeyLive
	}
	switch *claimState {
	case ClaimStateLive:
		return ClaimStateKeyLive
	case ClaimStateDraft:
		return ClaimStateKeyDraft
	case ClaimStatePending:
		return ClaimStateKeyPending
	case ClaimStateDeclined:
		return ClaimStateKeyDeclined
	default:
		return ClaimStateKeyHidden
	}
}

// The PUBLIC display-limit vocabulary (A2-R5) — the two values
// `claimed_by.content_limit` renders and the works index's `content_limit`
// filters on. It is the EDITORIAL DISPLAY axis, and it is NOT content_rating:
//
//   - content_rating (all_ages | sensitive | r18) is the AGE axis: what the GAME
//     itself is rated. It answers "may a minor buy this".
//   - content_limit (sfw | nsfw) is the DISPLAY axis: whether the material a
//     consumer would RENDER for this work — cover, screenshots, synopsis — is
//     safe to put on a public page. It answers "may a search engine index this".
//
// The two disagree in bulk, which is the whole reason this vocabulary exists:
// on the 2026-07 production registry 5,568 works are r18 games whose wiki
// display material is editorially sfw. A downstream that mapped the age axis
// onto its display gate hid all of them, collapsing its indexable surface from
// 6,117 works to 599 (doc 106 §38). The reverse leak exists too (50 all_ages
// works whose material the wiki marked nsfw), so neither axis is a widening or
// a narrowing of the other — they are different questions.
const (
	DisplayLimitKeySFW  = "sfw"
	DisplayLimitKeyNSFW = "nsfw"
)

// WikiContentLimitNSFW is the galgame body's own spelling of "this work's
// display material is NSFW" (galgame.content_limit).
//
// Since the W1-pre nativization the catalog does not READ this vocabulary at
// request time: CatalogWork.DisplayNSFW holds the same judgement as a boolean,
// and this constant survives as the wiki-side comparison the mirror step makes
// while translating one into the other (wikirescue.mirrorDisplayNSFW). It dies
// with the galgame family.
const WikiContentLimitNSFW = "nsfw"

// DisplayLimitKey projects a registry row onto the display vocabulary. It lives
// in the model package for exactly the reason ClaimStateKey does: the read face
// renders it as claimed_by.content_limit, the reindexer bakes it into the works
// index as the filterable content_limit field, and the works list compiles it
// into a SQL predicate (service.displayLimitWhere). A filter that selected a
// different set than the record it returns is the failure this function exists
// to make unrepresentable.
//
// The two branches, and why they differ:
//
//   - CLAIMED (site non-empty AND product_work_id set — ClaimStateKey's claimed
//     judgement verbatim): the EDITORIAL DECLARATION decides. displayNSFW is
//     CatalogWork.DisplayNSFW, the flag a human editor set. Nothing declared is
//     sfw: the conservative choice HERE is to keep the work visible, because
//     over-marking is precisely the incident (see the vocabulary note).
//   - BODYLESS: there is no editorial judgement to read, so the age axis is the
//     only signal available — r18 → nsfw, all_ages/sensitive → sfw. That is the
//     mapping every downstream already applies to unclaimed rows
//     (contentLimitFromRating), so bodyless rows keep the behaviour they have.
//
// displayNSFW was galgame.content_limit == 'nsfw', read out of the wiki body per
// request, until the W1-pre wave nativized it onto the registry row
// (refs/plans/10-data-layer-retirement/02-w1pre-bridge-nativization.md §5b). Same
// judgement, same two branches, one less table: a boolean cannot carry a value
// outside the vocabulary, so the old "unrecognized string → sfw" arm is gone by
// construction rather than by a check.
//
// Two values, total: every row gets exactly one, so the pair is a partition and
// naming both is no gate at all.
func DisplayLimitKey(site *string, productWorkID *int64, displayNSFW bool, contentRating int16) string {
	if site == nil || *site == "" || productWorkID == nil {
		if contentRating == ContentRatingR18 {
			return DisplayLimitKeyNSFW
		}
		return DisplayLimitKeySFW
	}
	if displayNSFW {
		return DisplayLimitKeyNSFW
	}
	return DisplayLimitKeySFW
}

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

// Popularity metrics (refs/proj/62): what a catalog_work_popularity.value
// counts. An EXTENSIBLE vocabulary — new counting facets append new constants
// (never renumber): the Bangumi favorite-shelf wave (refs/proj/71) did exactly
// that, appending 10-14 below. The unit is
// source-relative — metric 0 on DLsite counts sales, and a hypothetical
// metric 0 on another store would count that store's downloads — so consumers
// always render per (source_id, metric), never sum across sources.
const (
	PopularityMetricDownloads int16 = 0 // purchase/download counter (DLsite dl_count)
	PopularityMetricWishlist  int16 = 1 // wishlist/favorite counter (DLsite wishlist_count)
	PopularityMetricReviews   int16 = 2 // written-review counter (DLsite review_count)

	// Bangumi favorite shelves (refs/proj/71): how many Bangumi users placed
	// the subject on each of the five collection shelves — the first extension
	// of this vocabulary, per the design note above. Values come verbatim from
	// src_bangumi.subject.favorite; a present 0 is a real row, an absent shelf
	// is no row. NOTE: the Archive dump serializes Bangumi's "collect" shelf
	// (completed/collected) under the key "done" — the constant keeps the
	// canonical Bangumi shelf name.
	PopularityMetricBgmWish    int16 = 10 // wish shelf — users planning to play
	PopularityMetricBgmCollect int16 = 11 // collect shelf — users who completed it (dump key "done")
	PopularityMetricBgmDoing   int16 = 12 // doing shelf — users currently playing
	PopularityMetricBgmOnHold  int16 = 13 // on-hold shelf — users who paused it
	PopularityMetricBgmDropped int16 = 14 // dropped shelf — users who abandoned it
)

// Match-candidate reasons (doc 10 §8 + doc 17 §4): how the pair was proposed.
const (
	CandidateReasonSharedExternalID int16 = 0 // same (source, external_id) on both entities — strong
	CandidateReasonNameNormEqual    int16 = 1 // NFKC/kana-folded names equal — medium
	CandidateReasonNameFuzzy        int16 = 2 // edit-distance/phonetic similarity — weak, conservative thresholds
	CandidateReasonImporterSuggest  int16 = 3 // ingestion pipeline heuristic
	CandidateReasonLLMSuggest       int16 = 4 // LLM suggestion (never auto-accepted, doc 17 §4)
	// AliasDeclared: one source-side entity's alias folds to another source's
	// credit_name whole name (step 25 leg B). Weaker than a shared handle — the
	// declaration is community data with no cross-source structural anchor — so
	// these are NEVER auto-linked; they seed the human-review / future-batch
	// queue only.
	CandidateReasonAliasDeclared int16 = 5
)

// Match-candidate status. Rejected rows are kept FOREVER — deleting them
// would let the same pair resurface on every import run.
const (
	CandidateStatusPending  int16 = 0
	CandidateStatusAccepted int16 = 1 // acted on: a merge proposal, or a person link (credit_name, step 22)
	CandidateStatusRejected int16 = 2
	CandidateStatusDeferred int16 = 3
	// NeedsManual: an accepted person-link candidate whose two names already
	// belong to DIFFERENT persons (step 22). Person merge is future work, so
	// the pair is flagged for manual handling rather than auto-merged. Counted
	// by the same GROUP BY status aggregate the step-19 dashboard already runs.
	CandidateStatusNeedsManual int16 = 4
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
