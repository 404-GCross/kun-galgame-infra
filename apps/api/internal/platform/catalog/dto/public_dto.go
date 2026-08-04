package dto

import "time"

// NextMoe open-API catalog public projection (step 03). These DTOs are the
// FROZEN v1 public contract for the catalog face (`/v1/catalog/*`) — deliberately
// SEPARATE from the internal S2S read DTOs (read_dto.go) so the public wire can
// evolve on its own "add-only, never change" cadence
// (docs/developer-platform/02-public-api.md §3.5). Enum ints are projected to stable
// string keys (medium / content_rating / title kind / label kind / …) so the
// contract is self-describing and never leaks the internal numeric state machine
// (投影化不透内部形状). The aggregate is a cross-media identity registry view:
// probable/related anchors NEVER cross this face (exact-only, 裁定 3/硬红线), and
// content_rating=r18 works are fully hidden in Phase 1 (裁定 6).

// PublicClaimedBy is the cross-face content pointer (design §3.3): a consumer
// follows it to the owning product face (e.g. site=galgame_wiki + work_id → GET
// /v1/galgame/{work_id}). Null when the catalog row is unclaimed.
type PublicClaimedBy struct {
	Site   string `json:"site"`
	WorkID int64  `json:"work_id"`
	// State is the claim's VISIBILITY on that product face (A2-1e / R7) — a
	// CATALOG-owned vocabulary, never a product's own status machine:
	//
	//	live     — publicly visible there; follow the pointer freely
	//	draft    — exists but unpublished; render no product content for it
	//	pending  — submitted, awaiting a curator's decision; not public yet
	//	declined — a curator refused it; not public, may be revised
	//	hidden   — the product withdrew it; render neither badge nor content
	//
	// Always present. It is the difference between "this work is on the wiki"
	// and "this work has a wiki row" — without it a consumer that re-anchors on
	// claimed_by silently republishes entries the product took down.
	//
	// live is the ONLY value a consumer may follow through to product content;
	// pending/declined joined the vocabulary with the editing-face nativization
	// and are, like draft and hidden, "do not render" for every consumer.
	State string `json:"state" doc:"live|draft|pending|declined|hidden"`
	// ContentLimit is the claim's EDITORIAL DISPLAY axis (A2-R5) — whether the
	// material a consumer would RENDER for this work (cover, screenshots,
	// synopsis) is safe to put on a public page. For a claimed work it is the
	// wiki body's own content_limit, set by a human editor.
	//
	// It is NOT content_rating, which is the AGE axis (what the GAME is rated).
	// The two disagree in bulk — most r18 games carry editorially sfw display
	// material — so a consumer that maps content_rating onto its display gate
	// hides the majority of a healthy catalogue (doc 106 §38). Read this field
	// for "may I show / index this", read content_rating for "may a minor buy
	// it".
	//
	// Always present on a claimed_by object, whatever the claim's state (the
	// editorial judgement is independent of visibility). Absent for an unclaimed
	// row — claimed_by is null there, and the consumer falls back to mapping the
	// age axis, which is what this projection does for a bodyless work anyway.
	ContentLimit string `json:"content_limit" doc:"sfw|nsfw"`
}

// PublicWorkBrief is the lightweight work identity shared by lookup results,
// relation ends and the entity reverse-lookups. It never carries an r18 work
// (those are dropped upstream). claimed_by points at the content face; medium
// tells the consumer whether a catalog detail GET would resolve (only galgame is
// independently fetchable in Phase 1, 裁定 1).
type PublicWorkBrief struct {
	ID            int64            `json:"id"`
	Medium        string           `json:"medium"`
	DisplayName   string           `json:"display_name"`
	ContentRating string           `json:"content_rating"`
	ClaimedBy     *PublicClaimedBy `json:"claimed_by"`
}

// PublicCatalogTitle is one display title (official / alias / abbreviation).
// search_hint titles are findability-only and NEVER surface on the public face.
type PublicCatalogTitle struct {
	Lang  string `json:"lang"`
	Title string `json:"title"`
	Latin string `json:"latin,omitempty"`
	Kind  string `json:"kind"`
}

// PublicCatalogRef is one EXACT cross-source external anchor (probable/related
// tiers never cross this face). It is the "which upstream id is this work known
// by" projection — the backbone of the lookup killer feature.
type PublicCatalogRef struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
}

// PublicRelation is one cross-media relation edge, rendered single-directionally
// from the viewed work's perspective (phrase = the direction's phrase); the other
// end is a brief. r18 ends are dropped (裁定 6).
type PublicRelation struct {
	RelationType string          `json:"relation_type"`
	Phrase       string          `json:"phrase"`
	Work         PublicWorkBrief `json:"work"`
}

// PublicCreditItem is one credited name under a role. id is the credit-name id
// (addressable via /v1/catalog/names/{id}). note / provenance / source are
// stripped (裁定 7 whitelist).
type PublicCreditItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Lang        string `json:"lang"`
	Latin       string `json:"latin,omitempty"`
	CharacterID int64  `json:"character_id,omitempty"`
	Character   string `json:"character,omitempty"`
}

// PublicCreditGroup is all credits sharing one role.
type PublicCreditGroup struct {
	RoleKey  string             `json:"role_key"`
	RoleName string             `json:"role_name"`
	Credits  []PublicCreditItem `json:"credits"`
}

