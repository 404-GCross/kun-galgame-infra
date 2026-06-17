package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRating(t *testing.T) {
	// VNDB c_*_avg is 0-200 (avg vote * 100); maps to int16 0-2, round-half-up.
	cases := map[int]int16{0: 0, 49: 0, 50: 1, 100: 1, 149: 1, 150: 2, 200: 2, 999: 2, -5: 0}
	for in, want := range cases {
		if got := rating(in); got != want {
			t.Errorf("rating(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestScrRelPath(t *testing.T) {
	// Verified against the real rsync layout (sf/07/5707.jpg exists).
	cases := map[string]string{
		"sf5707":   "sf/07/5707.jpg",
		"sf10000":  "sf/00/10000.jpg",
		"sf100007": "sf/07/100007.jpg",
		"sf1":      "sf/01/1.jpg",
	}
	for scr, want := range cases {
		got, err := scrRelPath(scr)
		if err != nil {
			t.Fatalf("scrRelPath(%q) error: %v", scr, err)
		}
		if got != want {
			t.Errorf("scrRelPath(%q) = %q, want %q", scr, got, want)
		}
	}
	if _, err := scrRelPath("sfXYZ"); err == nil {
		t.Error("scrRelPath(sfXYZ) expected error, got nil")
	}
}

func TestLoadScreenshotMap(t *testing.T) {
	// id<TAB>scr<TAB>rid; a (vn,scr) repeated across releases must dedup, order kept.
	content := "v1\tsf5707\tr3\n" +
		"v1\tsf5708\tr3\n" +
		"v1\tsf5707\tr9\n" + // duplicate (v1, sf5707) under a different release
		"v2\tsf42\tr1\n"
	p := filepath.Join(t.TempDir(), "vn_screenshots")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := loadScreenshotMap(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m["v1"], []string{"sf5707", "sf5708"}) {
		t.Errorf("v1 = %v, want [sf5707 sf5708] (deduped, ordered)", m["v1"])
	}
	if !reflect.DeepEqual(m["v2"], []string{"sf42"}) {
		t.Errorf("v2 = %v, want [sf42]", m["v2"])
	}
}

func TestLoadImageMeta(t *testing.T) {
	// images columns: id width height c_votecount c_sexual_avg c_sexual_stddev
	//                 c_violence_avg c_violence_stddev c_weight
	content := "ch12\t250\t300\t14\t0\t0\t0\t0\t1\n" + // not sf → skipped
		"sf5707\t1280\t720\t8\t150\t30\t100\t10\t1\n" + // sexual 150→2, violence 100→1
		"sf42\t800\t600\t3\t0\t0\t200\t5\t1\n" // sexual 0→0, violence 200→2
	p := filepath.Join(t.TempDir(), "images")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := loadImageMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["ch12"]; ok {
		t.Error("ch12 should be skipped (not a screenshot)")
	}
	if got := m["sf5707"]; got != (imgMeta{sexual: 2, violence: 1}) {
		t.Errorf("sf5707 = %+v, want {sexual:2 violence:1}", got)
	}
	if got := m["sf42"]; got != (imgMeta{sexual: 0, violence: 2}) {
		t.Errorf("sf42 = %+v, want {sexual:0 violence:2}", got)
	}
}
