package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// The projections in public_names.go are pure functions over alias rows, so the
// per-locale election is pinned here directly rather than through a face: the
// interesting cases (a primary losing to nothing, a translation beating a
// spelling variant, a langless row) are all orderings, and driving them through
// fixtures would hide the rule behind seed data.

func alias(name, lang string, kind int16, primary, display bool) displayAlias {
	return displayAlias{Name: name, Lang: lang, Kind: kind, IsPrimary: primary, IsDisplay: display}
}

func TestFlatAliasesExcludesDisplayAndDedupes(t *testing.T) {
	// Arrives in (name, id) order, as entityAliases guarantees.
	rows := []displayAlias{
		alias("緒方剛志", "ja", model.AliasKindTranslation, false, true), // the display name
		alias("绪方刚", "zh-Hans", model.AliasKindTranslation, false, false),
		alias("绪方刚志", "zh-Hans", model.AliasKindTranslation, true, false),
		alias("绪方刚志", "", model.AliasKindSpellingVariant, false, false), // same spelling, other lang
	}
	got := flatAliases(rows)
	if len(got) != 2 || got[0] != "绪方刚" || got[1] != "绪方刚志" {
		t.Fatalf("flatAliases = %+v, want the two zh spellings in arrival order", got)
	}
}

func TestFlatAliasesEmptyIsNeverNil(t *testing.T) {
	got := flatAliases(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("flatAliases(nil) = %#v, want an allocated empty slice so the face emits []", got)
	}
}

func TestLocalizedNamesElection(t *testing.T) {
	tests := []struct {
		name string
		rows []displayAlias
		want map[string]string // locale → elected value
	}{{
		name: "is_primary_for_locale wins even against an earlier row",
		rows: []displayAlias{
			alias("绪方刚", "zh-Hans", model.AliasKindTranslation, false, false),
			alias("绪方刚志", "zh-Hans", model.AliasKindTranslation, true, false),
		},
		want: map[string]string{"zh-Hans": "绪方刚志"},
	}, {
		name: "with no primary, translation outranks spelling_variant",
		rows: []displayAlias{
			alias("Ogata", "en", model.AliasKindSpellingVariant, false, false),
			alias("Takeshi Ogata", "en", model.AliasKindTranslation, false, false),
		},
		want: map[string]string{"en": "Takeshi Ogata"},
	}, {
		name: "all else equal the arrival order decides, deterministically",
		rows: []displayAlias{
			alias("绪方刚", "zh-Hans", model.AliasKindTranslation, false, false),
			alias("绪方刚志", "zh-Hans", model.AliasKindTranslation, false, false),
		},
		want: map[string]string{"zh-Hans": "绪方刚"},
	}, {
		// The whole point of the field: a row equal to the display name still
		// answers "what is this called in Chinese?" and must NOT be dropped the
		// way aliases[] drops it.
		name: "a value equal to the display name is kept",
		rows: []displayAlias{
			alias("美坂栞", "zh-Hans", model.AliasKindTranslation, false, true),
		},
		want: map[string]string{"zh-Hans": "美坂栞"},
	}, {
		name: "a row with no declared language answers no locale",
		rows: []displayAlias{
			alias("绪方刚志", "", model.AliasKindTranslation, true, false),
		},
		want: map[string]string{},
	}, {
		name: "locales are independent",
		rows: []displayAlias{
			alias("绪方刚志", "zh-Hans", model.AliasKindTranslation, false, false),
			alias("Ogata", "en", model.AliasKindSpellingVariant, false, false),
		},
		want: map[string]string{"zh-Hans": "绪方刚志", "en": "Ogata"},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := localizedNames(tc.rows)
			if len(got) != len(tc.want) {
				t.Fatalf("localized = %+v, want %+v", got, tc.want)
			}
			for locale, want := range tc.want {
				if got[locale].Value != want {
					t.Fatalf("localized[%q] = %q, want %q", locale, got[locale].Value, want)
				}
			}
		})
	}
}

func TestLocalizedNamesEmptyIsNeverNil(t *testing.T) {
	got := localizedNames(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("localizedNames(nil) = %#v, want an allocated empty map so the face emits {}", got)
	}
}

func TestAliasKindKeyNeverInventsAKind(t *testing.T) {
	if got := aliasKindKey(model.AliasKindTranslation); got != "translation" {
		t.Fatalf("translation rendered as %q", got)
	}
	if got := aliasKindKey(model.AliasKindSpellingVariant); got != "spelling_variant" {
		t.Fatalf("spelling_variant rendered as %q", got)
	}
	// An unrecognised code must be visibly unknown rather than silently
	// claiming to be a translation.
	if got := aliasKindKey(99); got != "unknown_99" {
		t.Fatalf("unknown kind rendered as %q, want a visibly unknown value", got)
	}
}
