package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDateMarshalJSON(t *testing.T) {
	// Date value → bare "YYYY-MM-DD" (NOT RFC3339).
	d := Date(time.Date(2019, 8, 16, 0, 0, 0, 0, time.UTC))
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2019-08-16"` {
		t.Fatalf("got %s, want \"2019-08-16\"", b)
	}

	// Even with a stray clock time (shouldn't happen for a date column,
	// but be defensive), only the date part is emitted.
	d2 := Date(time.Date(2019, 8, 16, 13, 45, 0, 0, time.UTC))
	b2, _ := json.Marshal(d2)
	if string(b2) != `"2019-08-16"` {
		t.Fatalf("got %s, want \"2019-08-16\"", b2)
	}

	// Zero value → null.
	var zero Date
	bz, _ := json.Marshal(zero)
	if string(bz) != "null" {
		t.Fatalf("got %s, want null", bz)
	}
}

func TestDatePointerInStruct(t *testing.T) {
	type wrap struct {
		ReleaseDate *Date `json:"release_date"`
	}
	d := Date(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC))
	b, _ := json.Marshal(wrap{ReleaseDate: &d})
	if string(b) != `{"release_date":"2024-03-01"}` {
		t.Fatalf("got %s", b)
	}
	// nil pointer → null
	bn, _ := json.Marshal(wrap{ReleaseDate: nil})
	if string(bn) != `{"release_date":null}` {
		t.Fatalf("got %s", bn)
	}
}

func TestDateUnmarshalJSON(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"2020-05-15"`), &d); err != nil {
		t.Fatal(err)
	}
	if got := d.Time(); got.Year() != 2020 || got.Month() != 5 || got.Day() != 15 {
		t.Fatalf("got %v", got)
	}

	// null / empty → zero value, no error
	var d2 Date
	if err := json.Unmarshal([]byte(`null`), &d2); err != nil {
		t.Fatal(err)
	}
	if !d2.Time().IsZero() {
		t.Fatalf("null should produce zero Date, got %v", d2.Time())
	}

	// Garbage → error
	var d3 Date
	if err := json.Unmarshal([]byte(`"2020-13-99"`), &d3); err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestDateScanValue(t *testing.T) {
	// Scan: driver hands a time.Time (PG date column)
	var d Date
	src := time.Date(2018, 12, 31, 0, 0, 0, 0, time.UTC)
	if err := d.Scan(src); err != nil {
		t.Fatal(err)
	}
	if !d.Time().Equal(src) {
		t.Fatalf("scan got %v want %v", d.Time(), src)
	}

	// Value: → bare date string
	v, err := d.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "2018-12-31" {
		t.Fatalf("value got %v want 2018-12-31", v)
	}

	// Scan nil → zero
	var dn Date
	if err := dn.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if !dn.Time().IsZero() {
		t.Fatalf("scan nil should be zero, got %v", dn.Time())
	}

	// Value of zero → SQL NULL
	var dz Date
	vz, _ := dz.Value()
	if vz != nil {
		t.Fatalf("zero Value should be nil, got %v", vz)
	}
}

func TestNewDate(t *testing.T) {
	if NewDate(nil) != nil {
		t.Fatal("NewDate(nil) should be nil")
	}
	tt := time.Date(2021, 7, 21, 9, 0, 0, 0, time.FixedZone("JST", 9*3600))
	d := NewDate(&tt)
	// Normalized to UTC: 2021-07-21 09:00 JST = 2021-07-21 00:00 UTC
	if got := d.Time(); got.Year() != 2021 || got.Month() != 7 || got.Day() != 21 {
		t.Fatalf("got %v", got)
	}
	b, _ := json.Marshal(d)
	if string(b) != `"2021-07-21"` {
		t.Fatalf("got %s", b)
	}
}
