// Package processor implements the image decode → resize → encode pipeline.
// All codec work is pure Go (no CGO / no libwebp system dep) to keep the
// build and deploy story simple.
//
// Decoding covers JPEG, PNG, GIF (first frame), and WebP (via
// golang.org/x/image/webp, read-only).
//
// Resizing uses golang.org/x/image/draw with CatmullRom for quality.
//
// WebP encoding uses github.com/HugoSmits86/nativewebp (pure Go). Quality is
// approximated — this package is slower and slightly larger than libwebp,
// but removes the CGO dependency entirely.
package processor

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"

	_ "image/gif"  // register GIF decoder (first frame via image.Decode)
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder

	"api/internal/platform/image/preset"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder (read-only)
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

// encodeWebP encodes the given image to WebP bytes. Quality is ignored by
// nativewebp v0.x; kept in signature for future libwebp drop-in.
func encodeWebP(img image.Image, _ int) ([]byte, error) {
	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, nil); err != nil {
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
	}, nil
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
	return Decode(bytes.NewReader(b))
}

