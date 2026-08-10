package getchuportraits

import "fmt"

type Slot struct {
	Name          string
	StagingColumn string
	ImageKind     string
	TargetColumn  string
	Preset        string
	UploaderSub   string
}

var SlotBust = Slot{
	Name:          "bust",
	StagingColumn: "nameplate_url",
	ImageKind:     "nameplate",
	TargetColumn:  "image_hash",
	Preset:        "character",
	UploaderSub:   "system:getchu-portrait-backfill",
}

var SlotFigure = Slot{
	Name:          "figure",
	StagingColumn: "portrait_url",
	ImageKind:     "portrait",
	TargetColumn:  "figure_hash",
	Preset:        "character_figure",
	UploaderSub:   "system:getchu-figure-backfill",
}

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
