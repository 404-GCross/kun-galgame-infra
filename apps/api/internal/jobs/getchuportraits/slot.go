package getchuportraits

import "fmt"

// Slot is which of a character's two images a run fills.
//
// The bust and the figure differ in exactly three places — which staging column
// pairs the image to a roster row, which catalog column receives the hash, and
// which preset renders it — and in nothing else. Everything that is actually
// hard here (the page's own <tr> pairing, the roster name match, the
// edition walk, fill-missing, the upload pool) is identical, so the two share
// one implementation and differ by this value.
//
// Forking the lane instead would mean two copies of the matching path, and the
// two copies deciding differently who a character is: one would write the
// bust of one girl and the other the figure of another, onto the same profile,
// with nothing downstream able to notice.
type Slot struct {
	Name string
	// StagingColumn is the item_characters column carrying the URL the PAGE
	// paired with this roster row. This is the first-party pairing; it is never
	// derived from ordinal position.
	StagingColumn string
	// ImageKind is the item_images.kind whose rows hold those bytes.
	ImageKind string
	// TargetColumn is the catalog_character column that receives the hash.
	TargetColumn string
	// Preset is the image-service preset. The catalog client's
	// image_allowed_presets MUST list it or every upload is 403'd.
	Preset string
	// UploaderSub stamps a machine identity on first_uploader_sub.
	UploaderSub string
}

// SlotBust is the 250x300 upper-body crop Getchu calls `charaN.jpg`.
// Rendered at 256x360 `cover`, which is nearly its own 5:6.
var SlotBust = Slot{
	Name:          "bust",
	StagingColumn: "nameplate_url",
	ImageKind:     "nameplate",
	TargetColumn:  "image_hash",
	Preset:        "character",
	UploaderSub:   "system:getchu-portrait-backfill",
}

// SlotFigure is the 500x500 full-body standing art Getchu calls `charabN.jpg`.
// Rendered `inside` so the whole figure survives; see the character_figure
// preset.
//
// Note the names are the opposite way round from what they look like: the kind
// called `nameplate` is the bust, and the kind called `portrait` is the figure.
// Measured, not assumed.
var SlotFigure = Slot{
	Name:          "figure",
	StagingColumn: "portrait_url",
	ImageKind:     "portrait",
	TargetColumn:  "figure_hash",
	Preset:        "character_figure",
	UploaderSub:   "system:getchu-figure-backfill",
}

// ParseSlot resolves the --slot flag. There is no default: the two slots write
// different columns from different bytes, and guessing which one a bare
// invocation meant is not a recoverable mistake.
func ParseSlot(name string) (Slot, error) {
	switch name {
	case SlotBust.Name:
		return SlotBust, nil
	case SlotFigure.Name:
		return SlotFigure, nil
	default:
		return Slot{}, fmt.Errorf("unknown --slot %q; want %q (250x300 upper-body crop -> image_hash) or %q (500x500 full-body art -> figure_hash)",
			name, SlotBust.Name, SlotFigure.Name)
	}
}
