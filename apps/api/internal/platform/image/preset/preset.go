// Package preset loads and serves the image-service preset configuration.
// Presets define which output variants are generated for each upload flavor
// (avatar, galgame_banner, topic) and which input MIMEs are allowed.
//
// The main compression pipeline (fit 1920x1080, webp@82, strip EXIF) is
// shared across all presets — presets only control which *extra* variants
// get generated on top of the main image.
package preset

import (
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// FitMode selects how a variant's target dimensions constrain the input.
type FitMode string

const (
	// FitInside keeps aspect ratio, shrinking to fit inside target box.
	FitInside FitMode = "inside"
	// FitCover fills target box, cropping if needed. Used for square
	// avatar thumbnails.
	FitCover FitMode = "cover"
)

// MainPipelineConfig is the global image processing policy. Applies to
// every upload regardless of preset.
type MainPipelineConfig struct {
	FitWidth  int    `yaml:"fit_width"`
	FitHeight int    `yaml:"fit_height"`
	Format    string `yaml:"format"`
	Quality   int    `yaml:"quality"`
	StripEXIF bool   `yaml:"strip_exif"`
}

// VariantSpec describes one output variant to generate from the main image.
type VariantSpec struct {
	Name    string  `yaml:"name"`
	Width   int     `yaml:"width"`
	Height  int     `yaml:"height"`
	Fit     FitMode `yaml:"fit"`
	Format  string  `yaml:"format"`
	Quality int     `yaml:"quality"`
}

// Preset describes one preset (avatar / galgame_banner / topic).
type Preset struct {
	Variants    []VariantSpec `yaml:"variants"`
	AllowedMIME []string      `yaml:"allowed_mime"`
}

// IsMIMEAllowed reports whether the given MIME is in this preset's allow list.
func (p *Preset) IsMIMEAllowed(mime string) bool {
	return slices.Contains(p.AllowedMIME, mime)
}

// Config is the parsed YAML root.
type Config struct {
	MainPipeline MainPipelineConfig `yaml:"main_pipeline"`
	Presets      map[string]Preset  `yaml:"presets"`
}

// Get returns the preset by name, or (zero, false) if not defined.
func (c *Config) Get(name string) (Preset, bool) {
	p, ok := c.Presets[name]
	return p, ok
}

// Load reads and parses the given YAML file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("preset: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("preset: parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("preset: %w", err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.MainPipeline.FitWidth <= 0 || c.MainPipeline.FitHeight <= 0 {
		return fmt.Errorf("main_pipeline.fit_width and fit_height must be > 0")
	}
	if c.MainPipeline.Quality < 1 || c.MainPipeline.Quality > 100 {
		return fmt.Errorf("main_pipeline.quality must be 1..100")
	}
	if c.MainPipeline.Format == "" {
		c.MainPipeline.Format = "webp"
	}
	if len(c.Presets) == 0 {
		return fmt.Errorf("no presets defined")
	}
	for name, p := range c.Presets {
		for i, v := range p.Variants {
			if v.Name == "" {
				return fmt.Errorf("preset %q variant[%d] missing name", name, i)
			}
			if v.Width <= 0 || v.Height <= 0 {
				return fmt.Errorf("preset %q variant %q invalid size %dx%d", name, v.Name, v.Width, v.Height)
			}
			if v.Fit == "" {
				p.Variants[i].Fit = FitCover
			}
			if v.Format == "" {
				p.Variants[i].Format = "webp"
			}
			if v.Quality == 0 {
				p.Variants[i].Quality = 77
			}
		}
	}
	return nil
}
