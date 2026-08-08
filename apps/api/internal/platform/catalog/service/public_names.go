package service

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// ── the localized-name lane (wave 191) ───────────────────────────────────────
//
// catalog_character_alias / catalog_name_alias / catalog_label_alias are the
// SAME table three times: (owner, name, lang) unique, kind, latin,
// is_primary_for_locale. That is the standard one-row-per-(entity, name,
// language) model, and it has been populated for a long time — the bangumi zh
// name lane (internal/jobs/bgmzhnames) files Chinese names into all three, and
// the intro-MT glossary already reads them back to keep translated prose
// consistent with the names it mentions.
//
// What was missing was the READ FACE. The label and credit-name records flattened
// the rows to bare strings and deduplicated by spelling, discarding lang; the
// character record published no aliases at all. So a consumer could not answer
// "what is this character called in Chinese?" from the public API even though
// the answer was sitting in the table. PublicNameBuckets made that worse by
// LOOKING like an answer: {"ja": "…"} with no zh key reads as "no Chinese name",
// which was never what it meant.
//
// One loader serves all three because the tables are identical; the two
// projections below then answer two genuinely different questions, which is why
// they treat the display name differently:
//
//   - aliases[]  — "what ELSE is this called?" (also-known-as lines, search-as-
//     you-type). The display name is excluded; spellings are deduplicated.
//   - localized{} — "what is this called in language X?". The display name is
//     NOT excluded: a zh row equal to display_name still truthfully answers the
//     question, and dropping it is exactly how the buckets came to imply that
//     no Chinese name existed.
//
// Search-hint rows (AliasKindSearchHint) are excluded from BOTH: findability-only
// by that kind's own contract, never displayed — e.g. the verbatim DLsite slash
// string the refs/proj/175 heal demoted.

// aliasSource names one of the three alias tables and the owner table it hangs
// off, so the loader below can serve all three. Every value is a compile-time
// constant from the three vars underneath — nothing here is caller-controlled,
// which is what makes the fmt.Sprintf into the query safe.
type aliasSource struct {
	table        string // the alias table
	ownerCol     string // its FK column into the owner table
	ownerTable   string // the owner table
	ownerNameCol string // the owner's display-name column
}

var (
	labelAliasSource = aliasSource{
		table: "catalog_label_alias", ownerCol: "label_id",
		ownerTable: "catalog_label", ownerNameCol: "display_name",
	}
	characterAliasSource = aliasSource{
		table: "catalog_character_alias", ownerCol: "character_id",
		ownerTable: "catalog_character", ownerNameCol: "display_name",
	}
	creditNameAliasSource = aliasSource{
		table: "catalog_name_alias", ownerCol: "credit_name_id",
		ownerTable: "catalog_credit_name", ownerNameCol: "name",
	}
)

// displayAlias is one displayable alias row. Every column carries an explicit
// gorm tag: IsPrimaryForLocale would otherwise be snake-cased into something
// that matches no column, and an untagged ad-hoc struct fails silently by
// scanning the zero value.
type displayAlias struct {
	Name      string `gorm:"column:name"`
	Lang      string `gorm:"column:lang"`
	Kind      int16  `gorm:"column:kind"`
	IsPrimary bool   `gorm:"column:is_primary"`
	IsDisplay bool   `gorm:"column:is_display"`
}

// entityAliases loads one owner's displayable alias rows, ordered (name, id) —
// the order the flat projection has always emitted, kept byte-identical so that
// adding the localized lane cannot perturb aliases[].
func (s *PublicService) entityAliases(ctx context.Context, src aliasSource, ownerID int64) ([]displayAlias, error) {
	q := fmt.Sprintf(`
		SELECT a.name, a.lang, a.kind,
		       a.is_primary_for_locale AS is_primary,
		       (a.name = o.%s) AS is_display
		FROM %s a
		JOIN %s o ON o.id = a.%s
		WHERE a.%s = ? AND a.kind <> ?
		ORDER BY a.name, a.id`,
		src.ownerNameCol, src.table, src.ownerTable, src.ownerCol, src.ownerCol)

	var rows []displayAlias
	if err := s.db.WithContext(ctx).Raw(q, ownerID, model.AliasKindSearchHint).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// flatAliases is the also-known-as projection: display name excluded, the same
// spelling under two langs rendered once, [] when there is nothing to say.
func flatAliases(rows []displayAlias) []string {
	out := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r.IsDisplay {
			continue
		}
		if _, dup := seen[r.Name]; dup {
			continue
		}
		seen[r.Name] = struct{}{}
		out = append(out, r.Name)
	}
	return out
}

