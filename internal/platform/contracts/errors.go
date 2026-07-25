// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"errors"
	"fmt"
)

// ErrorCategory is a stable Platform failure category. Every contract
// method that can fail reports one of these categories so application
// services and transports never need to inspect adapter-specific errors.
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
// transports must only ever see Error values.
type Error struct {
	Category ErrorCategory
	Message  string
	Err      error
	// Fields names which submitted fields were rejected and why (ADR 0089).
	//
	// It is on the error rather than beside it because a rejection *is* the
	// error — a command that answers "that username is taken" in a separate
	// channel leaves every caller to remember to look, and the one that forgets
	// turns a per-field rejection into a generic failure with no clue in it.
	//
	// Empty for every error that is not about a submission, which is nearly all
	// of them. A transport that finds it non-empty pushes it to the fields; one
	// that does not know about it is unaffected.
	Fields []FieldRejection
}

// FieldRejection is one submitted field the Platform refused, in the same shape
// the client's own validators produce (mosaic.session.v1.FieldError). Symmetric
// on purpose: a rejection from either side must render in the same place, or a
// screen tells you where the problem is in two different ways.
type FieldRejection struct {
	// Field is the field's name in the submitting form's scope — the same name
	// the input that wrote it declares.
	Field   string
	Message string
}

// RejectFields builds an InvalidArgument carrying per-field rejections.
func RejectFields(message string, fields ...FieldRejection) *Error {
	return &Error{Category: InvalidArgument, Message: message, Fields: fields}
}

// NewError constructs a categorized Platform error with no wrapped cause.
func NewError(category ErrorCategory, message string) *Error {
	return &Error{Category: category, Message: message}
}

// WrapError constructs a categorized Platform error that wraps err. Wrapped
// errors remain discoverable through errors.Is and errors.As.
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
