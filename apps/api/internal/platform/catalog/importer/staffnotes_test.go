package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRefineVNDBStaffRoles(t *testing.T) {
	one := func(roleID int64) []int64 { return []int64{roleID} }

	// Only the 其他 bucket refines, on the folded note form.
	assert.Equal(t, one(roleProgram), RefineVNDBStaffRoles(roleOtherStaffID, "Programming"))
	assert.Equal(t, one(roleProgram), RefineVNDBStaffRoles(roleOtherStaffID, " script "))
	assert.Equal(t, one(roleSpecialThanks), RefineVNDBStaffRoles(roleOtherStaffID, "Special thanks"))
	assert.Equal(t, one(roleThemeSongLyrics), RefineVNDBStaffRoles(roleOtherStaffID, "OP lyrics"))
	assert.Equal(t, one(roleAnimationWork), RefineVNDBStaffRoles(roleOtherStaffID, "OP movie"))
	assert.Equal(t, one(roleTitleDesign), RefineVNDBStaffRoles(roleOtherStaffID, "Logo design"))
	assert.Equal(t, one(roleArtWorker), RefineVNDBStaffRoles(roleOtherStaffID, "Image editing"))
	assert.Equal(t, one(roleArtWorker), RefineVNDBStaffRoles(roleOtherStaffID, "GUI"))
	assert.Equal(t, one(roleTranslator), RefineVNDBStaffRoles(roleOtherStaffID, "Localization"))

	// Exact table keys win before any splitting ("op, ed lyrics" is one key).
	assert.Equal(t, one(roleThemeSongLyrics), RefineVNDBStaffRoles(roleOtherStaffID, "OP, ED lyrics"))
	assert.Equal(t, one(roleAnimationWork), RefineVNDBStaffRoles(roleOtherStaffID, "OP/ED movie"))

	// A composite refines only when every component is a table key, deduped
	// in note order.
	assert.Equal(t, []int64{rolePlanningJP, roleProgram},
		RefineVNDBStaffRoles(roleOtherStaffID, "Planning, script"))
	assert.Equal(t, []int64{roleProducer, rolePublicity},
		RefineVNDBStaffRoles(roleOtherStaffID, "Producer, PR"))
	assert.Equal(t, []int64{roleArtWorker, roleAnimationWork},
		RefineVNDBStaffRoles(roleOtherStaffID, "Graphics/Movies"))
	assert.Equal(t, []int64{roleArtWorker, roleTitleDesign},
		RefineVNDBStaffRoles(roleOtherStaffID, "GUI & logo"))
	assert.Equal(t, one(roleProgram), RefineVNDBStaffRoles(roleOtherStaffID, "Program, script"), "same-role components dedupe")

	// One unknown component and the whole note stays in the bucket.
	assert.Equal(t, one(roleOtherStaffID), RefineVNDBStaffRoles(roleOtherStaffID, "Planning, draft"))
	assert.Equal(t, one(roleOtherStaffID), RefineVNDBStaffRoles(roleOtherStaffID, "Movie assistance"))
	assert.Equal(t, one(roleOtherStaffID), RefineVNDBStaffRoles(roleOtherStaffID, "Localization producer"))
	assert.Equal(t, one(roleOtherStaffID), RefineVNDBStaffRoles(roleOtherStaffID, ""))
	assert.Equal(t, one(roleOtherStaffID), RefineVNDBStaffRoles(roleOtherStaffID, ", ,"))

	// A classified role passes through untouched whatever the note says.
	assert.Equal(t, one(247), RefineVNDBStaffRoles(247, "Script"))
	assert.Equal(t, one(247), RefineVNDBStaffRoles(247, "Planning, script"))
}
