package hihyou

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"api/internal/platform/news/model"
	"api/internal/platform/news/newstest"
	"api/pkg/config"

	"gorm.io/gorm"
)

func writeCorpus(t *testing.T, dir string, arts ...*Article) {
	t.Helper()
	c := Corpus{Dir: dir}
	if err := c.Mkdirs(); err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Write(c.ArticlePath(a.Data.ID), b); err != nil {
			t.Fatal(err)
		}
	}
}

// healthy is four items under two sections. The third heading carries only a
// picture, which is real upstream shape and must be dropped per item rather than
// stored as a row with an empty preview.
func healthy(cv int64) *Article {
	a := article("【Gal周报200期】x",
		text(17, true, "新闻日期：2026年8月1日~2026年8月8日"),
		text(17, false, "新作资讯"),
		text(17, true, "1.《A》情报公开"), text(17, false, body),
		picture("http://i0.hdslb.com/bfs/new_dyn/a.png"),
		text(17, true, "2.《B》发售日决定"), text(17, false, body),
		text(17, false, "汉化情报"),
		text(17, true, "3.《C》汉化发布"), text(17, false, body),
		text(17, true, "4.《D》图集"), picture("//i0.hdslb.com/bfs/article/d.png"),
	)
	a.Data.ID = cv
	a.Data.PublishTime = 1786257779
	return a
}

func openTestDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	db, release, ok := newstest.Open()
	if !ok {
		t.Skip("news test database unavailable")
	}
	t.Cleanup(release)
	if err := newstest.Truncate(db); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_news_test sslmode=disable"
	}
	return db, dsn
}

func TestImportWritesPendingItemsAndIsIdempotent(t *testing.T) {
	db, dsn := openTestDB(t)
	dir := t.TempDir()
	// 期201 holds a single item, which the gate refuses. It must contribute no
	// rows at all — a mis-segmented issue is quarantined whole, never partially
	// ingested, because its rows look plausible one at a time.
	broken := article("【Gal周报201期】x", text(17, false, "新作资讯"),
		text(17, true, "《只有一条》"), text(17, false, body))
	broken.Data.ID = 999
	writeCorpus(t, dir, healthy(1001), broken)

	opts := Opts{Dir: dir, NoImages: true, DSN: dsn}
	cfg := &config.Config{}
	ctx := context.Background()

	dry, err := Import(ctx, cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if dry.WouldCreate != 3 || dry.Created != 0 {
		t.Fatalf("dry run: would_create=%d created=%d, want 3/0", dry.WouldCreate, dry.Created)
	}
	if dry.Passing != 1 || len(dry.Quarantined) != 1 {
		t.Fatalf("dry run: passing=%d quarantined=%d, want 1/1", dry.Passing, len(dry.Quarantined))
	}
	var n int64
	db.Model(&model.NewsItem{}).Count(&n)
	if n != 0 {
		t.Fatalf("dry run wrote %d rows", n)
	}

	opts.Apply = true
	applied, err := Import(ctx, cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Created != 3 || applied.DroppedNoBody != 1 {
		t.Fatalf("apply: created=%d dropped=%d, want 3/1", applied.Created, applied.DroppedNoBody)
	}

	var rows []model.NewsItem
	if err := db.Where("source_key = ?", model.SourceKeyHihyou).
		Order("external_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("stored %d rows, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Status != model.StatusPending {
			t.Errorf("%s: status %d — the backfill must not bypass the gate", r.ExternalID, r.Status)
		}
		if r.SourceURL != SourceURL(1001) {
			t.Errorf("%s: source_url %q", r.ExternalID, r.SourceURL)
		}
		if r.Lane != model.LaneNews {
			t.Errorf("%s: lane %q — an extracted item is not the column itself", r.ExternalID, r.Lane)
		}
	}
	if rows[0].ExternalID != ExternalID(1001, 1) {
		t.Errorf("external_id = %q", rows[0].ExternalID)
	}
	// The serial prefix is stripped: a published item's "1." has no referent.
	if rows[0].Title != "《A》情报公开" || rows[0].UpstreamCategory != "新作资讯" {
		t.Errorf("first row = %q / %q", rows[0].Title, rows[0].UpstreamCategory)
	}
	if rows[2].UpstreamCategory != "汉化情报" {
		t.Errorf("third row section = %q", rows[2].UpstreamCategory)
	}

	// Re-running must recognise every row. The identity is (cv, ordinal), so a
	// second import that created anything would mean the segmentation drifted.
	again, err := Import(ctx, cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created != 0 || again.Updated != 0 || again.Unchanged != 3 {
		t.Fatalf("re-import: created=%d updated=%d unchanged=%d, want 0/0/3",
			again.Created, again.Updated, again.Unchanged)
	}
}

func TestImportSeedsSourceRow(t *testing.T) {
	// One newstest.Open per test and no more: the suite lock is a session-level
	// advisory lock, so a second handle inside the same test waits on the first
	// one forever rather than failing.
	db, dsn := openTestDB(t)
	dir := t.TempDir()
	writeCorpus(t, dir, healthy(1002))

	if _, err := Import(context.Background(), &config.Config{},
		Opts{Dir: dir, NoImages: true, Apply: true, DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	var src model.NewsSource
	if err := db.Where("key = ?", model.SourceKeyHihyou).Take(&src).Error; err != nil {
		t.Fatal(err)
	}
	// 「注明出处就行」 is the whole permission: a row published without the
	// attribution and the column link breaks the only condition she set.
	if src.Attribution == "" || src.ColumnURL == "" || src.PublisherUID != 115235 {
		t.Errorf("source row = %+v", src)
	}
}

func TestCorpusTreatsRateLimitedFileAsAbsent(t *testing.T) {
	dir := t.TempDir()
	c := Corpus{Dir: dir}
	if err := c.Mkdirs(); err != nil {
		t.Fatal(err)
	}
	// A stored -509 envelope parses fine and would otherwise count as harvested,
	// which is what turns a second-chance pass into a no-op that reports success.
	if err := os.WriteFile(filepath.Join(dir, "article", "cv7.json"),
		[]byte(`{"code":-509,"message":"请求过于频繁","data":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c.Has(7) {
		t.Error("a rate-limited response was accepted as a harvested article")
	}
	entries, bad, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || len(bad) != 1 {
		t.Errorf("load: %d entries, %d incomplete", len(entries), len(bad))
	}
}
