// public_works_include.go — the works LIST `include=` rich-brief blocks
// (A2-1a, refs/proj/126 D1/D7).
//
// Contract shape, in one place because it is the whole point of the wave:
//
//   - Every block is OPT-IN. With no include= the response is byte-identical
//     to the frozen W1 list contract — the hard gate of this wave.
//   - Every block is BATCH-loaded over the page's work ids (never per row);
//     the loaders are the same ones the detail face uses, so a block on the
//     list means the same thing it means on the detail record.
//   - An unknown token is IGNORED (§3.5 clause 2, the same posture as the
//     detail face's ParsePublicInclude) — a client that misspells a token gets
//     a thinner response, never a 400.
//
// D7 (the four product keys): names / intros pivot the catalog's BCP-47
// languages onto ja-jp / zh-cn / zh-tw / en-us because that is the vocabulary
// every downstream product face already renders. Languages outside the four
// are dropped rather than passed through — the block is a rendering
// convenience, and the complete language set stays available on the detail
// face (titles[] / intro[]).
package service

import (
	"context"
	"slices"
	"strings"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// WorksListInclude selects the works-list rich-brief blocks. Deliberately its
// own type rather than the detail face's PublicInclude: the two vocabularies
// are disjoint and must be free to evolve apart.
type WorksListInclude struct {
	Names   bool
	Intros  bool
	Labels  bool
	Ratings bool
	Covers  bool
	Refs    bool
}

// any reports whether any block was requested (the no-op short-circuit that
// keeps the default page exactly as cheap as it was).
func (i WorksListInclude) any() bool {
	return i.Names || i.Intros || i.Labels || i.Ratings || i.Covers || i.Refs
}

// ParseWorksListInclude resolves the comma-separated include token set for the
// works list. Unknown tokens are ignored.
func ParseWorksListInclude(raw string) WorksListInclude {
	var inc WorksListInclude
	for _, tok := range strings.Split(raw, ",") {
		switch strings.TrimSpace(tok) {
		case "names":
			inc.Names = true
		case "intros":
			inc.Intros = true
		case "labels":
			inc.Labels = true
		case "ratings":
			inc.Ratings = true
		case "covers":
			inc.Covers = true
		case "refs":
			inc.Refs = true
		}
	}
	return inc
}

// d7ProductKey maps a catalog BCP-47 language tag to its product key. ok=false
// for anything outside the four keys (dropped, per the D7 ruling).
//
// The mapping is deliberately prefix-based on the zh side: the registry holds
// zh-Hans (the canonical import spelling), bare zh (older bangumi rows) and
// zh-Hant, and a bare `zh` is Simplified in every source that produces it.
func d7ProductKey(lang string) (string, bool) {
	switch {
	case lang == "ja" || strings.HasPrefix(lang, "ja-"):
		return "ja-jp", true
	case lang == "en" || strings.HasPrefix(lang, "en-"):
		return "en-us", true
	case lang == "zh-Hant" || strings.HasPrefix(lang, "zh-Hant-") || lang == "zh-TW" || lang == "zh-HK":
		return "zh-tw", true
	case lang == "zh" || strings.HasPrefix(lang, "zh"):
		return "zh-cn", true
	default:
		return "", false
	}
}

// attachWorkListBlocks loads and attaches every requested include= block to a
// page of list items, in one batched query set per block. items and rows are
// index-aligned (the caller built them in the same pass).
func (s *PublicService) attachWorkListBlocks(
	ctx context.Context, items []dto.PublicWorkListItem, rows []workListSourceRow,
	subjects []claimSubject, covers map[int64][]WorkCoverRow, inc WorksListInclude, nsfw bool,
	displayNSFW map[int64]bool,
) error {
	if !inc.any() || len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}

	if inc.Names {
		// subjects, not ids: a claimed work's names come from the wiki bridge
		// (A2-R1) — the same partition every other bridged block runs on.
		titles, err := s.read.loadWorkTitles(ctx, subjects)
		if err != nil {
			return err
		}
		for i, r := range rows {
			items[i].Names = publicWorkNames(titles[r.ID])
		}
	}
	if inc.Intros {
		intros, err := s.read.loadWorkIntros(ctx, subjects)
		if err != nil {
			return err
		}
		for i, r := range rows {
			items[i].Intros = s.publicWorkIntros(intros[r.ID])
		}
	}
	if inc.Labels {
		labels, err := s.read.loadWorkLabels(ctx, ids)
		if err != nil {
			return err
		}
		blocks := make([][]dto.PublicWorkLabel, len(rows))
		for i, r := range rows {
			items[i].Labels = publicWorkLabels(labels[r.ID])
			blocks[i] = items[i].Labels
		}
		// ONE work_count aggregate for the whole page, through the same helper
		// the detail record uses — the two faces cannot disagree (A2-R1).
		if err := s.fillWorkLabelCounts(ctx, blocks, nsfw); err != nil {
			return err
		}
	}
	if inc.Ratings {
		ratings, err := s.read.loadWorkRatings(ctx, subjects)
		if err != nil {
			return err
		}
		for i, r := range rows {
			items[i].Ratings = s.publicRatings(ratings[r.ID])
		}
	}
	if inc.Covers {
		// One image_service batch for the WHOLE page — the metadata is both the
		// enrichment and the orientation evidence the slot picker runs on.
		all := make([]WorkCoverRow, 0, len(rows))
		for _, r := range rows {
			all = append(all, covers[r.ID]...)
		}
		meta := s.coverMetaFor(ctx, all)
		for i, r := range rows {
			items[i].Covers = s.pickCoverSlots(covers[r.ID], meta, nsfw && displayNSFW[r.ID])
		}
	}
	if inc.Refs {
		refs, err := s.workListRefs(ctx, ids)
		if err != nil {
			return err
		}
		for i, r := range rows {
			items[i].Refs = refs[r.ID]
		}
	}
	return nil
}

