package main

import "sort"

// portraitTarget is the long-edge (px) at/above which a portrait cover is
// display-ready; below it the cover goes to the super-resolution manifest.
const portraitTarget = 1080

// nsfwSexualFloor is the conservative auto-pin gate: a best portrait whose
// sexual rating is >= this is NOT auto-pinned (recorded as an nsfw-deferred
// candidate instead). The existing landscape-banner display applies NO per-cover
// NSFW gate (PopulateEffectiveBanner just picks sort_order=0), so the portrait
// pin adopts a conservative floor of its own — see the 41-doc environment fact 5.
const nsfwSexualFloor int16 = 2

// coverRow is one cover row with its resolved image_service dims. DimsKnown is
// false when the hash is absent from the images table (treated as non-portrait).
type coverRow struct {
	GameID         int
	Hash           string
	Kind           string
	Sexual         int16
	Violence       int16
	Source         string
	PortraitPinned bool
	Width          int
	Height         int
	DimsKnown      bool
}

// state is the per-game three-state (+guards) classification.
type state int

const (
	stateNoPortrait    state = iota // no portrait cover at all → no action (frontend fallback)
	stateAlreadyPinned              // best portrait already portrait_pinned → idempotent skip
	stateNSFWDeferred               // best portrait sexual >= floor → not auto-pinned, recorded
	stateDirectPin                  // best portrait long-edge >= 1080 → pin directly
	stateNeedUpscale                // best portrait long-edge < 1080 → super-resolution manifest
)

func (s state) String() string {
	switch s {
	case stateNoPortrait:
		return "no_portrait"
	case stateAlreadyPinned:
		return "already_pinned"
	case stateNSFWDeferred:
		return "nsfw_deferred"
	case stateDirectPin:
		return "direct_pin"
	case stateNeedUpscale:
		return "need_upscale"
	default:
		return "unknown"
	}
}

// selection is one game's resolved best portrait + classification.
type selection struct {
	GameID     int
	Best       *coverRow // chosen best portrait; nil for stateNoPortrait
	State      state
	HasUpscale bool // the game already carries a source='upscale' cover row
}

// isPortrait reports whether height exceeds width * 1.05 (the U-track cutoff).
// Exact rational 21/20 to avoid float noise (mirrors portraitfill.isPortrait).
func isPortrait(w, h int) bool { return int64(h)*20 > int64(w)*21 }

// kindRank orders cover kinds for the tie-break: main beats pkgfront beats rest.
func kindRank(kind string) int {
	switch kind {
	case "main":
		return 0
	case "pkgfront":
		return 1
	default:
		return 2
	}
}

// selectBest returns the largest-long-edge portrait cover: max height, then
// kind preference (main > pkgfront > rest), then image_hash for a stable
// deterministic tie-break (cover rows carry no numeric id). nil when the game
// has no dims-known portrait cover.
func selectBest(covers []coverRow) *coverRow {
	portraits := make([]coverRow, 0, len(covers))
	for _, c := range covers {
		if c.DimsKnown && isPortrait(c.Width, c.Height) {
			portraits = append(portraits, c)
		}
	}
	if len(portraits) == 0 {
		return nil
	}
	sort.Slice(portraits, func(i, j int) bool {
		a, b := portraits[i], portraits[j]
		if a.Height != b.Height {
			return a.Height > b.Height // larger long-edge first
		}
		if ka, kb := kindRank(a.Kind), kindRank(b.Kind); ka != kb {
			return ka < kb
		}
		return a.Hash < b.Hash
	})
	best := portraits[0]
	return &best
}

// classify resolves one game's covers into a selection. Guard order matters:
// an already-pinned best is left untouched (idempotent) before the NSFW gate,
// so a pre-existing manual pin is never disturbed.
func classify(gameID int, covers []coverRow) selection {
	sel := selection{GameID: gameID}
	for _, c := range covers {
		if c.Source == "upscale" {
			sel.HasUpscale = true
			break
		}
	}
	best := selectBest(covers)
	if best == nil {
		sel.State = stateNoPortrait
		return sel
	}
	sel.Best = best
	switch {
	case best.PortraitPinned:
		sel.State = stateAlreadyPinned
	case best.Sexual >= nsfwSexualFloor:
		sel.State = stateNSFWDeferred
	case best.Height >= portraitTarget:
		sel.State = stateDirectPin
	default:
		sel.State = stateNeedUpscale
	}
	return sel
}