// PublicCatalogWork is the frozen v1 work record — the body of GET
// /v1/catalog/works/{id}. relations / credits are include-gated (omitted unless
// requested). refs are exact-only; titles / claimed_by / release_date always
// present (claimed_by / release_date may be null).
type PublicCatalogWork struct {
	ID            int64   `json:"id"`
	Medium        string  `json:"medium"`
	DisplayName   string  `json:"display_name"`
	OLang         string  `json:"olang"`
	ContentRating string  `json:"content_rating"`
	ReleaseDate   *string `json:"release_date"`
	// Created is the REGISTRY row's creation instant (RFC3339, A2-1e) — when
	// this identity entered the catalog, which is NOT the work's release date
	// and NOT its product-side creation time. Same source convention as
	// `updated` (the registry row's own timestamps).
	Created string `json:"created"`
	// Updated is the registry row's last-modified instant (RFC3339) — the
	// changes-feed watermark (doc 106 G6).
	Updated   string               `json:"updated"`
	Titles    []PublicCatalogTitle `json:"titles"`
	Refs      []PublicCatalogRef   `json:"refs"`
	ClaimedBy *PublicClaimedBy     `json:"claimed_by"`
	Relations []PublicRelation     `json:"relations,omitempty"`
	Credits   []PublicCreditGroup  `json:"credits,omitempty"`
	// Full-facet expansion (wave 104, add-only): every aggregation facet the
	// S2S face carries, projected to the public conventions — source keys not
	// ids, CDN URLs not hashes, string vocabularies not enum ints. Always
	// present ([] when empty, never null).
	Releases    []PublicRelease         `json:"releases"`
	Popularity  []PublicPopularity      `json:"popularity"`
	Ratings     []PublicRating          `json:"ratings"`
	Tags        []PublicTag             `json:"tags"`
	Playtimes   []PublicPlaytime        `json:"playtimes"`
	Series      []PublicSeries          `json:"series"`
	Platforms   []PublicPlatform        `json:"platforms"`
	Intro       []PublicWorkIntro       `json:"intro"`
	Covers      []PublicCover           `json:"covers"`
	Screenshots []PublicScreenshot      `json:"screenshots"`
	Characters  []PublicRosterCharacter `json:"characters"`
	Labels      []PublicWorkLabel       `json:"labels"`
	// Engines are the engine attributions (A2-1e, catalog_work_engine). Always
	// present ([] when the work has none — VNDB publishes no engine data, so
	// only the wiki-curated facet fills this).
	Engines []PublicWorkEngine `json:"engines"`
	// Links are the work's NON-IDENTITY external web links (A2-1e, doc 126 D6):
	// the store / official / social pages the wiki's users hand-entered, absorbed
	// by the retirement wave as PLATFORM-CURATED rows with no user attribution.
	// Identity anchors (refs[]) and web links never mix — same hard boundary the
	// label face draws. Always present ([] when the work has none).
	Links []PublicWorkLink `json:"links"`
	// SeriesSiblings is the transitive-closure series membership (wave 113),
	// projected to briefs — every other live work in the same_series component
	// (vndb's series is the pairwise same_series relation, never a first-class
	// entity; a leaf work sees its whole family here). r18 ends drop unless nsfw.
	// Complementary to series[] (first-class catalog_series). Always present.
	SeriesSiblings []PublicWorkBrief `json:"series_siblings"`
}

// PublicLookupData is the external-id reverse-lookup result (GET
// /v1/catalog/lookup). Exact anchors only; the miss/hidden case is a 404
// (design §3.1). The `type` parameter picks WHICH entity family the external id
// is resolved against — work (the default, byte-identical to the original
// contract) | name | character | label — and exactly one block is populated:
// work + claimed_by for type=work, otherwise the matching entity record. The
// three typed blocks are omitted entirely (not null) when unused.
type PublicLookupData struct {
	Work      *PublicWorkBrief `json:"work"`
	ClaimedBy *PublicClaimedBy `json:"claimed_by"`
	Name      *PublicName      `json:"name,omitempty"`
	Character *PublicCharacter `json:"character,omitempty"`
	Label     *PublicLabel     `json:"label,omitempty"`
}

// PublicLookupPair is one (source, external_id) request in a batch lookup.
// Type is the per-pair lookup family (work | name | character | label); absent
// means work. An unknown token fails the WHOLE batch with a 400.
type PublicLookupPair struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	Type       string `json:"type,omitempty"`
}

// PublicLookupBatchRequest is the batch reverse-lookup body (≤100 pairs).
type PublicLookupBatchRequest struct {
	Items []PublicLookupPair `json:"items"`
}

// PublicLookupBatchItem echoes the request pair — including the RESOLVED type
// token (work when the pair omitted it) — and carries its resolution; every
// block is null for a miss / hidden entity (order preserved, 裁定 3).
type PublicLookupBatchItem struct {
	Source     string           `json:"source"`
	ExternalID string           `json:"external_id"`
	Type       string           `json:"type"`
	Work       *PublicWorkBrief `json:"work"`
	ClaimedBy  *PublicClaimedBy `json:"claimed_by"`
	Name       *PublicName      `json:"name,omitempty"`
	Character  *PublicCharacter `json:"character,omitempty"`
	Label      *PublicLabel     `json:"label,omitempty"`
}

// PublicLookupBatchData is the batch reverse-lookup envelope.
type PublicLookupBatchData struct {
	Items []PublicLookupBatchItem `json:"items"`
}

// PublicResolveRequest is the batch old-id → canonical request (redirect
// flattening, public projection of the internal resolve semantics).
type PublicResolveRequest struct {
	EntityType string  `json:"entity_type"`
	IDs        []int64 `json:"ids"`
}

// PublicResolveData maps each requested id (as a string key) to its canonical id;
// redirected lists the ids that actually moved.
type PublicResolveData struct {
	Mappings   map[string]int64 `json:"mappings"`
	Redirected []int64          `json:"redirected"`
}

