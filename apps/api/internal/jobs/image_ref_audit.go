package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"api/internal/infrastructure/database"
	jobmodel "api/internal/jobs/model"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

// JobImageRefAudit is this job's registry name; the job reads its own previous
// run back out of job_run, so the name has to be shared with the registration.
const JobImageRefAudit = "image-ref-audit"

// ImageRefAuditOpts controls the reconcile sweep.
type ImageRefAuditOpts struct {
	Batch   int           // hashes per meta-batch probe (max 1000)
	Timeout time.Duration // overall run timeout
}

// DefaultImageRefAuditOpts is what the scheduler uses.
func DefaultImageRefAuditOpts() ImageRefAuditOpts {
	return ImageRefAuditOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

// maxListed caps how many hashes ride the persisted summary. The list is what
// the NEXT run diffs against, so truncating it would make dropped hashes look
// newly-broken forever; instead the run fails loudly if the set ever exceeds
// this, which at 386k references means something is systemically wrong.
const maxListed = 500

// RunImageRefAudit reconciles catalog's image REFERENCES against the bytes
// image_service actually still holds, and is the safety net for a failure mode
// the two systems cannot see on their own: image_service keys bytes by hash and
// catalog keys references by row, and neither knows about the other. Deleting an
// image in the admin console (DELETE /admin/image/:hash without ?force) sets
// deleted_at and stops there — every catalog_work_cover / catalog_work_screenshot
// row pointing at it keeps returning a hash whose bytes are gone, so the gallery
// renders empty frames. Thirty days later the GC hard-deletes the row and the
// S3 objects and the image is unrecoverable. Until this job existed the only
// discovery channel was a user complaint (2026-08-03: 18 dead references across
// 10 works, found that way; 13 had already passed the hard-delete horizon).
//
// The point of running this DAILY is the 30-day soft-delete window: a deletion
// caught inside it is fully reversible (clear deleted_at, the bytes are still in
// storage), so a daily sweep downgrades "permanent data loss" to "an alert".
//
// It fails only on NEWLY broken references, diffed against its own previous run.
// References that are already broken beyond rescue would otherwise turn this into
// a permanently red job, which is how alerts get ignored.
//
// The probe is POST /image/meta-batch, which answers only for rows that exist and
// are not soft-deleted — so "absent from the result" is exactly the condition we
// are hunting, with no need to reach into the image service's database.
func RunImageRefAudit(ctx context.Context, cfg *config.Config, opts ImageRefAuditOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000 // image_service / SDK reject batches > 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// An audit that skips itself when misconfigured is the very failure mode it
	// exists to catch, so an unset client is a hard error, not a soft skip.
	clientCfg := cfg.CatalogImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("catalog image client not configured (KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to report a clean audit it did not perform")
	}

	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("catalog db connect: %w", err)
	}
	defer catalogDB.Close()

	refs, err := collectCatalogImageRefs(ctx, catalogDB.DB())
	if err != nil {
		return nil, fmt.Errorf("collect catalog image refs: %w", err)
	}
	hashes := distinctHashes(refs)
	slog.Info("image-ref-audit: collected catalog references", "refs", len(refs), "distinct_hashes", len(hashes))
	if len(refs) == 0 {
		return Summary{"references": 0, "note": "nothing to audit"}, nil
	}

	cli := imageclient.New(imageclient.Config{
		BaseURL:      clientCfg.BaseURL,
		CDNBase:      cfg.ImageService.CDNBase,
		ClientID:     clientCfg.ClientID,
		ClientSecret: clientCfg.ClientSecret,
	})

	live := make(map[string]struct{}, len(hashes))
	for _, b := range chunk(hashes, opts.Batch) {
		meta, err := cli.MetaBatch(ctx, b)
		if err != nil {
			// A partial probe would report phantom breakage for every hash in
			// the batch that never got asked about, so bail instead.
			return nil, fmt.Errorf("meta-batch probe (%d hashes): %w", len(b), err)
		}
		for h := range meta {
			live[h] = struct{}{}
		}
	}

	brokenRefs := make([]imageRef, 0)
	brokenSet := make(map[string]struct{})
	for _, r := range refs {
		if _, ok := live[r.Hash]; ok {
			continue
		}
		brokenRefs = append(brokenRefs, r)
		brokenSet[r.Hash] = struct{}{}
	}

	summary := Summary{
		"references":      len(refs),
		"distinct_hashes": len(hashes),
		"broken_refs":     len(brokenRefs),
		"broken_hashes":   len(brokenSet),
	}
	if len(brokenRefs) == 0 {
		summary["broken_hash_list"] = []string{} // baseline for the next run's diff
		return summary, nil
	}

	summary["affected"] = affectedEntities(brokenRefs)
	if len(brokenSet) > maxListed {
		return summary, fmt.Errorf("%d broken hashes exceeds the %d the summary can carry — the next run would misread the truncated list as repair; investigate before raising the cap",
			len(brokenSet), maxListed)
	}
	broken := sortedKeys(brokenSet)
	summary["broken_hash_list"] = broken

	previous, hasBaseline, err := previousBrokenHashes(ctx, cfg)
	if err != nil {
		// Without a baseline every hash reads as new. Report the findings but
		// don't manufacture an alert out of a lookup failure.
		return summary, fmt.Errorf("read previous audit baseline: %w", err)
	}
	if !hasBaseline {
		summary["baseline"] = true
		slog.Warn("image-ref-audit: first run — recording baseline, not alerting",
			"broken_refs", len(brokenRefs), "broken_hashes", len(brokenSet))
		return summary, nil
	}

	fresh := newlyBroken(broken, previous)
	summary["new_broken"] = len(fresh)
	if len(fresh) == 0 {
		slog.Info("image-ref-audit: no new breakage", "known_broken_hashes", len(brokenSet))
		return summary, nil
	}
	summary["new_broken_list"] = fresh

	// Newly broken means the bytes went away recently, which means the deletion
	// is probably still inside the 30-day soft-delete window and reversible —
	// this is the actionable alert, and it is time-boxed.
	return summary, fmt.Errorf("%d catalog image reference(s) newly point at deleted bytes (%d refs broken in total) — check image_service for a soft-delete still inside its 30-day window and restore it before the GC hard-deletes: %v",
		len(fresh), len(brokenRefs), fresh)
}

