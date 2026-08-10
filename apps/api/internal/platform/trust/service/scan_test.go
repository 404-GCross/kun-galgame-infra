package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"api/internal/platform/trust/model"
)

func getScan(t *testing.T, id int64) *model.TrustScanResult {
	t.Helper()
	var r model.TrustScanResult
	if err := testDB.Take(&r, id).Error; err != nil {
		t.Fatalf("reload scan %d: %v", id, err)
	}
	return &r
}

func TestScanIngestPersistsPending(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB, nil)

	author := int64(42)
	res, err := svc.Ingest(context.Background(), ScanParams{
		Site: tSite, SubjectKind: tKind, SubjectID: tSubj, Text: "hello world", AuthorID: &author,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.ScanID == 0 {
		t.Fatal("expected a scan id")
	}
	if res.Truncated {
		t.Fatal("short text must not be truncated")
	}
	row := getScan(t, res.ScanID)
	if row.Status != model.ScanStatusPending {
		t.Fatalf("status = %d, want pending(%d)", row.Status, model.ScanStatusPending)
	}
	if row.Mode != model.ScanModeShadow {
		t.Fatalf("mode = %d, want shadow(%d)", row.Mode, model.ScanModeShadow)
	}
	if row.ContentText != "hello world" {
		t.Fatalf("content_text = %q, want %q", row.ContentText, "hello world")
	}
	if row.AuthorID == nil || *row.AuthorID != author {
		t.Fatalf("author_id = %v, want %d", row.AuthorID, author)
	}
	if row.Flagged != nil || row.Score != nil || row.ScoredAt != nil || row.Channel != "" {
		t.Fatalf("intake must leave verdict fields empty: flagged=%v score=%v scored_at=%v channel=%q",
			row.Flagged, row.Score, row.ScoredAt, row.Channel)
	}
	if len(row.Categories) != 0 {
		t.Fatalf("categories must be NULL at intake, got %s", row.Categories)
	}
}

func TestScanIngestRegistryFailLoud(t *testing.T) {
	cleanTables(t)
	svc := NewScanService(testDB, nil)
	_, err := svc.Ingest(context.Background(), ScanParams{
		Site: tSite, SubjectKind: "unregistered_kind", SubjectID: tSubj, Text: "x",
	})
	if !errors.Is(err, ErrSubjectKindNotRegistered) {
		t.Fatalf("err = %v, want ErrSubjectKindNotRegistered", err)
	}
	var n int64
	testDB.Model(&model.TrustScanResult{}).Count(&n)
	if n != 0 {
		t.Fatalf("a rejected scan must write no row, got %d", n)
	}
}

func TestScanIngestDeprecatedKindFailLoud(t *testing.T) {
	cleanTables(t)
	kind := model.TrustSubjectKind{Site: tSite, Key: tKind, IsDeprecated: true}
	if err := testDB.Create(&kind).Error; err != nil {
		t.Fatalf("register deprecated kind: %v", err)
	}
	svc := NewScanService(testDB, nil)
	_, err := svc.Ingest(context.Background(), ScanParams{
		Site: tSite, SubjectKind: tKind, SubjectID: tSubj, Text: "x",
	})
	if !errors.Is(err, ErrSubjectKindNotRegistered) {
		t.Fatalf("deprecated kind: err = %v, want ErrSubjectKindNotRegistered", err)
	}
}

func TestScanIngestTruncation(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB, nil)

	long := strings.Repeat("あ", maxScanTextRunes+500)
	res, err := svc.Ingest(context.Background(), ScanParams{
		Site: tSite, SubjectKind: tKind, SubjectID: "long", Text: long,
	})
	if err != nil {
		t.Fatalf("ingest long: %v", err)
	}
	if !res.Truncated {
		t.Fatal("over-cap text must report truncated=true")
	}
	row := getScan(t, res.ScanID)
	if got := len([]rune(row.ContentText)); got != maxScanTextRunes {
		t.Fatalf("stored rune count = %d, want %d (rune-safe cap)", got, maxScanTextRunes)
	}
}

