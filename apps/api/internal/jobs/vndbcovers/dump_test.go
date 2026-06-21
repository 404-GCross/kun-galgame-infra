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
	// No .header → documented layout id=0, image=1.
	writeFile(t, vn, "v1\tcv11\tja\tfoo\nv2\t\\N\ten\tbar\n")

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

	m, err := loadCoverMeta(img)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 cv rows (sf99 skipped), got %d: %v", len(m), m)
	}
	// cv11: 800x600 -> pixels 480000; sexual rating(40)=0, violence rating(160)=2.
	if m["cv11"] != (cvMeta{sexual: 0, violence: 2, pixels: 480000}) {
		t.Errorf("cv11 = %+v, want {0,2,480000}", m["cv11"])
	}
	if m["cv22"] != (cvMeta{sexual: 2, violence: 1, pixels: 1}) {
		t.Errorf("cv22 = %+v, want {2,1,1}", m["cv22"])
	}
}

func TestLoadReleaseCovers(t *testing.T) {
	dir := t.TempDir()
	relVN := filepath.Join(dir, "releases_vn")
	relImg := filepath.Join(dir, "releases_images")
	writeFile(t, relVN+".header", "id\tvid\trtype")
	writeFile(t, relVN, "r1\tv1\tcomplete\nr2\tv1\tpartial\nr3\tv2\tcomplete\n")
	writeFile(t, relImg+".header", "id\timg\tvid\titype\tlang")
	writeFile(t, relImg,
		"r1\tcv10\t\\N\tpkgfront\t\\N\n"+
			"r1\tch99\t\\N\tpkgfront\t\\N\n"+ // non-cv id → skipped
			"r2\tcv11\t\\N\tdig\t\\N\n"+
			"r3\tcv20\t\\N\tpkgback\t\\N\n")

	meta := map[string]cvMeta{"cv10": {sexual: 1, violence: 0, pixels: 200}}
	out, err := loadReleaseCovers(relVN, relImg, meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["v1"]) != 2 || len(out["v2"]) != 1 {
		t.Fatalf("want v1:2 (cv10+cv11) v2:1 (cv20), got v1:%d v2:%d", len(out["v1"]), len(out["v2"]))
	}
	var cv10 *coverItem
	for i := range out["v1"] {
		if out["v1"][i].cvID == "cv10" {
			cv10 = &out["v1"][i]
		}
	}
	if cv10 == nil || cv10.kind != "pkgfront" || cv10.sexual != 1 || cv10.pixels != 200 {
		t.Fatalf("cv10 wrong/missing: %+v", cv10)
	}
}

func TestDumpSourceLookup(t *testing.T) {
	d := &dumpSource{
		vnCover: map[string]string{"v1": "cv11"},
		relCovers: map[string][]coverItem{
			"v1": {
				{cvID: "cv22", kind: "pkgfront", sexual: 1, violence: 0},
				{cvID: "cv11", kind: "dig", sexual: 2, violence: 1}, // dup of the main cover → dropped
				{cvID: "cv33", kind: "pkgback"},
			},
		},
		meta: map[string]cvMeta{"cv11": {sexual: 1, violence: 2, pixels: 12345}},
	}
	got := d.lookup("v1")
	// main first (cv11, kind=main, ratings from db/images), then cv22, cv33; cv11 dup dropped.
	if len(got) != 3 {
		t.Fatalf("want 3 covers (main + 2 release, dup dropped), got %d: %+v", len(got), got)
	}
	if got[0].cvID != "cv11" || got[0].kind != kindMain || got[0].sexual != 1 || got[0].violence != 2 {
		t.Fatalf("main cover wrong: %+v", got[0])
	}
	if got[1].cvID != "cv22" || got[1].kind != "pkgfront" {
		t.Fatalf("release cover[1] wrong: %+v", got[1])
	}
	if got[2].cvID != "cv33" || got[2].kind != "pkgback" {
		t.Fatalf("release cover[2] wrong: %+v", got[2])
	}
	if len(d.lookup("v9")) != 0 {
		t.Error("lookup(v9) should be empty")
	}
}
