package errors

import "fmt"

// AppError represents an application error with code and message
type AppError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Error implements the error interface
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("code=%d message=%s error=%v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("code=%d message=%s", e.Code, e.Message)
}

// Unwrap returns the underlying error
func (e *AppError) Unwrap() error {
	return e.Err
}

// New creates a new AppError with code and message
func New(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

// NewWithCode creates a new AppError using the default message for the code
func NewWithCode(code int) *AppError {
	return &AppError{Code: code, Message: GetMessage(code)}
}

// Wrap creates a new AppError wrapping an existing error
func Wrap(code int, message string, err error) *AppError {
	return &AppError{Code: code, Message: message, Err: err}
}

// WrapWithCode creates a new AppError wrapping an existing error using default message
func WrapWithCode(code int, err error) *AppError {
	return &AppError{Code: code, Message: GetMessage(code), Err: err}
}

// Is checks if the error is an AppError with the given code
func Is(err error, code int) bool {
	if appErr, ok := err.(*AppError); ok {
		return appErr.Code == code
	}
	return false
}
