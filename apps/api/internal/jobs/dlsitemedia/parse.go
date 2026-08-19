package dlsitemedia

import (
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// Work-level provisional stamp for brand-new rows only (OnConflict DoNothing);
// sync-image-grades refines machine-source rows to per-image grades nightly.
func ageToSexual(age string) int16 {
	switch strings.TrimSpace(age) {
	case "3":
		return 2
	case "2":
		return 1
	default:
		return 0
	}
}

type pageParts struct {
	Parts []struct {
		Text  string `json:"text"`
		Items []struct {
			Text string `json:"text"`
		} `json:"items"`
	} `json:"parts"`
}

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

type productMedia struct {
	ImageMain    imageObj        `json:"image_main"`
	ImageSamples json.RawMessage `json:"image_samples"`
}

type imageObj struct {
	URL string `json:"url"`
}

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

func isPlaceholder(u string) bool { return strings.Contains(u, "no_img_main") }

func mirrorPath(root, workno, filename string) string {
	return filepath.Join(root, workno, filename)
}

func isBodyless(site *string) bool { return site == nil || *site == "" }
