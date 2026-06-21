package vndbcovers

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFitForUpload(t *testing.T) {
	// Within both limits → passed through unchanged (no decode/re-encode).
	small := makeJPEG(t, 200, 200)
	out, skip, err := fitForUpload(small, 200*200)
	if skip || err != nil {
		t.Fatalf("small image should pass: skip=%v err=%v", skip, err)
	}
	if !bytes.Equal(out, small) {
		t.Fatal("within-limit image must pass through unchanged")
	}

	// Over the pixel limit → decoded + downscaled to ≤ downscaleLongest and ≤ limits.
	wide := makeJPEG(t, 4000, 3000) // real 12 MP; claim >50 MP to trigger the path
	out, skip, err = fitForUpload(wide, 60_000_000)
	if skip || err != nil {
		t.Fatalf("oversized image should downscale: skip=%v err=%v", skip, err)
	}
	if len(out) > maxUploadBytes {
		t.Fatalf("downscaled output still over byte limit: %d", len(out))
	}
	di, _, derr := image.Decode(bytes.NewReader(out))
	if derr != nil {
		t.Fatalf("downscaled output not decodable: %v", derr)
	}
	if b := di.Bounds(); b.Dx() > downscaleLongest || b.Dy() > downscaleLongest {
		t.Fatalf("downscaled dims %dx%d exceed %d", b.Dx(), b.Dy(), downscaleLongest)
	}

	// Un-decodable bytes → skip (not a hard error for the run).
	if _, skip, _ := fitForUpload([]byte("not an image"), 60_000_000); !skip {
		t.Fatal("undecodable bytes should skip")
	}

	// Absurd pixel count → skip without attempting the (OOM-risky) decode.
	if _, skip, _ := fitForUpload([]byte("x"), 300_000_000); !skip {
		t.Fatal("pixels beyond safe-decode limit should skip")
	}
}
