package dlsitemedia

import (
	"path/filepath"
	"testing"
)

func TestAgeToSexual(t *testing.T) {
	cases := map[string]int16{
		"3": 2, // adult
		"2": 1, // r15
		"1": 0, // general
		"":  0, // unknown
		"9": 0, // unexpected → safe default
	}
	for age, want := range cases {
		if got := ageToSexual(age); got != want {
			t.Errorf("ageToSexual(%q) = %d, want %d", age, got, want)
		}
	}
}

func TestIntroFromPage(t *testing.T) {
	// The description lives in parts[].text + multiimage items[].text — NOT in the
	// `outline` metadata object, which this extraction must ignore entirely.
	page := []byte(`{
		"outline": {"販売日": "2022年10月28日", "年齢指定": "R18", "ジャンル": "学園"},
		"parts": [
			{"type": "type_text", "heading": "STORY", "text": "  A story blurb.  "},
			{"type": "type_multiimages", "items": [
				{"image": "//x/a.jpg", "text": "●Aoi (CV: X) a heroine"},
				{"image": "//x/b.jpg", "text": ""}
			]},
			{"type": "type_image", "text": "", "images": ["//x/c.jpg"]}
		]
	}`)
	got := introFromPage(page)
	want := "A story blurb.\n\n●Aoi (CV: X) a heroine"
	if got != want {
		t.Errorf("introFromPage:\n got=%q\nwant=%q", got, want)
	}

	// outline-only (no parts) → empty (the outline object is never the intro).
	if got := introFromPage([]byte(`{"outline": {"x": "y"}}`)); got != "" {
		t.Errorf("outline-only should yield empty intro, got %q", got)
	}
	if got := introFromPage(nil); got != "" {
		t.Errorf("nil page should yield empty intro, got %q", got)
	}
	if got := introFromPage([]byte(`not json`)); got != "" {
		t.Errorf("bad json should yield empty intro, got %q", got)
	}
}

func TestCoverFile(t *testing.T) {
	ok := []byte(`{"image_main": {"url": "//img.dlsite.jp/modpub/images2/work/doujin/RJ094000/RJ093895_img_main.jpg"}}`)
	if name, got := coverFile(ok); !got || name != "RJ093895_img_main.jpg" {
		t.Errorf("coverFile ok = (%q,%v), want (RJ093895_img_main.jpg,true)", name, got)
	}
	// Placeholder → skip.
	ph := []byte(`{"image_main": {"url": "//img.dlsite.jp/.../no_img_main.gif"}}`)
	if name, got := coverFile(ph); got || name != "" {
		t.Errorf("coverFile placeholder = (%q,%v), want ('',false)", name, got)
	}
	// Empty / absent → skip.
	if _, got := coverFile([]byte(`{"image_main": {"url": ""}}`)); got {
		t.Error("coverFile empty url should be false")
	}
	if _, got := coverFile([]byte(`{}`)); got {
		t.Error("coverFile absent image_main should be false")
	}
}

func TestSampleFiles(t *testing.T) {
	// Ordered filenames; the index becomes sort_order.
	prod := []byte(`{"image_samples": [
		{"url": "//img.dlsite.jp/.../RJ093895_img_smp1.jpg"},
		{"url": "//img.dlsite.jp/.../RJ093895_img_smp2.jpg"},
		{"url": ""},
		{"url": "//img.dlsite.jp/.../RJ093895_img_smpa3.jpg"}
	]}`)
	got := sampleFiles(prod)
	want := []string{"RJ093895_img_smp1.jpg", "RJ093895_img_smp2.jpg", "RJ093895_img_smpa3.jpg"}
	if len(got) != len(want) {
		t.Fatalf("sampleFiles len = %d (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sampleFiles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Non-array image_samples (null / object) → no samples (tolerated).
	if s := sampleFiles([]byte(`{"image_samples": null}`)); s != nil {
		t.Errorf("null image_samples should yield nil, got %v", s)
	}
	if s := sampleFiles([]byte(`{"image_samples": {"broken": true}}`)); len(s) != 0 {
		t.Errorf("object image_samples should yield no samples, got %v", s)
	}
}

func TestMirrorPathAndBodyless(t *testing.T) {
	if got := mirrorPath("/m", "RJ093895", "RJ093895_img_main.jpg"); got != filepath.Join("/m", "RJ093895", "RJ093895_img_main.jpg") {
		t.Errorf("mirrorPath = %q", got)
	}
	empty := ""
	claimed := "galgame_wiki"
	if !isBodyless(nil) || !isBodyless(&empty) {
		t.Error("nil / '' site must be bodyless")
	}
	if isBodyless(&claimed) {
		t.Error("claimed site must NOT be bodyless")
	}
}

func TestParseKinds(t *testing.T) {
	all, _ := ParseKinds("all")
	if !all.Intro || !all.Cover || !all.Screenshot {
		t.Errorf("all = %+v", all)
	}
	def, _ := ParseKinds("")
	if def != all {
		t.Errorf("empty should equal all, got %+v", def)
	}
	mix, err := ParseKinds("intro, cover")
	if err != nil || !mix.Intro || !mix.Cover || mix.Screenshot {
		t.Errorf("mix = %+v err=%v", mix, err)
	}
	if _, err := ParseKinds("bogus"); err == nil {
		t.Error("bogus kind should error")
	}
}
