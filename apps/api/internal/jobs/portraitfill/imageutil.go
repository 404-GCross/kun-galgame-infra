package portraitfill

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder for image.Decode
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder for image.Decode
)

// Upload-fit limits mirror the sibling sync-vndb-covers job (internal/jobs/
// vndbcovers): the image service rejects bodies over its Fiber BodyLimit
// (4 MiB) and images over MaxDecodedPixels (50 MP). Portrait main covers are
// small (VNDB caps cover height at ~1000 px), so fitForUpload is almost always
// a byte-for-byte pass-through; the downscale branch only guards the rare
// oversized image. Kept as an intentional small copy so this one-shot backfill
// stays self-contained and does not couple to the vndbcovers internals.
const (
	maxUploadBytes   = 3_500_000   // under the 4 MiB BodyLimit, with margin for multipart overhead
	maxPixels        = 50_000_000  // == processor MaxDecodedPixels (decode-bomb guard)
	safeDecodePixels = 200_000_000 // refuse to decode beyond this (OOM guard)
	downscaleLongest = 2560        // cap the longest side when downscaling
)

// rating maps a VNDB 0-200 average flag value (average vote * 100, raw scale
// 0=safe .. 2=explicit) onto the int16 0-2 cover column. Round-half-up; clamp.
func rating(avg100 int) int16 { return int16(min(2, max(0, (avg100+50)/100))) }

// ratingFloat maps a VNDB API 0-2 average flag onto the int16 0-2 column.
func ratingFloat(avg float64) int16 { return rating(int(avg * 100)) }

// isPortrait reports whether height exceeds width * 1.05 (the portrait cutoff
// used across the U track). Uses the exact rational 21/20 to avoid float noise.
func isPortrait(w, h int64) bool { return h*20 > w*21 }

// longEdgeBucket labels a portrait image by its long edge (== height) for the
// super-resolution forecast: which need upscaling (< 1080) and which are ready.
func longEdgeBucket(longEdge int) string {
	switch {
	case longEdge < 512:
		return "<512"
	case longEdge < 768:
		return "512-767"
	case longEdge < 1080:
		return "768-1079"
	default:
		return ">=1080"
	}
}

// fitForUpload returns bytes safe to upload -- within both the byte (BodyLimit)
// and pixel (processor) limits. Within-limit images pass through untouched (no
// decode). Oversized ones are decoded, downscaled and re-encoded as JPEG.
// Undecodable or absurdly-large images return skip=true (caller logs + skips).
func fitForUpload(body []byte, pixels int) (out []byte, skip bool, err error) {
	if len(body) <= maxUploadBytes && (pixels == 0 || pixels <= maxPixels) {
		return body, false, nil
	}
	if pixels > safeDecodePixels {
		return nil, true, fmt.Errorf("%d MP exceeds safe-decode limit", pixels/1_000_000)
	}
	src, _, derr := image.Decode(bytes.NewReader(body))
	if derr != nil {
		return nil, true, fmt.Errorf("decode: %w", derr)
	}
	resized := downscale(src, downscaleLongest)
	for _, q := range []int{82, 68, 55} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: q}); err != nil {
			return nil, true, fmt.Errorf("encode: %w", err)
		}
		if buf.Len() <= maxUploadBytes {
			return buf.Bytes(), false, nil
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, downscale(resized, 1600), &jpeg.Options{Quality: 70}); err != nil {
		return nil, true, fmt.Errorf("encode: %w", err)
	}
	if buf.Len() > maxUploadBytes {
		return nil, true, fmt.Errorf("still %d bytes after downscale", buf.Len())
	}
	return buf.Bytes(), false, nil
}

// downscale returns src resized so its longest side is <= longest (aspect kept),
// or src unchanged if already within bounds. High-quality CatmullRom.
func downscale(src image.Image, longest int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= longest && h <= longest {
		return src
	}
	nw, nh := longest, longest
	if w >= h {
		nh = h * longest / w
	} else {
		nw = w * longest / h
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// limiter spaces outbound requests at least minGap apart. One instance guards
// the polite <= 3 req/s image-download rate; concurrency stays 1 so the gap is
// the whole story.
type limiter struct {
	mu      sync.Mutex
	minGap  time.Duration
	lastReq time.Time
}

func newLimiter(gap time.Duration) *limiter { return &limiter{minGap: gap} }

func (l *limiter) wait() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := l.minGap - time.Since(l.lastReq); d > 0 {
		time.Sleep(d)
	}
	l.lastReq = time.Now()
}

// httpGetLimited fetches url after honoring the rate limiter, capping the body.
func httpGetLimited(c *http.Client, lim *limiter, url string) ([]byte, error) {
	if lim != nil {
		lim.wait()
	}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
