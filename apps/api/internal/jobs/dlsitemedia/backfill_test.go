package dlsitemedia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/seed"
	"api/internal/platform/galgame/galgametest"
	"api/pkg/imageclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

// TestIntroWritePath exercises the pure-DB intro path end to end: the XOR guard
// (a CLAIMED work is refused), the no-text skip, the verbatim ja/dlsite write,
// and idempotency (both the preloaded-exists skip AND the ON CONFLICT DO NOTHING
// guard). The cover/screenshot upload paths need the image service and are
// validated by the rehearsal apply, not here.
func TestIntroWritePath(t *testing.T) {
	db := testDB
	for _, tbl := range []string{"catalog_work_intro", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	reg, err := resolveRegistry(context.Background(), db)
	require.NoError(t, err)
	require.NotZero(t, reg.dlsiteSource)

	mkWork := func(name string, site *string) int64 {
		w := model.CatalogWork{MediumID: reg.galgameMedium, OLang: "ja", DisplayName: name, Site: site}
		require.NoError(t, db.Create(&w).Error)
		return w.ID
	}
	claimed := "kungal"
	wBody := mkWork("bodyless-with-intro", nil)
	wEmpty := mkWork("bodyless-no-text", nil)
	wClaimed := mkWork("claimed", &claimed)

	cands := []candidate{
		{WorkID: wBody, Workno: "RJ000001", Site: nil},
		{WorkID: wEmpty, Workno: "RJ000002", Site: nil},
		{WorkID: wClaimed, Workno: "RJ000003", Site: &claimed},
	}
	metas := map[string]dlsiteMeta{
		"RJ000001": {Age: "3", Intro: "A bodyless doujin blurb."},
		"RJ000002": {Age: "1", Intro: ""}, // no prose
		"RJ000003": {Age: "3", Intro: "A claimed work's store blurb (wave 166)."},
	}

	ctx := context.Background()
	run := func(apply bool) *runner {
		exist, err := preloadExisting(ctx, db, []int64{wBody, wEmpty, wClaimed}, reg.dlsiteSource, langJa)
		require.NoError(t, err)
		r := &runner{db: db, sourceID: reg.dlsiteSource, exist: exist}
		for _, c := range cands {
			r.writeIntro(ctx, c, metas[c.Workno], apply)
		}
		return r
	}

	// --- dry run: classifies, writes nothing.
	r := run(false)
	assert.Equal(t, 2, r.c.introWould, "wBody and wClaimed both would write (wave 166)")
	assert.Equal(t, 1, r.c.introNoText, "wEmpty no text")
	assert.Equal(t, 0, r.c.introWritten)
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro").Scan(&n).Error)
	assert.EqualValues(t, 0, n, "dry run writes nothing")

	// --- apply: writes wBody's AND the claimed work's intro (ja, dlsite,
	// verbatim). Before wave 166 the claimed one was refused, which is what
	// left published works with an English-only intro.
	r = run(true)
	assert.Equal(t, 2, r.c.introWritten)
	var row model.CatalogWorkIntro
	require.NoError(t, db.Where("work_id = ?", wBody).First(&row).Error)
	assert.Equal(t, "ja", row.Lang)
	assert.EqualValues(t, reg.dlsiteSource, row.SourceID)
	assert.Equal(t, "A bodyless doujin blurb.", row.Intro)
	var claimedRow model.CatalogWorkIntro
	require.NoError(t, db.Where("work_id = ?", wClaimed).First(&claimedRow).Error)
	assert.Equal(t, "ja", claimedRow.Lang)
	assert.Equal(t, "A claimed work's store blurb (wave 166).", claimedRow.Intro)
	assert.EqualValues(t, 0, claimedRow.Provenance, "an ingested store blurb is a source row, never machine")

	// --- second apply via the preloaded-exists skip: zero new writes.
	r = run(true)
	assert.Equal(t, 0, r.c.introWritten)
	assert.Equal(t, 2, r.c.introExists, "preloaded exists → skip before write")

	// --- and the ON CONFLICT guard: force a write attempt with a STALE (empty)
	// exist map; the DB unique key still refuses the duplicate.
	rStale := &runner{db: db, sourceID: reg.dlsiteSource,
		exist: &existing{intro: map[int64]bool{}, cover: map[int64]bool{}, shot: map[int64]map[int]bool{}}}
	rStale.writeIntro(ctx, cands[0], metas["RJ000001"], true)
	assert.Equal(t, 0, rStale.c.introWritten, "ON CONFLICT refuses the duplicate")
	assert.Equal(t, 1, rStale.c.introExists)
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro WHERE work_id = ?", wBody).Scan(&n).Error)
	assert.EqualValues(t, 1, n, "still exactly one row for the retried work")
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro").Scan(&n).Error)
	assert.EqualValues(t, 2, n, "bodyless + claimed, nothing more")
}

// --- refs/proj/125: the CLAIMED screenshot lane ---

// claimedLaneGalgameIDs are the fixture galgame body ids the claimed screenshot
// lane's bridge-emptiness check joins against.
var claimedLaneGalgameIDs = []int64{9101, 9102, 9103, 9104}

// ensureGalgameScreenshotStub provisions the two galgame-family tables the
// claimed lane's candidate query reads: galgame (the body the claim points at)
// and galgame_screenshot (the bridge whose EMPTINESS admits a work). In
// prod/dev both live in kun_catalog alongside catalog; here they come from the
// real models (galgametest) so the shape is the same for every package sharing
// the integration database. The inserts pass every real-schema NOT NULL column
// without a default. Cleanup is a targeted DELETE. Idempotent.
func ensureGalgameScreenshotStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, galgametest.EnsureBodyTables(db))
	// Screenshots first: galgame_screenshot carries an FK to galgame(id) in the
	// real schema, so the children must go before the parents.
	require.NoError(t, db.Exec(`DELETE FROM galgame_screenshot WHERE galgame_id IN ?`, claimedLaneGalgameIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, claimedLaneGalgameIDs).Error)
	for _, id := range claimedLaneGalgameIDs {
		require.NoError(t, db.Exec(`INSERT INTO galgame (id, catalog_work_id, user_id,
			intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw) VALUES (?, NULL, 0, '', '', '', '')`, id).Error)
	}
}

