package jobs

import (
	"context"

	"api/pkg/config"
)

// RegisterAll registers every job and its schedule in one place — the
// authoritative "what jobs exist + when" list (in git, reviewable). Only
// genuinely periodic tasks get a schedule; on-demand ones (future:
// reindex-search) would register with a zero Schedule (manual-trigger only).
//
// Schedules are in the process local timezone — set TZ on the oauth
// container (docs/jobs/01-implementation-plan.md §6), else they are UTC.
//
// Wave 161 unregistered seven jobs whose only subject was the galgame table
// family: sync-vndb (03:00), reconcile-galgame-claims (03:20), sync-vndb-covers
// (03:45), sync-vndb-screenshots (03:50), sync-vndb-enrich (05:00),
// sync-vndb-scores (05:15) and build-galgame-stats (05:45). They are
// unregistered in the SAME deploy that takes down the wiki write faces, well
// ahead of the DROP: a scheduled job that writes a table which is about to
// disappear is a race with a deploy window, and one of them (sync-vndb) mints
// brand-new galgame rows nightly — behind a migration that has already run,
// those rows would be invisible to everything and lost at T3.
//
// The catalog registry keeps its own upstream lanes (jobs/workratings,
// bangumicovers, worktags, ...), which read the same VNDB/Bangumi sources
// directly and write catalog tables.
func RegisterAll(r *Registry) {
	r.Register(Job{
		Name:     "image-gc",
		Desc:     "image_service TTL 生命周期（冷候选/软删/物删）",
		Schedule: Schedule{DailyAt: "03:30"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunImageGC(ctx, cfg, DefaultImageGCOpts())
		},
	})

	r.Register(Job{
		Name:     "galgame-image-refping",
		Desc:     "galgame banner reference-ping，防 image_service TTL 回收",
		Schedule: Schedule{DailyAt: "04:00"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunGalgameImageRefping(ctx, cfg, DefaultGalgameImageRefpingOpts())
		},
	})

	r.Register(Job{
		Name:     "catalog-image-refping",
		Desc:     "catalog 角色立绘 reference-ping，防 image_service TTL 回收（site=catalog）",
		Schedule: Schedule{DailyAt: "04:15"}, // between galgame-image-refping (04:00) and user-avatar-refping (04:30)
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunCatalogImageRefping(ctx, cfg, DefaultCatalogImageRefpingOpts())
		},
	})

	r.Register(Job{
		Name:     "user-avatar-refping",
		Desc:     "用户头像 reference-ping，防 image_service TTL 回收",
		Schedule: Schedule{DailyAt: "04:30"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunUserAvatarRefping(ctx, cfg, DefaultUserAvatarRefpingOpts())
		},
	})

	r.Register(Job{
		Name:     "artifact-gc",
		Desc:     "artifact 生命周期（孤儿上传回收 + 软删物理回收）",
		Schedule: Schedule{DailyAt: "05:30"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunArtifactGC(ctx, cfg, DefaultArtifactGCOpts(cfg))
		},
	})

	r.Register(Job{
		Name:     "prune-developer-usage",
		Desc:     "developer_api_usage 留存修剪（删除 day < 今天−400 天 的计量汇总行）",
		Schedule: Schedule{DailyAt: "06:00"}, // after artifact-gc (05:30); off the image/vndb window
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunPruneDeveloperUsage(ctx, cfg, DefaultPruneDeveloperUsageOpts())
		},
	})
}
