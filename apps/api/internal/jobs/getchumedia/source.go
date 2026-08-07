package getchumedia

import (
	"context"
	"fmt"
	"path"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// candidate is one work this lane may fill, with the Getchu ids it is anchored
// to and the content rating its screenshots inherit.
type candidate struct {
	WorkID        int64
	ContentRating int16
	GetchuIDs     []string
}

type candidateRow struct {
	WorkID        int64  `gorm:"column:work_id"`
	ContentRating int16  `gorm:"column:content_rating"`
	GetchuID      string `gorm:"column:getchu_id"`
}

// loadCandidates resolves the galgame works that carry an EXACT Getchu release
// anchor and have NO Getchu-sourced screenshot row yet.
//
// PER-SOURCE FILL-MISSING (wave 188, the user's 2026-08-07 ruling). The old rule
// was whole-work emptiness: Getchu was a FALLBACK for works showing nothing at
// all, never a supplement (refs/proj/125). That ruling is OVERTURNED. The lanes
// are semantically different images — a vndb row is a GAME SCREENSHOT, a
// getchu/dlsite row is OFFICIAL SAMPLE CG — and the read face groups the gallery
// per source, so a second lane is an addition to the page, not a dilution of a
// curated one. Admission is therefore scoped to THIS source: a work with vndb,
// dlsite or curated rows but no getchu rows is a candidate.
//
// No population filter beyond that. Unlike an intro, a screenshot gallery costs
// the reader nothing when the work is a draft, and the claimed/bodyless split
// does not change whether this source's lane is empty.
//
// Cross-source identical bytes are NOT admitted twice: the (work_id, image_hash)
// unique key rejects them at write time and the first writer keeps its source
// attribution (see preloadHashes, which deliberately reads ALL sources' hashes).
func loadCandidates(ctx context.Context, db *gorm.DB, source, medium int16) ([]candidate, error) {
	var rows []candidateRow
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT w.id AS work_id, w.content_rating, r.external_id AS getchu_id
		FROM catalog_work w
		JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
			AND r.source_id = ? AND r.link_kind = ?
		WHERE w.medium_id = ? AND w.deleted_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM catalog_work_screenshot s
				WHERE s.work_id = w.id AND s.source_id = ?)
		ORDER BY w.id, r.external_id`,
		model.EntityTypeRelease, source, model.LinkKindExact, medium, source).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	var out []candidate
	for i := 0; i < len(rows); {
		c := candidate{WorkID: rows[i].WorkID, ContentRating: rows[i].ContentRating}
		for ; i < len(rows) && rows[i].WorkID == c.WorkID; i++ {
			c.GetchuIDs = append(c.GetchuIDs, rows[i].GetchuID)
		}
		out = append(out, c)
	}
	return out, nil
}

// stagedImage is one mirrored sample image: where the bytes are and what to
// call the upload.
type stagedImage struct {
	GetchuID string
	Ordinal  int
	File     string // basename, which is also the mirror filename
	// SHA256 of the downloaded bytes, recorded by the mirror pass. Used ONLY to
	// skip re-uploading bytes this work has already sent (see fill); it is not
	// assumed equal to the image service's own hash, and the
	// (work_id, image_hash) unique key remains the correctness backstop.
	SHA256 string
}

// loadSamples reads the crawler's staging database for the sample images that
// have actually been mirrored.
//
// `sample` is the CG-screenshot kind. The other three are not screenshots:
// `package` is the store cover (and the anchored population is already at 100%
// cover coverage — the whole kind would fill five gaps), while `nameplate` and
// `portrait` are character art, whose home is catalog_character.image_hash and
// which cannot be attached to a character without the pairing the page carries
// and the parser does not yet record.
//
// The `_s.jpg` suffix is Getchu's thumbnail of the image beside it, not a
// separate CG. Only three such rows survived the parser, but uploading one
// would put a postage stamp in a gallery, so they are excluded here too.
func loadSamples(ctx context.Context, gdb *gorm.DB) (map[string][]stagedImage, error) {
	var rows []struct {
		GetchuID string `gorm:"column:getchu_id"`
		Ordinal  int    `gorm:"column:ordinal"`
		URL      string `gorm:"column:url"`
		SHA256   string `gorm:"column:sha256"`
	}
	err := gdb.WithContext(ctx).Raw(`
		SELECT getchu_id, ordinal, url, coalesce(sha256,'') AS sha256 FROM item_images
		WHERE kind = 'sample' AND local_path IS NOT NULL AND url NOT LIKE '%\_s.jpg'
		ORDER BY getchu_id, ordinal`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read staging item_images: %w", err)
	}
	out := map[string][]stagedImage{}
	for _, r := range rows {
		out[r.GetchuID] = append(out[r.GetchuID], stagedImage{
			GetchuID: r.GetchuID, Ordinal: r.Ordinal, File: path.Base(r.URL), SHA256: r.SHA256,
		})
	}
	return out, nil
}

// preloadHashes loads the image hashes already on the candidate works, so a
// re-run that finds the same bytes under a different sort order still skips.
// The (work_id, image_hash) unique index is the backstop.
//
// ALL sources, not just getchu, and that stays true under wave 188's per-source
// admission: the unique key spans the whole work, so bytes another lane already
// uploaded must not be re-uploaded here — the first writer keeps the row and its
// source attribution.
func preloadHashes(ctx context.Context, db *gorm.DB, workIDs []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID    int64  `gorm:"column:work_id"`
		ImageHash string `gorm:"column:image_hash"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT work_id, image_hash FROM catalog_work_screenshot WHERE work_id IN ?`, workIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		set := out[r.WorkID]
		if set == nil {
			set = map[string]bool{}
			out[r.WorkID] = set
		}
		set[r.ImageHash] = true
	}
	return out, nil
}

// mirrorPath is where kun-getchu-api's mirror pass writes an image:
// <root>/<getchu_id>/<basename>. Deriving it rather than reading the staging
// row's local_path keeps the job runnable from a machine that did not do the
// mirroring — the path recorded there is absolute on the crawler's host.
func mirrorPath(root, getchuID, file string) string {
	return strings.Join([]string{strings.TrimRight(root, "/"), getchuID, file}, "/")
}
