package dispatcher

import (
	"errors"
	"fmt"
)

type dispatchErrorType string

const (
	errorTypeTransient dispatchErrorType = "transient"
	errorTypePermanent dispatchErrorType = "permanent"
)

// DispatchError classifies dispatch failures so callers can decide retry behavior.
type DispatchError struct {
	errType    dispatchErrorType
	message    string
	cause      error
	statusCode int
}

func (e *DispatchError) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch {
	case e.cause != nil && e.statusCode > 0:
		return fmt.Sprintf("%s (status=%d): %v", e.message, e.statusCode, e.cause)
	case e.cause != nil:
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	case e.statusCode > 0:
		return fmt.Sprintf("%s (status=%d)", e.message, e.statusCode)
	default:
		return e.message
	}
}

func (e *DispatchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newDispatchError(errType dispatchErrorType, message string, cause error, statusCode int) *DispatchError {
	return &DispatchError{
		errType:    errType,
		message:    message,
		cause:      cause,
		statusCode: statusCode,
	}
}

// NewTransientError creates a retryable dispatch error.
func NewTransientError(message string, cause error) error {
	return newDispatchError(errorTypeTransient, message, cause, 0)
}

// NewPermanentError creates a non-retryable dispatch error.
func NewPermanentError(message string, cause error) error {
	return newDispatchError(errorTypePermanent, message, cause, 0)
}

// IsTransient reports whether an error should trigger a retry.
func IsTransient(err error) bool {
	var dispatchErr *DispatchError
	if !errors.As(err, &dispatchErr) {
		return false
	}
	return dispatchErr.errType == errorTypeTransient
}
