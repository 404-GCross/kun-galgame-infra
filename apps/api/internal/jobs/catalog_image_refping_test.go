package jobs

import (
	"context"
	"os"
	"sort"
	"testing"

	catalogmodel "api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	hCharA      = "aaaa111111111111111111111111111111111111111111111111111111111111"
	hCharB      = "bbbb222222222222222222222222222222222222222222222222222222222222"
	hCharC      = "cccc333333333333333333333333333333333333333333333333333333333333"
	hCharD      = "3333999999999999999999999999999999999999999999999999999999999999"
	hCharE      = "7777dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	hCoverB     = "dddd444444444444444444444444444444444444444444444444444444444444"
	hCoverSha   = "eeee555555555555555555555555555555555555555555555555555555555555"
	hCharCoverX = "ffff666666666666666666666666666666666666666666666666666666666666"
	hShotB      = "1111777777777777777777777777777777777777777777777777777777777777"
	hShotSha    = "2222888888888888888888888888888888888888888888888888888888888888"
	hLogoA      = "4444aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hLogoB      = "5555bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hPhotoA     = "6666cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	hPhotoB     = "7777dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func migrateCatalogRefpingTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&catalogmodel.CatalogMedium{}, &catalogmodel.CatalogSource{},
		&catalogmodel.CatalogWork{}, &catalogmodel.CatalogCharacter{},
		&catalogmodel.CatalogWorkCover{}, &catalogmodel.CatalogWorkScreenshot{},
		&catalogmodel.CatalogLabel{},
		&catalogmodel.CatalogPerson{}))
	require.NoError(t, db.Exec(`TRUNCATE catalog_work, catalog_character, catalog_label, catalog_person RESTART IDENTITY CASCADE`).Error)
	require.NoError(t, db.Exec(`INSERT INTO catalog_medium (id, key, name_cn) VALUES (1,'galgame','G') ON CONFLICT (id) DO NOTHING`).Error)
	require.NoError(t, db.Exec(`INSERT INTO catalog_source (id, key, trust_tier) VALUES (1,'user',0) ON CONFLICT (id) DO NOTHING`).Error)
}

func TestCatalogRefping_CollectsLiveNonNullPortraitHashes(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	ctx := context.Background()
	mk := func(hash *string) *catalogmodel.CatalogCharacter {
		c := &catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: hash}
		require.NoError(t, db.Create(c).Error)
		return c
	}
	sp := func(s string) *string { return &s }

	mk(sp(hCharA))
	mk(sp(hCharA))
	mk(sp(hCharC))
	mk(nil)
	mk(sp(""))
	softDeleted := mk(sp(hCharB))
	require.NoError(t, db.Delete(softDeleted).Error)

	got, err := collectCatalogRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharA, hCharC}
	sort.Strings(want)
	assert.Equal(t, want, got, "only live, non-empty, non-deleted image_hash values, deduped")
}

func TestCatalogRefping_IncludesBodylessAndShadowedMedia(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	ctx := context.Background()
	sp := func(s string) *string { return &s }

	require.NoError(t, db.Create(&catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: sp(hCharCoverX)}).Error)

	bodyless := &catalogmodel.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "bodyless"}
	require.NoError(t, db.Create(bodyless).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: hCoverB, SourceID: 1}).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkScreenshot{
		WorkID: bodyless.ID, ImageHash: hShotB, SourceID: 1}).Error)

	claimed := &catalogmodel.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "claimed",
		Site: sp("galgame_wiki"), ProductWorkID: func() *int64 { v := int64(999); return &v }()}
	require.NoError(t, db.Create(claimed).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkCover{
		WorkID: claimed.ID, ImageHash: hCoverSha, SourceID: 1}).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkScreenshot{
		WorkID: claimed.ID, ImageHash: hShotSha, SourceID: 1}).Error)

	got, err := collectCatalogRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharCoverX, hCoverB, hCoverSha, hShotB, hShotSha}
	sort.Strings(want)
	assert.Equal(t, want, got, "character portrait + bodyless cover/screenshot + SHADOWED claimed cover/screenshot all pinged")
}

func TestCatalogRefping_IncludesFullBodyFigures(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	sp := func(s string) *string { return &s }
	mk := func(img, fig, src *string) *catalogmodel.CatalogCharacter {
		c := &catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: img, FigureHash: fig, FigureSourceHash: src}
		require.NoError(t, db.Create(c).Error)
		return c
	}

	mk(sp(hCharA), sp(hCharB), nil)
	mk(nil, sp(hCharC), sp(hCharE))
	mk(sp(hCharA), nil, nil)
	mk(nil, sp(""), nil)
	gone := mk(nil, sp(hCharD), sp(hCharD))
	require.NoError(t, db.Delete(gone).Error)

	got, err := collectCatalogRefpingHashes(context.Background(), db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharA, hCharB, hCharC, hCharE}
	sort.Strings(want)
	assert.Equal(t, want, got, "image_hash, figure_hash and the pre-cutout figure_source_hash must all be kept alive")
}

func TestCatalogRefping_IncludesLabelLogos(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	mk := func(logo string) *catalogmodel.CatalogLabel {
		l := &catalogmodel.CatalogLabel{DisplayName: "brand", LogoHash: logo}
		require.NoError(t, db.Create(l).Error)
		return l
	}

	mk(hLogoA)
	mk(hLogoA)
	mk("")
	gone := mk(hLogoB)
	require.NoError(t, db.Delete(gone).Error)

	got, err := collectCatalogRefpingHashes(context.Background(), db)
	require.NoError(t, err)
	sort.Strings(got)

	assert.Equal(t, []string{hLogoA}, got, "live, non-empty label logos only, deduped")
}

func TestCatalogRefping_IncludesPersonPhotos(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	mk := func(photo string) *catalogmodel.CatalogPerson {
		p := &catalogmodel.CatalogPerson{DisplayName: "person", PhotoHash: photo}
		require.NoError(t, db.Create(p).Error)
		return p
	}

	mk(hPhotoA)
	mk(hPhotoA)
	mk("")
	gone := mk(hPhotoB)
	require.NoError(t, db.Delete(gone).Error)

	got, err := collectCatalogRefpingHashes(context.Background(), db)
	require.NoError(t, err)
	sort.Strings(got)

	assert.Equal(t, []string{hPhotoA}, got, "live, non-empty person photos only, deduped")
}
