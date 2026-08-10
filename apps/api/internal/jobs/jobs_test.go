package jobs

import (
	"testing"
	"time"
)

func TestScheduleZero(t *testing.T) {
	if !(Schedule{}).Zero() {
		t.Fatal("empty schedule should be Zero")
	}
	if (Schedule{DailyAt: "03:00"}).Zero() {
		t.Fatal("DailyAt schedule should not be Zero")
	}
	if (Schedule{Every: time.Hour}).Zero() {
		t.Fatal("Every schedule should not be Zero")
	}
}

func TestScheduleNextDailyAt(t *testing.T) {
	loc := time.UTC
	s := Schedule{DailyAt: "03:00"}

	now := time.Date(2026, 5, 16, 1, 0, 0, 0, loc)
	got := s.Next(now)
	want := time.Date(2026, 5, 16, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("before fire: got %v want %v", got, want)
	}

	now = time.Date(2026, 5, 16, 3, 0, 0, 0, loc)
	got = s.Next(now)
	want = time.Date(2026, 5, 17, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("at fire: got %v want %v", got, want)
	}

	now = time.Date(2026, 5, 16, 9, 30, 0, 0, loc)
	got = s.Next(now)
	want = time.Date(2026, 5, 17, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("after fire: got %v want %v", got, want)
	}
}

func TestScheduleNextEveryAndBad(t *testing.T) {
	now := time.Date(2026, 5, 16, 1, 0, 0, 0, time.UTC)

	if got := (Schedule{Every: 2 * time.Hour}).Next(now); !got.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("Every: got %v", got)
	}
	if got := (Schedule{}).Next(now); !got.IsZero() {
		t.Fatalf("zero schedule Next should be zero, got %v", got)
	}
	if got := (Schedule{DailyAt: "nonsense"}).Next(now); !got.IsZero() {
		t.Fatalf("bad DailyAt Next should be zero, got %v", got)
	}
}

func TestRegistryListSortedAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(Job{Name: "sync-vndb"})
	r.Register(Job{Name: "image-gc"})
	r.Register(Job{Name: "galgame-image-refping"})

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("want 3 jobs, got %d", len(list))
	}
	if list[0].Name != "galgame-image-refping" || list[2].Name != "sync-vndb" {
		t.Fatalf("not name-sorted: %v", []string{list[0].Name, list[1].Name, list[2].Name})
	}
	if _, ok := r.Get("image-gc"); !ok {
		t.Fatal("Get(image-gc) missing")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get(nope) should be absent")
	}
}

func TestAdvisoryKeyStableAndDistinct(t *testing.T) {
	a, b := advisoryKey("sync-vndb"), advisoryKey("sync-vndb")
	if a != b {
		t.Fatal("advisoryKey not stable")
	}
	if advisoryKey("sync-vndb") == advisoryKey("image-gc") {
		t.Fatal("advisoryKey collision on distinct names")
	}
}
