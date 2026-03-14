package utils

import (
	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
}

// Validate validates a struct using the validator
func Validate(s any) error {
	return validate.Struct(s)
}

// GetValidator returns the validator instance
func GetValidator() *validator.Validate {
	return validate
}
