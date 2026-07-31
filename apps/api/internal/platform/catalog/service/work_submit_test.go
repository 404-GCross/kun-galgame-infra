package service

import (
	"errors"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
)

// The submission mint (wave 162). What is worth pinning is not that the row
// appears — it is that ONE transaction produced the row, its content, its
// curated release, its registry revision and its birth event, and that a
// refusal anywhere leaves none of them.

// submitSite is the post-re-site tenant key; TestSubmitWorkOnTheFormerMirrorSite
// covers the legacy `galgame_wiki` spelling separately.
const submitSite = "kungal"

// submitFields carries an identity link on purpose: it is both content AND, on
// the registry-issued path, the idempotency key. submitFieldsNoAnchor is the
// variant that has neither.
func submitFields(name string) map[string]any {
	return map[string]any{
		editspec.FieldWorkDisplayName:   name,
		editspec.FieldWorkOLang:         "ja",
		editspec.FieldWorkContentRating: float64(model.ContentRatingR18),
		editspec.FieldWorkTitles: []any{
			map[string]any{"lang": "ja", "title": name, "kind": float64(0)},
			map[string]any{"lang": "zh-Hans", "title": "投稿作品", "kind": float64(1)},
		},
		editspec.FieldWorkIntros: []any{
			map[string]any{"lang": "zh-Hans", "intro": "投稿者が書いた紹介。"},
		},
		editspec.FieldWorkLinks: []any{"https://vndb.org/v19658"},
	}
}

// submitFieldsNoAnchor is a submission of a game nothing upstream knows yet —
// the case with no natural idempotency key at all.
func submitFieldsNoAnchor(name string) map[string]any {
	f := submitFields(name)
	delete(f, editspec.FieldWorkLinks)
	return f
}

