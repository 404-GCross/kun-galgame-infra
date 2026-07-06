package importer

import (
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/llmsuggest"
	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"

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
		fmt.Fprintf(os.Stderr, "SKIP: no test db: %v\n", err)
		os.Exit(0)
	}
	for _, step := range []func(*gorm.DB) error{migrate.Run, seed.Run, srcb.EnsureSchema, llmsuggest.EnsureSchema} {
		if err := step(db); err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: setup: %v\n", err)
			os.Exit(0)
		}
	}
	// EG staging stand-ins.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS creaters (id int, raw jsonb)`,
		`CREATE TABLE IF NOT EXISTS characters (id int, raw jsonb)`,
		`CREATE TABLE IF NOT EXISTS staff (game bigint, creater_id bigint, shubetu int)`,
		`CREATE TABLE IF NOT EXISTS appearances (game bigint, character_id bigint)`,
		`CREATE TABLE IF NOT EXISTS appearance_actors (game bigint, character_id bigint, actor_id bigint)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: %s: %v\n", s, err)
			os.Exit(0)
		}
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	tables := []string{
		"catalog_credit", "catalog_match_candidate", "catalog_external_ref", "catalog_revision",
		"catalog_credit_name", "catalog_label", "catalog_character", "catalog_work",
		"src_bangumi.subject_person", "src_bangumi.subject_character", "src_bangumi.person_character",
		"src_bangumi.person", "src_bangumi.character",
		"src_llm.bid_identity_verdict", "creaters", "characters", "staff", "appearances", "appearance_actors",
	}
	for _, tb := range tables {
		require.NoError(t, testDB.Exec("TRUNCATE "+tb+" RESTART IDENTITY CASCADE").Error)
	}
}

// seedGatedWork creates a claimed work + its bid-exact ref + a pass verdict, so
// the Bangumi gate resolves bid → work.
func seedGatedWork(t *testing.T, bid int64, layer string) int64 {
	t.Helper()
	var workID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, site, product_work_id, olang, display_name, content_rating, status, extra, field_provenance)
		VALUES (1,'galgame_wiki',?, 'ja','w',0,0,'{}','{}') RETURNING id`, bid).Scan(&workID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 3, ?, 0, 'rule:wiki-bid-typed')`, workID, fmt.Sprint(bid)).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_llm.bid_identity_verdict (work_id, galgame_id, bid, layer, input_hash, model, prompt_version)
		VALUES (?, ?, ?, ?, ?, 'm', 'p')`, workID, bid, bid, layer, fmt.Sprintf("h%d", bid)).Error)
	return workID
}

