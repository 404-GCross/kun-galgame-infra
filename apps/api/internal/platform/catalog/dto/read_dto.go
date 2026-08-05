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
	// Characters is the work's roster (step 46): the roster-edge appearance set
	// (catalog_work_character) UNIONed with any voice-credited characters. A
	// credit-only character (voiced but with no roster edge — e.g. VNDB-only VA
	// credits) still surfaces with kind=0. Sorted main-first then display name.
	Characters []WorkCharacter `json:"characters"`
	// Intro is the work's multilingual intro (step 52 media-aggregation pilot):
	// one element per language, merged to a single shape regardless of source. A
	// CLAIMED work's intro is bridged from its galgame body (galgame.intro_*); a
	// BODYLESS work's from its catalog_work_intro rows. Strict XOR: a claimed
	// work with no galgame intro yields [] (never a fallback to native rows).
	Intro []WorkIntro `json:"intro"`
	// Covers is the work's cover set (step 53 media-aggregation wave II), one
	// element per cover in a single shape regardless of source: the work's
	// catalog_work_cover rows, claimed and bodyless alike (wave 164 retired the
	// galgame_cover bridge — W1-pre step m had already mirrored the wiki covers
	// into the native table). The PORTRAIT covers (portrait_pinned=true) are what
	// kungal/moyu read for the portrait-first UI.
	Covers []WorkCover `json:"covers"`
	// Screenshots is the work's screenshot set (step 54 media-aggregation wave
	// III + refs/proj/125), one element per screenshot in a single shape
	// regardless of source: the work's catalog_work_screenshot rows, claimed and
	// bodyless alike (wave 164 collapsed the bridge ∪ native union onto the native
	// lane, the rescued wiki rows having landed there at wikirescue step n).
	// source_id keeps the lanes — rescued wiki, dlsite store samples — attributable.
	Screenshots []WorkScreenshot `json:"screenshots"`
	// Ratings is the work's rating set (step 58a media-aggregation ratings
	// facet), at most one element per source, in a single shape regardless of
	// source. Scores are SOURCE-NATIVE (bangumi 0-10 mean, erogamespace 0-100
	// median) — consumers render per source_id, never mix scales. A CLAIMED
	// work's ratings are bridged from galgame_bangumi_meta ∪ galgame_eg_meta; a
	// BODYLESS work's from its catalog_work_rating rows. Strict XOR: a claimed
	// work with no scored meta yields [] (never a fallback to native rows).
	Ratings []WorkRating `json:"ratings"`
	// Tags is the work's content-tag set (step 58b + T2 media-aggregation tags
	// facet), one element per tag name per source, in a single shape regardless
	// of source. Names are VERBATIM (no vocabulary mapping). Under the (facet,
	// source) XOR (T2), a work's tags are the UNION of two per-source lanes: the
	// WIKI bridge lane (galgame_wiki/vndb, localized display names, non-spoiler
	// only — CLAIMED works only) and the CATALOG-native lane (bangumi/dlsite,
	// catalog_work_tag rows — ALL works). A claimed work therefore surfaces both
	// its wiki tags and its bgm folksonomy; a bodyless work degenerates to the
	// native lane. Ordered (count DESC, name) after the merge. Distinct from
	// labels — labels are the attribution vocabulary (organizational
	// responsibility), tags are content description.
	Tags []WorkTag `json:"tags"`
	// Popularity is the work's per-metric popularity counter set (step 62), at
	// most one element per (source, metric), in a single shape regardless of
	// source. Values are VERBATIM source counters — a DLsite sale and a future
	// Bangumi favorite are different units, so consumers render per
	// (source_id, metric) and never sum across sources. A CLAIMED work's
	// counters are bridged from galgame_dlsite_meta (downloads / wishlist /
	// reviews); a BODYLESS work's from its catalog_work_popularity rows.
	// Strict XOR: a claimed work with no published counter yields [] (never a
	// fallback to native rows).
	Popularity []WorkPopularity `json:"popularity"`
	// Playtimes is the work's per-source playtime estimate set (step 91), at
	// most one element per source. minutes is normalized to MINUTES (a unit
	// conversion on ingest: EG hours ×60; vndb c_length is native minutes) —
	// the estimate semantics stay source-native (vndb = vote-backed median,
	// erogamespace = community median), so consumers render per source_id.
	// This facet has NO claimed bridge: every work reads its native rows.
	Playtimes []WorkPlaytime `json:"playtimes"`
	// Series is the work's series memberships (step 94): first-class series
	// entities (dlsite lane first), each with its display name and total
	// anchored member count. Catalog-native — no claimed bridge (the wiki
	// family's galgame_series vocabulary lives on its own face).
	Series []WorkSeries `json:"series"`
	// Platforms is the work's explicit work-level platform set (step 96):
	// catalog_work_platform rows — today the bgm lane (Bangumi asserts
	// platforms on the subject; its bodyless works mostly have no releases).
	// Release-level platforms ride on releases[].platform / .platforms;
	// consumers union the two grains as needed. Codes are catalog_platform
	// registry keys (VNDB codes: win/and/ios/…).
	Platforms []WorkPlatform `json:"platforms"`
	// Relations is the work's cross-media relation edge set (wave 104), both
	// directions from this work's perspective. The internal face carries r18
	// ends verbatim (the public face gates them behind nsfw).
	Relations []WorkRelation `json:"relations"`
	// SeriesSiblings is the transitive-closure series membership (wave 113):
	// every other live work reachable through same_series edges, so a leaf work
	// sees its whole series family (vndb never materializes a series entity —
	// its series is the pairwise same_series relation). Complementary to
	// series[] (first-class catalog_series, dlsite lane).
	SeriesSiblings []WorkSeriesSibling `json:"series_siblings"`
}