// PublicRedirectItem is one id-convergence event.
type PublicRedirectItem struct {
	EntityType string    `json:"entity_type"`
	OldID      int64     `json:"old_id"`
	CurrentID  int64     `json:"current_id"`
	MergedAt   time.Time `json:"merged_at"`
}

// PublicRedirectsData is the keyset redirect feed envelope. next_cursor is null
// on the last page.
type PublicRedirectsData struct {
	Items      []PublicRedirectItem `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

// PublicMovedData is the body of a 301 answer from a detail lane whose id was
// merged away: the requested id, the survivor it now lives on, and the entity
// family both belong to. It rides the standard {code, message, data} envelope
// with code = errors.ErrMoved, alongside a Location header — so a client that
// does NOT follow redirects still learns where to go, in one hop, without ever
// receiving the survivor's record under the dead id.
type PublicMovedData struct {
	EntityType string `json:"entity_type"`
	ID         int64  `json:"id"`
	CurrentID  int64  `json:"current_id"`
}

// PublicNameBuckets holds an entity display name in exactly ONE language bucket
// (a credit name / character lives in a single language). Consumers pick by their
// UI locale.
type PublicNameBuckets struct {
	Ja    string `json:"ja,omitempty"`
	Zh    string `json:"zh,omitempty"`
	Other string `json:"other,omitempty"`
}

// PublicSiblingName is another credited name of the same person (public links
// only — a hidden link never surfaces, 裁定 6).
type PublicSiblingName struct {
	ID    int64             `json:"id"`
	Name  PublicNameBuckets `json:"name"`
	Latin string            `json:"latin,omitempty"`
}

// PublicNameRole is one role a credited name holds on a work (with the voiced
// character for voice credits).
type PublicNameRole struct {
	RoleKey     string `json:"role_key"`
	RoleName    string `json:"role_name"`
	CharacterID int64  `json:"character_id,omitempty"`
	Character   string `json:"character,omitempty"`
}

// PublicNameCredit is one work a credited name is credited on, with its roles.
type PublicNameCredit struct {
	Work  PublicWorkBrief  `json:"work"`
	Roles []PublicNameRole `json:"roles"`
}

// PublicName is the frozen v1 credited-identity record (GET
// /v1/catalog/names/{id}; {id} is a credit-name id). It carries the same-person
// grouping (person_id + public siblings) via the existing link-visibility
// doctrine. credits are include-gated + keyset-less offset paginated.
type PublicName struct {
	ID       int64               `json:"id"`
	Name     PublicNameBuckets   `json:"name"`
	Latin    string              `json:"latin,omitempty"`
	PersonID int64               `json:"person_id,omitempty"`
	Siblings []PublicSiblingName `json:"siblings"`
	// Intros is the multilingual description set (wave 108): bridged at read
	// time from the credit name's OWN bangumi anchor (per-name provenance —
	// never a person-identity assertion; person resolution stays frozen).
	Intros []PublicNameIntro `json:"intros"`
	// Refs are this name's EXACT cross-source identity anchors (doc 106 G4).
	Refs       []PublicCatalogRef `json:"refs"`
	Credits    []PublicNameCredit `json:"credits,omitempty"`
	NextOffset *int               `json:"next_offset,omitempty"`
}

// PublicVoiceName is one credited name that voiced a character on a work.
type PublicVoiceName struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Lang  string `json:"lang"`
	Latin string `json:"latin,omitempty"`
}

// PublicCharacterWork is one work a character appears in, with its voice names.
type PublicCharacterWork struct {
	Work   PublicWorkBrief   `json:"work"`
	Voices []PublicVoiceName `json:"voices"`
}

// PublicCharacter is the frozen v1 character record (GET
// /v1/catalog/characters/{id}). works (appears-in) are include-gated.
type PublicCharacter struct {
	ID    int64             `json:"id"`
	Name  PublicNameBuckets `json:"name"`
	Latin string            `json:"latin,omitempty"`
	// Refs are this character's EXACT cross-source identity anchors (doc 106 G4).
	Refs       []PublicCatalogRef    `json:"refs"`
	Works      []PublicCharacterWork `json:"works,omitempty"`
	NextOffset *int                  `json:"next_offset,omitempty"`
	// Traits is the VNDB trait set (step 93) at or below the requested
	// spoilers ceiling (default 0 = safe); sexual-family traits are served
	// only with nsfw=1 (caller-controlled, wave 104). Always present.
	Traits []PublicCharacterTrait `json:"traits"`
	// Intros is the multilingual description set (wave 107): one element per
	// language (lowest source_id wins — the step-65 intro merge); vndb
	// spoiler spans were already stripped at import. Always present.
	Intros []PublicCharacterIntro `json:"intros"`
	// Image is the portrait as a complete CDN URL; omitted when the
	// character has no portrait.
	Image string `json:"image,omitempty"`
}

// PublicLabelWork is one work attributed to a label, with the attribution nature.
type PublicLabelWork struct {
	Work PublicWorkBrief `json:"work"`
	Kind string          `json:"kind"`
}

// PublicLabelIntro is one language's description of a label, merged to the
// winning source per language (lowest source_id wins — the step-65 intro merge).
// source is the catalog_source key (public-face convention — never the numeric
// source_id).
type PublicLabelIntro struct {
	Lang   string `json:"lang"`
	Intro  string `json:"intro"`
	Source string `json:"source"`
}

// PublicLabelLink is one non-identity web-presence link of a label (official
// site / twitter / ci-en), rendered as an absolute URL. It projects the label's
// entity_type=3, link_kind=related refs; the exact/probable identity anchors
// NEVER surface here (identity anchors and web links never cross). source is
// the catalog_source key.
type PublicLabelLink struct {
	Source string `json:"source"`
	URL    string `json:"url"`
}

// PublicLabel is the frozen v1 label record (GET /v1/catalog/labels/{id}).
// intros / links are always present (empty → [], never null); works (attributed)
// are include-gated.
type PublicLabel struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	// Lang is the display name's own language tag (BCP-47, A2-1e); empty when
	// unrecorded.
	Lang string `json:"lang,omitempty"`
	// Aliases are the label's alternate spellings (A2-1e, catalog_label_alias)
	// — the 12,935 rows the wiki's official aliases migrated into. Deduplicated,
	// display-name excluded, always present ([] when the label has none).
	Aliases []string `json:"aliases"`
	// WorkCount is NSFW-AWARE, exactly like the browse lane's (A2-1e): the
	// number of works this caller would page through via
	// works?label_id=&claim_state=live — the call an entity page makes (146).
	WorkCount int `json:"work_count"`
	// Refs are the EXACT identity anchors (doc 106 G4); links stays the
	// separate non-identity web-presence projection — the two never mix.
	Refs       []PublicCatalogRef `json:"refs"`
	Intros     []PublicLabelIntro `json:"intros"`
	Links      []PublicLabelLink  `json:"links"`
	Works      []PublicLabelWork  `json:"works,omitempty"`
	NextOffset *int               `json:"next_offset,omitempty"`
}

// PublicEntityHit is one entity-search hit (name / character / label / work /
// tag). id is numeric; entity_type routes the consumer to the right detail
// endpoint.
type PublicEntityHit struct {
	ID         int64    `json:"id"`
	EntityType string   `json:"entity_type"`
	Name       string   `json:"name"`
	Latin      string   `json:"latin,omitempty"`
	Sources    []string `json:"sources"`
	// ContentRating is present on works hits only (wave 105): all_ages |
	// sensitive | r18 (r18 hits appear only with nsfw=1).
	ContentRating string `json:"content_rating,omitempty"`
	// Tier / Kind are present on TAG hits only (A2-1d): the canonical tag's
	// display tier (core | longtail | hidden) and kind (content | meta), the
	// same vocabulary GET /v1/catalog/tags renders. Both are omitted on every
	// other family, which is what keeps the four pre-existing hit shapes
	// byte-identical to the frozen contract.
	Tier string `json:"tier,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// PublicEntitySearchData is the entity-search envelope.
type PublicEntitySearchData struct {
	Items []PublicEntityHit `json:"items"`
	Total int64             `json:"total"`
}

// ── wave-104 full-facet blocks (公开面全量数据; add-only) ──────────────────────

// PublicPopularity is one per-(source, metric) popularity counter. Values are
// verbatim per source — never summed across sources.
type PublicPopularity struct {
	Source string `json:"source"`
	Metric string `json:"metric" doc:"downloads|wishlist|reviews|bgm_wish|bgm_collect|bgm_doing|bgm_on_hold|bgm_dropped"`
	Value  int64  `json:"value"`
}

// PublicRating is one source-native rating (scale is source-native — a vndb 10
// scale and an erogamescape 100 scale are different units).
type PublicRating struct {
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	VoteCount int     `json:"vote_count"`
	Rank      *int    `json:"rank,omitempty"`
}

// PublicTag is one content tag, verbatim source folksonomy (no vocabulary
// mapping; count omitted when the source has no vote concept).
type PublicTag struct {
	Name   string `json:"name"`
	Count  int    `json:"count,omitempty"`
	Source string `json:"source"`
	// Canonical overlay (doc 106 G5, the step-74 vocabulary): present only
	// when this (source, name) maps into the canonical tag vocabulary —
	// unmapped tags omit all three keys (verbatim rendering as before).
	CanonicalID int64  `json:"canonical_id,omitempty"`
	Tier        string `json:"tier,omitempty" doc:"core|longtail|hidden"`
	Kind        string `json:"kind,omitempty" doc:"content|meta"`
	// ── the safety axis (A2-1e / R8) ────────────────────────────────────────
	//
	// Spoiler is this WORK-TAG EDGE's spoiler level: 0 none, 1 minor, 2 major.
	// Sexual flags the TAG ITSELF as belonging to the sexual-content category.
	// Both are always present.
	//
	// COVERAGE, stated plainly because a consumer must not read the default as
	// an assertion: the axis exists upstream only for the VNDB-derived tag
	// vocabulary (which is where every spoiler-worthy tag lives). Bangumi and
	// DLsite folksonomy rows carry no spoiler and no category concept at all.
	//
	// sexual therefore comes from the CANONICAL tag whenever this row maps into
	// that vocabulary (canonical_id present): a mapped bangumi/dlsite row
	// reports the canonical answer, not its source's silence. An UNMAPPED row
	// falls back to its raw source flag and renders false — that is the ABSENCE
	// of the axis, not a guarantee of safety. spoiler has no canonical
	// counterpart and stays per-edge, so 0 on folksonomy means absent likewise.
	//
	// Rows with spoiler > 0 appear ONLY when the caller opts in with
	// `spoilers=1|2` on the work detail; the default response contains none, so
	// a consumer that ignores this axis entirely still shows nothing spoiling.
	Spoiler int16 `json:"spoiler" doc:"0=none 1=minor 2=major"`
	Sexual  bool  `json:"sexual"`
	// WorkCount is the nsfw-aware taxonomy aggregate for the CANONICAL tag this
	// row maps to (A2-R1) — the number of works the caller reaches by following
	// the chip to works?tag_id=&claim_state=live, and the same number tags/{id}
	// reports.
	//
	// A POINTER, and deliberately so: the key is absent for an UNMAPPED tag
	// (no canonical_id → no landing page → no count to state, the same rule its
	// canonical_id / tier / kind already follow), but a mapped tag ALWAYS
	// carries it — including a genuine 0, which a bare int + omitempty would
	// have silently turned back into "unmapped".
	WorkCount *int `json:"work_count,omitempty"`
}

// PublicPlaytime is one per-source playtime estimate (minutes).
type PublicPlaytime struct {
	Source    string `json:"source"`
	Minutes   int    `json:"minutes"`
	VoteCount int    `json:"vote_count"`
}

// PublicSeries is one series membership.
type PublicSeries struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	MemberCount int    `json:"member_count"`
}