// workListRefs batch-loads the EXACT identity anchors for a page of works, in
// the detail face's shape: work-level anchors UNION the anchors of the work's
// own releases, deduplicated on (source, external_id).
//
// One query for the whole page — the release join happens in SQL rather than by
// loading release rows per work. link_kind is pinned to EXACT in the predicate:
// probable is an unreviewed hypothesis and related is a web link, and neither
// has ever crossed the public face (硬红线). dead_at IS NULL joins that
// predicate: an anchor whose upstream entry has been deleted would render as a
// link that 404s, so it drops out of the projection (the assertion itself
// stays in the table — see CatalogExternalRef.DeadAt). Works with no anchor are simply
// absent from the map, so their block is omitted rather than an empty array —
// the same "absent means nothing to say" rule the other include= blocks follow.
func (s *PublicService) workListRefs(ctx context.Context, ids []int64) (map[int64][]dto.PublicCatalogRef, error) {
	out := make(map[int64][]dto.PublicCatalogRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID     int64  `gorm:"column:work_id"`
		Source     string `gorm:"column:source"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT r.entity_id AS work_id, src.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id IN ? AND r.link_kind = ? AND r.dead_at IS NULL
		UNION ALL
		SELECT rel.work_id, src.key AS source, r.external_id
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND rel.work_id IN ? AND r.link_kind = ? AND r.dead_at IS NULL
		ORDER BY work_id, source, external_id`,
		model.EntityTypeWork, ids, model.LinkKindExact,
		model.EntityTypeRelease, ids, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[[2]any]struct{}, len(rows))
	for _, r := range rows {
		key := [2]any{r.WorkID, r.Source + "\x00" + r.ExternalID}
		if _, dup := seen[key]; dup {
			continue // the same anchor at both grains renders once
		}
		seen[key] = struct{}{}
		out[r.WorkID] = append(out[r.WorkID], dto.PublicCatalogRef{
			Source: publicSourceKey(r.Source), ExternalID: r.ExternalID,
		})
	}
	return out, nil
}

// publicWorkNames pivots a work's titles onto the four D7 product keys. Within
// a key the LOWEST kind wins (official → alias → abbreviation, the detail
// face's own ordering) and the loader's (kind, id) order breaks the rest of
// the tie, so two languages mapping to the same key (zh-Hans and bare zh both
// → zh-cn) resolve to the first row the ordering yields — deterministic, and
// the same row on every call. nil when the work has no title in any of the
// four keys (the block is then omitted entirely).
func publicWorkNames(titles []WorkTitleRow) *dto.PublicWorkNames {
	var out dto.PublicWorkNames
	filled := false
	for _, t := range titles {
		key, ok := d7ProductKey(t.Lang)
		if !ok {
			continue
		}
		switch key {
		case "ja-jp":
			if out.JaJP == "" {
				out.JaJP, filled = t.Title, true
			}
		case "zh-cn":
			if out.ZhCN == "" {
				out.ZhCN, filled = t.Title, true
			}
		case "zh-tw":
			if out.ZhTW == "" {
				out.ZhTW, filled = t.Title, true
			}
		case "en-us":
			if out.EnUS == "" {
				out.EnUS, filled = t.Title, true
			}
		}
	}
	if !filled {
		return nil
	}
	return &out
}

