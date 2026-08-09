package repincovers

import "sort"

// upscaleTarget is the long edge (px) at or above which a cover is
// display-ready. Below it the cover goes to the super-resolution export
// instead of being pinned as-is. Same 1080 the retired pin-portrait-covers
// used, so the two waves' products are the same size.
const upscaleTarget = 1080

// nsfwSexualFloor keeps the conservative auto-pin gate the first wave chose: a
// winner rated at or above this is NOT auto-pinned, it is reported and left
// alone. Re-pinning is a batch decision, and a batch has no business promoting
// explicit art onto a card that today shows something safe.
const nsfwSexualFloor int16 = 2

// tierIneligible is the rank of a kind that must never win a pin.
const tierIneligible = 99

// tier ranks a cover kind for the pin ladder. This is THE fix: the retired
// selector sorted by pixel height first and consulted kind only to break a tie,
// so any tall scan of a disc face, a box back or a booklet page outranked the
// actual front cover. Here the kind decides the tier and size only orders
// within it.
//
//   - 0: dig / main — the digital-edition cover and the source's own chosen
//     main image. The user's 2026-08-08 ruling put these in ONE tier compared
//     on resolution, rather than ranking dig above main: both are clean key
//     art, so the larger file is simply the better one.
//   - 1: pkgfront — the boxed edition's front. Acceptable, but it is a scan of
//     a physical case (plastic edges, obi, wear), so it loses to clean art.
//   - 2: "" — unknown kind, in practice a wiki-era user upload. Usually fine,
//     but nothing asserts what it depicts, so a named cover outranks it.
//   - 99: pkgback / pkgmed / pkgcontent / pkgside — the box back, the disc
//     face, the booklet, the spine. NEVER a cover. These are the rows the old
//     rule promoted, and no size makes them eligible: a work whose only
//     portrait art is a disc face keeps whatever it has rather than being
//     re-pinned onto another disc face.
func tier(kind string) int {
	switch kind {
	case "dig", "main":
		return 0
	case "pkgfront":
		return 1
	case "":
		return 2
	default:
		return tierIneligible
	}
}

// isPortrait reports whether the height exceeds width * 1.05, copied verbatim
// from the read face's isPortraitDims so the pin this job writes and the slot
// the public face picks agree on what "vertical" means. The 5% margin keeps a
// near-square scan out of the portrait slot.
//
// The gate matters more than it looks: a VNDB `dig` row is often a LANDSCAPE
// download-store banner rather than a cover, so "prefer digital" without a
// shape gate would hang a wide banner on a portrait card.
func isPortrait(w, h int) bool { return w > 0 && h > 0 && int64(h)*20 > int64(w)*21 }

// Cover is one catalog_work_cover row with its resolved image_service dims.
// DimsKnown is false when image_service does not know the hash, which makes the
// row ineligible — an unknown shape is never guessed at.
type Cover struct {
	ID        int64
	WorkID    int64
	Hash      string
	Kind      string
	SourceKey string
	Sexual    int16
	SortOrder int
	Pinned    bool
	Width     int
	Height    int
	DimsKnown bool
}

// LongEdge is the larger dimension, which for a portrait cover is the height.
func (c Cover) LongEdge() int {
	if c.Width > c.Height {
		return c.Width
	}
	return c.Height
}

// Action is what the plan decided for one work.
type Action int

const (
	// ActionNone: the ladder agrees with the pin already in place.
	ActionNone Action = iota
	// ActionDirectPin: the winner is display-ready, just move the pin.
	ActionDirectPin
	// ActionUpscale: the winner is the right picture but under the target, so
	// it goes through super-resolution and the PRODUCT gets pinned. Moving the
	// pin without this step is what would trade a correct-but-small cover for
	// a wrong-but-sharp one.
	ActionUpscale
	// ActionDeferredNSFW: the winner is rated explicit; reported, never pinned.
	ActionDeferredNSFW
)

func (a Action) String() string {
	switch a {
	case ActionDirectPin:
		return "direct_pin"
	case ActionUpscale:
		return "upscale"
	case ActionDeferredNSFW:
		return "nsfw_deferred"
	default:
		return "none"
	}
}

// Plan is one work's decision. Old is the cover pinned today (nil if the work
// somehow lost its pin between the load and now); New is the ladder's winner.
type Plan struct {
	WorkID int64
	Old    *Cover
	New    *Cover
	Action Action
}

// selectWinner returns the ladder's choice among a work's covers, or nil when
// the work has no eligible portrait cover at all (in which case the existing
// pin stands — this job never UNPINS a work down to nothing).
func selectWinner(covers []Cover) *Cover {
	eligible := make([]Cover, 0, len(covers))
	for _, c := range covers {
		if c.DimsKnown && isPortrait(c.Width, c.Height) && tier(c.Kind) < tierIneligible {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if ta, tb := tier(a.Kind), tier(b.Kind); ta != tb {
			return ta < tb
		}
		if a.LongEdge() != b.LongEdge() {
			return a.LongEdge() > b.LongEdge()
		}
		return a.Hash < b.Hash // deterministic tie-break
	})
	best := eligible[0]
	return &best
}

// planWork resolves one work's covers into a Plan.
func planWork(workID int64, covers []Cover) Plan {
	p := Plan{WorkID: workID}
	for i := range covers {
		if covers[i].Pinned && p.Old == nil {
			p.Old = &covers[i]
		}
	}
	p.New = selectWinner(covers)
	switch {
	case p.New == nil:
		return p // nothing eligible; leave the work alone
	case p.Old != nil && p.Old.Hash == p.New.Hash:
		return p // already correct
	case p.New.Sexual >= nsfwSexualFloor:
		p.Action = ActionDeferredNSFW
	case p.New.LongEdge() >= upscaleTarget:
		p.Action = ActionDirectPin
	default:
		p.Action = ActionUpscale
	}
	return p
}