// PublicPlatform is one work-level platform row (release-level platforms ride
// on releases[]; consumers union the two grains).
type PublicPlatform struct {
	Platform string `json:"platform"`
	Source   string `json:"source"`
}

// PublicWorkIntro is one language's intro. machine=true flags an LLM machine
// translation (surfaced only when the language has no source row).
type PublicWorkIntro struct {
	Lang    string `json:"lang"`
	Intro   string `json:"intro"`
	Source  string `json:"source"`
	Machine bool   `json:"machine,omitempty"`
}

// PublicCover is one cover image — a complete CDN URL, never a bare hash.
// sexual/violence carry the source's content flags (0=safe 1=suggestive 2=explicit).
// width/height/thumbhash are the intrinsic display metadata resolved from
// image_service at read time (A2-1a); all three are omitted when the lookup is
// unwired or the hash is unknown, so a consumer falls back to a skeleton
// instead of reserving a wrong aspect ratio.
type PublicCover struct {
	URL            string `json:"url"`
	Kind           string `json:"kind,omitempty"`
	PortraitPinned bool   `json:"portrait_pinned"`
	Sexual         int16  `json:"sexual"`
	Violence       int16  `json:"violence"`
	Source         string `json:"source"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	Thumbhash      string `json:"thumbhash,omitempty"`
}

// PublicScreenshot is one screenshot — a complete CDN URL.
// width/height/thumbhash are the intrinsic display metadata resolved from
// image_service at read time (A2-1b, the same lookup covers[] rides); all
// three are omitted when the lookup is unwired or the hash is unknown, so a
// consumer falls back to a skeleton instead of reserving a wrong aspect ratio.
type PublicScreenshot struct {
	URL       string `json:"url"`
	Caption   string `json:"caption,omitempty"`
	Sexual    int16  `json:"sexual"`
	Violence  int16  `json:"violence"`
	Source    string `json:"source"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Thumbhash string `json:"thumbhash,omitempty"`
}

