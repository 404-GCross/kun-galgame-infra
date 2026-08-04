package getchuportraits

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"

	"api/internal/jobs/getchuchars"

	"gorm.io/gorm"
)

// auditPair is one character that ALREADY has a portrait and also resolves to a
// Getchu bust. This lane will never write to these — which is exactly what
// makes them the falsification set.
//
// The claim under test is "the bust this lane would attach to character X is
// really X". Nothing in the pipeline can check that from the inside: the page's
// own <tr> pairing is taken on trust, and the name match has no image sense at
// all. But 22,428 characters carry an independently-sourced VNDB portrait AND a
// Getchu bust, so for those the pipeline's answer can be compared against an
// answer it never saw. If the two pictures are the same person far more often
// than chance, the pairing is sound; if they are not, the wave is wrong before
// a single row is written.
type auditPair struct {
	CharacterID int64
	ImageHash   string // the portrait the catalog already shows
	GetchuID    string
	File        string // the bust this lane WOULD have used
}

// collectAudit gathers every matched character that has both pictures.
func collectAudit(ctx context.Context, db, gdb *gorm.DB, matched []getchuchars.Candidate) ([]auditPair, error) {
	plates, err := loadNameplates(ctx, gdb)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(matched))
	for _, m := range matched {
		ids = append(ids, m.CharacterID)
	}
	have, err := loadPortraitHashes(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]auditPair, 0, len(matched))
	for _, m := range matched {
		hash, ok := have[m.CharacterID]
		if !ok {
			continue
		}
		plate, ok := pickPlate(m, plates)
		if !ok {
			continue
		}
		out = append(out, auditPair{
			CharacterID: m.CharacterID, ImageHash: hash,
			GetchuID: plate.GetchuID, File: plate.File,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CharacterID < out[j].CharacterID })
	return out, nil
}

// loadPortraitHashes returns the existing portrait hash for the ids that have one.
func loadPortraitHashes(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	for i := 0; i < len(ids); i += 5000 {
		batch := ids[i:min(i+5000, len(ids))]
		var rows []struct {
			ID        int64  `gorm:"column:id"`
			ImageHash string `gorm:"column:image_hash"`
		}
		if err := db.WithContext(ctx).Raw(
			`SELECT id, image_hash FROM catalog_character
			 WHERE id IN ? AND deleted_at IS NULL AND image_hash IS NOT NULL`, batch).
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("load portrait hashes: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r.ImageHash
		}
	}
	return out, nil
}

// writeAudit dumps the falsification set as CSV for an offline image compare.
func writeAudit(path string, pairs []auditPair) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"character_id", "image_hash", "getchu_id", "file"}); err != nil {
		return err
	}
	for _, p := range pairs {
		if err := w.Write([]string{
			strconv.FormatInt(p.CharacterID, 10), p.ImageHash, p.GetchuID, p.File,
		}); err != nil {
			return err
		}
	}
	return nil
}
