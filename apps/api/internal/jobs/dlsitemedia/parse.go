package dlsitemedia

import (
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// ageToSexual maps a DLsite work-level age_category to the catalog per-image
// `sexual` flag (scale 0-3, matching galgame). DLsite grades at the WORK level,
// not per image: age_category 3=adult, 2=r15, 1=general. adult→2 is deliberately
// conservative — DLsite's public preview images are actually masked (less
// explicit than the work), so a work-level 2 over-flags rather than under-filters
// (better an NSFW filter false-positive than a leak). It is the only granularity
// available. violence has no DLsite grade and is always 0.
func ageToSexual(age string) int16 {
	switch strings.TrimSpace(age) {
	case "3": // adult
		return 2
	case "2": // r15
		return 1
	default: // general (1) / unknown
		return 0
	}
}

// pageParts is the slice of dlsite.works.page_json this backfill reads: the
// maker 作品紹介 (work introduction) blocks (see kun-dlsite-api
// internal/enrich.PageData). Each block carries prose in `text`, and a
// multiimage block additionally carries per-character `items[].text`.
type pageParts struct {
	Parts []struct {
		Text  string `json:"text"`
		Items []struct {
			Text string `json:"text"`
		} `json:"items"`
	} `json:"parts"`
}

// introFromPage rebuilds a bodyless work's description from the DLsite maker
// introduction stored in page_json.parts.
//
// DATA REALITY (verified 2026-07-17 against the local dlsite DB): the description
// lives in page_json.parts[].text (+ multiimage items[].text), NOT in
// page_json.outline. `outline` is the DLsite spec-table object
// (販売日/ジャンル/年齢指定…) — a key/value map, not prose (`page_json->>'outline'`
// would serialise that whole object as JSON text). The step-55 spec's "outline is
// the description" note was stale: the staging DB was re-enriched with the
// structured parser (kun-dlsite-api internal/enrich), which splits the spec table
// (outline) from the introduction blocks (parts). Text is joined verbatim in
// document order with blank-line separators — no cleaning (§5, same verbatim
// discipline as the step-52 VNDB intro pilot). Returns "" when there is no prose.
func introFromPage(pageJSON []byte) string {
	if len(pageJSON) == 0 {
		return ""
	}
	var p pageParts
	if err := json.Unmarshal(pageJSON, &p); err != nil {
		return ""
	}
	var blocks []string
	for _, part := range p.Parts {
		if t := strings.TrimSpace(part.Text); t != "" {
			blocks = append(blocks, t)
		}
		for _, it := range part.Items {
			if t := strings.TrimSpace(it.Text); t != "" {
				blocks = append(blocks, t)
			}
		}
	}
	return strings.Join(blocks, "\n\n")
}

// productMedia is the slice of dlsite.works.product_json this backfill reads.
// image_samples is decoded as a raw message because DLsite returns it as an array
// for most works but null/other for some (the endpoint is not a stable API), so a
// non-array shape is tolerated as "no samples" — matching the mirror producer.
type productMedia struct {
	ImageMain    imageObj        `json:"image_main"`
	ImageSamples json.RawMessage `json:"image_samples"`
}

type imageObj struct {
	URL string `json:"url"`
}

// coverFile returns the mirror filename for a work's landscape store cover
// (image_main), or ok=false when the URL is empty or the DLsite "no image"
// placeholder (no_img_main.gif). The filename is the URL's last path segment,
// byte-identical to what kun-dlsite-api's mirror writes under
// <workno>/<filename> (the cross-repo mirror contract) — this reader NEVER dials
// DLsite; it only derives the local path.
func coverFile(productJSON []byte) (string, bool) {
	var p productMedia
	if err := json.Unmarshal(productJSON, &p); err != nil {
		return "", false
	}
	raw := strings.TrimSpace(p.ImageMain.URL)
	if raw == "" || isPlaceholder(raw) {
		return "", false
	}
	name := filenameFromURL(normalizeURL(raw))
	return name, name != ""
}

// sampleFiles returns the ordered mirror filenames for a work's sample images
// (image_samples[]). The index in the returned slice is the screenshot
// sort_order. Empty/placeholder URLs are skipped so the returned indices stay
// contiguous (0..n-1); a non-array image_samples yields no samples.
func sampleFiles(productJSON []byte) []string {
	var p productMedia
	if err := json.Unmarshal(productJSON, &p); err != nil {
		return nil
	}
	if len(p.ImageSamples) == 0 {
		return nil
	}
	var samples []imageObj
	_ = json.Unmarshal(p.ImageSamples, &samples)
	var out []string
	for _, s := range samples {
		raw := strings.TrimSpace(s.URL)
		if raw == "" || isPlaceholder(raw) {
			continue
		}
		if name := filenameFromURL(normalizeURL(raw)); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// normalizeURL upgrades DLsite's protocol-relative URLs (//img.dlsite.jp/...) to
// https and leaves absolute URLs untouched; blank yields "".
func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

// filenameFromURL returns the last path segment of a URL, stripped of any query.
func filenameFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

// isPlaceholder reports whether a URL is DLsite's "no image" placeholder
// (…/no_img_main.gif), which is skipped rather than mirrored/uploaded.
func isPlaceholder(u string) bool { return strings.Contains(u, "no_img_main") }

// mirrorPath is the local path a mirrored image lives at:
// <root>/<workno>/<filename> (the kun-dlsite-api mirror cross-repo contract).
func mirrorPath(root, workno, filename string) string {
	return filepath.Join(root, workno, filename)
}

// isBodyless reports whether a catalog_work is bodyless (site NULL or ”). The
// XOR guard (§8.D): native media rows are written ONLY for bodyless works — a
// claimed work bridges its media at read time and must never get a native row.
func isBodyless(site *string) bool { return site == nil || *site == "" }