func TestSubmitWorkMintsPendingClaim(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()

	res, err := s.SubmitWork(ctx, SubmitWorkParams{
		Site: submitSite, ProductWorkID: 90001, ActorUID: 7,
		Fields:   submitFields("新作ゲーム"),
		Released: ReleaseDate{Y: 2019, M: 5},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.ClaimState != model.ClaimStateKeyPending || res.WorkID == 0 || res.EventID == 0 || res.ReleaseID == 0 {
		t.Fatalf("result: %+v", res)
	}
	if res.ProductWorkID != 90001 {
		t.Fatalf("a supplied product id must be echoed verbatim: %+v", res)
	}

	var work model.CatalogWork
	if err := testDB.First(&work, res.WorkID).Error; err != nil {
		t.Fatal(err)
	}
	if work.MediumID != galgameMediumID || work.Site == nil || *work.Site != submitSite ||
		work.ProductWorkID == nil || *work.ProductWorkID != 90001 {
		t.Fatalf("claim identity: %+v", work)
	}
	if work.ClaimState == nil || *work.ClaimState != model.ClaimStatePending {
		t.Fatalf("claim_state: %v", work.ClaimState)
	}
	// The registry row's own status is live — that axis says "this identity is
	// real", while pending says "the submission is unreviewed". Conflating them
	// is what the wiki's single status column did.
	if work.Status != model.WorkStatusLive {
		t.Fatalf("registry status: %d", work.Status)
	}
	// The scalar payload landed through the field table, not a second writer.
	if work.DisplayName != "新作ゲーム" || work.OLang != "ja" || work.ContentRating != model.ContentRatingR18 {
		t.Fatalf("scalars: %+v", work)
	}

	var titles int64
	testDB.Raw(`SELECT count(*) FROM catalog_work_title WHERE work_id = ?`, res.WorkID).Scan(&titles)
	if titles != 2 {
		t.Fatalf("titles: %d", titles)
	}
	var intros int64
	testDB.Raw(`SELECT count(*) FROM catalog_work_intro WHERE work_id = ? AND lang = 'zh-Hans'`, res.WorkID).Scan(&intros)
	if intros != 1 {
		t.Fatalf("intros: %d", intros)
	}
	// A submitted identity URL is a CANDIDATE, never an anchor: a mistyped id
	// must not hijack another work on the spot (work_links.go's two grades).
	var linkKind int16
	if err := testDB.Raw(`SELECT link_kind FROM catalog_external_ref
	                      WHERE entity_id = ? AND entity_type = ? AND matched_by = 'curated'`,
		res.WorkID, model.EntityTypeWork).Scan(&linkKind).Error; err != nil {
		t.Fatal(err)
	}
	if linkKind != model.LinkKindProbable {
		t.Fatalf("submitted vndb link must be a candidate, got link_kind %d", linkKind)
	}

	// The date is a curated release row with a nullable tail for precision —
	// month known, day not.
	var rel model.CatalogRelease
	if err := testDB.First(&rel, res.ReleaseID).Error; err != nil {
		t.Fatal(err)
	}
	if rel.WorkID != res.WorkID || rel.ReleasedY == nil || *rel.ReleasedY != 2019 ||
		rel.ReleasedM == nil || *rel.ReleasedM != 5 || rel.ReleasedD != nil {
		t.Fatalf("release: %+v", rel)
	}

	// The registry's machine snapshot log records the birth, same as ClaimWork.
	var revisions int64
	testDB.Raw(`SELECT count(*) FROM catalog_revision WHERE entity_type = ? AND entity_id = ? AND action = ?`,
		model.EntityTypeWork, res.WorkID, model.RevisionActionCreated).Scan(&revisions)
	if revisions != 1 {
		t.Fatalf("registry revisions: %d", revisions)
	}

	// And exactly one claim event: the birth, with a NULL from_state, landing
	// straight on pending.
	events, err := s.EventsSince(ctx, 0, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events: %+v", events)
	}
	e := events[0]
	if e.FromState != nil || e.ToState != model.ClaimStateKeyPending ||
		e.WorkID != res.WorkID || e.ActorUID != 7 || e.Site != submitSite {
		t.Fatalf("birth event: %+v", e)
	}
}

// TestSubmitWorkIsIdempotent: a retrying wizard must never mint a second
// identity for the same product id (the 147/148 duplicate-label shape).
func TestSubmitWorkIsIdempotent(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()
	params := SubmitWorkParams{
		Site: submitSite, ProductWorkID: 90002, ActorUID: 7, Fields: submitFields("二重投稿"),
	}
	first, err := s.SubmitWork(ctx, params)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	_, err = s.SubmitWork(ctx, params)
	var exists *ClaimExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("second submit: %v", err)
	}
	if exists.WorkID != first.WorkID || exists.CurrentState != model.ClaimStateKeyPending {
		t.Fatalf("conflict echo: %+v", exists)
	}
	// Approving it and submitting again still conflicts — and now echoes the
	// state the wizard should render instead of a retry.
	act(t, s, first.WorkID, ClaimActionApprove, ClaimActionParams{ActorUID: 99})
	_, err = s.SubmitWork(ctx, params)
	if !errors.As(err, &exists) || exists.CurrentState != model.ClaimStateKeyLive {
		t.Fatalf("post-approval conflict: %v / %+v", err, exists)
	}
	var works int64
	testDB.Raw(`SELECT count(*) FROM catalog_work WHERE site = ? AND product_work_id = ?`,
		submitSite, 90002).Scan(&works)
	if works != 1 {
		t.Fatalf("mint count: %d", works)
	}
}

// TestSubmitWorkIssuesTheIdentity is the charter §6.P4-verdict 1 path: a wizard
// that has nothing to name the work by omits product_work_id and the registry
// issues one. The claim must be complete the moment the row exists — a work
// with a site and no product id projects as unclaimed, which is a state no
// reader has a name for.
func TestSubmitWorkIssuesTheIdentity(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()

	res, err := s.SubmitWork(ctx, SubmitWorkParams{
		Site: submitSite, ActorUID: 7, Fields: submitFieldsNoAnchor("番号なし投稿"),
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.ProductWorkID != res.WorkID {
		t.Fatalf("the claim must adopt the minted work id: %+v", res)
	}
	if res.ClaimState != model.ClaimStateKeyPending {
		t.Fatalf("result: %+v", res)
	}

	var work model.CatalogWork
	if err := testDB.First(&work, res.WorkID).Error; err != nil {
		t.Fatal(err)
	}
	if work.ProductWorkID == nil || *work.ProductWorkID != res.WorkID {
		t.Fatalf("stored claim: %+v", work.ProductWorkID)
	}
	// The row projects as a CLAIMED, pending work — claimed_by is (site,
	// product_work_id) together, so this is the assertion that the adoption
	// actually took effect on the contract and not just on a column.
	if got := model.ClaimStateKey(work.Site, work.ProductWorkID, work.ClaimState); got != model.ClaimStateKeyPending {
		t.Fatalf("projection: %s", got)
	}

	// The birth event exists exactly as on the supplied-id path.
	events, err := s.EventsSince(ctx, 0, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].FromState != nil ||
		events[0].ToState != model.ClaimStateKeyPending || events[0].WorkID != res.WorkID {
		t.Fatalf("birth event: %+v", events)
	}
	// And the feed's identity snapshot carries the issued id, so a consumer
	// routes to the product row it is about to create.
	if events[0].ProductWorkID == nil || *events[0].ProductWorkID != res.WorkID {
		t.Fatalf("feed snapshot: %+v", events[0])
	}
	// It reaches the review queue like any other submission.
	items, total, err := s.PendingClaims(ctx, submitSite, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].WorkID != res.WorkID {
		t.Fatalf("queue: total=%d items=%+v", total, items)
	}
}

// TestSubmitWorkIssuedIdempotency pins the two halves of the idempotency
// answer on the registry-issued path: an identity anchor in the payload IS the
// key, and its absence is an honest gap rather than a silent one.
func TestSubmitWorkIssuedIdempotency(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()

	anchored := SubmitWorkParams{Site: submitSite, ActorUID: 7, Fields: submitFields("锚あり")}
	first, err := s.SubmitWork(ctx, anchored)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// A retry carries the same VNDB link and is recognized by it.
	_, err = s.SubmitWork(ctx, anchored)
	var exists *ClaimExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("anchored retry: %v", err)
	}
	if exists.WorkID != first.WorkID || exists.MatchedBy != ClaimMatchAnchor ||
		exists.CurrentState != model.ClaimStateKeyPending || exists.Anchor != "vndb:v19658" {
		t.Fatalf("conflict echo: %+v", exists)
	}
	if exists.ProductWorkID != first.WorkID {
		t.Fatalf("the conflict must carry the issued product id: %+v", exists)
	}

	// A DIFFERENT user submitting the same game for the same site hits the same
	// rule — which is the answer that page wants anyway (point them at the
	// entry that exists instead of minting a rival identity for it).
	other := anchored
	other.ActorUID = 8
	if _, err := s.SubmitWork(ctx, other); !errors.As(err, &exists) {
		t.Fatalf("second submitter: %v", err)
	}

	// The anchor is scoped to the caller's own tenant: another site claiming the
	// same VNDB id is not a duplicate, the registry is multi-tenant.
	foreign := anchored
	foreign.Site = "moyu"
	if _, err := s.SubmitWork(ctx, foreign); err != nil {
		t.Fatalf("another tenant must still be able to submit: %v", err)
	}

	// A supplied product id keeps the exact key and is NOT diverted by the
	// anchor — behaviour unchanged for the stub flows.
	supplied := anchored
	supplied.ProductWorkID = 90777
	if _, err := s.SubmitWork(ctx, supplied); err != nil {
		t.Fatalf("a supplied id must not be diverted by the anchor: %v", err)
	}

	// And the documented gap: no id AND no identity anchor = no key to match
	// on, so a retry mints a second work. Pinned so the contract cannot change
	// by accident — if this ever starts failing, the endpoint gained an
	// idempotency key and its documentation must say so.
	bare := SubmitWorkParams{Site: submitSite, ActorUID: 7, Fields: submitFieldsNoAnchor("锚なし")}
	a, err := s.SubmitWork(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SubmitWork(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	if a.WorkID == b.WorkID {
		t.Fatal("unexpectedly idempotent — update the endpoint documentation")
	}
}

// TestSubmitWorkRefusesMirroredFacets: the mint obeys the SAME mirror gate the
// editing face does — a work claimed for the site the duty chain owns cannot
// have its mirrored facets written, and the whole mint rolls back rather than
// leaving a half-filled row behind.
func TestSubmitWorkRefusesMirroredFacets(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()
	fields := submitFields("鏡面作品")

	_, err := s.SubmitWork(ctx, SubmitWorkParams{
		Site: "galgame_wiki", ProductWorkID: 90003, ActorUID: 7, Fields: fields,
	})
	var gate *editspec.MirrorGateError
	if !errors.As(err, &gate) {
		t.Fatalf("mirrored submit: %v", err)
	}
	var works int64
	testDB.Raw(`SELECT count(*) FROM catalog_work WHERE product_work_id = ?`, 90003).Scan(&works)
	if works != 0 {
		t.Fatalf("refused mint left %d rows behind", works)
	}
	var events int64
	testDB.Raw(`SELECT count(*) FROM catalog_claim_event`).Scan(&events)
	if events != 0 {
		t.Fatalf("refused mint left %d events behind", events)
	}

	// The open half of the same population still submits: drop the gated facets
	// and the mint goes through on the mirrored site too.
	delete(fields, editspec.FieldWorkTitles)
	res, err := s.SubmitWork(ctx, SubmitWorkParams{
		Site: "galgame_wiki", ProductWorkID: 90003, ActorUID: 7, Fields: fields,
	})
	if err != nil {
		t.Fatalf("ungated submit: %v", err)
	}
	if res.ClaimState != model.ClaimStateKeyPending {
		t.Fatalf("result: %+v", res)
	}
}

// TestSubmitWorkRejectsPayloads pins the closed payload vocabulary and the
// validation the field table already owns.
func TestSubmitWorkRejectsPayloads(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()
	base := func() SubmitWorkParams {
		return SubmitWorkParams{Site: submitSite, ProductWorkID: 90004, ActorUID: 7, Fields: submitFields("拒否")}
	}

	cases := []struct {
		name    string
		mutate  func(*SubmitWorkParams)
		wantErr error
	}{
		{"no site", func(p *SubmitWorkParams) { p.Site = "" }, ErrSubmitTargetRequired},
		// A missing product id is now LEGAL (the registry issues one); a
		// negative one is still a malformed request.
		{"negative product id", func(p *SubmitWorkParams) { p.ProductWorkID = -1 }, ErrSubmitTargetRequired},
		{"no display name", func(p *SubmitWorkParams) {
			delete(p.Fields, editspec.FieldWorkDisplayName)
		}, ErrSubmitDisplayNameRequired},
		{"day without month", func(p *SubmitWorkParams) {
			p.Released = ReleaseDate{Y: 2019, D: 4}
		}, ErrSubmitInvalidDate},
		{"impossible month", func(p *SubmitWorkParams) {
			p.Released = ReleaseDate{Y: 2019, M: 13}
		}, ErrSubmitInvalidDate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			tc.mutate(&p)
			if _, err := s.SubmitWork(ctx, p); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v want %v", err, tc.wantErr)
			}
		})
	}

	// Covers are in the matrix but NOT submittable: the bytes have to exist
	// before a facet can reference them.
	t.Run("covers are not submittable", func(t *testing.T) {
		p := base()
		p.Fields[editspec.FieldWorkCovers] = []any{}
		var fieldErr *editspec.SubmissionFieldError
		if _, err := s.SubmitWork(ctx, p); !errors.As(err, &fieldErr) ||
			fieldErr.Field != editspec.FieldWorkCovers {
			t.Fatalf("covers: %v", err)
		}
	})
	t.Run("unregistered key", func(t *testing.T) {
		p := base()
		p.Fields["catalog.work.status"] = 1
		var fieldErr *editspec.SubmissionFieldError
		if _, err := s.SubmitWork(ctx, p); !errors.As(err, &fieldErr) {
			t.Fatalf("status: %v", err)
		}
	})
	t.Run("field validator still runs", func(t *testing.T) {
		p := base()
		p.Fields[editspec.FieldWorkOLang] = "klingon"
		var fieldErr *editspec.SubmissionFieldError
		if _, err := s.SubmitWork(ctx, p); !errors.As(err, &fieldErr) ||
			fieldErr.Field != editspec.FieldWorkOLang || fieldErr.Unwrap() == nil {
			t.Fatalf("olang: %v", err)
		}
	})

	// None of the refusals minted anything.
	var works int64
	testDB.Raw(`SELECT count(*) FROM catalog_work WHERE product_work_id = ?`, 90004).Scan(&works)
	if works != 0 {
		t.Fatalf("refusals minted %d rows", works)
	}
}

// TestSubmitWorkFeedsTheReviewQueue closes the loop the endpoint exists for:
// a submission is immediately visible to the staff queue and can be approved
// through the ordinary state machine, with no special case for "born pending".
func TestSubmitWorkFeedsTheReviewQueue(t *testing.T) {
	s := newLifecycle(t)
	ctx := t.Context()
	res, err := s.SubmitWork(ctx, SubmitWorkParams{
		Site: submitSite, ProductWorkID: 90005, ActorUID: 7, Fields: submitFields("審査待ち"),
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	items, total, err := s.PendingClaims(ctx, submitSite, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].WorkID != res.WorkID {
		t.Fatalf("queue: total=%d items=%+v", total, items)
	}
	if items[0].SubmittedEventID == nil || *items[0].SubmittedEventID != res.EventID {
		t.Fatalf("queue must point at the birth event: %+v", items[0])
	}
	approved := act(t, s, res.WorkID, ClaimActionApprove, ClaimActionParams{ActorUID: 99})
	if approved.From == nil || *approved.From != model.ClaimStateKeyPending ||
		approved.To != model.ClaimStateKeyLive {
		t.Fatalf("approve: %+v", approved)
	}
}
