package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

func createSeries(t *testing.T, sourceKey, externalID, name string) int64 {
	t.Helper()
	var srcID int16
	if err := testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKey).Scan(&srcID).Error; err != nil || srcID == 0 {
		t.Fatalf("source %q: id=%d err=%v", sourceKey, srcID, err)
	}
	s := &model.CatalogSeries{DisplayName: name, SourceID: srcID, ExternalID: externalID}
	if err := testDB.Create(s).Error; err != nil {
		t.Fatalf("create series: %v", err)
	}
	return s.ID
}

func addSeriesMember(t *testing.T, seriesID, workID int64, position, kind int16) {
	t.Helper()
	m := &model.CatalogSeriesMember{SeriesID: seriesID, WorkID: workID, Position: position, Kind: kind}
	if err := testDB.Create(m).Error; err != nil {
		t.Fatalf("create series member: %v", err)
	}
}

func TestSeriesDetailOrdersByPositionAndPublishesMembers(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	first := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Line A")
	second := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Line A 2")
	fd := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Line A FD")
	unordered := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Line A ?")

	sid := createSeries(t, "dlsite", "SRI-184", "Line A")
	addSeriesMember(t, sid, unordered.ID, 0, model.SeriesMemberKindUnknown)
	addSeriesMember(t, sid, fd.ID, 3, model.SeriesMemberKindFandisc)
	addSeriesMember(t, sid, first.ID, 1, model.SeriesMemberKindMain)
	addSeriesMember(t, sid, second.ID, 2, model.SeriesMemberKindMain)

	rec, found, err := svc.SeriesDetail(ctx, sid, true, true, 50, 0)
	if err != nil || !found {
		t.Fatalf("SeriesDetail: found=%v err=%v", found, err)
	}
	wantIDs := []int64{first.ID, second.ID, fd.ID, unordered.ID}
	if len(rec.Works) != len(wantIDs) {
		t.Fatalf("works = %d, want %d", len(rec.Works), len(wantIDs))
	}
	for i, want := range wantIDs {
		if rec.Works[i].ID != want {
			t.Fatalf("works[%d].id = %d, want %d", i, rec.Works[i].ID, want)
		}
	}
	if len(rec.Members) != len(rec.Works) {
		t.Fatalf("members = %d, want parallel to works (%d)", len(rec.Members), len(rec.Works))
	}
	wantKinds := []string{"main", "main", "fandisc", "unknown"}
	wantPos := []int16{1, 2, 3, 0}
	for i := range rec.Members {
		if rec.Members[i].WorkID != rec.Works[i].ID {
			t.Fatalf("members[%d].work_id = %d, works[%d].id = %d — not parallel",
				i, rec.Members[i].WorkID, i, rec.Works[i].ID)
		}
		if rec.Members[i].Kind != wantKinds[i] || rec.Members[i].Position != wantPos[i] {
			t.Fatalf("members[%d] = {%d,%q}, want {%d,%q}",
				i, rec.Members[i].Position, rec.Members[i].Kind, wantPos[i], wantKinds[i])
		}
	}
}

func TestSeriesDetailMembersDropWithR18Briefs(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	safe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Safe")
	adult := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "Adult")
	sid := createSeries(t, "dlsite", "SRI-184b", "Mixed")
	addSeriesMember(t, sid, adult.ID, 1, model.SeriesMemberKindMain)
	addSeriesMember(t, sid, safe.ID, 2, model.SeriesMemberKindMain)

	rec, found, err := svc.SeriesDetail(ctx, sid, true, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("SeriesDetail: found=%v err=%v", found, err)
	}
	if len(rec.Works) != 1 || rec.Works[0].ID != safe.ID {
		t.Fatalf("sfw works = %+v, want only the safe work", rec.Works)
	}
	if len(rec.Members) != 1 || rec.Members[0].WorkID != safe.ID || rec.Members[0].Position != 2 {
		t.Fatalf("sfw members = %+v, want the safe membership only", rec.Members)
	}
}