// localizedNames is the per-locale projection: at most one entry per language,
// keyed by the BCP-47 tag in canonical casing.
//
// Rows whose lang cannot answer "the name in language X" are skipped — an
// undeclared language, and equally a value that is not a language tag at all.
// Both would hand every consumer a key it must special-case; the row still
// reaches aliases[], where it is only ever a spelling.
//
// Where a locale has several candidates the winner is is_primary_for_locale
// first — the one bit the bangumi lane sets deliberately, at most once per owner
// and never re-elected — then translation over spelling_variant, then the
// (name, id) order the rows arrive in. Never {} vs nil: the map is always
// allocated so the face emits {} rather than null.
func localizedNames(rows []displayAlias) map[string]dto.PublicLocalizedName {
	out := make(map[string]dto.PublicLocalizedName, len(rows))
	best := make(map[string]displayAlias, len(rows))
	for _, r := range rows {
		locale, ok := canonicalLocale(r.Lang)
		if !ok {
			continue
		}
		cur, seen := best[locale]
		if seen && !aliasBeats(r, cur) {
			continue
		}
		best[locale] = r
		out[locale] = dto.PublicLocalizedName{Value: r.Name, Kind: aliasKindKey(r.Kind)}
	}
	return out
}

// canonicalLocale renders a stored lang as the key consumers look up by, and
// reports whether it is a language tag at all.
//
// Two facts from the production tables force this. The first: the stored values
// are NOT canonically cased — `pt-br` sits alongside `zh-Hans`, and a consumer
// asking for `pt-BR` (the only spelling BCP-47 defines) would miss it. Casing is
// the one normalisation that invents nothing: `pt-br` and `pt-BR` are the same
// tag by the standard's own equality rule, so folding them is recording what the
// source said, not guessing at it.
//
// The second: one row's lang is the literal string `日语` — the Chinese word for
// "Japanese", written where a tag belongs. Publishing it verbatim would put a
// key in localized{} that no locale negotiation can ever match, which is not an
// open vocabulary but a leak. A value that is not a tag is treated exactly like
// an undeclared language: it answers no locale.
//
// The grammar checked here is deliberately the shape, not a registry: subtags
// are alphanumeric, 1-8 characters, and the first is alphabetic. Validating
// against the IANA registry would reject legitimately obscure tags a source may
// yet file, and this function's job is to keep non-tags out, not to police which
// languages exist.
func canonicalLocale(lang string) (string, bool) {
	if lang == "" || len(lang) > 35 { // RFC 5646 caps a well-formed tag at 35
		return "", false
	}
	parts := strings.Split(lang, "-")
	for i, p := range parts {
		if len(p) == 0 || len(p) > 8 || !isASCIIAlphanumeric(p) {
			return "", false
		}
		switch {
		case i == 0:
			// The primary subtag is always alphabetic and always lowercase.
			if !isASCIIAlpha(p) {
				return "", false
			}
			parts[i] = strings.ToLower(p)
		case len(p) == 4 && isASCIIAlpha(p):
			// A four-letter subtag is a script: Title case (`Hans`).
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		case len(p) == 2 && isASCIIAlpha(p), len(p) == 3 && isASCIIDigits(p):
			// Two letters or three digits is a region: upper case (`BR`).
			parts[i] = strings.ToUpper(p)
		default:
			// Variants and extensions are lower case.
			parts[i] = strings.ToLower(p)
		}
	}
	return strings.Join(parts, "-"), true
}

func isASCIIAlpha(s string) bool {
	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

func isASCIIDigits(s string) bool {
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isASCIIAlphanumeric(s string) bool {
	for _, c := range []byte(s) {
		alpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !alpha && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// aliasBeats reports whether candidate outranks incumbent for its locale. Ties
// go to the incumbent, which preserves the (name, id) arrival order as the final
// tiebreak and keeps the result deterministic.
func aliasBeats(candidate, incumbent displayAlias) bool {
	if candidate.IsPrimary != incumbent.IsPrimary {
		return candidate.IsPrimary
	}
	return candidate.Kind < incumbent.Kind
}

// aliasKindKey renders the AliasKind* constant as its public string. Search-hint
// never reaches a read face, so it has no public spelling here; an unknown code
// renders as the numeric fallback rather than silently claiming a kind.
func aliasKindKey(kind int16) string {
	switch kind {
	case model.AliasKindTranslation:
		return "translation"
	case model.AliasKindSpellingVariant:
		return "spelling_variant"
	default:
		return fmt.Sprintf("unknown_%d", kind)
	}
}
