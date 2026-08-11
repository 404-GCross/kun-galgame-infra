package imagerefs

import (
	"context"
	"os"
	"slices"
	"testing"

	"api/internal/platform/news/newstest"

	"gorm.io/gorm"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	db, release, ok := newstest.Open()
	if !ok {
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

func seed(t *testing.T) {
	t.Helper()
	if err := newstest.Truncate(testDB); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := testDB.Exec(`
		INSERT INTO news_source (key, display_name, homepage_url, attribution, publisher_uid, column_url, active)
		VALUES ('ymgal', 'x', 'https://x', 'attr', 1, '', true) ON CONFLICT (key) DO NOTHING`).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
}

// TestBannerAndInlineImagesAreCollected is the refping fuse: an item with a
// banner and three inline pictures must yield four hashes. A column that stops
// being collected fails nothing at write time — the bytes are simply gone about
// thirteen months later.
func TestBannerAndInlineImagesAreCollected(t *testing.T) {
	seed(t)
	if err := testDB.Exec(`
		INSERT INTO news_item (source_key, external_id, title, preview, source_url, banner_hash, published_at, status)
		VALUES ('ymgal', 'r1', 't', 'p', 'https://x/1', 'bannerhash', now(), 1)`).Error; err != nil {
		t.Fatalf("insert item: %v", err)
	}
	var id int64
	testDB.Raw(`SELECT id FROM news_item WHERE external_id = 'r1'`).Scan(&id)
	for i, h := range []string{"inline1", "inline2", "inline3"} {
		if err := testDB.Exec(`
			INSERT INTO news_item_image (item_id, image_hash, origin_url, position)
			VALUES (?, ?, 'https://i0.hdslb.com/x.jpg', ?)`, id, h, i).Error; err != nil {
			t.Fatalf("insert image %s: %v", h, err)
		}
	}

	hashes, err := DistinctHashes(context.Background(), testDB)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	slices.Sort(hashes)
	want := []string{"bannerhash", "inline1", "inline2", "inline3"}
	if !slices.Equal(hashes, want) {
		t.Errorf("collected %v, want %v", hashes, want)
	}
}

// TestWithdrawnItemsStillPinged: an item we pulled from the feed keeps its bytes
// until the decision is final. Hiding a row must not start a GC clock.
func TestWithdrawnItemsStillPinged(t *testing.T) {
	seed(t)
	if err := testDB.Exec(`
		INSERT INTO news_item (source_key, external_id, title, preview, source_url, banner_hash, published_at, status, dead_at)
		VALUES ('ymgal', 'r2', 't', 'p', 'https://x/2', 'deadbanner', now(), 3, now())`).Error; err != nil {
		t.Fatalf("insert item: %v", err)
	}
	hashes, err := DistinctHashes(context.Background(), testDB)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !slices.Contains(hashes, "deadbanner") {
		t.Errorf("a withdrawn/dead item's banner was dropped from the refping set: %v", hashes)
	}
}

// TestRegistryCoversEveryHashColumn is the completeness assertion over a
// hand-maintained list: any future column whose name ends in _hash must be
// registered here, or it silently leaves the sweep.
func TestRegistryCoversEveryHashColumn(t *testing.T) {
	type col struct {
		Table  string `gorm:"column:table_name"`
		Column string `gorm:"column:column_name"`
	}
	var found []col
	if err := testDB.Raw(`
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name LIKE 'news%' AND column_name LIKE '%hash%'`,
	).Scan(&found).Error; err != nil {
		t.Fatalf("read columns: %v", err)
	}
	registered := Columns()
	for _, c := range found {
		if !slices.Contains(registered, [2]string{c.Table, c.Column}) {
			t.Errorf("%s.%s holds an image hash but is not in the imagerefs registry — "+
				"it will not be reference-pinged and its bytes will be collected in ~13 months",
				c.Table, c.Column)
		}
	}
	if len(found) != len(registered) {
		t.Errorf("registry has %d columns, schema has %d hash-bearing columns", len(registered), len(found))
	}
}
