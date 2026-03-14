package response

import (
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

// Response is the standard API response structure
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Success returns a successful response
func Success(c fiber.Ctx, data any) error {
	return c.JSON(Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// SuccessWithMessage returns a successful response with custom message
func SuccessWithMessage(c fiber.Ctx, message string, data any) error {
	return c.JSON(Response{
		Code:    0,
		Message: message,
		Data:    data,
	})
}

// Error returns an error response
func Error(c fiber.Ctx, status int, code int, message string) error {
	return c.Status(status).JSON(Response{
		Code:    code,
		Message: message,
	})
}

// ErrorWithCode returns an error response using default message for the code
func ErrorWithCode(c fiber.Ctx, status int, code int) error {
	return c.Status(status).JSON(Response{
		Code:    code,
		Message: errors.GetMessage(code),
	})
}

// FromAppError returns an error response from AppError
func FromAppError(c fiber.Ctx, status int, err *errors.AppError) error {
	return c.Status(status).JSON(Response{
		Code:    err.Code,
		Message: err.Message,
	})
}

// BadRequest returns a 400 error response
func BadRequest(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusBadRequest, code, message)
}

// Unauthorized returns a 401 error response
func Unauthorized(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusUnauthorized, code, message)
}

// Forbidden returns a 403 error response
func Forbidden(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusForbidden, code, message)
}

// NotFound returns a 404 error response
func NotFound(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusNotFound, code, message)
}

// InternalError returns a 500 error response
func InternalError(c fiber.Ctx, message string) error {
	return Error(c, fiber.StatusInternalServerError, -1, message)
}
