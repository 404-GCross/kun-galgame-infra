// attribution_gate_test.go — wave 200: evidence is not attribution.
//
// The spine used ONE work set for two different jobs. As evidence ("which works
// does this producer co-occur with") breadth is a virtue. As attribution
// ("which works is this producer responsible for") breadth is a lie: an English
// localisation publisher became a co-author of the Japanese original. Worse, it
// self-healed the wrong way — deleting such an edge only invited the next mint
// to write it again, because the rule that proposed it never changed.
//
// So the two sets are pinned apart here, in both directions.
package orglabels

import (
	"context"
	"testing"

	srcv "api/internal/platform/catalog/srcvndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalisationPublisherIsEvidenceButNotAttribution: a producer whose ONLY
// tie to the work is the English release must not be minted onto that work.
func TestLocalisationPublisherIsEvidenceButNotAttribution(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 910) // olang ja
	mkWorkAnchor(t, sourceVNDB, "v910", 910)

	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.producers (id,type,lang,name,latin,alias,description) VALUES
		('p90','co','en','Sekai Project','','',''),
		('p91','co','ja','きゃべつそふと','','','')`).Error)
	// rJA is the Japanese original; rEN is the English edition of the same work.
	require.NoError(t, testDB.Create(&srcv.Release{ID: "rJA", OLang: "ja", Official: true}).Error)
	require.NoError(t, testDB.Create(&srcv.Release{ID: "rEN", OLang: "en", Official: true}).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_vn (id,vid,rtype) VALUES
		('rJA','v910','complete'),('rEN','v910','complete')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_producers (id,pid,developer,publisher) VALUES
		('rJA','p91',true,true),('rEN','p90',false,true)`).Error)

	orgs, err := loadVNDBOrgs(testDB, 0)
	require.NoError(t, err)
	by := map[string]orgRec{}
	for _, o := range orgs {
		by[o.extID] = o
	}

	// Evidence keeps the localisation publisher — it IS a signal that this
	// company and this work belong to the same neighbourhood, and the grader
	// needs every one of those it can get.
	assert.Equal(t, []int64{910}, by["p90"].works, "the EN publisher stays visible as evidence")
	// Attribution does not. And the set is EMPTY, not absent: that distinction
	// is the whole fix, because an empty set that reads as "unset" falls back
	// to the evidence set and restores the bug.
	assert.Empty(t, by["p90"].attribWorks, "the EN publisher is not attributable to a Japanese work")
	assert.True(t, by["p90"].editionAware, "vndb states editions, so the empty set is an ANSWER")
	assert.Equal(t, []int64{910}, by["p91"].attribWorks, "the original developer is")
}

// TestMintFollowsAttributionNotEvidence closes the loop: the gate above is only
// worth anything if the writer reads it. A localisation-only producer that
// mints a label must mint it with NO work edges.
func TestMintFollowsAttributionNotEvidence(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 920)
	mkWorkAnchor(t, sourceVNDB, "v920", 920)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.producers (id,type,lang,name,latin,alias,description) VALUES
		('p92','co','en','Some Localiser','','','')`).Error)
	require.NoError(t, testDB.Create(&srcv.Release{ID: "rL", OLang: "en", Official: true}).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_vn (id,vid,rtype) VALUES ('rL','v920','complete')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_producers (id,pid,developer,publisher) VALUES ('rL','p92',false,true)`).Error)

	_, err := anchorAll(context.Background(), testDB, testDB, "vndb", 0, true)
	require.NoError(t, err)

	var edges int64
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_work_label WHERE work_id = 920`).Scan(&edges).Error)
	assert.Zero(t, edges, "a company known only from the English release must not be credited with the Japanese work")
}

// TestPatchGroupIsNeverAttributed: a fan patch is in the original language and
// would sail through a language-only gate. `patch` is the second half of the
// rule, and it needs its own witness.
func TestPatchGroupIsNeverAttributed(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 930)
	mkWorkAnchor(t, sourceVNDB, "v930", 930)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.producers (id,type,lang,name,latin,alias,description) VALUES
		('p93','ng','ja','パッチ班','','','')`).Error)
	require.NoError(t, testDB.Create(&srcv.Release{ID: "rP", OLang: "ja", Patch: true}).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_vn (id,vid,rtype) VALUES ('rP','v930','complete')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_producers (id,pid,developer,publisher) VALUES ('rP','p93',true,false)`).Error)

	orgs, err := loadVNDBOrgs(testDB, 0)
	require.NoError(t, err)
	for _, o := range orgs {
		if o.extID == "p93" {
			assert.Empty(t, o.attribWorks, "a patch group did not make the work")
			assert.Equal(t, []int64{930}, o.works, "…but it is still evidence")
			return
		}
	}
	t.Fatal("p93 not loaded")
}

// TestBangumiKeepsOneSet: a source with no edition layer must keep behaving
// exactly as before — the split is a vndb capability, not a global narrowing.
func TestBangumiKeepsOneSet(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	cleanAll(t)

	mkWork(t, 940)
	mkWorkAnchor(t, sourceBangumi, "940", 940)
	require.NoError(t, testDB.Exec(
		`INSERT INTO src_bangumi.person (id,name,type,summary,comments,collects,parser_version,ingested_at,infobox_raw,infobox_parsed,parse_error)
		 VALUES (77,'ある会社',2,'',0,0,1,now(),'','{}','')`).Error)
	require.NoError(t, testDB.Exec(
		`INSERT INTO src_bangumi.subject_person (subject_id,person_id,position,appear_eps) VALUES (940,77,1,'')`).Error)

	orgs, err := loadBGMOrgs(testDB, 0)
	require.NoError(t, err)
	require.Len(t, orgs, 1)
	assert.Equal(t, []int64{940}, orgs[0].works)
	assert.False(t, orgs[0].editionAware, "bangumi draws no edition distinction, so evidence IS attribution")

	_, err = anchorAll(context.Background(), testDB, testDB, "bangumi", 0, true)
	require.NoError(t, err)
	var edges int64
	// The capacity is the bangumi lane's own business (edgeKindFor); what this
	// test guards is that an edge is minted at all.
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_work_label WHERE work_id = 940`).Scan(&edges).Error)
	assert.Equal(t, int64(1), edges, "the bangumi lane still mints its edge")
}
