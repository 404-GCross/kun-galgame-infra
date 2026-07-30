// claim_hook_test.go — wave 146: the catalog claim on the two galgame write
// paths that CREATE a published row (admin direct-create and a trusted
// publisher's direct submit). Every later status transition is an editing-engine
// merge and is covered by the galgame.game OnMerge hook instead, so these cases
// pin exactly the gap OnMerge cannot see: a row that is born at status=0.
//
// The properties: the hook fires for a publication, never for a pending draft
// (which may still be withdrawn — an identity minted for it would outlive the
// galgame), and a claim that fails leaves the publication itself untouched.
package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/catalogsync"
	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// claimSpy records the galgame ids a write path handed to the claim hook.
type claimSpy struct{ ids []int64 }

func (s *claimSpy) hook() ClaimHookFunc {
	return func(_ context.Context, id int64) { s.ids = append(s.ids, id) }
}

// brokenCatalogPool is a lazily-dialed pool pointing nowhere: every query it
// serves fails. It stands in for "the catalog side is down" without needing a
// fixture, so the failure semantics are tested against the REAL hook.
func brokenCatalogPool(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		postgres.Open("host=127.0.0.1 port=1 user=nobody dbname=nope sslmode=disable"),
		&gorm.Config{DisableAutomaticPing: true, Logger: gormlogger.Default.LogMode(gormlogger.Silent)},
	)
	require.NoError(t, err, "the stand-in pool must construct; only its QUERIES may fail")
	return db
}

// TestSubmitClaimsOnlyOnDirectPublish: a trusted publisher's submit is born at
// status=0 and claims immediately; an ordinary user's pending(3) submission does
// not (the nightly reconcile owns it once it has survived the day).
func TestSubmitClaimsOnlyOnDirectPublish(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	spy := &claimSpy{}
	svc := NewSubmissionService(testGalgameRepo, testMessageRepo).
		WithEditing(testEngine).WithClaimHook(spy.hook())

	published, err := svc.Submit(ctx, 7, []string{"creator"}, &dto.SubmitGalgameRequest{
		NameZhCN: "直发作品",
	})
	require.NoError(t, err)
	require.Equal(t, model.GalgameStatusPublished, published.Status)
	assert.Equal(t, []int64{int64(published.ID)}, spy.ids,
		"a direct publish must register its catalog identity on the spot")

	pending, err := svc.Submit(ctx, 42, nil, &dto.SubmitGalgameRequest{
		VNDBID: "v1460", NameZhCN: "待审作品",
	})
	require.NoError(t, err)
	require.Equal(t, model.GalgameStatusPending, pending.Status)
	assert.Equal(t, []int64{int64(published.ID)}, spy.ids,
		"a pending draft must not mint an identity it may never need")
}

// TestCreateClaimsCatalog: admin direct-create is born published, so it claims.
func TestCreateClaimsCatalog(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	spy := &claimSpy{}
	svc := NewGalgameService(testGalgameRepo, nil, nil, nil).
		WithEditing(testEngine, testEditq).WithClaimHook(spy.hook())

	g, err := svc.Create(ctx, 3, &dto.CreateGalgameRequest{VNDBID: "v1461", NameZhCN: "管理员建条"})
	require.NoError(t, err)
	require.Equal(t, model.GalgameStatusPublished, g.Status)
	assert.Equal(t, []int64{int64(g.ID)}, spy.ids)
}

// TestPublishSurvivesClaimFailure is the failure semantics the write paths
// depend on: the real hook over an unreachable catalog pool logs a warning and
// returns, and both publications land exactly as they would without it.
func TestPublishSurvivesClaimFailure(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	failing := catalogsync.Hook(testDB, brokenCatalogPool(t))

	submitted, err := NewSubmissionService(testGalgameRepo, testMessageRepo).
		WithEditing(testEngine).WithClaimHook(failing).
		Submit(ctx, 7, []string{"creator"}, &dto.SubmitGalgameRequest{NameZhCN: "直发但注册失败"})
	require.NoError(t, err, "a registry outage must never fail a publication")
	assert.Equal(t, model.GalgameStatusPublished, submitted.Status)

	created, err := NewGalgameService(testGalgameRepo, nil, nil, nil).
		WithEditing(testEngine, testEditq).WithClaimHook(failing).
		Create(ctx, 3, &dto.CreateGalgameRequest{VNDBID: "v1462", NameZhCN: "建条但注册失败"})
	require.NoError(t, err)
	assert.Equal(t, model.GalgameStatusPublished, created.Status)

	// Both rows are really there — the publication is the thing that must not be
	// rolled back by a best-effort side effect.
	var n int64
	require.NoError(t, testDB.Model(&model.Galgame{}).
		Where("id IN ?", []int{submitted.ID, created.ID}).Count(&n).Error)
	assert.Equal(t, int64(2), n)
}
