// public_display_limit_test.go — A2-R5: the EDITORIAL DISPLAY axis
// (refs/proj/140).
//
// The incident this closes (doc 106 §38): a downstream mapped the catalog's AGE
// axis (content_rating) onto its own DISPLAY gate (content_limit) and hid every
// r18 game — 94.5% of the claimed live population — collapsing its indexable
// surface from 6,117 works to 599. The two axes are different questions, and on
// production they disagree in bulk: 5,568 works are r18 games whose wiki display
// material is editorially sfw, and 50 are all_ages games the wiki marked nsfw.
//
// The cases below pin the axis where it actually runs: the Go projection, its
// SQL twin (cross-checked row-for-row, exactly as the claim_state axis is), the
// three-gate conjunction, and the value that reaches the wire.
package service

import (
	"strings"
	"testing"

	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
)

// ensureGalgameDisplayStub makes sure the wiki body table the claimed branch
// reads exists with the one column this axis needs. Against the real schema
// (the shared kun_catalog_test) both statements are no-ops; against a database
// that only ever ran the catalog migration they create the minimum the bridge
// needs — the same stub posture the handler package's title-bridge suite uses.
func ensureGalgameDisplayStub(t *testing.T) {
	t.Helper()
	if err := testDB.Exec(`CREATE TABLE IF NOT EXISTS galgame (
		id bigint PRIMARY KEY,
		user_id bigint NOT NULL DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("galgame stub: %v", err)
	}
	if err := testDB.Exec(
		`ALTER TABLE galgame ADD COLUMN IF NOT EXISTS content_limit varchar(10) DEFAULT 'sfw'`).Error; err != nil {
		t.Fatalf("galgame content_limit column: %v", err)
	}
}

// insertGalgameBodyLimit writes one wiki body carrying just its display flag.
// user_id is passed explicitly for the real schema (NOT NULL, no default).
func insertGalgameBodyLimit(t *testing.T, galgameID int64, contentLimit string) {
	t.Helper()
	if err := testDB.Exec(
		`INSERT INTO galgame (id, user_id, content_limit) VALUES (?, 0, ?)
		 ON CONFLICT (id) DO UPDATE SET content_limit = EXCLUDED.content_limit`,
		galgameID, contentLimit).Error; err != nil {
		t.Fatalf("insert galgame body %d: %v", galgameID, err)
	}
}

// displayRow is one column combination plus the wiki body (if any) behind it.
type displayRow struct {
	name   string
	site   *string
	pwid   *int64
	wiki   string // "" = no body row at all
	rating int16
}

// displayLimitFixture creates one LIVE galgame work per combination — including
// the two rows where the axes DISAGREE, which are the point — and returns them
// keyed by the value model.DisplayLimitKey says they carry.
//
// Every claim gets its OWN product_work_id: (medium_id, site, product_work_id)
// is unique (uq_catalog_work_claim), and reusing one would reject the insert
// instead of exercising the case.
func displayLimitFixture(t *testing.T) (byLimit map[string][]int64, all []int64) {
	t.Helper()
	ensureGalgameDisplayStub(t)
	wiki, empty, letmoe := "galgame_wiki", "", "letmoe"
	pw := func(n int64) *int64 { return &n }

	rows := []displayRow{
		// ── bodyless: the age axis is the only signal ──
		{"bodyless all_ages", nil, nil, "", model.ContentRatingAllAges},
		{"bodyless sensitive", nil, nil, "", model.ContentRatingSensitive},
		{"bodyless r18", nil, nil, "", model.ContentRatingR18},
		{"empty site is bodyless", &empty, pw(9401), "", model.ContentRatingR18},
		{"site without a product work id", &wiki, nil, "", model.ContentRatingR18},
		// ── claimed: the wiki body decides, and the rating is ignored ──
		{"claimed r18 game, wiki says sfw", &wiki, pw(9405), "sfw", model.ContentRatingR18},
		{"claimed all_ages game, wiki says nsfw", &wiki, pw(9406), "nsfw", model.ContentRatingAllAges},
		{"claimed r18 game, wiki says nsfw", &wiki, pw(9407), "nsfw", model.ContentRatingR18},
		{"claimed, wiki body missing", &wiki, pw(9408), "", model.ContentRatingR18},
		{"claimed, wiki value outside the vocabulary", &wiki, pw(9409), "bogus", model.ContentRatingR18},
		// ── a claimer with no wiki lane: no body to read, so sfw ──
		{"non-wiki claim of an r18 game", &letmoe, pw(9410), "", model.ContentRatingR18},
	}

	byLimit = map[string][]int64{}
	for _, r := range rows {
		w := createWorkX(t, galgameMediumID, r.rating, model.WorkStatusLive, r.name)
		setClaimColumns(t, w.ID, r.site, r.pwid, nil)
		if r.wiki != "" && r.pwid != nil {
			insertGalgameBodyLimit(t, *r.pwid, r.wiki)
		}
		key := model.DisplayLimitKey(r.site, r.pwid, r.wiki, r.rating)
		byLimit[key] = append(byLimit[key], w.ID)
		all = append(all, w.ID)
	}
	return byLimit, all
}

// TestDisplayLimitWhereMatchesProjection is the load-bearing case: for EVERY
// column combination the SQL predicate selects a row exactly when
// model.DisplayLimitKey names that row's value. The Go projection and its DB
// twin are two languages saying one thing, and this is what keeps them saying
// it (the claim_state axis's TestClaimStateWhereMatchesProjection, verbatim
// posture).
func TestDisplayLimitWhereMatchesProjection(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	byLimit, all := displayLimitFixture(t)

	// Sanity: the fixture actually exercises both values, and both DISAGREEMENT
	// directions (an r18 game reading sfw, an all_ages game reading nsfw).
	for _, lim := range []string{model.DisplayLimitKeySFW, model.DisplayLimitKeyNSFW} {
		if len(byLimit[lim]) == 0 {
			t.Fatalf("fixture covers no %q row", lim)
		}
	}

	seen := map[int64]int{}
	for lim, want := range byLimit {
		got := idSet(listIDs(t, WorksListFilter{Sort: "id", NSFW: true, DisplayLimits: []string{lim}}))
		if len(got) != len(want) {
			t.Fatalf("content_limit=%s selected %d rows, want %d (%v vs %v)", lim, len(got), len(want), got, want)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("content_limit=%s must select work %d (model.DisplayLimitKey says %s)", lim, id, lim)
			}
			seen[id]++
		}
	}
	// A partition, not two overlapping filters: every row landed on exactly one
	// value, so naming both is the ungated set and no row can hide from both.
	for _, id := range all {
		if seen[id] != 1 {
			t.Fatalf("work %d matched %d display limits, want exactly 1", id, seen[id])
		}
	}
	if n := len(idSet(listIDs(t, WorksListFilter{
		Sort: "id", NSFW: true,
		DisplayLimits: []string{model.DisplayLimitKeySFW, model.DisplayLimitKeyNSFW},
	}))); n != len(all) {
		t.Fatalf("both values selected %d rows, want the whole set of %d", n, len(all))
	}
}

// TestWorksListDisplayLimitIsNotTheAgeAxis is the wave's actual promise: an r18
// game whose wiki material is editorially safe is served by content_limit=sfw
// (and is NOT served by content_rating=all_ages), and an all_ages game the wiki
// marked nsfw is excluded from it. Those are the 5,568 + 50 production rows.
func TestWorksListDisplayLimitIsNotTheAgeAxis(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	ensureGalgameDisplayStub(t)

	safeR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "成人ゲーム・素材は安全")
	spicySFW := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "全年齢ゲーム・素材は成人")
	bodylessR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "無認領・成人")
	for i, id := range []int64{safeR18.ID, spicySFW.ID} {
		claimWork(t, id, "galgame_wiki", int64(9500+i))
	}
	insertGalgameBodyLimit(t, 9500, "sfw")
	insertGalgameBodyLimit(t, 9501, "nsfw")

	sfw := idSet(listIDs(t, WorksListFilter{
		Sort: "id", NSFW: true, DisplayLimits: []string{model.DisplayLimitKeySFW},
	}))
	if !sfw[safeR18.ID] {
		t.Fatalf("content_limit=sfw dropped the claimed r18 work with safe material — the exact 5,568-row incident")
	}
	if sfw[spicySFW.ID] {
		t.Fatalf("content_limit=sfw served an all_ages work the wiki marked nsfw — the reverse leak")
	}
	if sfw[bodylessR18.ID] {
		t.Fatalf("content_limit=sfw served a BODYLESS r18 work: with no editorial flag the rating is the only signal")
	}

	nsfw := idSet(listIDs(t, WorksListFilter{
		Sort: "id", NSFW: true, DisplayLimits: []string{model.DisplayLimitKeyNSFW},
	}))
	if !nsfw[spicySFW.ID] || !nsfw[bodylessR18.ID] || nsfw[safeR18.ID] {
		t.Fatalf("content_limit=nsfw = %v, want exactly the wiki-nsfw work and the bodyless r18 one", nsfw)
	}

	// No parameter = no gate: every pre-existing caller's page is unchanged.
	if n := len(idSet(listIDs(t, WorksListFilter{Sort: "id", NSFW: true}))); n != 3 {
		t.Fatalf("no content_limit selected %d rows, want all 3", n)
	}
}

// TestWorksListThreeGatesAreOrthogonal pins nsfw × content_limit × claim_state
// as three independent conjuncts: a row is served only when ALL of them admit
// it, and opening one never reopens another. It also checks the paged walk
// against the single big page under all three at once — one predicate, not a
// per-page afterthought.
func TestWorksListThreeGatesAreOrthogonal(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	ensureGalgameDisplayStub(t)

	// Four claimed works spanning (rating × wiki flag × claim state).
	safeLive := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "成人・安全・公開")
	safeDraft := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "成人・安全・下書き")
	spicyLive := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "成人・成人素材・公開")
	sfwLive := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "全年齢・安全・公開")
	for i, id := range []int64{safeLive.ID, safeDraft.ID, spicyLive.ID, sfwLive.ID} {
		claimWork(t, id, "galgame_wiki", int64(9600+i))
	}
	for id, limit := range map[int64]string{9600: "sfw", 9601: "sfw", 9602: "nsfw", 9603: "sfw"} {
		insertGalgameBodyLimit(t, id, limit)
	}
	setClaimState(t, safeLive.ID, i16(model.ClaimStateLive))
	setClaimState(t, safeDraft.ID, i16(model.ClaimStateDraft))
	setClaimState(t, spicyLive.ID, i16(model.ClaimStateLive))
	setClaimState(t, sfwLive.ID, i16(model.ClaimStateLive))

	live := []string{model.ClaimStateKeyLive}
	sfwOnly := []string{model.DisplayLimitKeySFW}
	for _, tc := range []struct {
		name   string
		nsfw   bool
		limits []string
		states []string
		want   []int64
	}{
		{"no gate at all", true, nil, nil, []int64{safeLive.ID, safeDraft.ID, spicyLive.ID, sfwLive.ID}},
		{"age gate alone drops every r18 game", false, nil, nil, []int64{sfwLive.ID}},
		{"display gate alone keeps the safe r18 games", true, sfwOnly, nil, []int64{safeLive.ID, safeDraft.ID, sfwLive.ID}},
		{"display + claim gate", true, sfwOnly, live, []int64{safeLive.ID, sfwLive.ID}},
		{"the display gate never reopens the age gate", false, sfwOnly, nil, []int64{sfwLive.ID}},
		{"the claim gate never reopens the display gate", true, sfwOnly, live, []int64{safeLive.ID, sfwLive.ID}},
		{"nsfw display + live claim", true, []string{model.DisplayLimitKeyNSFW}, live, []int64{spicyLive.ID}},
	} {
		got := idSet(listIDs(t, WorksListFilter{
			Sort: "id", NSFW: tc.nsfw, DisplayLimits: tc.limits, ClaimStates: tc.states,
		}))
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %d rows %v, want %d %v", tc.name, len(got), got, len(tc.want), tc.want)
		}
		for _, id := range tc.want {
			if !got[id] {
				t.Fatalf("%s: work %d missing", tc.name, id)
			}
		}
	}

	// The keyset walk under all three gates is complete: paging with limit 2
	// returns the same set one big page does.
	f := WorksListFilter{Sort: "id", NSFW: true, DisplayLimits: sfwOnly, ClaimStates: live}
	walked := idSet(listIDs(t, f))
	onePage, err := newPublicSvc().WorksList(t.Context(), f, "", 100)
	if err != nil {
		t.Fatalf("WorksList single page: %v", err)
	}
	if len(onePage.Items) != len(walked) {
		t.Fatalf("single page served %d rows but the keyset walk served %d — the gate must not depend on paging",
			len(onePage.Items), len(walked))
	}
}

// TestClaimedByContentLimitOnEveryFace pins the VALUE on the wire: every face
// that emits a claimed_by object carries content_limit, and it is the wiki
// body's flag — not a re-encoding of the rating. An unclaimed row still emits
// null rather than an object.
func TestClaimedByContentLimitOnEveryFace(t *testing.T) {
	cleanTables(t)
	ensureGalgameDisplayStub(t)
	svc := newPublicSvc()
	ctx := t.Context()

	safeR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "LimitSafeR18")
	spicySFW := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "LimitSpicySFW")
	bodyless := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "LimitBodyless")
	claimWork(t, safeR18.ID, "galgame_wiki", 9700)
	claimWork(t, spicySFW.ID, "galgame_wiki", 9701)
	insertGalgameBodyLimit(t, 9700, "sfw")
	insertGalgameBodyLimit(t, 9701, "nsfw")
	// The draft/hidden claims carry the editorial flag too — the display axis is
	// independent of the claim's visibility.
	setClaimState(t, spicySFW.ID, i16(model.ClaimStateDraft))

	want := map[int64]string{safeR18.ID: "sfw", spicySFW.ID: "nsfw"}

	// (1) works LIST (which is also the search face's and the calendar's item
	// projection — all three share enrichWorkListItems).
	page, err := svc.WorksList(ctx, WorksListFilter{Sort: "id", NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("WorksList: %v", err)
	}
	seen := 0
	for _, it := range page.Items {
		if it.ID == bodyless.ID {
			if it.ClaimedBy != nil {
				t.Fatalf("bodyless work got claimed_by %+v, want null", it.ClaimedBy)
			}
			continue
		}
		if it.ClaimedBy == nil || it.ClaimedBy.ContentLimit != want[it.ID] {
			t.Fatalf("list work %d claimed_by = %+v, want content_limit %q", it.ID, it.ClaimedBy, want[it.ID])
		}
		seen++
	}
	if seen != len(want) {
		t.Fatalf("list covered %d claimed works, want %d", seen, len(want))
	}

	// (2) work DETAIL.
	for id, limit := range want {
		rec, found, err := svc.WorkDetail(ctx, id, PublicInclude{}, true, 0)
		if err != nil || !found {
			t.Fatalf("WorkDetail %d: found=%v err=%v", id, found, err)
		}
		if rec.ClaimedBy == nil || rec.ClaimedBy.ContentLimit != limit {
			t.Fatalf("detail work %d claimed_by = %+v, want content_limit %q", id, rec.ClaimedBy, limit)
		}
	}

	// (3) lookup — single and batch, both through the anchor registry, and both
	// blocks of the record (the brief AND the top-level claimed_by).
	addExternalRef(t, model.EntityTypeWork, safeR18.ID, srcVNDB, "v70501", model.LinkKindExact)
	single, found, err := svc.Lookup(ctx, "vndb", "v70501", true)
	if err != nil || !found {
		t.Fatalf("Lookup: found=%v err=%v", found, err)
	}
	if single.ClaimedBy == nil || single.ClaimedBy.ContentLimit != "sfw" {
		t.Fatalf("lookup claimed_by = %+v, want content_limit sfw", single.ClaimedBy)
	}
	if single.Work == nil || single.Work.ClaimedBy == nil || single.Work.ClaimedBy.ContentLimit != "sfw" {
		t.Fatalf("lookup brief claimed_by = %+v, want content_limit sfw", single.Work)
	}

	// (4) the batch claim loader (entity → works lanes): a label's works block.
	claims, err := svc.claimedByFor(ctx, []int64{safeR18.ID, spicySFW.ID, bodyless.ID})
	if err != nil {
		t.Fatalf("claimedByFor: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("claimedByFor returned %d claims, want 2 (the bodyless row has none)", len(claims))
	}
	for id, limit := range want {
		if claims[id] == nil || claims[id].ContentLimit != limit {
			t.Fatalf("batch claim %d = %+v, want content_limit %q", id, claims[id], limit)
		}
	}
}

// TestCalendarDisplayLimitGateAndETag pins the calendar half: the gate decides
// BUCKET MEMBERSHIP and the count, the navigation bounds follow it, and — the
// cache-correctness half — two different gates can never mint the same ETag.
func TestCalendarDisplayLimitGateAndETag(t *testing.T) {
	cleanTables(t)
	ensureGalgameDisplayStub(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// One 2024-06 work per display value, both claimed r18 games so the AGE axis
	// cannot be what separates them.
	safe := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "CalSafe")
	spicy := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "CalSpicy")
	claimWork(t, safe.ID, "galgame_wiki", 9800)
	claimWork(t, spicy.ID, "galgame_wiki", 9801)
	insertGalgameBodyLimit(t, 9800, "sfw")
	insertGalgameBodyLimit(t, 9801, "nsfw")
	createRelease(t, safe.ID, 2024, 6, 14)
	// The nsfw one is a year later, so the gate also moves the navigation bounds.
	createRelease(t, spicy.ID, 2025, 6, 14)

	june2024 := CalendarBucket{Kind: CalendarMonthBucket, Year: 2024, Month: 6}
	june2025 := CalendarBucket{Kind: CalendarMonthBucket, Year: 2025, Month: 6}
	ungated := CalendarFilter{NSFW: true}
	sfwOnly := CalendarFilter{NSFW: true, DisplayLimits: []string{model.DisplayLimitKeySFW}}

	for _, tc := range []struct {
		name   string
		bucket CalendarBucket
		f      CalendarFilter
		want   int64
	}{
		{"ungated june 2024", june2024, ungated, 1},
		{"ungated june 2025", june2025, ungated, 1},
		{"sfw-gated june 2024", june2024, sfwOnly, 1},
		{"sfw-gated june 2025", june2025, sfwOnly, 0},
	} {
		count, _, err := svc.CalendarMeta(ctx, tc.bucket, tc.f)
		if err != nil {
			t.Fatalf("%s: CalendarMeta: %v", tc.name, err)
		}
		if count != tc.want {
			t.Fatalf("%s: count = %d, want %d", tc.name, count, tc.want)
		}
		page, err := svc.CalendarPage(ctx, tc.bucket, tc.f, "", 50)
		if err != nil {
			t.Fatalf("%s: CalendarPage: %v", tc.name, err)
		}
		if int64(len(page.Items)) != tc.want {
			t.Fatalf("%s: page carries %d rows but the count says %d — one gate, two queries",
				tc.name, len(page.Items), tc.want)
		}
	}

	// The navigation frame runs under the caller's own gate.
	_, maxOrd, found, err := svc.CalendarBounds(ctx, sfwOnly)
	if err != nil || !found {
		t.Fatalf("CalendarBounds: found=%v err=%v", found, err)
	}
	if maxOrd != 202406_00 {
		t.Fatalf("sfw-gated max month = %d, want 202406_00 (the nsfw 2025 work is outside this population)", maxOrd)
	}

	// ETag: the display gate is in the population key, so an ungated and a gated
	// caller can never collide on a validator — even when their counts agree.
	ungatedCount, ungatedMax, err := svc.CalendarMeta(ctx, june2024, ungated)
	if err != nil {
		t.Fatalf("CalendarMeta ungated: %v", err)
	}
	gatedCount, gatedMax, err := svc.CalendarMeta(ctx, june2024, sfwOnly)
	if err != nil {
		t.Fatalf("CalendarMeta gated: %v", err)
	}
	if ungatedCount != gatedCount {
		t.Fatalf("the two populations must have EQUAL counts here (%d vs %d) — that is what makes the key load-bearing",
			ungatedCount, gatedCount)
	}
	a := CalendarETag("month-2024-06", ungated.PopulationKey(), ungatedCount, ungatedMax)
	b := CalendarETag("month-2024-06", sfwOnly.PopulationKey(), gatedCount, gatedMax)
	if a == b {
		t.Fatalf("ungated and content_limit=sfw share the ETag %s — a cross-gate cache smear", a)
	}
	nsfwOnly := CalendarFilter{NSFW: true, DisplayLimits: []string{model.DisplayLimitKeyNSFW}}
	if sfwOnly.PopulationKey() == nsfwOnly.PopulationKey() {
		t.Fatalf("sfw and nsfw share the population key %q", sfwOnly.PopulationKey())
	}
}

// TestDisplayLimitVocabularyIsClosed pins the token set the handler 400s against.
func TestDisplayLimitVocabularyIsClosed(t *testing.T) {
	for _, ok := range []string{"sfw", "nsfw"} {
		if !IsDisplayLimit(ok) {
			t.Fatalf("%q must be a legal content_limit token", ok)
		}
	}
	// `all` is the WIKI face's third token; here absence already means both, so
	// accepting it would quietly imply the two parameters are the same thing.
	for _, bad := range []string{"", "all", "SFW", "NSFW", "r18", "safe", "true"} {
		if IsDisplayLimit(bad) {
			t.Fatalf("%q must NOT be a legal content_limit token", bad)
		}
	}
}

// TestDisplayLimitFilterCompilation pins the gate inside the ONE Meilisearch
// expression: absent = no clause at all, one value = one equality, both = a
// parenthesized OR group that still ANDs with the rest.
func TestDisplayLimitFilterCompilation(t *testing.T) {
	if got := (WorksSearchFilter{}).meiliFilter(""); strings.Contains(got, "content_limit") {
		t.Fatalf("no content_limit param must emit no clause: %q", got)
	}

	one := WorksSearchFilter{DisplayLimits: []string{model.DisplayLimitKeySFW}}.meiliFilter("")
	if !strings.Contains(one, "(content_limit = 'sfw')") {
		t.Fatalf("single content_limit clause = %q", one)
	}
	// It rides the same expression as the age gate — one door, not two.
	if !strings.Contains(one, "content_rating != 2") {
		t.Fatalf("content_limit must not replace the other clauses: %q", one)
	}

	both := WorksSearchFilter{
		DisplayLimits: []string{model.DisplayLimitKeySFW, model.DisplayLimitKeyNSFW},
	}.meiliFilter("")
	if !strings.Contains(both, "(content_limit = 'sfw' OR content_limit = 'nsfw')") {
		t.Fatalf("multi content_limit clause = %q", both)
	}
	// And it composes with the OTHER two gates in one conjunction.
	all := WorksSearchFilter{
		DisplayLimits: []string{model.DisplayLimitKeySFW},
		ClaimStates:   []string{model.ClaimStateKeyLive},
	}.meiliFilter("")
	for _, want := range []string{"content_rating != 2", "(claim_state = 'live')", "(content_limit = 'sfw')"} {
		if !strings.Contains(all, want) {
			t.Fatalf("three-gate expression %q is missing %q", all, want)
		}
	}
}

// TestWorksSearchDisplayLimitGate is the end-to-end search case: three works
// indexed through the PRODUCTION projection, and every content_limit query
// returns exactly its own set with total agreeing with the page.
func TestWorksSearchDisplayLimitGate(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	ensureGalgameDisplayStub(t)
	idx := worksSearchIndexer(t)
	svc := newPublicSvc().WithWorksSearch(idx)

	safeR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "検索・成人・安全素材")
	spicySFW := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "検索・全年齢・成人素材")
	bodylessR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "検索・無認領・成人")
	claimWork(t, safeR18.ID, "galgame_wiki", 9900)
	claimWork(t, spicySFW.ID, "galgame_wiki", 9901)
	insertGalgameBodyLimit(t, 9900, "sfw")
	insertGalgameBodyLimit(t, 9901, "nsfw")

	// Index off the very columns the reindexer reads, through the very
	// projection it uses — a look-alike here would prove nothing.
	docs := make([]catsearch.WorkDocInput, 0, 3)
	for _, w := range []struct {
		id   int64
		name string
	}{
		{safeR18.ID, "検索・成人・安全素材"}, {spicySFW.ID, "検索・全年齢・成人素材"},
		{bodylessR18.ID, "検索・無認領・成人"},
	} {
		var row struct {
			Site             *string `gorm:"column:site"`
			ProductWorkID    *int64  `gorm:"column:product_work_id"`
			ClaimState       *int16  `gorm:"column:claim_state"`
			ContentRating    int16   `gorm:"column:content_rating"`
			WikiContentLimit string  `gorm:"column:wiki_content_limit"`
		}
		if err := testDB.Raw(`
			SELECT w.site, w.product_work_id, w.claim_state, w.content_rating,
			       coalesce(g.content_limit, '') AS wiki_content_limit
			FROM catalog_work w
			LEFT JOIN galgame g ON w.site = 'galgame_wiki' AND g.id = w.product_work_id
			WHERE w.id = ?`, w.id).Scan(&row).Error; err != nil {
			t.Fatalf("read claim columns: %v", err)
		}
		docs = append(docs, catsearch.WorkDocInput{
			ID: w.id, DisplayName: w.name, OLang: "ja",
			ContentRating: row.ContentRating,
			Claimed:       row.Site != nil && *row.Site != "",
			ClaimState:    model.ClaimStateKey(row.Site, row.ProductWorkID, row.ClaimState),
			ContentLimit:  model.DisplayLimitKey(row.Site, row.ProductWorkID, row.WikiContentLimit, row.ContentRating),
			UpdatedTS:     1700000000,
		})
	}
	indexWorks(t, idx, docs)

	for _, tc := range []struct {
		limits []string
		want   []int64
	}{
		{nil, []int64{safeR18.ID, spicySFW.ID, bodylessR18.ID}},
		{[]string{model.DisplayLimitKeySFW}, []int64{safeR18.ID}},
		{[]string{model.DisplayLimitKeyNSFW}, []int64{spicySFW.ID, bodylessR18.ID}},
		{[]string{model.DisplayLimitKeySFW, model.DisplayLimitKeyNSFW}, []int64{safeR18.ID, spicySFW.ID, bodylessR18.ID}},
	} {
		data, err := svc.WorksSearch(t.Context(), WorksSearchFilter{
			Sort: "id", NSFW: true, DisplayLimits: tc.limits, Limit: 50,
		})
		if err != nil {
			t.Fatalf("content_limit=%v: WorksSearch: %v", tc.limits, err)
		}
		got := make(map[int64]bool, len(data.Items))
		for _, it := range data.Items {
			got[it.ID] = true
		}
		if len(got) != len(tc.want) {
			t.Fatalf("content_limit=%v: got %d items %v, want %d", tc.limits, len(got), got, len(tc.want))
		}
		for _, id := range tc.want {
			if !got[id] {
				t.Fatalf("content_limit=%v: work %d missing from %v", tc.limits, id, got)
			}
		}
		// ONE DOOR: total is counted over the same expression as the page.
		if int(data.Total) != len(data.Items) {
			t.Fatalf("content_limit=%v: total=%d but page carries %d rows — total and items must share one filter",
				tc.limits, data.Total, len(data.Items))
		}
	}
}
