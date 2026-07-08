// Package handler exposes the catalog service over Huma (code-first OpenAPI
// 3.1) on Fiber v3, following the artifact service's shape: the house
// {code,message,data} envelope, Fiber-layer auth middleware bridged into the
// Huma context, and Setup functions that cmd/gen-openapi can call with nil
// dependencies to export the spec.
package handler

import (
	"strings"

	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// msgOK is the house success message (matches pkg/response.Success).
const msgOK = "成功"

// Envelope is the house response body {code,message,data} so catalog stays
// wire-compatible with the rest of the ecosystem.
type Envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data,omitempty"`
}

func okEnvelope[T any](data T) Envelope[T] {
	return Envelope[T]{Code: 0, Message: msgOK, Data: data}
}

// houseError is a huma.StatusError that marshals to the house {code,message}
// envelope as application/json (not RFC7807 problem+json). Data is optional —
// set only where an error carries a structured payload (e.g. the owning
// identity of a claim conflict); it is omitted for the ordinary code+message
// errors.
type houseError struct {
	status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *houseError) Error() string { return e.Message }

// GetStatus satisfies huma.StatusError (the HTTP status code).
func (e *houseError) GetStatus() int { return e.status }

// ContentType satisfies huma.ContentTypeFilter so the error body is served
// as application/json rather than application/problem+json.
func (e *houseError) ContentType(string) string { return "application/json" }

// apiErr builds a houseError from an HTTP status + a house error code.
func apiErr(status, code int) *houseError {
	return &houseError{status: status, Code: code, Message: errors.GetMessage(code)}
}

// apiErrMsg is apiErr with an explicit message for dynamic detail.
func apiErrMsg(status, code int, msg string) *houseError {
	return &houseError{status: status, Code: code, Message: msg}
}

// apiErrData is apiErrMsg with a structured payload carried in the envelope's
// data field (used by the claim-conflict 409 to return the owning identity).
func apiErrData(status, code int, msg string, data any) *houseError {
	return &houseError{status: status, Code: code, Message: msg, Data: data}
}

// InstallErrorEnvelope overrides huma.NewError so Huma-internal errors
// (validation, parsing) also come out as the house envelope. Call once at
// startup before registering operations.
func InstallErrorEnvelope() {
	huma.NewError = func(status int, message string, errs ...error) huma.StatusError {
		if len(errs) > 0 {
			parts := make([]string, 0, len(errs)+1)
			if message != "" {
				parts = append(parts, message)
			}
			for _, e := range errs {
				if e != nil {
					parts = append(parts, e.Error())
				}
			}
			message = strings.Join(parts, "; ")
		}
		return &houseError{status: status, Code: statusToCode(status), Message: message}
	}
}

// statusToCode maps an HTTP status to a generic house error code (catalog has
// no service-specific code block; the generic 1-10 range covers its needs).
func statusToCode(status int) int {
	switch status {
	case 400, 422:
		return errors.ErrValidationFailed
	case 401:
		return errors.ErrAuthUnauthorized
	case 403:
		return errors.ErrForbidden
	case 404:
		return errors.ErrNotFound
	default:
		return errors.ErrInternalServer
	}
}
