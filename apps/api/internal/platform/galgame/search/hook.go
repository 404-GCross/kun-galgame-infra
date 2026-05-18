package search

import (
	"context"
	"log/slog"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// Hook provides fire-and-forget write-through from CRUD paths to Meilisearch.
//
// Philosophy (Phase 1):
//   - Called by handlers after a successful DB mutation
//   - Runs in a goroutine so the HTTP response isn't delayed
//   - Failures only log — reindex-search is the recovery path
//   - Safe to no-op if the hook is nil (for tests / lightweight commands)
//
// Not called from:
//   - Batch scripts (sync-vndb, migrate-*) — they rely on reindex-search
//   - Link/alias/revision handlers — TODO, for now rely on periodic reindex
type Hook struct {
	db      *gorm.DB // wiki DB, used to reload with preloads
	indexer *Indexer
}

// NewHook creates a Hook. Passing nil db or indexer makes all methods no-ops.
func NewHook(db *gorm.DB, indexer *Indexer) *Hook {
	if db == nil || indexer == nil {
		return nil
	}
	return &Hook{db: db, indexer: indexer}
}

// Galgame reindexes a galgame by id (reloads with relations preloaded).
// No-op if h is nil.
func (h *Hook) Galgame(id int) {
	if h == nil {
		return
	}
	go func() {
		ctx := context.Background()
		var g model.Galgame
		if err := h.db.
			Preload("Alias").
			Preload("Tag.Tag").
			Preload("Official.Official").
			Preload("Engine.Engine").
			First(&g, id).Error; err != nil {
			slog.Warn("search hook: load galgame", "id", id, "err", err)
			return
		}
		if err := h.indexer.UpsertGalgame(ctx, &g); err != nil {
			slog.Warn("search hook: upsert galgame", "id", id, "err", err)
		}
	}()
}

// GalgameDelete removes a galgame from the index by id.
// Only invoke when the row is actually deleted from DB (currently not used —
// wiki soft-deletes via status, doesn't hard-delete galgames).
func (h *Hook) GalgameDelete(id int) {
	if h == nil {
		return
	}
	go func() {
		if err := h.indexer.DeleteGalgame(context.Background(), id); err != nil {
			slog.Warn("search hook: delete galgame", "id", id, "err", err)
		}
	}()
}

// Tag reindexes a tag by id. Reloads with aliases preloaded + galgame_count.
func (h *Hook) Tag(id int) {
	if h == nil {
		return
	}
	go func() {
		ctx := context.Background()
		var t model.GalgameTag
		if err := h.db.
			Preload("Alias").
			Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
			Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id").
			First(&t, id).Error; err != nil {
			slog.Warn("search hook: load tag", "id", id, "err", err)
			return
		}
		if err := h.indexer.UpsertTag(ctx, &t); err != nil {
			slog.Warn("search hook: upsert tag", "id", id, "err", err)
		}
	}()
}

// TagDelete removes a tag from the index by id. Invoke only when the tag
// row is actually deleted from DB.
func (h *Hook) TagDelete(id int) {
	if h == nil {
		return
	}
	go func() {
		if err := h.indexer.DeleteTag(context.Background(), id); err != nil {
			slog.Warn("search hook: delete tag", "id", id, "err", err)
		}
	}()
}

// OfficialDelete removes an official from the index by id. Invoke only
// when the official row is actually deleted from DB.
func (h *Hook) OfficialDelete(id int) {
	if h == nil {
		return
	}
	go func() {
		if err := h.indexer.DeleteOfficial(context.Background(), id); err != nil {
			slog.Warn("search hook: delete official", "id", id, "err", err)
		}
	}()
}

// Official reindexes an official by id.
func (h *Hook) Official(id int) {
	if h == nil {
		return
	}
	go func() {
		ctx := context.Background()
		var o model.GalgameOfficial
		if err := h.db.
			Preload("Alias").
			Select("galgame_official.*, COALESCE(oc.cnt, 0) AS cnt").
			Joins("LEFT JOIN (SELECT r.official_id, COUNT(*) AS cnt FROM galgame_official_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.official_id) oc ON oc.official_id = galgame_official.id").
			First(&o, id).Error; err != nil {
			slog.Warn("search hook: load official", "id", id, "err", err)
			return
		}
		if err := h.indexer.UpsertOfficial(ctx, &o); err != nil {
			slog.Warn("search hook: upsert official", "id", id, "err", err)
		}
	}()
}
