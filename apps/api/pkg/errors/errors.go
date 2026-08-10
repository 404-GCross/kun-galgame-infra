package errors

import "fmt"

type AppError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("code=%d message=%s error=%v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("code=%d message=%s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

func New(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

func NewWithCode(code int) *AppError {
	return &AppError{Code: code, Message: GetMessage(code)}
}

func Wrap(code int, message string, err error) *AppError {
	return &AppError{Code: code, Message: message, Err: err}
}

func WrapWithCode(code int, err error) *AppError {
	return &AppError{Code: code, Message: GetMessage(code), Err: err}
}

func Is(err error, code int) bool {
	if appErr, ok := err.(*AppError); ok {
		return appErr.Code == code
	}
	return false
}
