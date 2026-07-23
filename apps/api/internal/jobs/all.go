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
func RegisterAll(r *Registry) {
	r.Register(Job{
		Name:     "sync-vndb",
		Desc:     "VNDB → galgame wiki 增量同步（status=2 草稿）",
		Schedule: Schedule{DailyAt: "03:00"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunSyncVNDB(ctx, cfg, DefaultSyncVNDBOpts())
		},
	})

	r.Register(Job{
		Name:     "sync-vndb-enrich",
		Desc:     "VNDB → galgame wiki 富集 links+tags+officials（已发布游戏中尚缺 vndb 数据的，如新建/claim）",
		Schedule: Schedule{DailyAt: "05:00"}, // staggered after sync-vndb (03:00) + refpings
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunSyncVNDBEnrich(ctx, cfg, DefaultSyncVNDBEnrichOpts())
		},
	})

	r.Register(Job{
		Name:     "sync-vndb-covers",
		Desc:     "VNDB → galgame wiki 封面同步（已发布但尚无封面的游戏，如新建/claim）",
		Schedule: Schedule{DailyAt: "03:45"}, // after image-gc (03:30), before galgame-image-refping (04:00)
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunSyncVNDBCovers(ctx, cfg, DefaultSyncVNDBCoversOpts())
		},
	})

	r.Register(Job{
		Name:     "sync-vndb-screenshots",
		Desc:     "VNDB → galgame wiki 截图同步（已发布但尚无 vndb 截图的游戏，如新建/claim）",
		Schedule: Schedule{DailyAt: "03:50"}, // after sync-vndb-covers (03:45), before galgame-image-refping (04:00)
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunSyncVNDBScreenshots(ctx, cfg, DefaultSyncVNDBScreenshotsOpts())
		},
	})

	r.Register(Job{
		Name:     "sync-vndb-scores",
		Desc:     "VNDB → galgame wiki 评分同步（rating+votecount 落窄表 galgame_vndb_meta）",
		Schedule: Schedule{DailyAt: "05:15"}, // staggered: after sync-vndb-enrich (05:00), before artifact-gc (05:30)
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunSyncVNDBScores(ctx, cfg, DefaultSyncVNDBScoresOpts())
		},
	})

	r.Register(Job{
		Name:     "build-galgame-stats",
		Desc:     "跨源统计物化（release_years/score 直方图/yearly_scores/coverage 快照落 galgame_stats）",
		Schedule: Schedule{DailyAt: "05:45"}, // staggered: after sync-vndb-scores (05:15) has refreshed the narrow tables
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunBuildGalgameStats(ctx, cfg, DefaultBuildGalgameStatsOpts())
		},
	})

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
