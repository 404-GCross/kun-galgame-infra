// Package migrate owns the kun_news schema: the AutoMigrate table list, the
// idempotent raw-SQL section (feed index, unique keys, the image FK, the
// preview-length CHECK) and the news_source seed. It is called by
// cmd/migrate-news and by the news integration tests, which provision their
// database with the exact production schema.
package migrate

import (
	"fmt"

	"api/internal/platform/news/model"

	"gorm.io/gorm"
)

// Run applies the full kun_news schema. Idempotent: safe on every deploy and
// repeatedly against the same database.
func Run(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&model.NewsSource{},
		&model.NewsItem{},
		&model.NewsItemImage{},
		&model.NewsItemWork{},
	); err != nil {
		return fmt.Errorf("news automigrate: %w", err)
	}
	if err := rawSQL(db); err != nil {
		return err
	}
	return seedSources(db)
}

func rawSQL(db *gorm.DB) error {
	for _, s := range []struct{ name, stmt string }{
		// (source_key, external_id) is the ingestion identity: re-running an
		// importer must UPDATE the row it wrote last time, never append a second
		// copy of the same upstream article.
		{"news_item_src_ext", `
			CREATE UNIQUE INDEX IF NOT EXISTS news_item_src_ext
			    ON news_item (source_key, external_id)`},
		{"news_item_feed", `
			CREATE INDEX IF NOT EXISTS news_item_feed
			    ON news_item (published_at DESC, id DESC)`},
		{"news_item_image_uk", `
			CREATE UNIQUE INDEX IF NOT EXISTS news_item_image_uk
			    ON news_item_image (item_id, image_hash)`},
		// The source FK is declared here rather than through a GORM association:
		// news_item.id is an IDENTITY primary key, and GORM copies a referenced
		// PK's full type onto the FK column, which would make item_id an identity
		// column too (gorm-identity-pk-recipe).
		{"news_item_source_fk", `
			DO $$ BEGIN
			    ALTER TABLE news_item
			        ADD CONSTRAINT news_item_source_fk
			        FOREIGN KEY (source_key) REFERENCES news_source(key);
			EXCEPTION WHEN duplicate_object THEN NULL; END $$`},
		{"news_item_image_item_fk", `
			DO $$ BEGIN
			    ALTER TABLE news_item_image
			        ADD CONSTRAINT news_item_image_item_fk
			        FOREIGN KEY (item_id) REFERENCES news_item(id) ON DELETE CASCADE;
			EXCEPTION WHEN duplicate_object THEN NULL; END $$`},
		{"news_item_work_item_fk", `
			DO $$ BEGIN
			    ALTER TABLE news_item_work
			        ADD CONSTRAINT news_item_work_item_fk
			        FOREIGN KEY (item_id) REFERENCES news_item(id) ON DELETE CASCADE;
			EXCEPTION WHEN duplicate_object THEN NULL; END $$`},
		// 月幕 authorised the preview message and the banner, explicitly not the
		// article body. The importers truncate; this constraint is what makes an
		// importer bug fail loudly instead of quietly storing a full article.
		{"news_item_preview_len", fmt.Sprintf(`
			DO $$ BEGIN
			    ALTER TABLE news_item
			        ADD CONSTRAINT news_item_preview_len
			        CHECK (char_length(preview) <= %d);
			EXCEPTION WHEN duplicate_object THEN NULL; END $$`, model.PreviewMaxRunes)},
	} {
		if err := db.Exec(s.stmt).Error; err != nil {
			return fmt.Errorf("news schema %s: %w", s.name, err)
		}
	}
	return nil
}

// seedSources inserts the partner rows the read face needs. ON CONFLICT DO
// NOTHING, so a row edited in place (attribution wording, column_url) survives
// every redeploy.
//
// Only 月幕 is seeded here. Galgame 批评's row lands with its backfill, which is
// where the column URLs it must carry are first known — a placeholder
// homepage_url would put a wrong link on every item published under her
// attribution, which is the one condition she asked for.
func seedSources(db *gorm.DB) error {
	const stmt = `
		INSERT INTO news_source (key, display_name, homepage_url, attribution, publisher_uid, column_url, active)
		VALUES (?, ?, ?, ?, ?, '', true)
		ON CONFLICT (key) DO NOTHING`
	if err := db.Exec(stmt,
		model.SourceKeyYmgal,
		"月幕 Galgame",
		"https://www.ymgal.games",
		"本条情报转载自月幕 Galgame,点击标题可跳转至月幕原文",
		114748,
	).Error; err != nil {
		return fmt.Errorf("news seed source %s: %w", model.SourceKeyYmgal, err)
	}
	return nil
}