// PublicRosterVoice is one voice credit on a roster character (id is a
// credit-name id — GET /v1/catalog/names/{id}).
type PublicRosterVoice struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// PublicRosterCharacter is one character on the work's roster (id — GET
// /v1/catalog/characters/{id}).
type PublicRosterCharacter struct {
	ID      int64               `json:"id"`
	Name    string              `json:"name"`
	Latin   string              `json:"latin,omitempty"`
	Kind    string              `json:"kind" doc:"main|secondary|appears|unknown"`
	Spoiler int16               `json:"spoiler" doc:"0=none 1=minor 2=major"`
	Image   string              `json:"image,omitempty"`
	Voices  []PublicRosterVoice `json:"voices"`
}

// PublicWorkLabel is one attribution edge to a label (id — GET
// /v1/catalog/labels/{id}).
type PublicWorkLabel struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	LabelKind   string `json:"label_kind"`
	Kind        string `json:"kind" doc:"attribution nature: circle|publisher|developer|brand"`
	// Lang is the label name's own language tag (BCP-47, A2-1e) — a Japanese
	// brand name and its English rendering are different strings and a consumer
	// that renders both needs to know which is which. Empty when unrecorded.
	Lang string `json:"lang,omitempty"`
	// WorkCount is the nsfw-aware taxonomy aggregate (A2-R1) — the number of
	// works this caller reaches by following the chip to
	// works?label_id=&claim_state=live, and
	// the same number labels/{id} and the labels browse lane report. ALWAYS
	// present (never omitempty): every label chip is an addressable identity and
	// 0 is a real answer, so a missing key would be indistinguishable from a
	// consumer's own parse failure — which is how the deprecated face's
	// permanent "+ 0" got shipped in the first place.
	WorkCount int `json:"work_count"`
}

// PublicWorkEngine is one engine attribution on a work (A2-1e) — the id side
// of GET /v1/catalog/engines/{id}. Deliberately the minimal {id, name} pair:
// description / aliases live on the engine record, not on every work that used
// the engine.
type PublicWorkEngine struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// WorkCount is the nsfw-aware taxonomy aggregate (A2-R1) — the number of
	// works this caller reaches by following the chip to
	// works?engine_id=&claim_state=live, and
	// the same number engines/{id} and the engines browse lane report. Always
	// present, for the same reason labels[] carries it unconditionally.
	WorkCount int `json:"work_count"`
}

// PublicWorkLink is one non-identity external web link of a work (A2-1e, doc
// 126 D6), rendered as an absolute URL. Same {source, url} shape as
// PublicLabelLink — and the same rule: identity anchors (refs[]) never appear
// here, web links never appear in refs[].
//
// There is deliberately no label/title key: the wiki link rows carried a
// user-typed caption, and the retirement wave absorbed the URL bytes WITHOUT
// it. Inventing one would be fabrication, so consumers render the source key
// (or the host) instead.
type PublicWorkLink struct {
	Source string `json:"source"`
	URL    string `json:"url"`
}

