package getchuportraits

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"api/internal/jobs/getchuchars"

	"gorm.io/gorm"
)

// candidate is one character to give a face, and where its bytes are.
type candidate struct {
	CharacterID int64
	GetchuID    string
	File        string // mirror basename of the chosen nameplate
}

// nameplateKey identifies one roster row's bust image.
type nameplateKey struct {
	GetchuID string
	Ordinal  int
}

// selectCandidates narrows the matcher's output to characters this lane may
// actually fill, and picks WHICH edition's image to use.
//
// Two independent filters, counted separately so a dry run says why the
// population is smaller than the match set:
//
//   - the character must have no portrait today (fill-missing);
//   - some edition of it must offer a nameplate at all.
//
// Whether those bytes are on disk yet is NOT a filter here — that is reported
// as Missing at fill time, so a dry run before the mirror pass still names the
// full population instead of reporting zero.
//
// The edition choice is the part that has to be done here in Go rather than in
// SQL. A character routinely resolves through several editions of one product,
// and getchuchars ranks those by prose richness — which says nothing about
// which one has an image. Asking SQL for "the" edition would pick a row with a
// NULL nameplate and report the character as having no art at all. So walk the
// editions in order and take the first that actually has one. This is the same
// shape as getchuintros' pickStory.
func selectCandidates(ctx context.Context, db, gdb *gorm.DB, matched []getchuchars.Candidate, st *Stats) ([]candidate, error) {
	plates, err := loadNameplates(ctx, gdb)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(matched))
	for _, m := range matched {
		ids = append(ids, m.CharacterID)
	}
	needs, err := loadPortraitless(ctx, db, ids)
	if err != nil {
		return nil, err
	}

	out := make([]candidate, 0, len(matched))
	for _, m := range matched {
		if !needs[m.CharacterID] {
			st.SkipHasImage++
			continue
		}
		file, ok := pickPlate(m, plates)
		if !ok {
			st.NoImage++
			continue
		}
		out = append(out, candidate{CharacterID: m.CharacterID, GetchuID: file.GetchuID, File: file.File})
	}
	return out, nil
}

// plateFile is a chosen image: which item's directory it is mirrored under and
// what the file is called.
type plateFile struct {
	GetchuID string
	File     string
}

// pickPlate returns the first edition of this character that carries a bust.
// Editions are richest-first, so a character whose richest edition has art
// keeps using that one run to run.
func pickPlate(c getchuchars.Candidate, plates map[nameplateKey]string) (plateFile, bool) {
	eds := c.Editions
	if len(eds) == 0 {
		// Defensive: a Candidate built before Editions existed. Its own
		// (GetchuID, Ordinal) is Editions[0] by definition.
		eds = []getchuchars.Edition{{GetchuID: c.GetchuID, Ordinal: c.Ordinal}}
	}
	for _, e := range eds {
		if url, ok := plates[nameplateKey{GetchuID: e.GetchuID, Ordinal: e.Ordinal}]; ok {
			return plateFile{GetchuID: e.GetchuID, File: path.Base(url)}, true
		}
	}
	return plateFile{}, false
}

// loadNameplates reads the page's own character→image pairing.
//
// It deliberately does NOT require the bytes to be mirrored. Whether a page
// offers art and whether anyone has downloaded it yet are different facts, and
// folding them together makes the dry run useless at the one moment it matters
// most: before the mirror pass, every fillable character would report as
// "no art" and the population would read as zero. Un-mirrored bytes surface as
// Missing instead — which IS the list of images to go and fetch.
//
// The join to item_images stays, without the local_path condition, because a
// nameplate_url the crawler never recorded as an image row is not fetchable and
// would only produce a permanent Missing.
func loadNameplates(ctx context.Context, gdb *gorm.DB) (map[nameplateKey]string, error) {
	var rows []struct {
		GetchuID string `gorm:"column:getchu_id"`
		Ordinal  int    `gorm:"column:ordinal"`
		URL      string `gorm:"column:url"`
	}
	err := gdb.WithContext(ctx).Raw(`
		SELECT c.getchu_id, c.ordinal, c.nameplate_url AS url
		FROM item_characters c
		JOIN item_images i ON i.getchu_id = c.getchu_id AND i.kind = 'nameplate'
			AND i.url = c.nameplate_url
		WHERE c.nameplate_url IS NOT NULL`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read staging nameplates: %w", err)
	}
	out := make(map[nameplateKey]string, len(rows))
	for _, r := range rows {
		out[nameplateKey{GetchuID: r.GetchuID, Ordinal: r.Ordinal}] = r.URL
	}
	return out, nil
}

// loadPortraitless returns the subset of ids whose character has no portrait
// yet. Asking for the ones that NEED filling (rather than the ones that have an
// image) keeps a character that vanished between the match and this query out
// of the candidate set instead of silently in it.
func loadPortraitless(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for i := 0; i < len(ids); i += 5000 {
		batch := ids[i:min(i+5000, len(ids))]
		var got []int64
		if err := db.WithContext(ctx).Raw(
			`SELECT id FROM catalog_character
			 WHERE id IN ? AND deleted_at IS NULL AND image_hash IS NULL`, batch).
			Scan(&got).Error; err != nil {
			return nil, fmt.Errorf("load portraitless characters: %w", err)
		}
		for _, id := range got {
			out[id] = true
		}
	}
	return out, nil
}

// writeIDs writes the distinct Getchu ids behind a candidate set, one per line
// and sorted, so re-running a dry run produces a byte-identical worklist.
func writeIDs(path string, cands []candidate) error {
	seen := map[string]bool{}
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		if !seen[c.GetchuID] {
			seen[c.GetchuID] = true
			ids = append(ids, c.GetchuID)
		}
	}
	sort.Strings(ids)
	return os.WriteFile(path, []byte(strings.Join(ids, "\n")+"\n"), 0o644)
}

// mirrorPath is where kun-getchu-api's mirror pass writes an image:
// <root>/<getchu_id>/<basename>. Derived rather than read from the staging
// row's local_path, which is absolute on the crawler's host and so meaningless
// anywhere else.
func mirrorPath(root, getchuID, file string) string {
	return strings.Join([]string{strings.TrimRight(root, "/"), getchuID, file}, "/")
}
