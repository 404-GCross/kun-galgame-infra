package service

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
)

type aliasSource struct {
	table        string
	ownerCol     string
	ownerTable   string
	ownerNameCol string
	// live is the row-level suppression predicate, correlated on alias "a", and
	// empty for the families whose alias list is not a registered edit field yet.
	live string
}

var (
	labelAliasSource = aliasSource{
		table: "catalog_label_alias", ownerCol: "label_id",
		ownerTable: "catalog_label", ownerNameCol: "display_name",
	}
	characterAliasSource = aliasSource{
		table: "catalog_character_alias", ownerCol: "character_id",
		ownerTable: "catalog_character", ownerNameCol: "display_name",
		live: editspec.NotSuppressedCharacterAliasSQL("a"),
	}
	creditNameAliasSource = aliasSource{
		table: "catalog_name_alias", ownerCol: "credit_name_id",
		ownerTable: "catalog_credit_name", ownerNameCol: "name",
	}
)

type displayAlias struct {
	Name       string `gorm:"column:name"`
	Lang       string `gorm:"column:lang"`
	Kind       int16  `gorm:"column:kind"`
	IsPrimary  bool   `gorm:"column:is_primary"`
	IsDisplay  bool   `gorm:"column:is_display"`
	Provenance int16  `gorm:"column:provenance"`
}

func (s *PublicService) entityAliases(ctx context.Context, src aliasSource, ownerID int64) ([]displayAlias, error) {
	live := ""
	if src.live != "" {
		live = " AND " + src.live
	}
	q := fmt.Sprintf(`
		SELECT a.name, a.lang, a.kind, a.provenance,
		       a.is_primary_for_locale AS is_primary,
		       (a.name = o.%s) AS is_display
		FROM %s a
		JOIN %s o ON o.id = a.%s
		WHERE a.%s = ? AND a.kind <> ?%s
		ORDER BY a.name, a.id`,
		src.ownerNameCol, src.table, src.ownerTable, src.ownerCol, src.ownerCol, live)

	var rows []displayAlias
	if err := s.db.WithContext(ctx).Raw(q, ownerID, model.AliasKindSearchHint).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

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

func localizedNames(rows []displayAlias) map[string]dto.PublicLocalizedName {
	out := make(map[string]dto.PublicLocalizedName, len(rows))
	best := make(map[string]displayAlias, len(rows))
	for _, r := range rows {
		// A machine-translated name may be listed and searched, but never
		// presented as THE localized name (refs/proj/178 §2: reviewed MT
		// names enter aliases[] only; localized{} stays source-provenance).
		if r.Provenance == model.AliasProvenanceMachine {
			continue
		}
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

func canonicalLocale(lang string) (string, bool) {
	if lang == "" || len(lang) > 35 {
		return "", false
	}
	parts := strings.Split(lang, "-")
	for i, p := range parts {
		if len(p) == 0 || len(p) > 8 || !isASCIIAlphanumeric(p) {
			return "", false
		}
		switch {
		case i == 0:
			if !isASCIIAlpha(p) {
				return "", false
			}
			parts[i] = strings.ToLower(p)
		case len(p) == 4 && isASCIIAlpha(p):
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		case len(p) == 2 && isASCIIAlpha(p), len(p) == 3 && isASCIIDigits(p):
			parts[i] = strings.ToUpper(p)
		default:
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

func aliasBeats(candidate, incumbent displayAlias) bool {
	if candidate.IsPrimary != incumbent.IsPrimary {
		return candidate.IsPrimary
	}
	return candidate.Kind < incumbent.Kind
}

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
