package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanAll extends cleanCatalog to the tables the alias rules touch.
func cleanAll(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`TRUNCATE catalog_match_candidate, catalog_credit_name,
		catalog_person, catalog_revision, catalog_name_alias, catalog_credit,
		catalog_external_ref, catalog_work RESTART IDENTITY CASCADE`).Error)
}

func mkCandidateReason(t *testing.T, a, b int64, reason int16) {
	t.Helper()
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	require.NoError(t, testDB.Create(&model.CatalogMatchCandidate{
		EntityType: model.EntityTypeCreditName, AID: lo, BID: hi,
		Reason: reason, Status: model.CandidateStatusPending,
	}).Error)
}

func seededRoleID(t *testing.T) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_role WHERE key = 'developer'`).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

func mkWork(t *testing.T) int64 {
	t.Helper()
	w := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "W", ContentRating: 0, Status: 0}
	require.NoError(t, testDB.Create(w).Error)
	return w.ID
}

func mkCredit(t *testing.T, work, creditName, role int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogCredit{WorkID: work, CreditNameID: creditName, RoleID: role}).Error)
}

func mkAlias(t *testing.T, owner int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogNameAlias{
		CreditNameID: owner, Name: name, Lang: "", Kind: model.AliasKindSearchHint,
	}).Error)
}

// A3: two names co-credited on the same work are linked; a pair with no shared
// credit and no bidirectional alias is left for T2.
func TestAliasA3CoCredit(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanAll(t)
	ctx := context.Background()
	role := seededRoleID(t)

	a := mkName(t, "田中ロミオ")
	b := mkName(t, "山田一")
	w := mkWork(t)
	mkCredit(t, w, a, role)
	mkCredit(t, w, b, role) // co-credited on the same work
	mkCandidateReason(t, a, b, model.CandidateReasonAliasDeclared)

	// A different-source lookalike with NO shared credit / bidirectional alias.
	c := mkName(t, "松田")
	d := mkName(t, "佐藤")
	mkCandidateReason(t, c, d, model.CandidateReasonAliasDeclared)

	st, err := run(ctx, testDB, io.Discard, 7, true, ruleSetAlias)
	require.NoError(t, err)
	assert.Equal(t, 1, st.A3Hits)
	assert.Zero(t, st.A4Hits)
	assert.Equal(t, 1, st.Unmatched, "no-evidence pair stays for T2")
	pa, pb := personIDOf(t, a), personIDOf(t, b)
	require.NotNil(t, pa)
	require.NotNil(t, pb)
	assert.Equal(t, *pa, *pb, "co-credited pair linked to one person")
	assert.Nil(t, personIDOf(t, c), "T2 pair untouched")
}

// A4: a bidirectional alias declaration (each side's name_alias names the other)
// is linked; a one-directional one is not.
func TestAliasA4Bidirectional(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanAll(t)
	ctx := context.Background()

	a := mkName(t, "麻枝准")
	b := mkName(t, "Jun Maeda")
	mkAlias(t, a, "Jun Maeda") // a declares b's name
	mkAlias(t, b, "麻枝准")       // b declares a's name → bidirectional
	mkCandidateReason(t, a, b, model.CandidateReasonAliasDeclared)

	// One-directional: only c declares d.
	c := mkName(t, "丸戸史明")
	d := mkName(t, "Fumiaki Maruto")
	mkAlias(t, c, "Fumiaki Maruto")
	mkCandidateReason(t, c, d, model.CandidateReasonAliasDeclared)

	st, err := run(ctx, testDB, io.Discard, 7, true, ruleSetAlias)
	require.NoError(t, err)
	assert.Equal(t, 1, st.A4Hits)
	assert.Zero(t, st.A3Hits)
	assert.Equal(t, 1, st.Unmatched, "one-directional declaration is not A4")
	assert.NotNil(t, personIDOf(t, a))
	assert.Nil(t, personIDOf(t, c), "one-directional pair untouched")
}

// The rule sets do not poach each other's candidates: an alias pass ignores
// shared-handle candidates and vice-versa.
func TestRuleSetIsolation(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanAll(t)
	ctx := context.Background()

	sa := mkName(t, "同名")
	sb := mkName(t, "同名") // shared-handle A1
	mkCandidateReason(t, sa, sb, model.CandidateReasonSharedExternalID)
	role := seededRoleID(t)
	x := mkName(t, "作者X")
	y := mkName(t, "作者Y")
	w := mkWork(t)
	mkCredit(t, w, x, role)
	mkCredit(t, w, y, role)
	mkCandidateReason(t, x, y, model.CandidateReasonAliasDeclared) // alias A3

	// Alias pass touches only the alias candidate.
	st, err := run(ctx, testDB, io.Discard, 7, true, ruleSetAlias)
	require.NoError(t, err)
	assert.Equal(t, 1, st.A3Hits)
	assert.Nil(t, personIDOf(t, sa), "shared-handle candidate untouched by alias pass")

	// Shared pass touches only the shared-handle candidate.
	st, err = run(ctx, testDB, io.Discard, 7, true, ruleSetShared)
	require.NoError(t, err)
	assert.Equal(t, 1, st.A1Hits)
	assert.NotNil(t, personIDOf(t, sa))
}

// --- export + receipts -----------------------------------------------------

func TestExportAndReceipts(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanAll(t)
	ctx := context.Background()

	// An alias_declared pair with no A3/A4 evidence → T2.
	a := mkName(t, "riya")
	b := mkName(t, "eufonius")
	mkAlias(t, a, "some other alias")
	mkCandidateReason(t, a, b, model.CandidateReasonAliasDeclared)

	dir := t.TempDir()
	tsv := filepath.Join(dir, "t2.tsv")
	require.NoError(t, runExport(testDB, tsv))
	body, err := os.ReadFile(tsv)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	require.Len(t, lines, 2, "header + one T2 row")
	assert.True(t, strings.HasPrefix(lines[0], "a_id\tb_id"))
	assert.Contains(t, lines[1], "riya")
	assert.Contains(t, lines[1], "some other alias", "alias sample carried for context")

	// Fill a receipt: link the pair.
	receipt := filepath.Join(dir, "r.tsv")
	require.NoError(t, os.WriteFile(receipt,
		[]byte("a_id\tb_id\tdecision\tnotes\n"+itoa(minI(a, b))+"\t"+itoa(maxI(a, b))+"\tlink\tsame person\n"), 0o644))

	// Dry writes nothing.
	_, err = runReceipts(ctx, testDB, io.Discard, receipt, 7, false)
	require.NoError(t, err)
	assert.Nil(t, personIDOf(t, a), "dry writes nothing")

	// Apply links.
	st, err := runReceipts(ctx, testDB, io.Discard, receipt, 7, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Linked)
	assert.NotNil(t, personIDOf(t, a))
	assert.Equal(t, model.CandidateStatusAccepted, candStatus(t, minI(a, b), maxI(a, b)))

	// Idempotent: re-applying the same receipt does nothing new.
	st, err = runReceipts(ctx, testDB, io.Discard, receipt, 7, true)
	require.NoError(t, err)
	assert.Zero(t, st.Linked)
	assert.Equal(t, 1, st.Already)
}

// A reject receipt flips the candidate to rejected (step-22 semantics).
func TestReceiptReject(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanAll(t)
	ctx := context.Background()

	a := mkName(t, "別人A")
	b := mkName(t, "別人B")
	mkCandidateReason(t, a, b, model.CandidateReasonAliasDeclared)

	dir := t.TempDir()
	receipt := filepath.Join(dir, "r.tsv")
	require.NoError(t, os.WriteFile(receipt,
		[]byte("a_id\tb_id\tdecision\n"+itoa(minI(a, b))+"\t"+itoa(maxI(a, b))+"\treject\n"), 0o644))

	st, err := runReceipts(ctx, testDB, io.Discard, receipt, 7, true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Rejected)
	assert.Equal(t, model.CandidateStatusRejected, candStatus(t, minI(a, b), maxI(a, b)))
	assert.Nil(t, personIDOf(t, a), "reject never links")
}

func minI(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