// WorkPlatform is one explicit work-level platform assertion (step 96).
type WorkPlatform struct {
	Platform string `json:"platform" doc:"catalog_platform registry code (win/and/ios/…)"`
	SourceID int16  `json:"source_id" doc:"catalog_source id that asserted it"`
}

// WorkSeries is one series a work belongs to (step 94).
type WorkSeries struct {
	ID          int64  `json:"id"`
	Name        string `json:"name" doc:"series display name, verbatim from the source (dlsite series_name)"`
	SourceID    int16  `json:"source_id" doc:"catalog_source id the series was materialized from"`
	MemberCount int    `json:"member_count" doc:"total anchored works in this series"`
}

// WorkPopularity is one (source, metric) popularity counter on a work, in the
// unified media-aggregation shape. metric is the PopularityMetric* vocabulary
// (0=downloads 1=wishlist 2=reviews; extensible — new counting facets append
// constants). value is the raw counter verbatim from the source; an
// unpublished counter has NO element (absent ≠ 0), while a source-published 0
// does.
type WorkPopularity struct {
	SourceID int16 `json:"source_id" doc:"catalog_source id (provenance + unit selector): counters are source-relative and never summed across sources"`
	Metric   int16 `json:"metric" doc:"popularity metric: 0=downloads 1=wishlist 2=reviews (extensible vocabulary)"`
	Value    int64 `json:"value" doc:"raw counter verbatim from the source; a published 0 is a real value, an unpublished counter has no element"`
}

// WorkPlaytime is one source's playtime estimate on a work, in the unified
// media-aggregation shape (step 91).
type WorkPlaytime struct {
	SourceID  int16 `json:"source_id" doc:"catalog_source id (provenance): vndb = vote-backed median, erogamespace = community median"`
	Minutes   int   `json:"minutes" doc:"median playtime in minutes (unit-normalized; estimate semantics stay source-native)"`
	VoteCount int   `json:"vote_count" doc:"user reports backing the estimate; 0 = the source publishes no per-work count"`
}