func TestScanRepeatableNotDeduped(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB, nil)

	first, _ := svc.Ingest(context.Background(), ScanParams{Site: tSite, SubjectKind: tKind, SubjectID: tSubj, Text: "v1"})
	second, _ := svc.Ingest(context.Background(), ScanParams{Site: tSite, SubjectKind: tKind, SubjectID: tSubj, Text: "v2"})
	if first.ScanID == second.ScanID {
		t.Fatal("re-scan of the same subject must open a new row, not dedup")
	}
	var n int64
	testDB.Model(&model.TrustScanResult{}).Where("site = ? AND subject_kind = ? AND subject_id = ?", tSite, tKind, tSubj).Count(&n)
	if n != 2 {
		t.Fatalf("expected 2 scan rows for the subject, got %d", n)
	}
}

func TestScanSiteBoundWhenNoWire(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB, allowFwd())

	res, err := svc.Ingest(context.Background(), ScanParams{
		CallerClientID: "not-a-forwarder", Site: tSite, WireSite: "",
		SubjectKind: tKind, SubjectID: tSubj, Text: "hello",
	})
	if err != nil {
		t.Fatalf("bound-site ingest: %v", err)
	}
	if row := getScan(t, res.ScanID); row.Site != tSite {
		t.Fatalf("row landed under site %q, want bound %q", row.Site, tSite)
	}
}

func TestScanWireSiteRejectedForNonForwarder(t *testing.T) {
	cleanTables(t)
	registerKind(t, "letmoe", tKind, nil, nil)
	svc := NewScanService(testDB, allowFwd())

	_, err := svc.Ingest(context.Background(), ScanParams{
		CallerClientID: "stranger", Site: tSite, WireSite: "letmoe",
		SubjectKind: tKind, SubjectID: tSubj, Text: "hello",
	})
	if !errors.Is(err, ErrForwarderNotAllowed) {
		t.Fatalf("wire site by stranger: err = %v, want ErrForwarderNotAllowed", err)
	}
	var count int64
	testDB.Model(&model.TrustScanResult{}).Count(&count)
	if count != 0 {
		t.Fatalf("a rejected relay scan must write no row, got %d", count)
	}
}

func TestScanWireSiteAcceptedForForwarder(t *testing.T) {
	cleanTables(t)
	const wireSite = "letmoe"
	registerKind(t, wireSite, tKind, nil, nil)
	svc := NewScanService(testDB, allowFwd())

	res, err := svc.Ingest(context.Background(), ScanParams{
		CallerClientID: fwdClient, Site: "ignored-bound", WireSite: wireSite,
		SubjectKind: tKind, SubjectID: tSubj, Text: "hello",
	})
	if err != nil {
		t.Fatalf("forwarder relay ingest: %v", err)
	}
	if row := getScan(t, res.ScanID); row.Site != wireSite {
		t.Fatalf("row landed under site %q, want wire %q", row.Site, wireSite)
	}
}

func TestScanWireSiteUnregisteredKind(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB, allowFwd())

	_, err := svc.Ingest(context.Background(), ScanParams{
		CallerClientID: fwdClient, Site: tSite, WireSite: "unregistered-site",
		SubjectKind: tKind, SubjectID: tSubj, Text: "hello",
	})
	if !errors.Is(err, ErrSubjectKindNotRegistered) {
		t.Fatalf("forwarder + unregistered wire site: err = %v, want ErrSubjectKindNotRegistered", err)
	}
}

func TestScanForwarderNoWireBinds(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB, allowFwd())

	res, err := svc.Ingest(context.Background(), ScanParams{
		CallerClientID: fwdClient, Site: tSite, WireSite: "",
		SubjectKind: tKind, SubjectID: tSubj, Text: "hello",
	})
	if err != nil {
		t.Fatalf("forwarder bound ingest: %v", err)
	}
	if row := getScan(t, res.ScanID); row.Site != tSite {
		t.Fatalf("row landed under site %q, want bound %q", row.Site, tSite)
	}
}
