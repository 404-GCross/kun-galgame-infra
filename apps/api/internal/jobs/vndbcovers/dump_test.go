package vndbcovers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRating(t *testing.T) {
	cases := []struct {
		in   int
		want int16
	}{
		{-10, 0}, {0, 0}, {49, 0}, {50, 1}, {100, 1}, {149, 1}, {150, 2}, {200, 2}, {250, 2},
	}
	for _, c := range cases {
		if got := rating(c.in); got != c.want {
			t.Errorf("rating(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestRatingFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want int16
	}{
		{0.0, 0}, {0.4, 0}, {0.5, 1}, {1.0, 1}, {1.49, 1}, {1.6, 2}, {2.0, 2},
	}
	for _, c := range cases {
		if got := ratingFloat(c.in); got != c.want {
			t.Errorf("ratingFloat(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCvRelPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"cv12345", "cv/45/12345.jpg"},
		{"cv7", "cv/07/7.jpg"},
		{"cv100", "cv/00/100.jpg"},
	}
	for _, c := range cases {
		got, err := cvRelPath(c.in)
		if err != nil || got != c.want {
			t.Errorf("cvRelPath(%q) = (%q, %v), want %q", c.in, got, err, c.want)
		}
	}
	if _, err := cvRelPath("cvX"); err == nil {
		t.Error("cvRelPath(\"cvX\") should error on a non-numeric id")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadVNCoverMap_WithHeader(t *testing.T) {
	dir := t.TempDir()
	vn := filepath.Join(dir, "vn")
	// `image` deliberately in a non-default position to prove the header is used.
	writeFile(t, vn+".header", "id\timage\tolang\tdescription")
	writeFile(t, vn, "v1\tcv11\tja\tfoo\nv2\t\\N\ten\tbar\nv3\tcv333\tja\tbaz\n")

	m, err := loadVNCoverMap(vn)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["v1"] != "cv11" || m["v3"] != "cv333" {
		t.Fatalf("want {v1:cv11, v3:cv333} (v2 \\N skipped), got %v", m)
	}
}

func TestLoadVNCoverMap_FallbackNoHeader(t *testing.T) {
	dir := t.TempDir()
	vn := filepath.Join(dir, "vn")
	// No .header → documented layout id=0, image=2.
	writeFile(t, vn, "v1\tja\tcv11\tfoo\nv2\ten\t\\N\tbar\n")

	m, err := loadVNCoverMap(vn)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["v1"] != "cv11" {
		t.Fatalf("want {v1:cv11}, got %v", m)
	}
}

func TestLoadCoverRatings(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "images")
	// columns: id width height c_votecount c_sexual_avg c_sexual_stddev c_violence_avg ...
	writeFile(t, img,
		"cv11\t800\t600\t5\t40\t10\t160\t0\t1\n"+
			"cv22\t1\t1\t1\t150\t0\t50\t0\t1\n"+
			"sf99\t1\t1\t1\t200\t0\t200\t0\t1\n")

	m, err := loadCoverRatings(img)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 cv ratings (sf99 skipped), got %d: %v", len(m), m)
	}
	if m["cv11"] != (rate{sexual: 0, violence: 2}) {
		t.Errorf("cv11 = %+v, want {0,2}", m["cv11"])
	}
	if m["cv22"] != (rate{sexual: 2, violence: 1}) {
		t.Errorf("cv22 = %+v, want {2,1}", m["cv22"])
	}
}

func TestDumpSourceLookup(t *testing.T) {
	d := &dumpSource{
		vnCover: map[string]string{"v1": "cv11"},
		ratings: map[string]rate{"cv11": {sexual: 1, violence: 2}},
	}
	m, ok := d.lookup("v1")
	if !ok || m.cvID != "cv11" || m.sexual != 1 || m.violence != 2 {
		t.Fatalf("lookup(v1) = (%+v, %v)", m, ok)
	}
	if _, ok := d.lookup("v9"); ok {
		t.Error("lookup(v9) should be a miss")
	}
}
