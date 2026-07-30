// public_works_search_claimstate_test.go — A2-R1 区 C: the works search
// claim_state gate (refs/proj/136).
//
// The incident this closes: kungal and moyu searches returned UNPUBLISHED
// (draft) and unclaimed registry rows, because works/search offered no
// claim-state filter at all — `claimed` cannot tell a LIVE claim from a DRAFT
// or a WITHDRAWN one, so there was no server-side gate to pass. The cases below
// pin the gate at both layers: the compiled Meilisearch expression (so it rides
// the SAME single filter total / facets / items share) and a real index, where
// claim_state=live must exclude every other state exactly.
package service

import (
	"strings"
	"testing"

	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
)

// TestClaimStateProjectionIsOneDefinition pins the projection the read face and
// the index MUST share. The NULL-column case is the load-bearing one: a claimed
// row no projector has stamped reads `live` on the record, so it must index as
// live too — otherwise claim_state=live would hide works whose own detail page
// says they are live.
func TestClaimStateProjectionIsOneDefinition(t *testing.T) {
	site, wiki, pwid := "galgame_wiki", "galgame_wiki", int64(42)
	draft, hidden, live, bogus := model.ClaimStateDraft, model.ClaimStateHidden, model.ClaimStateLive, int16(99)
	empty := ""

	for _, tc := range []struct {
		name  string
		site  *string
		pwid  *int64
		state *int16
		want  string
	}{
		{"unclaimed (site NULL)", nil, nil, nil, model.ClaimStateKeyNone},
		{"unclaimed (site empty)", &empty, &pwid, nil, model.ClaimStateKeyNone},
		{"site without a product work id", &site, nil, nil, model.ClaimStateKeyNone},
		{"claimed, column NULL", &wiki, &pwid, nil, model.ClaimStateKeyLive},
		{"claimed live", &wiki, &pwid, &live, model.ClaimStateKeyLive},
		{"claimed draft", &wiki, &pwid, &draft, model.ClaimStateKeyDraft},
		{"claimed hidden", &wiki, &pwid, &hidden, model.ClaimStateKeyHidden},
		{"unknown state is conservatively hidden", &wiki, &pwid, &bogus, model.ClaimStateKeyHidden},
	} {
		if got := model.ClaimStateKey(tc.site, tc.pwid, tc.state); got != tc.want {
			t.Fatalf("%s: ClaimStateKey = %q, want %q", tc.name, got, tc.want)
		}
		// And the read face's claimed_by must agree with it, always. The display
		// axis is held constant here (A2-R5 covers it in its own suite) — the
		// point of this case is that the STATE projection has one definition.
		cb := claimedBy(tc.site, tc.pwid, tc.state, "", model.ContentRatingAllAges)
		if tc.want == model.ClaimStateKeyNone {
			if cb != nil {
				t.Fatalf("%s: claimed_by = %+v, want null", tc.name, cb)
			}
			continue
		}
		if cb == nil || cb.State != tc.want {
			t.Fatalf("%s: claimed_by = %+v, want state %q", tc.name, cb, tc.want)
		}
	}
}

// TestClaimStateFilterCompilation pins the gate inside the ONE expression:
// absent = no clause at all (every pre-existing caller byte-identical), one
// value = one equality, several = a parenthesized OR group that still ANDs with
// the rest.
func TestClaimStateFilterCompilation(t *testing.T) {
	if got := (WorksSearchFilter{}).meiliFilter(""); strings.Contains(got, "claim_state") {
		t.Fatalf("no claim_state param must emit no clause: %q", got)
	}

	one := WorksSearchFilter{ClaimStates: []string{model.ClaimStateKeyLive}}.meiliFilter("")
	if !strings.Contains(one, "(claim_state = 'live')") {
		t.Fatalf("single claim_state clause = %q", one)
	}
	// It rides the same expression as the sfw gate — one door, not two.
	if !strings.Contains(one, "content_rating != 2") {
		t.Fatalf("claim_state must not replace the other clauses: %q", one)
	}

	many := WorksSearchFilter{
		ClaimStates: []string{model.ClaimStateKeyLive, model.ClaimStateKeyDraft},
	}.meiliFilter("")
	if !strings.Contains(many, "(claim_state = 'live' OR claim_state = 'draft')") {
		t.Fatalf("multi claim_state clause = %q", many)
	}
}

