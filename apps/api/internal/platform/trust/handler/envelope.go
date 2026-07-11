// Package handler exposes the trust service over Huma (code-first OpenAPI 3.1)
// on Fiber v3, following the catalog/community shape: the house
// {code,message,data} envelope, Fiber-layer auth bridged into the Huma context,
// and Setup functions cmd/gen-openapi can call with nil dependencies to export
// the specs (S2S intake face + admin review-inbox face).
package handler

import (
	"strings"

	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

const msgOK = "成功"

// Envelope is the house response body {code,message,data}.
type Envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data,omitempty"`
}

func okEnvelope[T any](data T) Envelope[T] {
	return Envelope[T]{Code: 0, Message: msgOK, Data: data}
}

// houseError is a huma.StatusError that marshals to the house {code,message}
// envelope as application/json (not RFC7807).
type houseError struct {
	status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *houseError) Error() string             { return e.Message }
func (e *houseError) GetStatus() int            { return e.status }
func (e *houseError) ContentType(string) string { return "application/json" }

func apiErr(status, code int) *houseError {
	return &houseError{status: status, Code: code, Message: errors.GetMessage(code)}
}

func apiErrMsg(status, code int, msg string) *houseError {
	return &houseError{status: status, Code: code, Message: msg}
}

// InstallErrorEnvelope overrides huma.NewError so Huma-internal errors
// (validation, parsing) also come out as the house envelope.
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
	case 429:
		return errors.ErrOperationFailed
	default:
		return errors.ErrInternalServer
	}
}