// PublicRelease is one release row (date is partial ISO YYYY[-MM[-DD]]).
type PublicRelease struct {
	// ID is the stable catalog release id (doc 106 G3) — the addressable
	// identity the release-level anchors hang off.
	ID        int64    `json:"id"`
	Kind      string   `json:"kind" doc:"default|digital|physical|trial|patch"`
	Date      *string  `json:"date"`
	Title     string   `json:"title,omitempty"`
	Lang      string   `json:"lang,omitempty"`
	Platform  string   `json:"platform,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
	// Refs are this release's EXACT external anchors (dlsite workno / vndb
	// release id / steam appid …) — release-level identity material that was
	// previously visible only flattened into the work-level refs (doc 106 G3).
	Refs []PublicCatalogRef `json:"refs"`
}

// PublicCharacterTrait is one VNDB trait on a character (group is the root
// group's display name, "" when the trait itself is a root).
type PublicCharacterTrait struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group,omitempty"`
	Spoiler int16  `json:"spoiler" doc:"0=none 1=minor 2=major"`
	Sexual  bool   `json:"sexual"`
	Lie     bool   `json:"lie"`
}

// ── doc-106 W1: works list / changes feed / tag detail ──────────────────────

// PublicWorkListItem is one row of the works browse lane (GET
// /v1/catalog/works): the brief identity plus the list-page essentials so a
// consumer renders a row without a detail follow-up (doc 106 §4.4).
type PublicWorkListItem struct {
	ID            int64            `json:"id"`
	Medium        string           `json:"medium"`
	DisplayName   string           `json:"display_name"`
	ContentRating string           `json:"content_rating"`
	OLang         string           `json:"olang"`
	ReleaseDate   *string          `json:"release_date"`
	ClaimedBy     *PublicClaimedBy `json:"claimed_by"`
	// Cover is one representative cover as a complete CDN URL (portrait pin
	// first, then lowest sort order); omitted when the work has none the
	// caller may see (sfw callers never receive a sexual-flagged cover).
	Cover   string `json:"cover,omitempty"`
	Updated string `json:"updated"`

	// ── include= rich-brief blocks (A2-1a, refs/proj/126 D1/D7) ──────────
	// Every block is absent unless its token appears in include=, so the
	// default response is byte-identical to the frozen W1 contract. Each is
	// batch-loaded per page (never per row).
	Names   *PublicWorkNames      `json:"names,omitempty"`
	Intros  *PublicWorkIntros     `json:"intros,omitempty"`
	Labels  []PublicWorkLabel     `json:"labels,omitempty"`
	Ratings []PublicRating        `json:"ratings,omitempty"`
	Covers  *PublicWorkCoverSlots `json:"covers,omitempty"`
	// Refs (include=refs, A2-1e) are the work's EXACT identity anchors, the
	// same exact-only projection the detail record's refs[] carries — so a
	// consumer holding a page of rows can map every one of them onto its own
	// upstream ids (vndb / dlsite / bangumi) without a detail round-trip.
	// Release-level anchors flatten in exactly as they do on the detail face.
	Refs []PublicCatalogRef `json:"refs,omitempty"`
}

// PublicWorkNames is the D7 product-key title pivot (include=names): the
// catalog's BCP-47 title languages projected onto the four product keys the
// downstream faces render (ja-jp / zh-cn / zh-tw / en-us). A language outside
// those four is DROPPED — this block is a rendering convenience, not the
// complete title set (the detail face's titles[] stays the full projection).
// Every key is omitted when the work has no title for it.
type PublicWorkNames struct {
	JaJP string `json:"ja-jp,omitempty"`
	ZhCN string `json:"zh-cn,omitempty"`
	ZhTW string `json:"zh-tw,omitempty"`
	EnUS string `json:"en-us,omitempty"`
}

// PublicWorkIntroSlot is one product key's intro with its provenance. machine
// mirrors PublicWorkIntro.Machine: an LLM machine translation, which surfaces
// only when that language has no source row.
type PublicWorkIntroSlot struct {
	Intro   string `json:"intro"`
	Source  string `json:"source"`
	Machine bool   `json:"machine,omitempty"`
}

// PublicWorkIntros is the D7 product-key intro pivot (include=intros), same
// four keys and same "outside the four is dropped" rule as PublicWorkNames.
type PublicWorkIntros struct {
	JaJP *PublicWorkIntroSlot `json:"ja-jp,omitempty"`
	ZhCN *PublicWorkIntroSlot `json:"zh-cn,omitempty"`
	ZhTW *PublicWorkIntroSlot `json:"zh-tw,omitempty"`
	EnUS *PublicWorkIntroSlot `json:"en-us,omitempty"`
}