// imageRef is one catalog row that points at an image hash.
type imageRef struct {
	Hash     string `gorm:"column:hash"`
	Kind     string `gorm:"column:kind"`
	EntityID int64  `gorm:"column:entity_id"`
}

// collectCatalogImageRefs returns every live catalog row that references an
// image, carrying the owning entity so a broken hash can be reported as the
// work/character a user would see it on.
//
// This is the same universe catalog-image-refping keeps alive, minus the
// aggregation: covers and screenshots in full (a shadowed media row still owns
// its bytes), portraits only while their character is live.
func collectCatalogImageRefs(ctx context.Context, db *gorm.DB) ([]imageRef, error) {
	const q = `
SELECT image_hash AS hash, 'work_cover' AS kind, work_id AS entity_id
    FROM catalog_work_cover WHERE image_hash IS NOT NULL AND image_hash <> ''
UNION ALL
SELECT image_hash, 'work_screenshot', work_id
    FROM catalog_work_screenshot WHERE image_hash IS NOT NULL AND image_hash <> ''
UNION ALL
SELECT image_hash, 'character_portrait', id
    FROM catalog_character
    WHERE image_hash IS NOT NULL AND image_hash <> '' AND deleted_at IS NULL
`
	var refs []imageRef
	if err := db.WithContext(ctx).Raw(q).Scan(&refs).Error; err != nil {
		return nil, err
	}
	return refs, nil
}

// previousBrokenHashes loads the broken set this job recorded last time. Runs
// that FAILED count: a run that reports new breakage fails by design, and the
// next one must diff against it or it would re-alert on the same hashes.
// Reports hasBaseline=false when no prior run carried a list (first deploy).
func previousBrokenHashes(ctx context.Context, cfg *config.Config) (map[string]struct{}, bool, error) {
	// job_run lives in the OAuth core DB regardless of which DB a job touches.
	coreDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, false, fmt.Errorf("core db connect: %w", err)
	}
	defer coreDB.Close()

	// jsonb_exists(), not the `?` operator: `?` is GORM's bind placeholder and
	// would be eaten as an argument slot.
	var row jobmodel.JobRun
	err = coreDB.DB().WithContext(ctx).
		Where("job_name = ? AND summary IS NOT NULL AND jsonb_exists(summary, 'broken_hash_list')", JobImageRefAudit).
		Order("started_at DESC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}

	var parsed struct {
		BrokenHashList []string `json:"broken_hash_list"`
	}
	if err := json.Unmarshal(row.Summary, &parsed); err != nil {
		return nil, false, fmt.Errorf("decode previous summary: %w", err)
	}
	out := make(map[string]struct{}, len(parsed.BrokenHashList))
	for _, h := range parsed.BrokenHashList {
		out[h] = struct{}{}
	}
	return out, true, nil
}

// newlyBroken returns the hashes in broken that the previous run had not
// already recorded — the alertable subset, since anything the last run knew
// about is either already handled or already past rescue.
func newlyBroken(broken []string, previous map[string]struct{}) []string {
	fresh := make([]string, 0)
	for _, h := range broken {
		if _, seen := previous[h]; !seen {
			fresh = append(fresh, h)
		}
	}
	return fresh
}

// affectedEntities counts the distinct entities behind the broken refs, keyed
// by kind, so the summary says "3 works" rather than only "8 hashes".
func affectedEntities(refs []imageRef) map[string]int {
	seen := make(map[string]map[int64]struct{})
	for _, r := range refs {
		if seen[r.Kind] == nil {
			seen[r.Kind] = make(map[int64]struct{})
		}
		seen[r.Kind][r.EntityID] = struct{}{}
	}
	out := make(map[string]int, len(seen))
	for kind, ids := range seen {
		out[kind] = len(ids)
	}
	return out
}

func distinctHashes(refs []imageRef) []string {
	set := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		set[r.Hash] = struct{}{}
	}
	return sortedKeys(set)
}

// sortedKeys keeps the persisted lists stable so a diff between two runs
// reflects real change, not map iteration order.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
