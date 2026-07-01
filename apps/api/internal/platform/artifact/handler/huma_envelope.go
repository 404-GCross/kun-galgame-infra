package handler

import (
	"strings"

	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// msgOK is the house success message (matches pkg/response.Success).
const msgOK = "成功"

// Envelope is the house response body {code,message,data}, matching
// pkg/response.Response so artifact-on-Huma stays wire-compatible with the rest
// of the ecosystem (oauth/image/wiki/forum/moyu). See docs/artifact/10.
type Envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data,omitempty"`
}

// okEnvelope builds a success envelope (code 0, message 成功).
func okEnvelope[T any](data T) Envelope[T] {
	return Envelope[T]{Code: 0, Message: msgOK, Data: data}
}

// houseError is a huma.StatusError that marshals to the house {code,message}
// envelope as application/json, instead of Huma's default RFC7807
// (application/problem+json). It carries the HTTP status separately.
type houseError struct {
	status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *houseError) Error() string { return e.Message }

// GetStatus satisfies huma.StatusError (the HTTP status code).
func (e *houseError) GetStatus() int { return e.status }

// ContentType satisfies huma.ContentTypeFilter so the error body is served as
// application/json rather than application/problem+json.
func (e *houseError) ContentType(string) string { return "application/json" }

// apiErr builds a houseError from an HTTP status + a house error code, using the
// code's registered message.
func apiErr(status, code int) *houseError {
	return &houseError{status: status, Code: code, Message: errors.GetMessage(code)}
}

// apiErrMsg is apiErr with an explicit message — for cases that carry dynamic
// detail (e.g. reclaim conflicts reporting the idle window).
func apiErrMsg(status, code int, msg string) *houseError {
	return &houseError{status: status, Code: code, Message: msg}
}

// InstallErrorEnvelope overrides huma.NewError so Huma-internal errors
// (request validation, body parsing, etc.) also come out as the house envelope.
// Call once at startup before registering operations.
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

// statusToCode maps an HTTP status to a house error code for errors Huma raises
// itself (where there's no explicit business code).
func statusToCode(status int) int {
	switch status {
	case 400, 422:
		return errors.ErrArtifactBadRequest
	case 401:
		return errors.ErrArtifactUnauthorized
	case 403:
		return errors.ErrArtifactForbidden
	case 404:
		return errors.ErrArtifactNotFound
	case 413:
		return errors.ErrArtifactTooBig
	case 429:
		return errors.ErrArtifactQuotaExceeded
	default:
		return errors.ErrArtifactStoreFailed
	}
}