// PublicCoverSlot is one filled cover slot: a complete CDN URL plus the
// intrinsic display metadata (omitted when image_service has no entry) and the
// source's content flags.
type PublicCoverSlot struct {
	URL       string `json:"url"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Thumbhash string `json:"thumbhash,omitempty"`
	Sexual    int16  `json:"sexual"`
	Violence  int16  `json:"violence"`
	Source    string `json:"source"`
}

// PublicWorkCoverSlots is the two-slot cover projection (include=covers): the
// vertical key art a card renders and the horizontal art a hero banner
// renders. Either may be null when the work has no cover that fits the slot
// (and an sfw caller never sees a sexual-flagged cover in either). The two MAY
// point at the same image when a work carries only one usable cover.
type PublicWorkCoverSlots struct {
	Portrait *PublicCoverSlot `json:"portrait"`
	Banner   *PublicCoverSlot `json:"banner"`
}

// PublicWorksListData is the keyset works-list envelope. next_cursor is null
// on the last page.
type PublicWorksListData struct {
	Items      []PublicWorkListItem `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

// PublicChangeItem is one changes-feed entry: which entity changed and when.
type PublicChangeItem struct {
	EntityType string `json:"entity_type"`
	ID         int64  `json:"id"`
	Updated    string `json:"updated"`
}

// PublicChangesData is the changes-feed envelope (GET /v1/catalog/changes).
// next_cursor is ALWAYS present — it advances past the last row even on a
// short page, so an incremental consumer keeps polling the same cursor for
// new rows (the /v1/galgame/changes semantics, doc 106 G2).
type PublicChangesData struct {
	Items      []PublicChangeItem `json:"items"`
	NextCursor string             `json:"next_cursor"`
}

// PublicTagDetail is the canonical-tag record (GET /v1/catalog/tags/{id} —
// the step-74/87/90 cross-source vocabulary, doc 106 G5). works (tagged) are
// include-gated.
type PublicTagDetail struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tier string `json:"tier" doc:"core|longtail|hidden"`
	Kind string `json:"kind" doc:"content|meta"`
	// WorkCount is NSFW-AWARE, exactly like the browse lane's (A2-1e): the
	// number of works this caller would page through via
	// works?tag_id=&claim_state=live — the call an entity page makes (146).
	WorkCount int `json:"work_count"`
	// Sexual flags the tag itself as belonging to the sexual-content category
	// (A2-1f). Always present. See PublicTagListItem.Sexual for the coverage
	// caveat — false means "no such axis for this tag", NOT "confirmed safe".
	Sexual bool `json:"sexual"`
	// Intros is the multilingual description set (A2-1b), merged to one
	// element per language exactly like a label's. Always present ([] when the
	// tag carries none).
	Intros []PublicTagIntro `json:"intros"`
	// Works are the works carrying any source tag mapped to this canonical
	// tag (include=works; nsfw-gated briefs).
	Works      []PublicWorkBrief `json:"works,omitempty"`
	NextOffset *int              `json:"next_offset,omitempty"`
}

// PublicTagIntro is one language's description of a canonical tag, merged to
// the winning source per language (lowest source_id wins — the step-65 intro
// merge). source is the catalog_source key (public-face convention — never the
// numeric source_id). Same shape as PublicLabelIntro, deliberately its own type
// so the two vocabularies stay free to evolve apart.
type PublicTagIntro struct {
	Lang   string `json:"lang"`
	Intro  string `json:"intro"`
	Source string `json:"source"`
}

// PublicCharacterIntro is one language's description of a character (wave
// 107). source is the catalog_source key.
type PublicCharacterIntro struct {
	Lang   string `json:"lang"`
	Intro  string `json:"intro"`
	Source string `json:"source"`
}

// PublicNameIntro is one description of a credited name (wave 108). source is
// the catalog_source key.
type PublicNameIntro struct {
	Lang   string `json:"lang"`
	Intro  string `json:"intro"`
	Source string `json:"source"`
}

// ── A2-1b: the taxonomy browse lanes (labels / tags / engines) ───────────────
//
// work_count on every taxonomy row is NSFW-AWARE: it counts the works a caller
// with the SAME nsfw setting would actually page through via
// works?label_id=/tag_id=/engine_id=. A count and its own member list can never
// disagree — the deliberate opposite of the deprecated galgame face's
// official.galgame_count, which was permanently 0 next to a non-empty list.
//
// Since wave 146 the member call it equals carries claim_state=live, the gate an
// entity page passes (A2-R4): a count that also included unpublished drafts and
// unclaimed registry rows promised works nobody could reach. The nsfw axis stays
// the caller's and is untouched by that ruling.

// PublicLabelListItem is one row of the label browse lane (GET
// /v1/catalog/labels). Follow id to /v1/catalog/labels/{id} for the full
// record (intros / links / refs).
type PublicLabelListItem struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind" doc:"game_brand|bunko|publisher|anime_studio|doujin_circle|group|other"`
	WorkCount   int    `json:"work_count"`
}

// PublicLabelsListData is the keyset label-list envelope. next_cursor is null
// on the last page.
type PublicLabelsListData struct {
	Items      []PublicLabelListItem `json:"items"`
	NextCursor *string               `json:"next_cursor"`
	// Total is the size of the WHOLE filtered set (A2-1e) — the same filters
	// that produced items, so paging to exhaustion collects exactly `total`
	// rows. It is NOT nsfw-dependent: a label/tag/engine row is an identity,
	// not content, and nsfw only ever gates the per-row work_count.
	Total int64 `json:"total"`
}

// PublicTagListItem is one row of the canonical-tag browse lane (GET
// /v1/catalog/tags).
type PublicTagListItem struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Tier      string `json:"tier" doc:"core|longtail|hidden"`
	Kind      string `json:"kind" doc:"content|meta"`
	WorkCount int    `json:"work_count"`
	// Sexual flags the tag as belonging to the sexual-content category (A2-1f),
	// derived from the VNDB-descended wiki vocabulary through the identity
	// anchor A2-0 minted for each mapped tag. Always present.
	//
	// COVERAGE — read this before gating on it: only a canonical tag whose
	// vocabulary carries the category has the axis at all. One minted purely
	// from Bangumi / DLsite folksonomy has no category upstream and renders
	// false — the ABSENCE of the axis, NOT an assertion that the tag is safe.
	// This flag is the same one the work-detail tags[] axis inherits: a mapped
	// source row reports its canonical tag's value (see PublicTag.Sexual).
	Sexual bool `json:"sexual"`
}

// PublicTagsListData is the keyset tag-list envelope.
type PublicTagsListData struct {
	Items      []PublicTagListItem `json:"items"`
	NextCursor *string             `json:"next_cursor"`
	// Total — see PublicLabelsListData.Total.
	Total int64 `json:"total"`
}

