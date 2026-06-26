// Package processor implements the image decode → resize → encode pipeline.
//
// Decoding covers JPEG, PNG, GIF (first frame), and WebP (via
// golang.org/x/image/webp, pure-Go, read-only).
//
// Resizing uses golang.org/x/image/draw with CatmullRom for quality.
//
// WebP ENCODING uses github.com/kolesa-team/go-webp (cgo bindings to
// libwebp). This REQUIRES CGO + a system libwebp (pkg-config `libwebp`).
// We need true lossy WebP at a controllable quality — the previous pure-Go
// nativewebp encoder could only emit lossless VP8L (no quality knob), which
// produced multi-MB outputs and is why uploads were originally disabled.
// libwebp gives us the design's "compressed WebP @ configurable quality"
// (see docs/image_service/01-design.md 技术栈备选 `kolesa-team/go-webp`).
package processor

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"

	_ "image/gif"  // register GIF decoder (first frame via image.Decode)
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder

	"api/internal/platform/image/preset"

	"github.com/kolesa-team/go-webp/encoder"
	"github.com/kolesa-team/go-webp/webp"
	"go.n16f.net/thumbhash"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder (read-only, pure-Go)
)

// MaxDecodedPixels caps decoded image area to defuse decompression bombs.
// 50 megapixels covers any reasonable photograph.
const MaxDecodedPixels = 50 * 1000 * 1000

// Errors returned by processor operations.
var (
	ErrUnsupportedInput = errors.New("processor: unsupported input format")
	ErrTooLarge         = errors.New("processor: decoded image exceeds pixel limit")
	ErrInvalidInput     = errors.New("processor: invalid input")
)

// Output is a single processed image payload ready to be stored.
type Output struct {
	Data        []byte
	MIME        string
	Ext         string
	Width       int
	Height      int
	Thumbhash   string // base64 ThumbHash placeholder; set on the main image only
	VariantName string // "" for main image, otherwise preset variant name
}

// Decode parses the input bytes as an image. Returns the decoded image and
// the detected format string ("jpeg"/"png"/"gif"/"webp").
func Decode(r io.Reader) (image.Image, string, error) {
	img, format, err := image.Decode(r)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	b := img.Bounds()
	if int64(b.Dx())*int64(b.Dy()) > MaxDecodedPixels {
		return nil, "", ErrTooLarge
	}
	return img, format, nil
}

// fitInside returns the dimensions to resize to, preserving aspect ratio
// and never enlarging beyond the original.
func fitInside(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW <= maxW && srcH <= maxH {
		return srcW, srcH
	}
	ratio := float64(srcW) / float64(srcH)
	targetW, targetH := maxW, maxH
	if float64(maxW)/float64(maxH) > ratio {
		// Target box is wider than the image — height is the constraint.
		targetW = int(float64(maxH) * ratio)
	} else {
		targetH = int(float64(maxW) / ratio)
	}
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	return targetW, targetH
}

// resizeInside produces an aspect-preserving resized image fit inside the
// given box. Never enlarges.
func resizeInside(src image.Image, maxW, maxH int) *image.NRGBA {
	b := src.Bounds()
	dstW, dstH := fitInside(b.Dx(), b.Dy(), maxW, maxH)
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// resizeCover produces a target-sized image that completely covers the
// target box, cropping if necessary. Used for square avatar thumbnails.
func resizeCover(src image.Image, targetW, targetH int) *image.NRGBA {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()

	srcAspect := float64(srcW) / float64(srcH)
	dstAspect := float64(targetW) / float64(targetH)

	// Work out the crop rectangle in the source image so the cropped
	// region has the target aspect ratio, then scale onto the target.
	var cropW, cropH int
	if srcAspect > dstAspect {
		// Source wider than target — crop left/right.
		cropH = srcH
		cropW = int(float64(srcH) * dstAspect)
	} else {
		cropW = srcW
		cropH = int(float64(srcW) / dstAspect)
	}
	if cropW < 1 {
		cropW = 1
	}
	if cropH < 1 {
		cropH = 1
	}
	cropX := b.Min.X + (srcW-cropW)/2
	cropY := b.Min.Y + (srcH-cropH)/2
	cropRect := image.Rect(cropX, cropY, cropX+cropW, cropY+cropH)

	dst := image.NewNRGBA(image.Rect(0, 0, targetW, targetH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, cropRect, draw.Over, nil)
	return dst
}

// encodeWebP encodes the image to lossy WebP at the given quality (1..100).
// Quality is clamped to that range defensively; preset validation already
// enforces 1..100 but a zero/garbage value must never reach libwebp.
func encodeWebP(img image.Image, quality int) ([]byte, error) {
	if quality < 1 {
		quality = 77
	}
	if quality > 100 {
		quality = 100
	}
	opts, err := encoder.NewLossyEncoderOptions(encoder.PresetDefault, float32(quality))
	if err != nil {
		return nil, fmt.Errorf("webp encoder options: %w", err)
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, opts); err != nil {
		return nil, fmt.Errorf("encode webp: %w", err)
	}
	return buf.Bytes(), nil
}

// ProcessMain runs the main compression pipeline on the decoded image.
func ProcessMain(src image.Image, main preset.MainPipelineConfig) (*Output, error) {
	resized := resizeInside(src, main.FitWidth, main.FitHeight)
	data, err := encodeWebP(resized, main.Quality)
	if err != nil {
		return nil, err
	}
	bounds := resized.Bounds()
	return &Output{
		Data:   data,
		MIME:   "image/webp",
		Ext:    "webp",
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
		// ThumbHash placeholder, computed from the same image we store.
		Thumbhash: Thumbhash(resized),
	}, nil
}

// Thumbhash computes the base64-encoded ThumbHash placeholder for a decoded
// image. The encoder internally downsamples to ≤128px, so any input size is
// cheap. base64 so it threads cleanly through JSON + DB. Single source of
// truth shared by the upload pipeline (ProcessMain) and the thumbhash backfill
// (cmd/migrate-image-thumbhash).
func Thumbhash(img image.Image) string {
	return base64.StdEncoding.EncodeToString(thumbhash.EncodeImage(img))
}

// ProcessVariant generates a single derivative from the decoded image.
func ProcessVariant(src image.Image, v preset.VariantSpec) (*Output, error) {
	var out *image.NRGBA
	switch v.Fit {
	case preset.FitCover:
		out = resizeCover(src, v.Width, v.Height)
	default: // inside
		out = resizeInside(src, v.Width, v.Height)
	}
	data, err := encodeWebP(out, v.Quality)
	if err != nil {
		return nil, err
	}
	bounds := out.Bounds()
	return &Output{
		Data:        data,
		MIME:        "image/webp",
		Ext:         "webp",
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		VariantName: v.Name,
	}, nil
}

// DecodeFromBytes is a convenience wrapper so callers don't have to bring
// bytes.NewReader.
func DecodeFromBytes(b []byte) (image.Image, string, error) {
	// Decompression-bomb guard: read only the header (DecodeConfig allocates no
	// pixel buffer) and reject oversized dimensions BEFORE the full decode
	// allocates the image. A small crafted file can otherwise expand to a huge
	// in-memory image before Decode's post-hoc pixel check fires.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxDecodedPixels {
		return nil, "", ErrTooLarge
	}
	return Decode(bytes.NewReader(b))
}