// WorkTag is one content tag on a work, in the unified media-aggregation shape.
// name is verbatim source text: the galgame wiki display name (zh-localized
// where the VNDB tagMap covers it) for a bridged claimed tag, the raw Bangumi
// folksonomy name for a bodyless tag. count is the source's vote count, absent
// when the source has no votes (the whole galgame tag layer carries none).
//
// Canonical overlay (step 74, additive & optional): when this tag's
// (source_id, name) is mapped into the cross-source canonical vocabulary,
// canonical_id/tier/kind carry the canonical row's identity + display tier +
// content/meta kind — a consumer filters/renders by them ("取精华": show tier=0,
// fold longer tiers; route kind=meta to an attribute filter instead of the tag
// cloud). An UNMAPPED tag omits all three, and the verbatim name/count/source_id
// are never altered (the overlay never masks the original per-source data).
type WorkTag struct {
	Name     string `json:"name" doc:"tag text, verbatim from the source (no vocabulary mapping)"`
	Count    int    `json:"count,omitempty" doc:"source vote count backing the tag; absent when the source has no votes (bridged galgame wiki tags)"`
	SourceID int16  `json:"source_id" doc:"catalog_source id (provenance): galgame_wiki/vndb for a bridged claimed tag, the backfill source (bangumi) for a bodyless tag"`
	// CanonicalID/Tier/Kind are the additive canonical overlay (step 74); absent
	// when the tag is not in the canonical vocabulary.
	CanonicalID *int64 `json:"canonical_id,omitempty" doc:"catalog_tag id when this (source_id, name) is mapped into the canonical vocabulary; absent otherwise"`
	Tier        *int16 `json:"tier,omitempty" doc:"canonical display tier (0=core 1=longtail 2=hidden); absent when unmapped"`
	Kind        *int16 `json:"kind,omitempty" doc:"canonical kind (0=content 1=meta/platform-attribute); absent when unmapped"`
}

// WorkRating is one source's rating on a work, in the unified media-aggregation
// shape. score is on the SOURCE-NATIVE scale — vndb (source key `vndb`) is a
// 1-10 mean, bangumi (key `bangumi`) a 0-10 mean, dlsite (key `dlsite`) a 0-5
// star mean, erogamespace (key `erogamespace`) a 0-100 median — so a consumer
// must branch on source_id to render it. rank is the source-internal rank,
// absent when the source has none (vndb meta / dlsite / EG) or the work is
// unranked there (bangumi rank 0).
type WorkRating struct {
	SourceID  int16   `json:"source_id" doc:"catalog_source id (provenance + scale selector): vndb = 1-10 mean, bangumi = 0-10 mean, dlsite = 0-5 star mean, erogamespace = 0-100 median"`
	Score     float64 `json:"score" doc:"rating on the source-native scale (never normalized across sources)"`
	VoteCount int     `json:"vote_count" doc:"number of ratings backing the score"`
	Rank      *int    `json:"rank,omitempty" doc:"source-internal rank; absent when the source has no rank or the work is unranked"`
}

// WorkScreenshot is one screenshot image on a work, in the unified
// media-aggregation shape, projected from catalog_work_screenshot. image_hash
// keys the bytes in the image service. Unlike WorkCover it carries a caption and
// has no kind / portrait_pinned. source_id references the catalog_source
// registry (§8.C) and stays the lane discriminator now that the rescued wiki
// rows (galgame_wiki / vndb) and the dlsite store samples share one table.
type WorkScreenshot struct {
	ImageHash string `json:"image_hash"`
	Caption   string `json:"caption"`
	SortOrder int    `json:"sort_order" doc:"gallery position"`
	// Sexual/Violence are per-image content-rating flags: 0=safe 1=suggestive 2=explicit.
	Sexual   int16 `json:"sexual" doc:"content flag: 0=safe 1=suggestive 2=explicit"`
	Violence int16 `json:"violence" doc:"content flag: 0=safe 1=suggestive 2=explicit"`
	SourceID int16 `json:"source_id" doc:"catalog_source id (provenance): galgame_wiki/vndb for a bridged claimed screenshot, the backfill source for a bodyless screenshot"`
}

// WorkCover is one cover image on a work, in the unified media-aggregation
// shape, projected from catalog_work_cover. image_hash keys the bytes in the
// image service. portrait_pinned marks the vertical portrait pin. source_id
// references the catalog_source registry (§8.C) and carries the provenance the
// row was mirrored or backfilled with (curated / vndb / bangumi / upscale).
type WorkCover struct {
	ImageHash string `json:"image_hash"`
	// Kind labels the cover type (main / pkgfront / dig / …); empty string =
	// unknown / user upload (galgame_cover semantics).
	Kind           string `json:"kind"`
	PortraitPinned bool   `json:"portrait_pinned" doc:"true = the vertical portrait pin (portrait-first UI)"`
	SortOrder      int    `json:"sort_order" doc:"gallery/pin position; 0 = pinned landscape banner"`
	// Sexual/Violence are per-image content-rating flags: 0=safe 1=suggestive 2=explicit.
	Sexual   int16 `json:"sexual" doc:"content flag: 0=safe 1=suggestive 2=explicit"`
	Violence int16 `json:"violence" doc:"content flag: 0=safe 1=suggestive 2=explicit"`
	SourceID int16 `json:"source_id" doc:"catalog_source id (provenance): galgame_wiki/vndb/bangumi/upscale for a bridged claimed cover, the backfill source for a bodyless cover"`
}