// PublicEngineListItem is one row of the engine browse lane (GET
// /v1/catalog/engines).
type PublicEngineListItem struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	WorkCount int    `json:"work_count"`
	// Description / Aliases (A2-1e) are on the LIST row as well as the record:
	// the engine facet is a few hundred hand-curated rows that consumers render
	// as one page, so a second round-trip per row to fetch a one-line blurb
	// would be pure waste. Always present (description "" / aliases [] when the
	// row carries none).
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
}

// PublicEnginesListData is the keyset engine-list envelope.
type PublicEnginesListData struct {
	Items      []PublicEngineListItem `json:"items"`
	NextCursor *string                `json:"next_cursor"`
	// Total is the size of the WHOLE filtered set (A2-1e), not of this page —
	// so a consumer can render "N engines" without walking the cursor.
	Total int64 `json:"total"`
}

// ── A2-1c: the release-calendar buckets ─────────────────────────────────────

// PublicCalendarData is the envelope of all three calendar buckets (GET
// /v1/catalog/calendar, …/calendar/pending, …/calendar/tba).
//
// items are works-list rows VERBATIM (PublicWorkListItem, include= and all) —
// the calendar introduces no item field of its own, so a consumer renders a
// calendar row with exactly the code that renders a browse row, and the
// `release_date` it prints is the very value the bucket was decided by.
//
// month / year echo which window the server actually served: both parameters
// default to the CURRENT Asia/Tokyo month / year, so a caller that omitted them
// cannot otherwise know what it got. Each is present only on its own bucket.
// count is the whole bucket's size (the page is at most `limit` of it) and
// comes free from the ETag meta query.
type PublicCalendarData struct {
	Month      string               `json:"month,omitempty" doc:"YYYY-MM — the month bucket only"`
	Year       string               `json:"year,omitempty" doc:"YYYY — the pending bucket only"`
	Count      int64                `json:"count"`
	Items      []PublicWorkListItem `json:"items"`
	NextCursor *string              `json:"next_cursor"`
	// Meta is the navigation frame (A2-1e / R10) — always present.
	Meta PublicCalendarMeta `json:"meta"`
}

// PublicCalendarMeta is the calendar's navigation frame (A2-1e, R10): what a
// month-view UI needs to draw its prev/next arrows and to recover from an empty
// month WITHOUT probing months one at a time.
//
// min_month / max_month are computed under the CALLER'S OWN population gates
// (nsfw × olang) — the same gates that decide what lands in the bucket — so
// "the newest month that has anything in it" means the same thing as "the
// newest month YOU can see something in". An sfw caller and an nsfw caller can
// legitimately get different bounds.
//
// The pending and tba buckets carry `today` only: they are not month-addressed,
// so month bounds and prev/next are meaningless there and the four keys are
// omitted rather than filled with something arbitrary.
type PublicCalendarMeta struct {
	// Today is the current Asia/Tokyo civil date (YYYY-MM-DD) — the same
	// timezone that decides the default month/year. Always present, on all
	// three buckets.
	Today string `json:"today"`
	// MinMonth / MaxMonth are the earliest / latest YYYY-MM that has at least
	// one member under these gates; both omitted when the whole population is
	// empty (there is then no month to jump to).
	MinMonth string `json:"min_month,omitempty"`
	MaxMonth string `json:"max_month,omitempty"`
	// HasPrev / HasNext say whether a non-empty month exists before / after the
	// REQUESTED month — derived from the bounds, so `has_next=false` is a real
	// end-of-data signal and not merely "the next month happens to be empty".
	// Month bucket only.
	HasPrev *bool `json:"has_prev,omitempty"`
	HasNext *bool `json:"has_next,omitempty"`
}

// PublicEngine is the engine record (GET /v1/catalog/engines/{id}) — the
// visual-novel / game engine a work was built with. VNDB publishes no engine
// data, so this facet's only copy is the hand-curated wiki one the
// data-layer-retirement wave migrated. refs are exact-only identity anchors,
// like every other entity's (doc 106 G4).
type PublicEngine struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	WorkCount int    `json:"work_count"`
	// Description is the engine blurb verbatim from the source (A2-1e); ""
	// when the row carries none. Aliases are its alternate spellings ([] when
	// none). Both are always present — the data has been in catalog_engine
	// since the retirement wave migrated it; this wave simply stops hiding it.
	Description string             `json:"description"`
	Aliases     []string           `json:"aliases"`
	Refs        []PublicCatalogRef `json:"refs"`
}

// ── A2-1d: the works product search ─────────────────────────────────────────

// PublicWorksSearchData is the envelope of GET /v1/catalog/works/search.
//
// items are works-list rows VERBATIM (PublicWorkListItem, include= and all), so
// a consumer renders a search result with exactly the code that renders a
// browse row — the Meilisearch documents behind the ranking never reach the
// wire.
//
// total is the size of the WHOLE filtered set under the same gate that produced
// items and facets — page through it and the rows you collect sum to total.
// (The deprecated wiki search did NOT hold this: its total ignored the sfw
// filter that its items applied.)
//
// page / limit echo the window actually served, so a caller that omitted them
// still knows where it is.
type PublicWorksSearchData struct {
	Total int64                `json:"total"`
	Page  int                  `json:"page"`
	Limit int                  `json:"limit"`
	Items []PublicWorkListItem `json:"items"`
	// Facets is present only when facets= asked for it. Outer keys are the
	// FILTER PARAMETER names the values can be fed straight back into
	// (tag_id, content_rating, …); inner keys are the values, counted over the
	// same filtered set as total. content_rating counts are keyed by the
	// public strings (all_ages | sensitive | r18), never the enum ints.
	Facets map[string]map[string]int64 `json:"facets,omitempty"`
}