// publicWorkIntros pivots a work's merged intros onto the four D7 product
// keys. The per-language merge already happened in loadWorkIntros — including
// the step-75 rule that a machine translation surfaces ONLY when the language
// has no source row — so this is a pure re-keying: first row wins per key, in
// the loader's lang-ascending order. nil when nothing lands in the four keys.
func (s *PublicService) publicWorkIntros(intros []WorkIntroRow) *dto.PublicWorkIntros {
	var out dto.PublicWorkIntros
	filled := false
	for _, in := range intros {
		key, ok := d7ProductKey(in.Lang)
		if !ok {
			continue
		}
		slot := &dto.PublicWorkIntroSlot{Intro: in.Intro, Source: s.sourceKey(in.SourceID), Machine: in.Machine}
		switch key {
		case "ja-jp":
			if out.JaJP == nil {
				out.JaJP, filled = slot, true
			}
		case "zh-cn":
			if out.ZhCN == nil {
				out.ZhCN, filled = slot, true
			}
		case "zh-tw":
			if out.ZhTW == nil {
				out.ZhTW, filled = slot, true
			}
		case "en-us":
			if out.EnUS == nil {
				out.EnUS, filled = slot, true
			}
		}
	}
	if !filled {
		return nil
	}
	return &out
}

// publicWorkLabels projects the label attributions to the same shape the
// detail face's labels[] carries. nil (block omitted) when the work has none.
//
// ONE ENTRY PER COMPANY (wave 200). The storage grain is (work, label, kind),
// so a studio that both made and published a work is two rows — and 56,438
// works carried at least one company twice, which every consumer then printed
// twice. A company is one company; the capacities it acted in are a property of
// it, not a reason to list it again.
func publicWorkLabels(rows []LabelAttribution) []dto.PublicWorkLabel {
	if len(rows) == 0 {
		return nil
	}
	out := make([]dto.PublicWorkLabel, 0, len(rows))
	at := make(map[int64]int, len(rows))
	primary := make(map[int64]int16, len(rows))
	for _, l := range rows {
		kind := workLabelKindKey(l.Kind)
		if i, ok := at[l.LabelID]; ok {
			out[i].Kinds = append(out[i].Kinds, kind)
			if workLabelKindRank(l.Kind) < workLabelKindRank(primary[l.LabelID]) {
				primary[l.LabelID] = l.Kind
				out[i].Kind = kind
			}
			continue
		}
		at[l.LabelID] = len(out)
		primary[l.LabelID] = l.Kind
		out = append(out, dto.PublicWorkLabel{
			ID: l.LabelID, DisplayName: l.DisplayName,
			LabelKind: labelKindKey(l.LabelKind), Kind: kind, Kinds: []string{kind}, Lang: l.Lang,
			LogoHash: l.LogoHash,
		})
	}
	for i := range out {
		slices.Sort(out[i].Kinds)
		out[i].Kinds = slices.Compact(out[i].Kinds)
	}
	return out
}

// workLabelKindRank orders the capacities by how much they identify the work's
// maker, so `kind` (the singular field consumers already read) keeps naming the
// most identifying one when a company acted in several. Brand and circle are
// the name a galgame actually ships under; developer beats publisher because
// "who made it" identifies a work better than "who put it on shelves".
func workLabelKindRank(k int16) int {
	switch k {
	case model.WorkLabelKindBrand:
		return 0
	case model.WorkLabelKindCircle:
		return 1
	case model.WorkLabelKindDeveloper:
		return 2
	case model.WorkLabelKindPublisher:
		return 3
	default:
		return 4
	}
}

