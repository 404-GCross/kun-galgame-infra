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
		Message: "成功",
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

// Error returns an error response with custom message
func Error(c fiber.Ctx, status int, code int, message string) error {
	return c.Status(status).JSON(Response{
		Code:    code,
		Message: message,
	})
}

// ErrorCode returns an error response using default message for the code
func ErrorCode(c fiber.Ctx, status int, code int) error {
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

// ---- 便捷函数：使用错误码自动获取消息 ----

// BadRequest returns a 400 error response (使用错误码)
func BadRequest(c fiber.Ctx, code int) error {
	return ErrorCode(c, fiber.StatusBadRequest, code)
}

// BadRequestMsg returns a 400 error response (自定义消息)
func BadRequestMsg(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusBadRequest, code, message)
}

// Unauthorized returns a 401 error response (使用错误码)
func Unauthorized(c fiber.Ctx, code int) error {
	return ErrorCode(c, fiber.StatusUnauthorized, code)
}

// UnauthorizedMsg returns a 401 error response (自定义消息)
func UnauthorizedMsg(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusUnauthorized, code, message)
}

// Forbidden returns a 403 error response (使用错误码)
func Forbidden(c fiber.Ctx, code int) error {
	return ErrorCode(c, fiber.StatusForbidden, code)
}

// ForbiddenMsg returns a 403 error response (自定义消息)
func ForbiddenMsg(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusForbidden, code, message)
}

// NotFound returns a 404 error response (使用错误码)
func NotFound(c fiber.Ctx, code int) error {
	return ErrorCode(c, fiber.StatusNotFound, code)
}

// NotFoundMsg returns a 404 error response (自定义消息)
func NotFoundMsg(c fiber.Ctx, code int, message string) error {
	return Error(c, fiber.StatusNotFound, code, message)
}

// TooManyRequests returns a 429 error response
func TooManyRequests(c fiber.Ctx) error {
	return ErrorCode(c, fiber.StatusTooManyRequests, errors.ErrTooManyRequests)
}

// InternalError returns a 500 error response
func InternalError(c fiber.Ctx, code int) error {
	return ErrorCode(c, fiber.StatusInternalServerError, code)
}

// InternalErrorMsg returns a 500 error response (自定义消息)
func InternalErrorMsg(c fiber.Ctx, message string) error {
	return Error(c, fiber.StatusInternalServerError, errors.ErrInternalServer, message)
}
