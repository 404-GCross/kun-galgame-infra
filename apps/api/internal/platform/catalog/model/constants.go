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