// WorkIntro is one language's intro on a work, with its provenance. source_id
// references the catalog_source registry (bridged claimed intros carry the
// galgame_wiki source; bodyless intros carry the backfill source, e.g. vndb).
type WorkIntro struct {
	Lang     string `json:"lang"`
	Intro    string `json:"intro"`
	SourceID int16  `json:"source_id" doc:"catalog_source id (provenance): e.g. galgame_wiki for a bridged claimed work, vndb for a bodyless backfill"`
	// Machine flags an LLM machine-translated intro (step 75). Present+true only
	// when the surfaced row is a machine translation (the language has no source
	// row); omitted for source/bridged rows so consumers can render an "MT" badge.
	Machine bool `json:"machine,omitempty" doc:"true if this intro is an LLM machine translation (no source text for this language); omitted for source/bridged rows"`
}

// WorkCharacter is one character on a work's roster, merging the appearance
// edge (catalog_work_character, which carries kind) with any voice credits
// (catalog_credit.character_id, which carry the voicing name). kind is 0 for a
// credit-only character that has no roster edge. va lists every credited name
// that voiced it — the CREDIT NAME nominal only; the link-visibility doctrine
// gates person aggregation, never the nominal itself, and no person is expanded
// here (link-visibility 铁则, step 19).
type WorkCharacter struct {
	CharacterID int64  `json:"character_id"`
	DisplayName string `json:"display_name"`
	Latin       string `json:"latin,omitempty"`
	// Gender: 1=male 2=female 3=other; absent = unknown (there is no 0 value).
	Gender int16 `json:"gender,omitempty"`
	// Kind is the appearance strength from the roster edge: 0=unknown 1=main
	// 2=secondary 3=appears (0 also when the character is reached only via a
	// voice credit and thus has no roster edge).
	Kind int16 `json:"kind" doc:"appearance strength: 0=unknown 1=main 2=secondary 3=appears"`
	// Spoiler is the roster edge's per-appearance spoiler level: 0=none 1=minor
	// 2=major (VNDB source; 0 for Bangumi/EG edges and for a credit-only
	// character with no roster edge).
	Spoiler int16 `json:"spoiler" doc:"appearance spoiler level: 0=none 1=minor 2=major"`
	// ImageHash is the BUST portrait content hash in the image service; absent
	// until the step-47 VNDB portrait wave backfills it.
	ImageHash string `json:"image_hash,omitempty"`
	// FigureHash is the FULL-BODY standing art content hash. A different asset
	// from ImageHash, not a fallback for it: render it at its own aspect
	// (preset "character_figure"), never cropped into a portrait box.
	FigureHash string            `json:"figure_hash,omitempty"`
	Va         []WorkCharacterVA `json:"va"`
}

// WorkCharacterVA is one credited name that voiced a character on this work
// (the nominal only — no person expansion).
type WorkCharacterVA struct {
	CreditNameID int64  `json:"credit_name_id"`
	Name         string `json:"name"`
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
	ID        int64 `json:"id"`
	Kind      int16 `json:"kind" doc:"0=default 1=digital 2=physical 3=trial 4=patch"`
	ReleasedY int16 `json:"released_y,omitempty"`
	ReleasedM int16 `json:"released_m,omitempty"`
	ReleasedD int16 `json:"released_d,omitempty"`
	// Platform is the release's primary platform code (step 96;
	// catalog_platform registry key, e.g. win). Empty when unknown.
	Platform string `json:"platform,omitempty"`
	// Platforms is the release's full platform code set (extra.platforms —
	// the step-76/96 shape); a multi-platform release lists every port.
	Platforms []string    `json:"platforms,omitempty"`
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
	ID            string   `json:"id" doc:"prefixed: n{id} / c{id} / b{id}"`
	EntityType    string   `json:"entity_type"`
	Name          string   `json:"name"`
	Latin         string   `json:"latin,omitempty"`
	Sources       []string `json:"sources"`
	Popularity    float64  `json:"popularity"`
	Kind          *int16   `json:"kind,omitempty" doc:"label kind (labels only)"`
	PersonID      *int64   `json:"person_id,omitempty" doc:"credit-name → person link (names only; absent = orphan)"`
	ContentRating *int16   `json:"content_rating,omitempty" doc:"works only (wave 105): 0=all_ages 1=sensitive 2=r18"`
	// LogoHash is the brand logo's content hash in the image service (labels
	// only, wave 170) — the same currency as a work cover's image_hash. It is
	// hydrated from Postgres by id, NOT carried by the search index. Absent =
	// the label has no logo (or the hit is not a label).
	LogoHash string `json:"logo_hash,omitempty" doc:"labels only: brand logo content hash in the image service; absent = none"`
}

