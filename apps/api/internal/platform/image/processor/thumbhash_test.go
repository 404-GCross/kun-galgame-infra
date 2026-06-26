package processor

import (
	"encoding/base64"
	"image"
	"image/color"
	"testing"

	"api/internal/platform/image/preset"

	"go.n16f.net/thumbhash"
)

// TestThumbhashRoundTrip verifies Thumbhash returns a non-empty, base64-encoded
// hash that the canonical decoder accepts, and that it is deterministic.
func TestThumbhashRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 240, 160))
	for y := range 160 {
		for x := range 240 {
			if x < 120 {
				img.Set(x, y, color.NRGBA{R: 200, G: 60, B: 60, A: 255})
			} else {
				img.Set(x, y, color.NRGBA{R: 60, G: 80, B: 200, A: 255})
			}
		}
	}

	th := Thumbhash(img)
	if th == "" {
		t.Fatal("Thumbhash returned empty string")
	}
	raw, err := base64.StdEncoding.DecodeString(th)
	if err != nil {
		t.Fatalf("thumbhash is not valid base64: %v", err)
	}
	if _, err := thumbhash.DecodeImage(raw); err != nil {
		t.Fatalf("thumbhash failed to decode: %v", err)
	}
	if again := Thumbhash(img); again != th {
		t.Fatalf("Thumbhash not deterministic: %q != %q", again, th)
	}
}

// TestProcessMainSetsThumbhash verifies the main pipeline populates the
// thumbhash on its Output alongside width/height.
func TestProcessMainSetsThumbhash(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := range 200 {
		for x := range 300 {
			src.Set(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	out, err := ProcessMain(src, preset.MainPipelineConfig{FitWidth: 1600, FitHeight: 1600, Quality: 80})
	if err != nil {
		t.Fatalf("ProcessMain: %v", err)
	}
	if out.Thumbhash == "" {
		t.Fatal("ProcessMain produced empty thumbhash")
	}
	if out.Width == 0 || out.Height == 0 {
		t.Fatalf("ProcessMain produced zero dims: %dx%d", out.Width, out.Height)
	}
}