// stubImageService stands in for image_service's /image/upload: it answers with
// the standard envelope, hashing the uploaded bytes so identical files dedup to
// identical hashes (matching the real content-addressed behavior). Only the
// upload endpoint is needed — the test drives the writers directly, so neither
// Health nor ReferencePing is called.
func stubImageService(t *testing.T) *imageclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/image/upload", r.URL.Path)
		require.NoError(t, r.ParseMultipartForm(1<<20))
		f, hdr, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close()
		sum := sha256.New()
		buf := make([]byte, hdr.Size)
		n, _ := f.Read(buf)
		sum.Write(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{"hash": hex.EncodeToString(sum.Sum(nil)), "size_bytes": hdr.Size},
		})
	}))
	t.Cleanup(srv.Close)
	return imageclient.New(imageclient.Config{BaseURL: srv.URL, ClientID: "test", ClientSecret: "test"})
}

// TestClaimedScreenshotLane pins the refs/proj/125 write side: the candidate
// query's three-condition targeting of claimed works, the PER-KIND guard split
// (screenshot admits a claimed work, intro/cover still refuse the very same
// candidate), the real write with the dlsite age→sexual mapping, touch
// inheritance, and idempotency (second apply writes nothing and touches nothing).
func TestClaimedScreenshotLane(t *testing.T) {
	db := testDB
	ctx := context.Background()
	ensureGalgameScreenshotStub(t, db)
	// No RESTART IDENTITY: the identity sequences are shared with every other
	// package running against this DB, and rewinding them cross-pollutes ids.
	for _, tbl := range []string{"catalog_external_ref", "catalog_release", "catalog_work_screenshot", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" CASCADE").Error)
	}

	reg, err := resolveRegistry(ctx, db)
	require.NoError(t, err)

	// anchored creates a galgame work + a release carrying an EXACT dlsite workno
	// anchor — the reachability every candidate query shares.
	anchored := func(name, workno string, site *string, galgameID *int64) int64 {
		w := model.CatalogWork{MediumID: reg.galgameMedium, OLang: "ja", DisplayName: name, Site: site, ProductWorkID: galgameID}
		require.NoError(t, db.Create(&w).Error)
		rel := model.CatalogRelease{WorkID: w.ID, Kind: 0}
		require.NoError(t, db.Create(&rel).Error)
		require.NoError(t, db.Create(&model.CatalogExternalRef{
			EntityType: model.EntityTypeRelease, EntityID: rel.ID, SourceID: reg.dlsiteSource,
			ExternalID: workno, LinkKind: model.LinkKindExact, MatchedBy: "import:test"}).Error)
		return w.ID
	}
	claimed := siteGalgameWiki
	wBodyless := anchored("bodyless", "RJ100001", nil, nil)
	wClaimedBare := anchored("claimed-no-screenshot", "RJ100002", &claimed, &claimedLaneGalgameIDs[0])
	wClaimedBridged := anchored("claimed-with-wiki-screenshot", "RJ100003", &claimed, &claimedLaneGalgameIDs[1])
	wClaimedNative := anchored("claimed-with-native-screenshot", "RJ100004", &claimed, &claimedLaneGalgameIDs[2])

	// A claim that is NOT on the public face. The wave-166 intro lane targets
	// PUBLISHED works — the draft sea is roughly 5x the published one and every
	// ja row written there becomes a downstream translation call. It carries a
	// native screenshot so the SCREENSHOT lane's own targeting keeps it out and
	// this fixture isolates exactly one variable: claim_state.
	wDraft := anchored("claimed-but-draft", "RJ100005", &claimed, &claimedLaneGalgameIDs[3])
	require.NoError(t, db.Model(&model.CatalogWork{}).Where("id = ?", wDraft).
		Update("claim_state", model.ClaimStateDraft).Error)

	// wClaimedBridged already shows a wiki screenshot → the store samples must not
	// supplement it. wClaimedNative already has a native row → nothing to do.
	require.NoError(t, db.Exec(`INSERT INTO galgame_screenshot (galgame_id, image_hash, sort_order, source)
		VALUES (?, 'wiki_shot_hash', 0, '')`, claimedLaneGalgameIDs[1]).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: wClaimedNative, ImageHash: "already_here", SortOrder: 0, SourceID: reg.dlsiteSource}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: wDraft, ImageHash: "draft_shot", SortOrder: 0, SourceID: reg.dlsiteSource}).Error)

	// --- candidate targeting: screenshot runs see both lanes, intro/cover-only
	// runs see the pre-125 bodyless set.
	shotCands, err := loadCandidates(ctx, db, reg, Kinds{Screenshot: true}, 0, 0)
	require.NoError(t, err)
	ids := map[int64]string{}
	for _, c := range shotCands {
		ids[c.WorkID] = c.Workno
	}
	assert.Contains(t, ids, wBodyless, "bodyless lane unchanged")
	assert.Contains(t, ids, wClaimedBare, "claimed with no screenshot at all → admitted")
	assert.NotContains(t, ids, wClaimedBridged, "claimed with a bridged wiki screenshot → excluded")
	assert.NotContains(t, ids, wClaimedNative, "claimed that already has a native row → excluded")
	bodyless, claimedCount := laneSplit(shotCands)
	assert.Equal(t, 1, bodyless)
	assert.Equal(t, 1, claimedCount)

	// COVER-only still resolves the bodyless lane alone — cover keeps its
	// claimed refusal (wave 164 already filled those slots from the wiki).
	coverCands, err := loadCandidates(ctx, db, reg, Kinds{Cover: true}, 0, 0)
	require.NoError(t, err)
	require.Len(t, coverCands, 1, "a cover-only run resolves the bodyless lane alone")
	assert.Equal(t, wBodyless, coverCands[0].WorkID)

	// INTRO, since wave 166, adds its own claimed lane: every claimed work with
	// a dlsite anchor and no ja intro from ANY source. All three claimed
	// fixtures qualify here — none of them has an intro row.
	introCands, err := loadCandidates(ctx, db, reg, Kinds{Intro: true}, 0, 0)
	require.NoError(t, err)
	introIDs := map[int64]bool{}
	for _, c := range introCands {
		introIDs[c.WorkID] = true
	}
	assert.Contains(t, introIDs, wBodyless, "bodyless lane unchanged")
	assert.Contains(t, introIDs, wClaimedBare, "claimed work with no ja intro → admitted")
	assert.Contains(t, introIDs, wClaimedBridged, "the screenshot lane's exclusions do not apply to intro")
	assert.NotContains(t, introIDs, wDraft, "a DRAFT claim is not on the public face → excluded")
	introBodyless, introClaimed := laneSplit(introCands)
	assert.Equal(t, 1, introBodyless)
	assert.Equal(t, 3, introClaimed, "the three live claims; the draft is out")

	// --- asymmetric windowing: --offset consumes the STABLE bodyless lane only, so
	// it can isolate the self-consuming claimed lane without ever skipping it.
	for _, off := range []int{1, 99} {
		windowed, err := loadCandidates(ctx, db, reg, Kinds{Screenshot: true}, 0, off)
		require.NoError(t, err)
		require.Len(t, windowed, 1, "offset %d skips the bodyless lane, never the claimed one", off)
		assert.Equal(t, wClaimedBare, windowed[0].WorkID)
	}
	capped, err := loadCandidates(ctx, db, reg, Kinds{Screenshot: true}, 1, 0)
	require.NoError(t, err)
	require.Len(t, capped, 1, "--limit caps the concatenation")
	assert.Equal(t, wBodyless, capped[0].WorkID)

	// --- the claimed candidate, driven through all three writers.
	var cand candidate
	for _, c := range shotCands {
		if c.WorkID == wClaimedBare {
			cand = c
		}
	}
	require.Equal(t, wClaimedBare, cand.WorkID)

	// Two mirrored samples + one sample whose bytes were never mirrored.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, cand.Workno), 0o755))
	for _, f := range []string{"a.jpg", "b.jpg"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, cand.Workno, f), []byte("bytes-of-"+f), 0o644))
	}
	meta := dlsiteMeta{Age: "3", Intro: "claimed prose", CoverFile: "cover.jpg",
		SampleFiles: []string{"a.jpg", "b.jpg", "never_mirrored.jpg"}}

	newRunner := func() *runner {
		exist, err := preloadExisting(ctx, db, []int64{cand.WorkID}, reg.dlsiteSource, langJa)
		require.NoError(t, err)
		return &runner{db: db, sourceID: reg.dlsiteSource, exist: exist, cli: stubImageService(t)}
	}

	// PER-KIND GUARD SPLIT, post-166: intro and screenshot write a claimed
	// work, COVER still refuses it. The split is not arbitrary — wave 164
	// flipped covers onto the native table with the wiki rows already mirrored
	// in, so a claimed work's cover slot is occupied and a store cover would be
	// a second, worse one; its intro slot may be genuinely empty.
	r := newRunner()
	r.writeIntro(ctx, cand, meta, true)
	r.writeCover(ctx, dir, cand, meta, true)
	assert.Equal(t, 1, r.c.introWritten, "intro now writes claimed (wave 166)")
	assert.Equal(t, 1, r.c.coverRefused, "cover still refuses claimed")
	assert.Zero(t, r.c.coverUploaded)
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro WHERE work_id = ?", cand.WorkID).Scan(&n).Error)
	assert.EqualValues(t, 1, n, "claimed intro materialised")
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_cover WHERE work_id = ?", cand.WorkID).Scan(&n).Error)
	assert.EqualValues(t, 0, n, "claimed cover never materialised")

	// Dry run first: forecasts, writes nothing.
	r = newRunner()
	assert.False(t, r.writeScreenshots(ctx, dir, cand, meta, false))
	assert.Equal(t, 2, r.c.shotWould)
	assert.Equal(t, 1, r.c.shotMissing, "unmirrored sample is a forecast miss, not an error")
	assert.Empty(t, r.touched, "a dry run touches nothing")
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_screenshot WHERE work_id = ?", cand.WorkID).Scan(&n).Error)
	assert.EqualValues(t, 0, n)

	// Apply: two dlsite rows on a CLAIMED work, sexual mapped from age_category 3.
	r = newRunner()
	assert.False(t, r.writeScreenshots(ctx, dir, cand, meta, true))
	assert.Equal(t, 2, r.c.shotUploaded)
	assert.Equal(t, 1, r.c.shotMissing)
	assert.Equal(t, []int64{cand.WorkID, cand.WorkID}, r.touched, "every real write feeds the touch list")
	require.NoError(t, repository.TouchWorks(ctx, r.db, r.touched))
	var rows []model.CatalogWorkScreenshot
	require.NoError(t, db.Where("work_id = ?", cand.WorkID).Order("sort_order").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.EqualValues(t, 0, rows[0].SortOrder)
	assert.EqualValues(t, 1, rows[1].SortOrder)
	for _, row := range rows {
		assert.EqualValues(t, reg.dlsiteSource, row.SourceID, "claimed native rows are dlsite-sourced only")
		assert.EqualValues(t, 2, row.Sexual, "age_category 3 (adult) → sexual 2")
		assert.EqualValues(t, 0, row.Violence)
		assert.Equal(t, "", row.Caption)
	}
	assert.Len(t, r.pingHashes, 2, "fresh uploads are reference-pinged immediately")

	// Second apply: the preloaded-exists skip means zero writes and zero touches.
	r = newRunner()
	assert.False(t, r.writeScreenshots(ctx, dir, cand, meta, true))
	assert.Zero(t, r.c.shotUploaded)
	assert.Equal(t, 2, r.c.shotExists)
	assert.Empty(t, r.touched, "idempotent re-run moves no watermark")

	// And the work drops out of the candidate set entirely (it now has native rows).
	shotCands, err = loadCandidates(ctx, db, reg, Kinds{Screenshot: true}, 0, 0)
	require.NoError(t, err)
	for _, c := range shotCands {
		assert.NotEqual(t, cand.WorkID, c.WorkID, "a filled work is no longer a candidate")
	}

	// A work with no samples at all is classified, not silently dropped.
	r = newRunner()
	assert.False(t, r.writeScreenshots(ctx, dir, cand, dlsiteMeta{Age: "1"}, true))
	assert.Equal(t, 1, r.c.shotNoSamples)
}
