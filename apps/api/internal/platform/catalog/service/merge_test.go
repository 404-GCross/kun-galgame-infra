package service

import (
	"testing"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T8-①: the identity PK recipe end to end — GORM Create backfills a
// generated id; explicit-ID writes still work (BY DEFAULT identity).
func TestIdentityCreateBackfillAndExplicitID(t *testing.T) {
	cleanTables(t)

	p := createPerson(t, "auto-id")
	assert.Positive(t, p.ID, "Create must scan the generated id back")

	explicit := &model.CatalogPerson{ID: 9990, DisplayName: "explicit-id"}
	require.NoError(t, testDB.Create(explicit).Error)
	assert.Equal(t, int64(9990), explicit.ID)

	after := createPerson(t, "auto-after-explicit")
	assert.Positive(t, after.ID)
	assert.NotEqual(t, explicit.ID, after.ID)
}

// T8-②a: the person merge flow — proposal lifecycle, cooling-off
// enforcement, name rehang, external-ref exact conflict demotion, usage
// merge, redirect, both-side revisions, proposal bookkeeping.
func TestMergePersonFullFlow(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createPerson(t, "target")
	source := createPerson(t, "source")
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_person SET description = 'imported bio', field_provenance = '{"description":[{"source":"bangumi","at":"2026-01-01"}]}' WHERE id = ?`,
		source.ID).Error)

	nTarget := createCreditName(t, &target.ID, "本名")
	nSource := createCreditName(t, &source.ID, "裏名義")

	// Conflicting exacts on the same source (bangumi=3): both must demote.
	addExternalRef(t, model.EntityTypePerson, target.ID, 3, "person-111", model.LinkKindExact)
	addExternalRef(t, model.EntityTypePerson, source.ID, 3, "person-222", model.LinkKindExact)
	// Non-conflicting exact on vndb (=2): moves and stays exact.
	addExternalRef(t, model.EntityTypePerson, source.ID, 2, "s5", model.LinkKindExact)
	// Identical row on both sides: the duplicate drops.
	addExternalRef(t, model.EntityTypePerson, target.ID, 5, "eg-1", model.LinkKindProbable)
	addExternalRef(t, model.EntityTypePerson, source.ID, 5, "eg-1", model.LinkKindProbable)

	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	addUsage(t, model.EntityTypePerson, target.ID, "kungal", late)
	addUsage(t, model.EntityTypePerson, source.ID, "kungal", early)
	addUsage(t, model.EntityTypePerson, source.ID, "moyu", early)

	// Pending candidate for the pair — settled by the merge (step 8).
	require.NoError(t, testDB.Create(&model.CatalogMatchCandidate{
		EntityType: model.EntityTypePerson,
		AID:        min(source.ID, target.ID), BID: max(source.ID, target.ID),
		Reason: model.CandidateReasonNameNormEqual, Status: model.CandidateStatusPending,
	}).Error)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, source.ID, target.ID, 7, "same person")
	require.NoError(t, err)

	// Executing an open (unapproved) proposal is illegal.
	require.ErrorIs(t, testMerge.ExecuteMerge(ctx, p.ID, nil), ErrProposalState)

	require.NoError(t, testMerge.ApproveMerge(ctx, p.ID, 7))
	var approved model.CatalogMergeProposal
	require.NoError(t, testDB.First(&approved, p.ID).Error)
	require.NotNil(t, approved.ExecuteAfter)
	assert.Greater(t, time.Until(*approved.ExecuteAfter), 47*time.Hour, "cooling-off must be >= 48h, admins not exempt")

	// The cooling-off window blocks execution.
	require.ErrorIs(t, testMerge.ExecuteMerge(ctx, p.ID, nil), ErrCoolingOff)

	require.NoError(t, testDB.Exec(`UPDATE catalog_merge_proposal SET execute_after = now() - interval '1 hour' WHERE id = ?`, p.ID).Error)
	actor := int64(7)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, &actor))

	// Rehang: the source's name now belongs to the target.
	var movedName model.CatalogCreditName
	require.NoError(t, testDB.First(&movedName, nSource.ID).Error)
	require.NotNil(t, movedName.PersonID)
	assert.Equal(t, target.ID, *movedName.PersonID)
	var keptName model.CatalogCreditName
	require.NoError(t, testDB.First(&keptName, nTarget.ID).Error)
	assert.Equal(t, target.ID, *keptName.PersonID)

	// Survivorship: empty target description filled from source, provenance
	// carried over.
	var mergedTarget model.CatalogPerson
	require.NoError(t, testDB.First(&mergedTarget, target.ID).Error)
	assert.Equal(t, "imported bio", mergedTarget.Description)
	assert.Equal(t, "bangumi", firstProvSource(mergedTarget.FieldProvenance, "description"))

	// External refs: bangumi exacts demoted (conflict), vndb exact moved
	// intact, duplicate probable dropped, source holds nothing.
	type refRow struct {
		SourceID   int16
		ExternalID string
		LinkKind   int16
	}
	var refs []refRow
	require.NoError(t, testDB.Raw(`SELECT source_id, external_id, link_kind FROM catalog_external_ref
		WHERE entity_type = ? AND entity_id = ? ORDER BY source_id, external_id`,
		model.EntityTypePerson, target.ID).Scan(&refs).Error)
	assert.Equal(t, []refRow{
		{2, "s5", model.LinkKindExact},
		{3, "person-111", model.LinkKindProbable},
		{3, "person-222", model.LinkKindProbable},
		{5, "eg-1", model.LinkKindProbable},
	}, refs)
	var srcRefs int64
	testDB.Model(&model.CatalogExternalRef{}).Where("entity_type = ? AND entity_id = ?", model.EntityTypePerson, source.ID).Count(&srcRefs)
	assert.Zero(t, srcRefs)

	// Usage: same-site rows merged with earliest first / latest last, moyu moved.
	var usage []model.CatalogEntityUsage
	require.NoError(t, testDB.Where("entity_type = ? AND entity_id = ?", model.EntityTypePerson, target.ID).
		Order("site").Find(&usage).Error)
	require.Len(t, usage, 2)
	assert.Equal(t, "kungal", usage[0].Site)
	assert.True(t, usage[0].FirstUsedAt.Equal(early), "first_used_at takes the earliest")
	assert.True(t, usage[0].LastConfirmedAt.Equal(late), "last_confirmed_at takes the latest")
	assert.Equal(t, "moyu", usage[1].Site)

	// Redirect + resolve.
	canonical, redirected, err := testResolve.Resolve(ctx, model.EntityTypePerson, source.ID)
	require.NoError(t, err)
	assert.True(t, redirected)
	assert.Equal(t, target.ID, canonical)

	// Revisions on both sides; target's changed_fields records survivorship.
	var srcRev, dstRev model.CatalogRevision
	require.NoError(t, testDB.Where("entity_type = ? AND entity_id = ? AND action = ?",
		model.EntityTypePerson, source.ID, model.RevisionActionMergedSource).First(&srcRev).Error)
	require.NoError(t, testDB.Where("entity_type = ? AND entity_id = ? AND action = ?",
		model.EntityTypePerson, target.ID, model.RevisionActionMergedTarget).First(&dstRev).Error)
	assert.Contains(t, string(srcRev.Snapshot), "裏名義", "source snapshot embeds its credit names")
	assert.Contains(t, string(dstRev.ChangedFields), "description")

	// Candidate for the pair settled; source soft-deleted; proposal executed.
	var candCount int64
	testDB.Model(&model.CatalogMatchCandidate{}).Count(&candCount)
	assert.Zero(t, candCount)
	var deletedCount int64
	testDB.Raw(`SELECT count(*) FROM catalog_person WHERE id = ? AND deleted_at IS NOT NULL`, source.ID).Scan(&deletedCount)
	assert.Equal(t, int64(1), deletedCount)
	var done model.CatalogMergeProposal
	require.NoError(t, testDB.First(&done, p.ID).Error)
	assert.Equal(t, model.ProposalStatusExecuted, done.Status)
	assert.NotNil(t, done.ExecutedAt)
}

// T8-②b: credit_name merge — alias dedup, credit repoint dedup, primary
// reference repoint, and the one hard delete of the family.
func TestMergeCreditNameDedup(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	person := createPerson(t, "one person")
	target := createCreditName(t, &person.ID, "同一表記")
	source := createCreditName(t, &person.ID, "同一表記(旧)")
	require.NoError(t, testDB.Exec(`UPDATE catalog_person SET primary_credit_name_id = ? WHERE id = ?`, source.ID, person.ID).Error)

	// Aliases: "A"/ja duplicates a target alias, "B"/en is new.
	createNameAlias(t, target.ID, "A", "ja")
	createNameAlias(t, source.ID, "A", "ja")
	createNameAlias(t, source.ID, "B", "en")

	// Credits: one duplicate edge (same work+role), one new edge.
	role := seededRoleID(t)
	w1, w2 := createWork(t, "work-1"), createWork(t, "work-2")
	createCredit(t, w1.ID, target.ID, role, nil)
	createCredit(t, w1.ID, source.ID, role, nil) // duplicate after repoint → dropped
	createCredit(t, w2.ID, source.ID, role, nil) // moves

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeCreditName, source.ID, target.ID, 7, "same spelling")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	// Aliases deduped: target ends with exactly A/ja + B/en.
	var aliases []model.CatalogNameAlias
	require.NoError(t, testDB.Where("credit_name_id = ?", target.ID).Order("name").Find(&aliases).Error)
	require.Len(t, aliases, 2)
	assert.Equal(t, "A", aliases[0].Name)
	assert.Equal(t, "B", aliases[1].Name)

	// Credits deduped: w1 has one credit, w2's moved.
	var credits []model.CatalogCredit
	require.NoError(t, testDB.Where("credit_name_id = ?", target.ID).Order("work_id").Find(&credits).Error)
	require.Len(t, credits, 2)
	assert.Equal(t, w1.ID, credits[0].WorkID)
	assert.Equal(t, w2.ID, credits[1].WorkID)

	// Primary reference repointed; source hard-deleted; id still resolves.
	var primary int64
	testDB.Raw(`SELECT primary_credit_name_id FROM catalog_person WHERE id = ?`, person.ID).Scan(&primary)
	assert.Equal(t, target.ID, primary)
	var rowCount int64
	testDB.Raw(`SELECT count(*) FROM catalog_credit_name WHERE id = ?`, source.ID).Scan(&rowCount)
	assert.Zero(t, rowCount, "credit_name source is the one hard delete")
	canonical, _, err := testResolve.Resolve(ctx, model.EntityTypeCreditName, source.ID)
	require.NoError(t, err)
	assert.Equal(t, target.ID, canonical)
}

// T8-③: merge→unmerge drill (doc 10 invariant 11) — zero data loss,
// field-by-field and child-by-child.
func TestMergeUnmergeDrill(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createPerson(t, "keeper")
	source := createPerson(t, "victim")
	gender := model.GenderFemale
	y, m, d := int16(1990), int16(4), int16(1)
	require.NoError(t, testDB.Model(&model.CatalogPerson{}).Where("id = ?", source.ID).Updates(map[string]any{
		"gender": gender, "birth_y": y, "birth_m": m, "birth_d": d, "description": "victim bio",
	}).Error)
	n1 := createCreditName(t, &source.ID, "名義一")
	latin := "Meigi Ichi"
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Where("id = ?", n1.ID).Update("latin", latin).Error)
	createCreditName(t, &source.ID, "名義二")
	createNameAlias(t, n1.ID, "めいぎ", "ja")
	createNameAlias(t, n1.ID, "Meigi", "en")
	require.NoError(t, testDB.Exec(`UPDATE catalog_person SET primary_credit_name_id = ? WHERE id = ?`, n1.ID, source.ID).Error)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, source.ID, target.ID, 7, "drill")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	newID, err := testMerge.Unmerge(ctx, p.ID, nil)
	require.NoError(t, err)
	require.NotZero(t, newID)
	assert.NotEqual(t, source.ID, newID, "unmerge rebuilds under a NEW id")

	// Scalar fields restored verbatim.
	var rebuilt model.CatalogPerson
	require.NoError(t, testDB.First(&rebuilt, newID).Error)
	assert.Equal(t, "victim", rebuilt.DisplayName)
	assert.Equal(t, "victim bio", rebuilt.Description)
	require.NotNil(t, rebuilt.Gender)
	assert.Equal(t, gender, *rebuilt.Gender)
	require.NotNil(t, rebuilt.BirthY)
	assert.Equal(t, [3]int16{y, m, d}, [3]int16{*rebuilt.BirthY, *rebuilt.BirthM, *rebuilt.BirthD})

	// Children rebuilt: two names, primary remapped to the rebuilt 名義一,
	// aliases intact.
	var names []model.CatalogCreditName
	require.NoError(t, testDB.Where("person_id = ?", newID).Order("id").Find(&names).Error)
	require.Len(t, names, 2)
	assert.Equal(t, "名義一", names[0].Name)
	require.NotNil(t, names[0].Latin)
	assert.Equal(t, latin, *names[0].Latin)
	assert.Equal(t, "名義二", names[1].Name)
	require.NotNil(t, rebuilt.PrimaryCreditNameID)
	assert.Equal(t, names[0].ID, *rebuilt.PrimaryCreditNameID)
	var aliases []model.CatalogNameAlias
	require.NoError(t, testDB.Where("credit_name_id = ?", names[0].ID).Order("name").Find(&aliases).Error)
	require.Len(t, aliases, 2)
	assert.Equal(t, "Meigi", aliases[0].Name)
	assert.Equal(t, "めいぎ", aliases[1].Name)

	// Redirect reversed: the old id resolves to the rebuilt entity, one hop.
	canonical, redirected, err := testResolve.Resolve(ctx, model.EntityTypePerson, source.ID)
	require.NoError(t, err)
	assert.True(t, redirected)
	assert.Equal(t, newID, canonical)

	// Reverted revisions on both sides.
	for _, id := range []int64{newID, target.ID} {
		var count int64
		testDB.Model(&model.CatalogRevision{}).
			Where("entity_type = ? AND entity_id = ? AND action = ?", model.EntityTypePerson, id, model.RevisionActionReverted).
			Count(&count)
		assert.Equal(t, int64(1), count, "reverted revision on entity %d", id)
	}
	// Note: the ORIGINAL rehung names still belong to the target — unmerge
	// rebuilds fresh copies from the snapshot (post-merge edits need manual
	// triage; nothing is lost).
}

// T8-④: redirect flattening across consecutive merges + the chain sentinel.
func TestRedirectFlattenAndSentinel(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	a := createPerson(t, "A")
	b := createPerson(t, "B")
	c := createPerson(t, "C")

	p1, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, a.ID, b.ID, 7, "")
	require.NoError(t, err)
	approveAndForceExecutable(t, p1.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p1.ID, nil))

	p2, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, b.ID, c.ID, 7, "")
	require.NoError(t, err)
	approveAndForceExecutable(t, p2.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p2.ID, nil))

	// A's redirect was flattened to C — one hop, no chain.
	canonical, redirected, err := testResolve.Resolve(ctx, model.EntityTypePerson, a.ID)
	require.NoError(t, err)
	assert.True(t, redirected)
	assert.Equal(t, c.ID, canonical)

	// ResolveBatch mixes redirected and canonical ids.
	batch, err := testResolve.ResolveBatch(ctx, model.EntityTypePerson, []int64{a.ID, b.ID, c.ID})
	require.NoError(t, err)
	assert.Equal(t, map[int64]int64{a.ID: c.ID, b.ID: c.ID, c.ID: c.ID}, batch)

	// Sentinel: a manually corrupted chain is a loud error, not a silent walk.
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_redirect SET current_id = ? WHERE entity_type = ? AND old_id = ?`,
		b.ID, model.EntityTypePerson, a.ID).Error)
	_, _, err = testResolve.Resolve(ctx, model.EntityTypePerson, a.ID)
	require.ErrorIs(t, err, ErrRedirectChain)

	// RedirectsSince keyset: two pages of one, no overlap, in order.
	page1, cursor, err := testResolve.RedirectsSince(ctx, nil, repository.RedirectCursor{}, 1)
	require.NoError(t, err)
	require.Len(t, page1, 1)
	page2, _, err := testResolve.RedirectsSince(ctx, nil, cursor, 10)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.NotEqual(t, page1[0].OldID, page2[0].OldID)
}

