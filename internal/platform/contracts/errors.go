package contracts

import (
	"errors"
	"fmt"
)

// ErrorCategory is a stable Platform failure category. Every contract
// method that can fail reports one of these categories so application
// services and transports never need to inspect adapter-specific errors
// (MEG-015 §03).
type ErrorCategory string

const (
	// InvalidArgument means the request cannot be accepted as submitted.
	InvalidArgument ErrorCategory = "invalid_argument"
	// Unauthenticated means the caller has no valid session.
	Unauthenticated ErrorCategory = "unauthenticated"
	// PermissionDenied means the caller lacks required permission or attribute.
	PermissionDenied ErrorCategory = "permission_denied"
	// NotFound means the requested resource does not exist or is not visible.
	NotFound ErrorCategory = "not_found"
	// Conflict means state changed or uniqueness was violated.
	Conflict ErrorCategory = "conflict"
	// Unavailable means a required dependency is not currently usable.
	Unavailable ErrorCategory = "unavailable"
	// Internal means an unexpected Platform or adapter failure occurred.
	Internal ErrorCategory = "internal"
)

// Error is the Platform contract error type. Adapters may retain
// driver-specific errors internally, but application services and
// transports must only ever see Error values (MEG-015 §03).
type Error struct {
	Category ErrorCategory
	Message  string
	Err      error
}

// NewError constructs a categorized Platform error with no wrapped cause.
func NewError(category ErrorCategory, message string) *Error {
	return &Error{Category: category, Message: message}
}

// WrapError constructs a categorized Platform error that wraps err. Wrapped
// errors remain discoverable through errors.Is and errors.As (MEG-001 §08).
func WrapError(category ErrorCategory, message string, err error) *Error {
	return &Error{Category: category, Message: message, Err: err}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap exposes the wrapped cause, if any, to errors.Is and errors.As.
func (e *Error) Unwrap() error {
	return e.Err
}

// CategoryOf reports the Platform error category carried by err. It returns
// Internal when err does not carry a Platform category, so callers always
// receive a stable category rather than having to nil-check or type-assert.
func CategoryOf(err error) ErrorCategory {
	var platformErr *Error
	if errors.As(err, &platformErr) {
		return platformErr.Category
	}
	return Internal
}
