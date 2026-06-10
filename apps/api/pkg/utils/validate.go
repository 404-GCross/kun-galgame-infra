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

	// `date_or_empty` accepts "" (no / cleared date) OR a "YYYY-MM-DD" date.
	// Use it for release_date-style fields instead of `datetime=2006-01-02`:
	// on a *string the stdlib `datetime` rejects a non-nil pointer to "",
	// because `omitempty` keys off pointer-nil-ness (!IsNil) rather than the
	// dereferenced value — so a non-nil pointer to "" is NOT skipped and ""
	// then fails the date parse. That blocked editing any galgame with no
	// release date (the FE sends release_date:"" for them). "" must stay a
	// valid input — the write layer maps it to NULL (overlayUpdate /
	// strNonEmpty); see docs/integration/galgame_wiki/01-galgame.md.
	_ = validate.RegisterValidation("date_or_empty", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s == "" {
			return true
		}
		_, err := time.Parse("2006-01-02", s)
		return err == nil
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