// T8-⑥: one open proposal per pair; closing frees the slot.
func TestOpenProposalDedup(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	a := createPerson(t, "A")
	b := createPerson(t, "B")

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, a.ID, b.ID, 7, "")
	require.NoError(t, err)
	_, err = testMerge.ProposeMerge(ctx, model.EntityTypePerson, a.ID, b.ID, 8, "again")
	require.ErrorIs(t, err, ErrDuplicateOpenProposal)

	require.NoError(t, testMerge.WithdrawMerge(ctx, p.ID, 7))
	_, err = testMerge.ProposeMerge(ctx, model.EntityTypePerson, a.ID, b.ID, 8, "after withdraw")
	require.NoError(t, err)

	// Same-entity proposals (after resolve) are rejected outright.
	_, err = testMerge.ProposeMerge(ctx, model.EntityTypePerson, a.ID, a.ID, 7, "")
	require.ErrorIs(t, err, ErrSameEntity)
}

// User-owned target fields survive a field_resolution=source override when
// the source value is import-owned (doc 10 invariant 6).
func TestMergeSurvivorshipUserProtection(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createPerson(t, "target")
	source := createPerson(t, "source")
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_person SET description = 'hand-written by a curator', field_provenance = '{"description":[{"source":"user","at":"2026-05-01"}]}' WHERE id = ?`,
		target.ID).Error)
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_person SET description = 'scraped import text', field_provenance = '{"description":[{"source":"bangumi","at":"2026-06-01"}]}' WHERE id = ?`,
		source.ID).Error)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, source.ID, target.ID, 7, "")
	require.NoError(t, err)
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_merge_proposal SET field_resolution = '{"description":"source"}' WHERE id = ?`, p.ID).Error)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	var merged model.CatalogPerson
	require.NoError(t, testDB.First(&merged, target.ID).Error)
	assert.Equal(t, "hand-written by a curator", merged.Description,
		"user-owned field must not be overwritten by an import-owned value")
	assert.Equal(t, "user", firstProvSource(merged.FieldProvenance, "description"))
}
