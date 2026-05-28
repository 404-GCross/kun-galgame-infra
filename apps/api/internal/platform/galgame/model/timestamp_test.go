package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimestampMarshalJSON(t *testing.T) {
	// A non-UTC time → serialized AS UTC (the whole point: host TZ must
	// not leak into the wire format).
	loc := time.FixedZone("PDT", -7*3600)
	ts := Timestamp(time.Date(2026, 5, 10, 9, 24, 28, 0, loc)) // 09:24 -0700 = 16:24 UTC
	b, err := json.Marshal(ts)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2026-05-10T16:24:28Z"` {
		t.Fatalf("got %s, want \"2026-05-10T16:24:28Z\"", b)
	}

	// Already-UTC → unchanged.
	ts2 := Timestamp(time.Date(2026, 5, 10, 16, 24, 28, 0, time.UTC))
	b2, _ := json.Marshal(ts2)
	if string(b2) != `"2026-05-10T16:24:28Z"` {
		t.Fatalf("got %s", b2)
	}

	// Zero → null.
	var zero Timestamp
	bz, _ := json.Marshal(zero)
	if string(bz) != "null" {
		t.Fatalf("got %s, want null", bz)
	}
}

func TestTimestampPointerInStruct(t *testing.T) {
	type wrap struct {
		CompletedTime *Timestamp `json:"completed_time,omitempty"`
	}
	ts := Timestamp(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC))
	b, _ := json.Marshal(wrap{CompletedTime: &ts})
	if string(b) != `{"completed_time":"2024-03-01T12:00:00Z"}` {
		t.Fatalf("got %s", b)
	}
	// nil pointer with omitempty → field dropped
	bn, _ := json.Marshal(wrap{CompletedTime: nil})
	if string(bn) != `{}` {
		t.Fatalf("got %s", bn)
	}
}

func TestTimestampUnmarshalJSON(t *testing.T) {
	var ts Timestamp
	// RFC3339 with offset → normalized to UTC
	if err := json.Unmarshal([]byte(`"2026-05-10T09:24:28-07:00"`), &ts); err != nil {
		t.Fatal(err)
	}
	got := ts.Time()
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC location, got %v", got.Location())
	}
	if got.Hour() != 16 { // 09 -0700 = 16 UTC
		t.Fatalf("expected 16h UTC, got %d", got.Hour())
	}

	// null → zero
	var ts2 Timestamp
	if err := json.Unmarshal([]byte(`null`), &ts2); err != nil {
		t.Fatal(err)
	}
	if !ts2.Time().IsZero() {
		t.Fatalf("null should be zero, got %v", ts2.Time())
	}
}

func TestTimestampScanValue(t *testing.T) {
	var ts Timestamp
	src := time.Date(2018, 12, 31, 23, 59, 59, 0, time.UTC)
	if err := ts.Scan(src); err != nil {
		t.Fatal(err)
	}
	if !ts.Time().Equal(src) {
		t.Fatalf("scan got %v want %v", ts.Time(), src)
	}
	v, err := ts.Value()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(time.Time); !ok {
		t.Fatalf("Value should be time.Time, got %T", v)
	}

	// nil scan → zero; zero value → NULL
	var tn Timestamp
	if err := tn.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if !tn.Time().IsZero() {
		t.Fatalf("scan nil should be zero")
	}
	vz, _ := tn.Value()
	if vz != nil {
		t.Fatalf("zero Value should be nil, got %v", vz)
	}
}