// --- entity reverse-lookups (step 19: names/{id}/works & characters/{id}/works) ---

// NameBuckets holds a name in exactly ONE language bucket — a credit name or
// character lives in a single language (search invariant 1: never mix zh/ja).
// Only the matching bucket is populated; consumers pick by their UI locale.
// Used for the entity SELF-DESCRIPTION heads (and sibling names); works-list
// annotations stay flat (name+lang), mirroring the credits endpoint.
type NameBuckets struct {
	Ja    string `json:"ja,omitempty"`
	Zh    string `json:"zh,omitempty"`
	Other string `json:"other,omitempty"`
}

// WorkBrief is the lightweight work projection shared by the reverse-lookup
// rows: identity + claim state, no body (the body lives in a product DB).
type WorkBrief struct {
	WorkID        int64  `json:"work_id"`
	DisplayName   string `json:"display_name"`
	MediumID      int16  `json:"medium_id"`
	ContentRating int16  `json:"content_rating" doc:"0=all_ages 1=sensitive 2=r18"`
	Status        int16  `json:"status" doc:"0=live 1=stub 2=merged"`
	// Site is the claiming tenant key; empty = unclaimed (an R2 registry row).
	Site string `json:"site,omitempty"`
}

// NameWorksResponse is a credit name's self-description plus the works it is
// credited on, offset-paginated. Name is always present (404 on a missing id).
type NameWorksResponse struct {
	Name  NameHead      `json:"name"`
	Items []NameWorkRow `json:"items"`
	Total int64         `json:"total"`
}

// NameHead is a credit name's own identity, returned so the entity page is
// self-sufficient on direct navigation. PersonID + Siblings expose the SAME
// person's other names (so the consumer's person page needs no second query) —
// but ONLY when this name's person link is public. A hidden link surfaces
// neither (the name appears as an independent identity — the link-visibility
// doctrine, model.LinkVisibility).
type NameHead struct {
	ID       int64         `json:"id"`
	Name     NameBuckets   `json:"name"`
	Latin    string        `json:"latin,omitempty"`
	PersonID int64         `json:"person_id,omitempty" doc:"same-person group id (public links only; absent = orphan or hidden)"`
	Siblings []SiblingName `json:"siblings" doc:"the same person's other public-linked names"`
	// The person block (wave 172) rides EXACTLY the PersonID gate: a hidden
	// credit_name→person link withholds all five, because a photograph and a
	// birthday are person facts and publishing them under a hidden link is the
	// same disclosure the link is being withheld to prevent.
	//
	// PhotoHash is ALWAYS present (never omitempty): "" is the real answer
	// "no photo here", and a missing key would be indistinguishable from a
	// consumer's parse failure.
	PhotoHash string `json:"photo_hash" doc:"person photo content hash in the image service; \"\" = no photo (or the person link is hidden)"`
	Gender    *int16 `json:"gender,omitempty" doc:"person gender code; absent = unknown, orphan or hidden link"`
	BirthY    *int16 `json:"birth_y,omitempty" doc:"fuzzy birth date, year; absent = not recorded at this precision"`
	BirthM    *int16 `json:"birth_m,omitempty" doc:"fuzzy birth date, month"`
	BirthD    *int16 `json:"birth_d,omitempty" doc:"fuzzy birth date, day"`
}

// SiblingName is another credit name of the same person (public links only).
type SiblingName struct {
	ID    int64       `json:"id"`
	Name  NameBuckets `json:"name"`
	Latin string      `json:"latin,omitempty"`
}

// NameWorkRow is one work a name is credited on, with every role it holds there.
type NameWorkRow struct {
	Work  WorkBrief      `json:"work"`
	Roles []NameWorkRole `json:"roles"`
}