// TestClaimStateVocabularyIsClosed pins the token set the handler 400s against.
func TestClaimStateVocabularyIsClosed(t *testing.T) {
	for _, ok := range []string{"none", "live", "draft", "hidden"} {
		if !IsWorksSearchClaimState(ok) {
			t.Fatalf("%q must be a legal claim_state token", ok)
		}
	}
	for _, bad := range []string{"", "liev", "LIVE", "published", "true", "claimed"} {
		if IsWorksSearchClaimState(bad) {
			t.Fatalf("%q must NOT be a legal claim_state token", bad)
		}
	}
}

// TestWorksSearchClaimStateGate is the end-to-end case: four works, one per
// state, indexed through the production projection — and every claim_state
// query returns exactly its own states, with total agreeing with the page.
func TestWorksSearchClaimStateGate(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	idx := worksSearchIndexer(t)
	svc := newPublicSvc().WithWorksSearch(idx)

	bodyless := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "無認領作品")
	live := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "公開作品")
	draft := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "下書き作品")
	hidden := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "取下げ作品")
	for i, w := range []int64{live.ID, draft.ID, hidden.ID} {
		claimWork(t, w, "galgame_wiki", int64(7100+i))
	}
	// live stays UNSTAMPED on purpose — a claimed row with a NULL column is the
	// zero-regression "live", and the gate must let it through.
	setClaimState(t, draft.ID, i16(model.ClaimStateDraft))
	setClaimState(t, hidden.ID, i16(model.ClaimStateHidden))

	// Index through the very projection the reindexer uses, off the very columns
	// it reads — a look-alike here would prove nothing.
	docs := make([]catsearch.WorkDocInput, 0, 4)
	for _, w := range []struct {
		id   int64
		name string
	}{
		{bodyless.ID, "無認領作品"}, {live.ID, "公開作品"},
		{draft.ID, "下書き作品"}, {hidden.ID, "取下げ作品"},
	} {
		var row struct {
			Site          *string `gorm:"column:site"`
			ProductWorkID *int64  `gorm:"column:product_work_id"`
			ClaimState    *int16  `gorm:"column:claim_state"`
		}
		if err := testDB.Raw(
			`SELECT site, product_work_id, claim_state FROM catalog_work WHERE id = ?`, w.id).
			Scan(&row).Error; err != nil {
			t.Fatalf("read claim columns: %v", err)
		}
		docs = append(docs, catsearch.WorkDocInput{
			ID: w.id, DisplayName: w.name, OLang: "ja",
			ContentRating: model.ContentRatingAllAges,
			Claimed:       row.Site != nil && *row.Site != "",
			ClaimState:    model.ClaimStateKey(row.Site, row.ProductWorkID, row.ClaimState),
			UpdatedTS:     1700000000,
		})
	}
	indexWorks(t, idx, docs)

	for _, tc := range []struct {
		states []string
		want   []int64
	}{
		{nil, []int64{bodyless.ID, live.ID, draft.ID, hidden.ID}},
		{[]string{model.ClaimStateKeyLive}, []int64{live.ID}},
		{[]string{model.ClaimStateKeyLive, model.ClaimStateKeyDraft}, []int64{live.ID, draft.ID}},
		{[]string{model.ClaimStateKeyNone}, []int64{bodyless.ID}},
		{[]string{model.ClaimStateKeyHidden}, []int64{hidden.ID}},
	} {
		data, err := svc.WorksSearch(t.Context(), WorksSearchFilter{
			Sort: "id", ClaimStates: tc.states, Limit: 50,
		})
		if err != nil {
			t.Fatalf("claim_state=%v: WorksSearch: %v", tc.states, err)
		}
		got := make(map[int64]bool, len(data.Items))
		for _, it := range data.Items {
			got[it.ID] = true
		}
		if len(got) != len(tc.want) {
			t.Fatalf("claim_state=%v: got %d items, want %d", tc.states, len(got), len(tc.want))
		}
		for _, id := range tc.want {
			if !got[id] {
				t.Fatalf("claim_state=%v: work %d missing from %v", tc.states, id, got)
			}
		}
		// ONE DOOR: total is counted over the same expression as the page, so on
		// a single page they must be equal — the deprecated face's trap was
		// exactly this pair disagreeing.
		if int(data.Total) != len(data.Items) {
			t.Fatalf("claim_state=%v: total=%d but page carries %d rows — total and items must share one filter",
				tc.states, data.Total, len(data.Items))
		}
	}
}
