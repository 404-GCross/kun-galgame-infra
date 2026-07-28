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
	ID            int64                `json:"id"`
	Medium        string               `json:"medium"`
	DisplayName   string               `json:"display_name"`
	OLang         string               `json:"olang"`
	ContentRating string               `json:"content_rating"`
	ReleaseDate   *string              `json:"release_date"`
	Titles        []PublicCatalogTitle `json:"titles"`
	Refs          []PublicCatalogRef   `json:"refs"`
	ClaimedBy     *PublicClaimedBy     `json:"claimed_by"`
	Relations     []PublicRelation     `json:"relations,omitempty"`
	Credits       []PublicCreditGroup  `json:"credits,omitempty"`
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
}

// PublicLookupData is the external-id reverse-lookup result (GET
// /v1/catalog/lookup). work/claimed_by are null-free on a hit; the miss/hidden
// case is a 404 (design §3.1). Exact anchors only.
type PublicLookupData struct {
	Work      *PublicWorkBrief `json:"work"`
	ClaimedBy *PublicClaimedBy `json:"claimed_by"`
}

// PublicLookupPair is one (source, external_id) request in a batch lookup.
type PublicLookupPair struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
}

// PublicLookupBatchRequest is the batch reverse-lookup body (≤100 pairs).
type PublicLookupBatchRequest struct {
	Items []PublicLookupPair `json:"items"`
}

// PublicLookupBatchItem echoes the request pair and carries its resolution;
// work is null for a miss / hidden work (order preserved, 裁定 3).
type PublicLookupBatchItem struct {
	Source     string           `json:"source"`
	ExternalID string           `json:"external_id"`
	Work       *PublicWorkBrief `json:"work"`
	ClaimedBy  *PublicClaimedBy `json:"claimed_by"`
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
	ID         int64               `json:"id"`
	Name       PublicNameBuckets   `json:"name"`
	Latin      string              `json:"latin,omitempty"`
	PersonID   int64               `json:"person_id,omitempty"`
	Siblings   []PublicSiblingName `json:"siblings"`
	Credits    []PublicNameCredit  `json:"credits,omitempty"`
	NextOffset *int                `json:"next_offset,omitempty"`
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
	ID         int64                 `json:"id"`
	Name       PublicNameBuckets     `json:"name"`
	Latin      string                `json:"latin,omitempty"`
	Works      []PublicCharacterWork `json:"works,omitempty"`
	NextOffset *int                  `json:"next_offset,omitempty"`
	// Traits is the VNDB trait set (step 93) at or below the requested
	// spoilers ceiling (default 0 = safe); sexual-family traits are served
	// only with nsfw=1 (caller-controlled, wave 104). Always present.
	Traits []PublicCharacterTrait `json:"traits"`
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
	ID          int64              `json:"id"`
	DisplayName string             `json:"display_name"`
	Kind        string             `json:"kind"`
	Intros      []PublicLabelIntro `json:"intros"`
	Links       []PublicLabelLink  `json:"links"`
	Works       []PublicLabelWork  `json:"works,omitempty"`
	NextOffset  *int               `json:"next_offset,omitempty"`
}

// PublicEntityHit is one entity-search hit (person / character / label). id is
// numeric; entity_type routes the consumer to the right detail endpoint.
type PublicEntityHit struct {
	ID         int64    `json:"id"`
	EntityType string   `json:"entity_type"`
	Name       string   `json:"name"`
	Latin      string   `json:"latin,omitempty"`
	Sources    []string `json:"sources"`
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
type PublicCover struct {
	URL            string `json:"url"`
	Kind           string `json:"kind,omitempty"`
	PortraitPinned bool   `json:"portrait_pinned"`
	Sexual         int16  `json:"sexual"`
	Violence       int16  `json:"violence"`
	Source         string `json:"source"`
}

// PublicScreenshot is one screenshot — a complete CDN URL.
type PublicScreenshot struct {
	URL      string `json:"url"`
	Caption  string `json:"caption,omitempty"`
	Sexual   int16  `json:"sexual"`
	Violence int16  `json:"violence"`
	Source   string `json:"source"`
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
}

// PublicRelease is one release row (date is partial ISO YYYY[-MM[-DD]]).
type PublicRelease struct {
	Kind      string   `json:"kind" doc:"default|digital|physical|trial|patch"`
	Date      *string  `json:"date"`
	Title     string   `json:"title,omitempty"`
	Lang      string   `json:"lang,omitempty"`
	Platform  string   `json:"platform,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
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