// publicRatings projects the rating rows to the same shape (and the same
// source-native scales — never aggregated) the detail face's ratings[]
// carries. nil (block omitted) when the work has none.
func (s *PublicService) publicRatings(rows []WorkRatingRow) []dto.PublicRating {
	if len(rows) == 0 {
		return nil
	}
	out := make([]dto.PublicRating, 0, len(rows))
	for _, r := range rows {
		out = append(out, dto.PublicRating{
			Source: s.sourceKey(r.SourceID), Score: r.Score, VoteCount: r.VoteCount, Rank: r.Rank,
		})
	}
	return out
}

// isPortraitDims reports whether height exceeds width * 1.05 — the U-track
// portrait cutoff, kept as the exact rational 21/20 to avoid float noise.
// Copied verbatim from cmd/pin-portrait-covers so the public face's idea of
// "portrait" is the same one the portrait-pinning job applies.
func isPortraitDims(w, h int) bool { return int64(h)*20 > int64(w)*21 }

// isBannerDims reports whether an image is WIDE enough to be hero art: at
// least 4:3. "Not portrait" is not the same thing as "banner" — a scan of a
// disc face or a booklet spread lands within a few pixels of square, clears
// any not-portrait test, and then outranks the real art because it happens to
// sort first. A hero slot wants art that is actually wide, so the near-square
// band belongs to NEITHER slot.
//
// The cutoff is 4:3 and not 3:2 because 4:3 is the shape the wiki's own hero
// art is in: on production ~20k covers sit wide of portrait but short of 3:2,
// and they are overwhelmingly wiki uploads, while the near-square scans this
// test exists to stop cluster at 1.0-1.1. 3:2 threw both away together.
func isBannerDims(w, h int) bool { return int64(w)*3 >= int64(h)*4 }

// bannerMinWidth is the width a wide cover must reach to be a FIRST-CHOICE
// hero. The wiki bridge carried a large population of 256px-wide thumbnails —
// the right shape, hopeless as art — and 40% of production's banner slots were
// filled by something under 400px. Width is a TIER, not a veto: when a work
// owns nothing wider, a small banner still beats center-cropping a portrait
// into a 16:9 frame, so it takes the slot on the second tier.
const bannerMinWidth = 800

// isCoverArt reports whether a cover's kind is the game's ARTWORK rather than
// a photograph of the physical product. The back of a box, a disc face, a
// booklet page and a spine are catalogue evidence, never the picture a card or
// a hero renders — at any resolution, in either slot. Mirrors the tier ladder
// in internal/jobs/repincovers, which is what picks portrait_pinned.
func isCoverArt(kind string) bool {
	switch kind {
	case "pkgback", "pkgmed", "pkgcontent", "pkgside":
		return false
	default:
		return true
	}
}

// pickCoverSlots resolves a work's cover set into the two display slots.
//
// Orientation comes from the image's real DIMENSIONS — the kind vocabulary in
// the registry is a sample-image taxonomy (catalog-native rows are all "main";
// the wiki bridge adds ""/dig/pkgfront/pkgback/pkgcontent/pkgmed/pkgside), and
// none of those words says which way up the picture is. So the same
// image_service batch that fills width/height/thumbhash is what classifies the
// slots, and with the lookup unwired the banner slot is simply null rather
// than guessed. Kind still gets a veto: it says what the picture IS, and a
// photo of the packaging is never the art either slot wants.
//
//   - portrait: the portrait_pinned row (the editorial pin, honoured whatever
//     its kind) → else the first portrait-shaped ARTWORK cover → else the
//     first portrait-shaped cover → else the first visible cover, so a card
//     always has key art when any cover is visible.
//   - banner: the first wide (isBannerDims) ARTWORK cover at bannerMinWidth or
//     more → else the first wide ARTWORK cover of any width; null when the
//     work has none. Null is the honest answer — a consumer with an empty hero
//     falls back to the portrait, which beats hanging a disc face there.
//
// Candidates arrive in the loader's (sort_order, image_hash) order, so "first"
// IS "lowest sort order". allowSexual decides whether a sexual-flagged cover
// may fill a slot AT ALL, and even when it may, a display-safe candidate wins
// every tier: the whole scan runs once over the safe covers and only re-runs
// over the rest for the slots still empty. Violence is not gated here, matching
// the list face's single-cover rule.
func (s *PublicService) pickCoverSlots(rows []WorkCoverRow, meta map[string]ImageMeta, allowSexual bool) *dto.PublicWorkCoverSlots {
	cand := s.scanCovers(rows, meta, false)
	if allowSexual && !cand.complete() {
		cand.fillFrom(s.scanCovers(rows, meta, true))
	}
	if cand.first == nil {
		return nil // no cover this caller may see → block omitted
	}
	return &dto.PublicWorkCoverSlots{
		Portrait: s.coverSlot(cand.portrait(), meta),
		Banner:   s.coverSlot(cand.banner(), meta),
	}
}

