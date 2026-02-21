package tap

import "fmt"

const (
	ErrInvalidRequest = "invalid_request"
	ErrUnauthorized   = "unauthorized"
	ErrNotFound       = "not_found"
	ErrTimeout        = "timeout"
	ErrRateLimited    = "rate_limited"
	ErrExecution      = "execution_error"
	ErrInternalServer = "internal_server_error"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
