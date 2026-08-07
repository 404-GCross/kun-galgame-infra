package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScreenshotsComeBackInPerSourceBlocks pins the wave-188 read-face ordering.
//
// sort_order is only meaningful WITHIN a source — every backfill numbers its own
// gallery from 0 — so once a work carries rows from several sources, ordering by
// sort_order first would interleave two unrelated sequences and fabricate an
// order nobody authored. source_id leads the key instead, which yields the
// per-source contiguous blocks the gallery UI groups on.
func TestScreenshotsComeBackInPerSourceBlocks(t *testing.T) {
	cleanTables(t)
	w := createWork(t, "multi-source gallery")

	// Two lanes with FULLY OVERLAPPING sort_orders — the case a sort_order-first
	// key would shuffle. Inserted interleaved and out of order so neither
	// insertion order nor the primary key can be what the assertion observes.
	const (
		srcVNDB   = int16(2)
		srcDLsite = int16(4)
	)
	for _, row := range []model.CatalogWorkScreenshot{
		{WorkID: w.ID, ImageHash: "dlsite-1", SortOrder: 1, SourceID: srcDLsite},
		{WorkID: w.ID, ImageHash: "vndb-0", SortOrder: 0, SourceID: srcVNDB},
		{WorkID: w.ID, ImageHash: "dlsite-0", SortOrder: 0, SourceID: srcDLsite},
		{WorkID: w.ID, ImageHash: "vndb-1", SortOrder: 1, SourceID: srcVNDB},
	} {
		require.NoError(t, testDB.Create(&row).Error)
	}

	detail, err := NewReadService(testDB).WorkByID(t.Context(), w.ID, 0)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Len(t, detail.Screenshots, 4)

	hashes := make([]string, 0, 4)
	sources := make([]int16, 0, 4)
	for _, sh := range detail.Screenshots {
		hashes = append(hashes, sh.ImageHash)
		sources = append(sources, sh.SourceID)
	}
	assert.Equal(t, []string{"vndb-0", "vndb-1", "dlsite-0", "dlsite-1"}, hashes,
		"each source's own sequence stays intact, and the two never interleave")
	assert.Equal(t, []int16{srcVNDB, srcVNDB, srcDLsite, srcDLsite}, sources,
		"blocks are contiguous, in source_id order")
}
