package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRefineVNDBStaffRole(t *testing.T) {
	// Only the 其他 bucket refines, on the folded note form.
	assert.Equal(t, roleProgram, RefineVNDBStaffRole(roleOtherStaffID, "Programming"))
	assert.Equal(t, roleProgram, RefineVNDBStaffRole(roleOtherStaffID, " script "))
	assert.Equal(t, roleSpecialThanks, RefineVNDBStaffRole(roleOtherStaffID, "Special thanks"))
	assert.Equal(t, roleThemeSongLyrics, RefineVNDBStaffRole(roleOtherStaffID, "OP lyrics"))
	assert.Equal(t, roleAnimationWork, RefineVNDBStaffRole(roleOtherStaffID, "OP movie"))
	assert.Equal(t, roleTitleDesign, RefineVNDBStaffRole(roleOtherStaffID, "Logo design"))
	assert.Equal(t, roleArtWorker, RefineVNDBStaffRole(roleOtherStaffID, "Image editing"))

	// A note the table does not know stays in the bucket — including the
	// composite forms, which must never half-map.
	assert.Equal(t, roleOtherStaffID, RefineVNDBStaffRole(roleOtherStaffID, "Movie assistance"))
	assert.Equal(t, roleOtherStaffID, RefineVNDBStaffRole(roleOtherStaffID, "Planning, script"))
	assert.Equal(t, roleOtherStaffID, RefineVNDBStaffRole(roleOtherStaffID, ""))

	// A classified role passes through untouched whatever the note says.
	assert.Equal(t, int64(247), RefineVNDBStaffRole(247, "Script"))
}
