package jobs

import (
	"context"

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
}
