package errors

import "errors"

var (
	// ErrBadRequest represents a 400 Bad Request error
	ErrBadRequest = errors.New("bad request")
	// ErrNotFound represents a 404 Not Found error
	ErrNotFound = errors.New("not found")
	// ErrInternal represents a 500 Internal Server Error
	ErrInternal = errors.New("internal server error")
)

// APIError is a typed error with an associated HTTP status code.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

// BadRequest constructs a 400 API error.
func BadRequest(message string) *APIError {
	if message == "" {
		message = ErrBadRequest.Error()
	}
	return &APIError{StatusCode: 400, Message: message}
}

// NotFound constructs a 404 API error.
func NotFound(message string) *APIError {
	if message == "" {
		message = ErrNotFound.Error()
	}
	return &APIError{StatusCode: 404, Message: message}
}

// Internal constructs a 500 API error.
func Internal(message string) *APIError {
	if message == "" {
		message = ErrInternal.Error()
	}
	return &APIError{StatusCode: 500, Message: message}
}
