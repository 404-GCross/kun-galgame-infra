package bangumiwiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Conformance against the bangumi/wiki-syntax-spec shared cases
// (testdata/spec, provenance in its README): every valid case must parse to
// the expected shape, every invalid case must error.

// specExpectation mirrors the spec's yaml schema.
type specExpectation struct {
	Type string `yaml:"type"`
	Data []struct {
		Key    string `yaml:"key"`
		Value  string `yaml:"value"`
		Array  bool   `yaml:"array"`
		Values []struct {
			K string `yaml:"k"`
			V string `yaml:"v"`
		} `yaml:"values"`
	} `yaml:"data"`
}

func (e specExpectation) toInfobox() Infobox {
	box := Infobox{Type: e.Type}
	for _, d := range e.Data {
		f := Field{Key: d.Key, Value: d.Value, Array: d.Array, Null: !d.Array && d.Value == ""}
		for _, v := range d.Values {
			f.Items = append(f.Items, Item{Key: v.K, Value: v.V})
		}
		box.Fields = append(box.Fields, f)
	}
	return box
}

func TestSpecValidCases(t *testing.T) {
	files, err := filepath.Glob("testdata/spec/valid/*.wiki")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, wikiPath := range files {
		name := strings.TrimSuffix(filepath.Base(wikiPath), ".wiki")
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(wikiPath)
			require.NoError(t, err)
			rawExpected, err := os.ReadFile(strings.TrimSuffix(wikiPath, ".wiki") + ".yaml")
			require.NoError(t, err)

			var expected specExpectation
			require.NoError(t, yaml.Unmarshal(rawExpected, &expected))

			got, err := Parse(string(input))
			require.NoError(t, err)
			assert.Equal(t, expected.toInfobox(), got)
		})
	}
}

func TestSpecInvalidCases(t *testing.T) {
	files, err := filepath.Glob("testdata/spec/invalid/*.wiki")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, wikiPath := range files {
		name := strings.TrimSuffix(filepath.Base(wikiPath), ".wiki")
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(wikiPath)
			require.NoError(t, err)
			_, err = Parse(string(input))
			assert.Error(t, err)
		})
	}
}
