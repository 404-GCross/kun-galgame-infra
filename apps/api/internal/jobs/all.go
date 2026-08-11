package jobs

import (
	"context"
	"log/slog"
	"time"

	"api/internal/jobs/ymgalnews"
	"api/pkg/config"
)

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
		Schedule: Schedule{DailyAt: "04:15"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunCatalogImageRefping(ctx, cfg, DefaultCatalogImageRefpingOpts())
		},
	})

	r.Register(Job{
		Name:     "news-image-refping",
		Desc:     "情报 banner 与文中图 reference-ping，防 image_service TTL 回收（site=news）",
		Schedule: Schedule{DailyAt: "04:20"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunNewsImageRefping(ctx, cfg, DefaultNewsImageRefpingOpts())
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
		Name:     JobImageRefAudit,
		Desc:     "catalog 图片引用对账（引用指向已删字节即告警，赶在 30 天物删前抢救）",
		Schedule: Schedule{DailyAt: "04:45"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunImageRefAudit(ctx, cfg, DefaultImageRefAuditOpts())
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
		Schedule: Schedule{DailyAt: "06:00"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return RunPruneDeveloperUsage(ctx, cfg, DefaultPruneDeveloperUsageOpts())
		},
	})

	// 苍麟 asked for a polite cadence in his own words — 「搞个十分钟一次的定时任务
	// 应该问题不大」 — and this poll spends exactly one request per lane per tick
	// (page 1 only), which stays well inside what he allowed.
	//
	// NOTE: this is the first registered job to use Schedule.Every. The field and
	// scheduler.runLoop have always supported it, but no job had exercised it in
	// production before 2026-08-11. First run happens Every after boot, not at
	// boot — do not watch the logs at startup expecting it.
	r.Register(Job{
		Name:     "ymgal-news-poll",
		Desc:     "月幕情报/专栏增量轮询（每 lane 只取第 1 页）",
		Schedule: Schedule{Every: 10 * time.Minute},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return runYmgalNews(ctx, cfg, ymgalnews.Opts{
				Lanes: []string{ymgalnews.LaneNews, ymgalnews.LaneColumn},
				Pages: 1, Apply: true, Gap: time.Second,
			})
		},
	})

	// The deep sweep is what can see a deletion at all: the poll only ever looks
	// at page 1, so an ad 苍麟 removed from further back would never come up. It
	// reports dead candidates and deliberately does not act on them.
	r.Register(Job{
		Name:     "ymgal-news-sweep",
		Desc:     "月幕深巡（前 5 页）+ 上游消失候选报告（只报不打）",
		Schedule: Schedule{DailyAt: "04:05"},
		Run: func(ctx context.Context, cfg *config.Config) (Summary, error) {
			return runYmgalNews(ctx, cfg, ymgalnews.Opts{
				Lanes: []string{ymgalnews.LaneNews, ymgalnews.LaneColumn},
				Pages: 5, Apply: true, Gap: 2 * time.Second,
			})
		},
	})
}

// runYmgalNews soft-skips when the upstream client is unconfigured, so a
// deployment without the credentials logs and moves on instead of failing a job
// every ten minutes.
func runYmgalNews(ctx context.Context, cfg *config.Config, opts ymgalnews.Opts) (Summary, error) {
	if cfg.Ymgal.ClientID == "" || cfg.Ymgal.ClientSecret == "" {
		slog.Warn("ymgal news: client not configured — skipping (set KUN_YMGAL_CLIENT_ID/SECRET)")
		return Summary{"skipped": "ymgal client not configured"}, nil
	}
	return ymgalnews.Run(ctx, cfg, opts)
}
