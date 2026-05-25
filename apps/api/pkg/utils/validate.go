package utils

import (
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
}

// Validate validates a struct using the validator
func Validate(s any) error {
	return validate.Struct(s)
}

// GetValidator returns the validator instance
func GetValidator() *validator.Validate {
	return validate
}