func TestBangumiWave(t *testing.T) {
	clean(t)
	passWork := seedGatedWork(t, 100, "pass")
	seedGatedWork(t, 200, "suspect") // gated OUT

	// persons: 1 individual (writer), 1 company (developer), + a VA.
	testDB.Exec(`INSERT INTO src_bangumi.person (id, type, name, career, infobox_raw, parse_error, summary, comments, collects, parser_version, ingested_at)
		VALUES (10,1,'麻枝准','[]','',' ','',0,0,'v',now()), (20,2,'Key','[]','','','',0,0,'v',now()), (30,1,'VA太郎','[]','','','',0,0,'v',now())`)
	// subject_person: person 10 as position 1001 on subject 100 + the same on the gated-out subject 200.
	testDB.Exec(`INSERT INTO src_bangumi.subject_person (person_id, subject_id, position, appear_eps) VALUES (10,100,1001,''),(20,100,1001,''),(10,200,1001,'')`)
	// a character + its VA link.
	testDB.Exec(`INSERT INTO src_bangumi.character (id, role, name, infobox_raw, parse_error, summary, comments, collects, parser_version, ingested_at) VALUES (50,1,'渚','','','',0,0,'v',now())`)
	testDB.Exec(`INSERT INTO src_bangumi.subject_character (character_id, subject_id, type, item_order) VALUES (50,100,1,0)`)
	testDB.Exec(`INSERT INTO src_bangumi.person_character (person_id, subject_id, character_id, type, summary) VALUES (30,100,50,1,'')`)
	// map position 1001 → an existing role so credits land.
	var roleID int64
	require.NoError(t, testDB.Raw(`SELECT role_id FROM catalog_source_role_map WHERE source_id=3 LIMIT 1`).Scan(&roleID).Error)
	testDB.Exec(`INSERT INTO catalog_source_role_map (source_id, source_role, role_id, note) VALUES (3, '4:1001', ?, '') ON CONFLICT DO NOTHING`, roleID)

	st, err := New(testDB, nil, Options{Source: "bangumi"}).Run("bangumi")
	require.NoError(t, err)
	assert.Equal(t, 3, st.NamesCreated, "3 credit_names: writer, company, VA")
	assert.Equal(t, 1, st.LabelsCreated, "company → label")
	assert.Equal(t, 1, st.CharactersCreated)
	// credits: person10 staff + company20 staff + VA on subject 100 (the 200 links are gated out).
	assert.Equal(t, 3, st.CreditsWritten)

	// Orphan invariant: every credit_name has person_id NULL.
	var orphaned int64
	testDB.Raw(`SELECT count(*) FROM catalog_credit_name WHERE person_id IS NOT NULL`).Scan(&orphaned)
	assert.Zero(t, orphaned, "all names orphan")
	var persons int64
	testDB.Table("catalog_person").Count(&persons)
	assert.Zero(t, persons, "no person rows created")

	// Self exact anchors with the right rule.
	var anchorRule string
	require.NoError(t, testDB.Raw(`SELECT matched_by FROM catalog_external_ref WHERE entity_type=1 AND source_id=3 AND external_id='10'`).Scan(&anchorRule).Error)
	assert.Equal(t, "rule:bangumi-person-import", anchorRule)

	// Company credit carries a label_id; individual does not.
	var companyLabels int64
	testDB.Raw(`SELECT count(*) FROM catalog_credit c JOIN catalog_external_ref r ON r.entity_type=1 AND r.entity_id=c.credit_name_id AND r.source_id=3 AND r.external_id='20' WHERE c.label_id IS NOT NULL`).Scan(&companyLabels)
	assert.Equal(t, int64(1), companyLabels)

	// Gate: no credit on the suspect work.
	var suspectCredits int64
	testDB.Raw(`SELECT count(*) FROM catalog_credit WHERE work_id <> ?`, passWork).Scan(&suspectCredits)
	assert.Zero(t, suspectCredits, "gated-out work has no credits")

	// VA credit has character_id + voice-actor role + the 主角 note.
	var vaNote string
	require.NoError(t, testDB.Raw(`SELECT note FROM catalog_credit WHERE role_id=1 AND character_id IS NOT NULL`).Scan(&vaNote).Error)
	assert.Equal(t, "主角", vaNote)

	// Imported revisions: one per created entity (3 names + 1 label + 1 char = 5).
	var revs int64
	testDB.Raw(`SELECT count(*) FROM catalog_revision WHERE action=?`, model.RevisionActionImported).Scan(&revs)
	assert.Equal(t, int64(5), revs)

	// Idempotency: a second run writes nothing.
	st2, err := New(testDB, nil, Options{Source: "bangumi"}).Run("bangumi")
	require.NoError(t, err)
	assert.Zero(t, st2.NamesCreated)
	assert.Zero(t, st2.CharactersCreated)
	assert.Zero(t, st2.CreditsWritten)
	assert.Equal(t, 3, st2.Already, "all 3 credits already present")
}

