package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"api/internal/platform/trust/model"
)

// getScan reloads a scan-result row.
func getScan(t *testing.T, id int64) *model.TrustScanResult {
	t.Helper()
	var r model.TrustScanResult
	if err := testDB.Take(&r, id).Error; err != nil {
		t.Fatalf("reload scan %d: %v", id, err)
	}
	return &r
}

// TestScanIngestPersistsPending: a valid scan lands status=pending / mode=shadow,
// with the verdict fields NULL and channel ” (an accept-type surface — no scoring).
func TestScanIngestPersistsPending(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB)

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
	// Verdict fields untouched at intake.
	if row.Flagged != nil || row.Score != nil || row.ScoredAt != nil || row.Channel != "" {
		t.Fatalf("intake must leave verdict fields empty: flagged=%v score=%v scored_at=%v channel=%q",
			row.Flagged, row.Score, row.ScoredAt, row.Channel)
	}
	if len(row.Categories) != 0 {
		t.Fatalf("categories must be NULL at intake, got %s", row.Categories)
	}
}

// TestScanIngestRegistryFailLoud: an unregistered (site, subject_kind) is
// rejected — the same tenant fail-loud as report intake (invariant 11). No row
// is written.
func TestScanIngestRegistryFailLoud(t *testing.T) {
	cleanTables(t)
	// Deliberately do NOT register the kind.
	svc := NewScanService(testDB)
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

// TestScanIngestDeprecatedKindFailLoud: a deprecated kind is also rejected.
func TestScanIngestDeprecatedKindFailLoud(t *testing.T) {
	cleanTables(t)
	kind := model.TrustSubjectKind{Site: tSite, Key: tKind, IsDeprecated: true}
	if err := testDB.Create(&kind).Error; err != nil {
		t.Fatalf("register deprecated kind: %v", err)
	}
	svc := NewScanService(testDB)
	_, err := svc.Ingest(context.Background(), ScanParams{
		Site: tSite, SubjectKind: tKind, SubjectID: tSubj, Text: "x",
	})
	if !errors.Is(err, ErrSubjectKindNotRegistered) {
		t.Fatalf("deprecated kind: err = %v, want ErrSubjectKindNotRegistered", err)
	}
}

// TestScanIngestTruncation: text over the rune cap is truncated (rune-safe) and
// the truncation is reported + stored at exactly the cap length.
func TestScanIngestTruncation(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB)

	// Multibyte runes so a byte-based cut would split a glyph; assert rune count.
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

// TestScanRepeatableNotDeduped: the same subject scanned twice yields TWO rows
// (scan events are naturally repeatable — every edit is one scan).
func TestScanRepeatableNotDeduped(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	svc := NewScanService(testDB)

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