// NameWorkRole is one role the name holds on a work; Character is set when the
// credit is a voice credit tied to a character.
type NameWorkRole struct {
	RoleID      int64  `json:"role_id"`
	RoleKey     string `json:"role_key"`
	RoleName    string `json:"role_name"`
	CharacterID int64  `json:"character_id,omitempty"`
	Character   string `json:"character,omitempty"`
}

// CharacterWorksResponse is a character's self-description plus the works it
// appears in, offset-paginated. Character is always present (404 on a miss).
type CharacterWorksResponse struct {
	Character CharacterHead      `json:"character"`
	Items     []CharacterWorkRow `json:"items"`
	Total     int64              `json:"total"`
}

// CharacterHead is a character's own identity (self-sufficient on direct nav).
type CharacterHead struct {
	ID    int64       `json:"id"`
	Name  NameBuckets `json:"name"`
	Latin string      `json:"latin,omitempty"`
}

// CharacterWorkRow is one work a character appears in — the UNION of the roster
// edge (appearance) and voice credits (step 46). Kind is the roster edge's
// appearance strength (0 when the work is reached only via a voice credit and
// thus has no edge); Voiced marks whether a voice credit names the character
// here (so a consumer can tell an appearance-only row from a voiced one even
// when kind is 0). Voices lists the voicing name(s).
type CharacterWorkRow struct {
	Work    WorkBrief   `json:"work"`
	Kind    int16       `json:"kind" doc:"roster appearance strength: 0=unknown 1=main 2=secondary 3=appears (0 also when reached only via a voice credit)"`
	Spoiler int16       `json:"spoiler" doc:"roster appearance spoiler level: 0=none 1=minor 2=major (0 also when reached only via a voice credit)"`
	Voiced  bool        `json:"voiced" doc:"true when a voice credit names this character on this work"`
	Voices  []VoiceName `json:"voices"`
}

// --- character detail (step 46: GET /catalog/characters/{id}) ---

// CharacterDetailResponse is a character's full self-description: identity
// fields + aliases. The roster/voice reverse (which works it appears in) lives
// at characters/{id}/works.
type CharacterDetailResponse struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Latin       string `json:"latin,omitempty"`
	Lang        string `json:"lang"`
	// Gender: 1=male 2=female 3=other; absent = unknown (there is no 0 value).
	Gender      int16  `json:"gender,omitempty"`
	Description string `json:"description,omitempty"`
	// InstanceOf is the base character id for a cross-universe variant (VNDB
	// instance_of); absent when the character is not a variant.
	InstanceOf int64 `json:"instance_of,omitempty"`
	// ImageHash is the BUST portrait content hash in the image service; absent
	// until the step-47 VNDB portrait wave backfills it.
	ImageHash string `json:"image_hash,omitempty"`
	// FigureHash is the FULL-BODY standing art content hash. A different asset
	// from ImageHash, not a fallback for it: render it at its own aspect
	// (preset "character_figure"), never cropped into a portrait box.
	FigureHash string `json:"figure_hash,omitempty"`
	// --- typical-set physical attributes (step 81 field PR C2) ---
	// The display-core attribute set, flattened from the real typed columns;
	// each is absent when unknown (there is no meaningful zero). BirthdayMonth/
	// Day are month+day only (a source-supplied year lives in Extra["bgm"]).
	// BloodType is 1=A 2=B 3=AB 4=O. Cup is the verbatim upper-cased cup token.
	BirthdayMonth int16  `json:"birthday_month,omitempty"`
	BirthdayDay   int16  `json:"birthday_day,omitempty"`
	BloodType     int16  `json:"blood_type,omitempty" doc:"1=A 2=B 3=AB 4=O; absent = unknown"`
	HeightCm      int16  `json:"height_cm,omitempty"`
	WeightKg      int16  `json:"weight_kg,omitempty"`
	BustCm        int16  `json:"bust_cm,omitempty"`
	WaistCm       int16  `json:"waist_cm,omitempty"`
	HipCm         int16  `json:"hip_cm,omitempty"`
	Cup           string `json:"cup,omitempty"`
	// Extra is the governed long-tail, source-namespaced ({"bgm": {…}}) — the
	// Bangumi infobox fields that did not promote to a typed column (星座/年龄/
	// 属性/趣味 …, plus a birthday year or an out-of-range measurement preserved
	// verbatim). Absent when the character has no long-tail. Values are strings
	// or string arrays.
	Extra map[string]any `json:"extra,omitempty"`
	// AttrSources is the source attribution for the typed attribute columns:
	// {column → source key} (e.g. {"gender":"vndb","blood_type":"bangumi"}) taken
	// from the latest field_provenance writer of each populated attribute. Absent
	// when no attribute is populated.
	AttrSources map[string]string `json:"attr_sources,omitempty"`
	Aliases     []CharacterAlias  `json:"aliases"`
	// Intros is the multilingual intro set (step 65): one element per language
	// (lowest source_id wins), the WorkIntro shape. Characters are
	// catalog-native, so these are always native catalog_character_intro rows
	// (no claimed bridge).
	Intros []WorkIntro `json:"intros"`
	// Traits is the VNDB trait set (step 93) at or below the requested
	// ?spoilers ceiling (default 0 — no spoiler leaks unless asked). Ordered
	// (group_tid, gorder, name) so groups render contiguously. sexual rides on
	// every row — consumers gate NSFW vocabulary themselves (the catalog is
	// R18-capable; the step-81 BWH/cup precedent).
	Traits []CharacterTrait `json:"traits"`
}