// coverCandidates is one scan's answer for every tier of both slots. Keeping
// the tiers separate (rather than resolving to a winner per scan) is what lets
// a display-safe cover outrank a sexual one AT ITS OWN TIER instead of only
// when the sexual one would have won outright.
type coverCandidates struct {
	pinned, portraitArt, portraitAny *WorkCoverRow
	bannerWide, bannerAny            *WorkCoverRow
	first                            *WorkCoverRow
}

// portrait resolves the portrait slot's tier ladder; never nil once a scan saw
// any visible cover, so a card always has key art.
func (c coverCandidates) portrait() *WorkCoverRow {
	switch {
	case c.pinned != nil:
		return c.pinned
	case c.portraitArt != nil:
		return c.portraitArt
	case c.portraitAny != nil:
		return c.portraitAny
	default:
		return c.first
	}
}

// banner resolves the banner slot's tier ladder; nil when the work owns no
// wide artwork at all.
func (c coverCandidates) banner() *WorkCoverRow {
	if c.bannerWide != nil {
		return c.bannerWide
	}
	return c.bannerAny
}

// complete reports whether every tier is filled, i.e. a second scan could not
// contribute anything.
func (c coverCandidates) complete() bool {
	return c.pinned != nil && c.portraitArt != nil && c.portraitAny != nil &&
		c.bannerWide != nil && c.bannerAny != nil && c.first != nil
}

// fillFrom takes other's answer for each tier this scan left empty.
func (c *coverCandidates) fillFrom(other coverCandidates) {
	for _, pair := range []struct{ dst, src **WorkCoverRow }{
		{&c.pinned, &other.pinned}, {&c.portraitArt, &other.portraitArt},
		{&c.portraitAny, &other.portraitAny}, {&c.bannerWide, &other.bannerWide},
		{&c.bannerAny, &other.bannerAny}, {&c.first, &other.first},
	} {
		if *pair.dst == nil {
			*pair.dst = *pair.src
		}
	}
}

// scanCovers walks a work's covers once and records the first candidate for
// every tier. allowSexual=false restricts the walk to display-safe covers.
func (s *PublicService) scanCovers(rows []WorkCoverRow, meta map[string]ImageMeta, allowSexual bool) coverCandidates {
	var out coverCandidates
	for i := range rows {
		c := &rows[i]
		if !allowSexual && c.Sexual != 0 {
			continue
		}
		if s.imageURL(c.ImageHash) == "" {
			continue // never a bare hash on the public face
		}
		if out.first == nil {
			out.first = c
		}
		if c.PortraitPinned && out.pinned == nil {
			out.pinned = c
		}
		m, ok := meta[c.ImageHash]
		if !ok || m.Width <= 0 || m.Height <= 0 {
			continue
		}
		switch {
		case isPortraitDims(m.Width, m.Height):
			if out.portraitAny == nil {
				out.portraitAny = c
			}
			if isCoverArt(c.Kind) && out.portraitArt == nil {
				out.portraitArt = c
			}
		case isBannerDims(m.Width, m.Height) && isCoverArt(c.Kind):
			if out.bannerAny == nil {
				out.bannerAny = c
			}
			if m.Width >= bannerMinWidth && out.bannerWide == nil {
				out.bannerWide = c
			}
		}
	}
	return out
}

// coverSlot renders one chosen cover row to its public slot (nil → nil, the
// unfilled-slot null).
func (s *PublicService) coverSlot(c *WorkCoverRow, meta map[string]ImageMeta) *dto.PublicCoverSlot {
	if c == nil {
		return nil
	}
	slot := &dto.PublicCoverSlot{
		URL: s.imageURL(c.ImageHash), Sexual: c.Sexual, Violence: c.Violence,
		Source: s.sourceKey(c.SourceID),
	}
	if m, ok := meta[c.ImageHash]; ok {
		slot.Width, slot.Height, slot.Thumbhash = m.Width, m.Height, m.Thumbhash
	}
	return slot
}
