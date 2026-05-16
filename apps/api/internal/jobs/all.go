package jobs

import (
	"context"

	"api/pkg/config"
)

// RegisterAll registers every job and its schedule in one place — the
// authoritative "what jobs exist + when" list (in git, reviewable). Only
// genuinely periodic tasks get a schedule; on-demand ones (future:
// reindex-search, sync-vndb-relations) would register with a zero
// Schedule (manual-trigger only).
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
}