func TestUnmappedRoleSkipped(t *testing.T) {
	clean(t)
	seedGatedWork(t, 100, "pass")
	testDB.Exec(`INSERT INTO src_bangumi.person (id, type, name, career, infobox_raw, parse_error, summary, comments, collects, parser_version, ingested_at) VALUES (10,1,'X','[]','','','',0,0,'v',now())`)
	// position 999999 is not in the role map.
	testDB.Exec(`INSERT INTO src_bangumi.subject_person (person_id, subject_id, position, appear_eps) VALUES (10,100,999999,'')`)

	st, err := New(testDB, nil, Options{Source: "bangumi"}).Run("bangumi")
	require.NoError(t, err)
	assert.Equal(t, 1, st.NamesCreated, "the name is still created")
	assert.Equal(t, 1, st.SkippedUnmappedRole)
	assert.Zero(t, st.CreditsWritten, "unmapped position → no credit")
}

func TestEGWaveAndCandidates(t *testing.T) {
	clean(t)
	// gated EG work (rosetta ref) for game 7.
	var workID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, site, product_work_id, olang, display_name, content_rating, status, extra, field_provenance)
		VALUES (1,'galgame_wiki',7,'ja','w',0,0,'{}','{}') RETURNING id`).Scan(&workID).Error)
	testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by) VALUES (5,?,5,'7',1,'rule:eg-vndb-rosetta')`, workID)
	// EG staging: creater 500 (illustrator shubetu 1) + VA 501; character 800.
	testDB.Exec(`INSERT INTO creaters (id, raw) VALUES (500, '{"name":"絵師","twitter_username":"SharedHandle"}'), (501,'{"name":"声太"}')`)
	testDB.Exec(`INSERT INTO characters (id, raw) VALUES (800, '{"name":"EGキャラ"}')`)
	testDB.Exec(`INSERT INTO staff (game, creater_id, shubetu) VALUES (7,500,1)`)
	testDB.Exec(`INSERT INTO appearance_actors (game, character_id, actor_id) VALUES (7,800,501)`)

	// A Bangumi person sharing the twitter handle (for the candidate).
	seedGatedWork(t, 100, "pass")
	testDB.Exec(`INSERT INTO src_bangumi.person (id, type, name, career, infobox_raw, infobox_parsed, parse_error, summary, comments, collects, parser_version, ingested_at)
		VALUES (10,1,'絵師B','[]','', '{"Type":"Crt","Fields":[{"Key":"Twitter","Value":"@SharedHandle","Items":null,"Array":false,"Null":false}]}'::jsonb, '','',0,0,'v',now())`)
	testDB.Exec(`INSERT INTO src_bangumi.subject_person (person_id, subject_id, position, appear_eps) VALUES (10,100,1001,'')`)
	var roleID int64
	testDB.Raw(`SELECT role_id FROM catalog_source_role_map WHERE source_id=3 LIMIT 1`).Scan(&roleID)
	testDB.Exec(`INSERT INTO catalog_source_role_map (source_id, source_role, role_id, note) VALUES (3,'4:1001',?,'') ON CONFLICT DO NOTHING`, roleID)

	im := New(testDB, testDB, Options{Source: "all"})
	_, err := im.Run("all")
	require.NoError(t, err)

	// EG creater illustrator credit written (shubetu 1 → illustration role).
	var egCredits int64
	testDB.Raw(`SELECT count(*) FROM catalog_credit WHERE source_id=5`).Scan(&egCredits)
	assert.Equal(t, int64(2), egCredits, "1 staff + 1 VA")
	// EG VA credit has voice-actor role + character.
	var egVA int64
	testDB.Raw(`SELECT count(*) FROM catalog_credit WHERE source_id=5 AND role_id=1 AND character_id IS NOT NULL`).Scan(&egVA)
	assert.Equal(t, int64(1), egVA)

	// Candidate: the shared twitter handle links the two credit_names.
	cs, err := im.RunCandidates()
	require.NoError(t, err)
	var cands int64
	testDB.Raw(`SELECT count(*) FROM catalog_match_candidate WHERE entity_type=1 AND reason=0`).Scan(&cands)
	assert.Equal(t, int64(1), cands, "one shared-handle candidate")
	assert.GreaterOrEqual(t, cs.Twitter, 1)
}
