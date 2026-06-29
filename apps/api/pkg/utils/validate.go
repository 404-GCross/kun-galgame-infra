package utils

import (
	"time"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
	// `kun_name` enforces the legacy username rule (see IsValidName /
	// name.go). Use this tag instead of `min=N,max=17` on any user-
	// supplied name field — applies the length bound + char allow-list
	// + invisible-codepoint reject in one go.
	_ = validate.RegisterValidation("kun_name", func(fl validator.FieldLevel) bool {
		return IsValidName(fl.Field().String())
	})

	// `date_or_empty` accepts "" (no / cleared date) OR a partial-precision
	// release date: "YYYY-MM-DD" | "YYYY-MM" | "YYYY". Use it for release_date
	// fields instead of `datetime=2006-01-02`:
	//   - on a *string the stdlib `datetime` rejects a non-nil pointer to "",
	//     because `omitempty` keys off pointer-nil-ness (!IsNil) rather than the
	//     dereferenced value — so a non-nil pointer to "" is NOT skipped and ""
	//     then fails the parse. "" must stay valid ("no / cleared date"); the
	//     write layer maps it to NULL.
	//   - partial precision (month/year) is accepted so editors can record
	//     "2026-06" / "2026"; the write layer normalizes to YYYY-MM-01 / YYYY-01-01
	//     and records release_precision (model.NormalizeReleaseDateInput; see
	//     docs/galgame_wiki/06-release-calendar-design.md §2).
	_ = validate.RegisterValidation("date_or_empty", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s == "" {
			return true
		}
		for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
			if _, err := time.Parse(layout, s); err == nil {
				return true
			}
		}
		return false
	})
}

// Validate validates a struct using the validator
func Validate(s any) error {
	return validate.Struct(s)
}

// GetValidator returns the validator instance
func GetValidator() *validator.Validate {
	return validate
}