// CharacterTrait is one VNDB trait on a character (step 93). group_tid/
// group_name locate the trait's root group (Hair/Eyes/Body/Personality/Role/…);
// group_name is empty when the trait itself is a root. spoiler_level is the
// per-character grade (0 none / 1 minor / 2 major); lie marks a trait
// presented as true that is actually false (VNDB semantics, surfaced verbatim).
type CharacterTrait struct {
	ID           int64  `json:"id"`
	Name         string `json:"name" doc:"verbatim VNDB English trait name"`
	GroupTID     string `json:"group_tid" doc:"VNDB root-group tid (i1=Hair, i35=Eyes, …); empty when the trait is itself a root"`
	GroupName    string `json:"group_name,omitempty" doc:"root-group display name; absent for a root trait"`
	Sexual       bool   `json:"sexual,omitempty" doc:"the trait belongs to a sexual trait family — consumers gate rendering themselves"`
	SpoilerLevel int16  `json:"spoiler_level" doc:"per-character spoiler grade: 0 none / 1 minor / 2 major"`
	Lie          bool   `json:"lie,omitempty" doc:"presented as true in-story but actually a lie (VNDB flag)"`
}

// CharacterAlias is one writing-variant of a character's name (not a new
// identity — mirrors the credit-name alias shape).
type CharacterAlias struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Latin              string `json:"latin,omitempty"`
	Lang               string `json:"lang"`
	Kind               int16  `json:"kind" doc:"0=translation 1=spelling_variant 2=search_hint"`
	IsPrimaryForLocale bool   `json:"is_primary_for_locale"`
}

// VoiceName is one credited name that voiced a character on a work.
type VoiceName struct {
	CreditNameID int64  `json:"credit_name_id"`
	Name         string `json:"name"`
	Lang         string `json:"lang"`
	Latin        string `json:"latin,omitempty"`
}

// WorkRelation is one cross-media relation edge (wave 104), rendered
// single-directionally from the viewed work's perspective.
type WorkRelation struct {
	RelationType  string `json:"relation_type" doc:"vocabulary key, e.g. sequel_of / fandisc_of / remake_of / collects / shares_character"`
	Phrase        string `json:"phrase" doc:"direction-resolved display phrase from this work's perspective"`
	WorkID        int64  `json:"work_id" doc:"the other end's catalog work id"`
	DisplayName   string `json:"display_name"`
	MediumID      int16  `json:"medium_id"`
	ContentRating int16  `json:"content_rating"`
	Status        int16  `json:"status"`
	Site          string `json:"site,omitempty" doc:"claim site when the other end is claimed"`
	ProductWorkID int64  `json:"product_work_id,omitempty"`
}

// WorkSeriesSibling is one work in the viewed work's same_series transitive
// closure (wave 113) — a brief so the consumer renders it without a second read.
type WorkSeriesSibling struct {
	WorkID        int64  `json:"work_id"`
	DisplayName   string `json:"display_name"`
	MediumID      int16  `json:"medium_id"`
	ContentRating int16  `json:"content_rating"`
	Status        int16  `json:"status"`
	Site          string `json:"site,omitempty"`
	ProductWorkID int64  `json:"product_work_id,omitempty"`
}
