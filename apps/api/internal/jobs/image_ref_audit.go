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
	"api/internal/platform/catalog/imagerefs"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const JobImageRefAudit = "image-ref-audit"

type ImageRefAuditOpts struct {
	Batch   int
	Timeout time.Duration
}

func DefaultImageRefAuditOpts() ImageRefAuditOpts {
	return ImageRefAuditOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

const maxListed = 500

func RunImageRefAudit(ctx context.Context, cfg *config.Config, opts ImageRefAuditOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	clientCfg := cfg.CatalogImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("catalog image client not configured (KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to report a clean audit it did not perform")
	}

	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("catalog db connect: %w", err)
	}
	defer catalogDB.Close()

	refs, err := imagerefs.Collect(ctx, catalogDB.DB())
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
			return nil, fmt.Errorf("meta-batch probe (%d hashes): %w", len(b), err)
		}
		for h := range meta {
			live[h] = struct{}{}
		}
	}

	brokenRefs := make([]imagerefs.Ref, 0)
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
		summary["broken_hash_list"] = []string{}
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

	return summary, fmt.Errorf("%d catalog image reference(s) newly point at deleted bytes (%d refs broken in total) — check image_service for a soft-delete still inside its 30-day window and restore it before the GC hard-deletes: %v",
		len(fresh), len(brokenRefs), fresh)
}

func previousBrokenHashes(ctx context.Context, cfg *config.Config) (map[string]struct{}, bool, error) {
	coreDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, false, fmt.Errorf("core db connect: %w", err)
	}
	defer coreDB.Close()

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

func newlyBroken(broken []string, previous map[string]struct{}) []string {
	fresh := make([]string, 0)
	for _, h := range broken {
		if _, seen := previous[h]; !seen {
			fresh = append(fresh, h)
		}
	}
	return fresh
}

func affectedEntities(refs []imagerefs.Ref) map[string]int {
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

func distinctHashes(refs []imagerefs.Ref) []string {
	set := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		set[r.Hash] = struct{}{}
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
