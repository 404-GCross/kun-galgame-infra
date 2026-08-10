package main

import (
	"fmt"
	"io"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "SKIP: TEST_DATABASE_DSN is unset")
		os.Exit(0)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func TestPlanAliasesKeepsEveryBrandFindable(t *testing.T) {
	t.Run("canonical is a segment and is not re-aliased", func(t *testing.T) {
		got := planAliases("POISON / POISON MOTION / POISON EXTASY", "POISON")
		assert.Equal(t, []aliasRow{
			{"POISON / POISON MOTION / POISON EXTASY", model.AliasKindSearchHint},
			{"POISON MOTION", model.AliasKindSpellingVariant},
			{"POISON EXTASY", model.AliasKindSpellingVariant},
		}, got)
	})
	t.Run("canonical the row never carried keeps both segments", func(t *testing.T) {
		got := planAliases("モニスタラッシュ / a Matures", "ア・マチュアズ")
		assert.Equal(t, []aliasRow{
			{"モニスタラッシュ / a Matures", model.AliasKindSearchHint},
			{"モニスタラッシュ", model.AliasKindSpellingVariant},
			{"a Matures", model.AliasKindSpellingVariant},
		}, got)
	})
	t.Run("unspaced slash still splits and trims", func(t *testing.T) {
		got := planAliases("A / B /C", "A")
		assert.Equal(t, []aliasRow{
			{"A / B /C", model.AliasKindSearchHint},
			{"B", model.AliasKindSpellingVariant},
			{"C", model.AliasKindSpellingVariant},
		}, got)
	})
}

func mkLabel(t *testing.T, name, canonical string) healCase {
	t.Helper()
	l := model.CatalogLabel{DisplayName: name, Lang: "ja", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(&l).Error)
	return healCase{LabelID: l.ID, Expect: name, Canonical: canonical}
}

func TestApplyCaseWritesRenameAliasesAndProvenance(t *testing.T) {
	c := mkLabel(t, "ココロリウム / ア・ラ・フィリア", "ココロリウム")

	res, err := applyCase(testDB, c, true, io.Discard)
	require.NoError(t, err)
	require.False(t, res.skipped)

	var got model.CatalogLabel
	require.NoError(t, testDB.First(&got, c.LabelID).Error)
	assert.Equal(t, "ココロリウム", got.DisplayName)
	assert.Contains(t, string(got.FieldProvenance), `"curated"`,
		"the rename is a curation act and must say so in field_provenance")

	var aliases []model.CatalogLabelAlias
	require.NoError(t, testDB.Where("label_id = ?", c.LabelID).Order("id").Find(&aliases).Error)
	require.Len(t, aliases, 2)
	assert.Equal(t, "ココロリウム / ア・ラ・フィリア", aliases[0].Name)
	assert.Equal(t, model.AliasKindSearchHint, aliases[0].Kind)
	assert.Equal(t, "ア・ラ・フィリア", aliases[1].Name)
	assert.Equal(t, model.AliasKindSpellingVariant, aliases[1].Kind)
	for _, a := range aliases {
		assert.Empty(t, a.Lang)
		assert.False(t, a.IsPrimaryForLocale, "the canonical display_name is the primary, never an alias")
	}

	res, err = applyCase(testDB, c, true, io.Discard)
	require.NoError(t, err)
	assert.True(t, res.skipped)
	assert.Contains(t, res.reason, "no slash")

	var after int64
	require.NoError(t, testDB.Model(&model.CatalogLabelAlias{}).
		Where("label_id = ?", c.LabelID).Count(&after).Error)
	assert.EqualValues(t, 2, after, "a second run must add no alias rows")
}

func TestApplyCaseDriftGuardRefusesAChangedRow(t *testing.T) {
	c := mkLabel(t, "X / Y / Z", "X")
	c.Expect = "X / Y"

	res, err := applyCase(testDB, c, true, io.Discard)
	require.NoError(t, err)
	require.True(t, res.skipped)
	assert.Contains(t, res.reason, "drifted")

	var got model.CatalogLabel
	require.NoError(t, testDB.First(&got, c.LabelID).Error)
	assert.Equal(t, "X / Y / Z", got.DisplayName, "a drifted row must never be written")
	var aliases int64
	require.NoError(t, testDB.Model(&model.CatalogLabelAlias{}).
		Where("label_id = ?", c.LabelID).Count(&aliases).Error)
	assert.Zero(t, aliases)
}

func TestApplyCaseDryRunWritesNothing(t *testing.T) {
	c := mkLabel(t, "Omega Program / 正経同人", "Omega Program")

	res, err := applyCase(testDB, c, false, io.Discard)
	require.NoError(t, err)
	require.False(t, res.skipped)
	require.Len(t, res.aliases, 2)

	var got model.CatalogLabel
	require.NoError(t, testDB.First(&got, c.LabelID).Error)
	assert.Equal(t, "Omega Program / 正経同人", got.DisplayName)
	var aliases int64
	require.NoError(t, testDB.Model(&model.CatalogLabelAlias{}).
		Where("label_id = ?", c.LabelID).Count(&aliases).Error)
	assert.Zero(t, aliases)
}
