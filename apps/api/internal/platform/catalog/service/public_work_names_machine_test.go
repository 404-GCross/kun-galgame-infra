package service

import (
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// Wave 210: a machine title fills a locale slot no source title claimed, and
// says so; a source title in the same slot beats it outright. The election is a
// first-row-wins scan, so this is really a test that nativeWorkTitles orders by
// provenance BEFORE kind — a machine official (kind 0) must still lose to a
// source alias (kind 1).
func TestPublicWorkNamesMachineFill(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()

	filled := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "こころ")
	addWorkTitle(t, filled.ID, "ja", "こころ", model.WorkTitleKindOfficial)
	addWorkTitleP(t, filled.ID, "zh-Hans", "心", model.WorkTitleKindOfficial, model.WorkTitleProvenanceMachine)

	sourced := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "そら")
	addWorkTitle(t, sourced.ID, "ja", "そら", model.WorkTitleKindOfficial)
	addWorkTitleP(t, sourced.ID, "zh-Hans", "机翻天空", model.WorkTitleKindOfficial, model.WorkTitleProvenanceMachine)
	addWorkTitleP(t, sourced.ID, "zh-Hans", "天空", model.WorkTitleKindAlias, model.WorkTitleProvenanceSource)

	page, err := svc.WorksList(t.Context(),
		WorksListFilter{Sort: "id", Include: ParseWorksListInclude("names")}, "", 50)
	if err != nil {
		t.Fatalf("WorksList include=names: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("page = %d items, want 2", len(page.Items))
	}
	got := map[int64]*dto.PublicWorkNames{}
	for _, it := range page.Items {
		if it.Names == nil {
			t.Fatalf("work %d has no names block", it.ID)
		}
		got[it.ID] = it.Names
	}

	f := got[filled.ID]
	if f.ZhCN == nil || f.ZhCN.Value != "心" || !f.ZhCN.Machine {
		t.Fatalf("names.zh-cn = %+v, want the machine row filling the empty slot, flagged", f.ZhCN)
	}
	if f.JaJP == nil || f.JaJP.Machine {
		t.Fatalf("names.ja-jp = %+v, want the source row unflagged", f.JaJP)
	}

	s := got[sourced.ID]
	if s.ZhCN == nil || s.ZhCN.Value != "天空" || s.ZhCN.Machine {
		t.Fatalf("names.zh-cn = %+v, a source alias must beat a machine official", s.ZhCN)
	}

	// omitempty on both the slot pointers and the flag: an unfilled slot is
	// absent, and a source-provenance slot carries no `machine` key at all.
	raw, err := json.Marshal(page.Items[0].Names)
	if err != nil {
		t.Fatalf("marshal names: %v", err)
	}
	if string(raw) != `{"ja-jp":{"value":"こころ"},"zh-cn":{"value":"心","machine":true}}` {
		t.Fatalf("names wire shape = %s", raw)
	}
}

// TestPublicWorkDetailTitlesCarryMachine covers the other title projection: the
// flat titles[] on the work detail face, which keeps every row rather than
// electing one per slot.
func TestPublicWorkDetailTitlesCarryMachine(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "ひかり")
	addWorkTitle(t, w.ID, "ja", "ひかり", model.WorkTitleKindOfficial)
	addWorkTitleP(t, w.ID, "zh-Hans", "光", model.WorkTitleKindOfficial, model.WorkTitleProvenanceMachine)

	rec, found, err := svc.WorkDetail(t.Context(), w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail: found=%v err=%v", found, err)
	}
	if len(rec.Titles) != 2 {
		t.Fatalf("titles = %+v, want both rows", rec.Titles)
	}
	// provenance leads the ORDER BY, so the source row comes first.
	if rec.Titles[0].Title != "ひかり" || rec.Titles[0].Machine {
		t.Fatalf("titles[0] = %+v, want the source row first and unflagged", rec.Titles[0])
	}
	if rec.Titles[1].Title != "光" || !rec.Titles[1].Machine {
		t.Fatalf("titles[1] = %+v, want the machine row flagged", rec.Titles[1])
	}
}
