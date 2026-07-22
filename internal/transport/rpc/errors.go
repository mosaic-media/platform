// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package rpc is the plumbing every Connect transport shares: the mapping from
// the Platform's error categories onto status codes (this file) and the
// telemetry interceptor that seeds a request's trace (telemetry.go).
//
// It exists because ADR 0061 made Connect the only client transport. With one
// transport family there is exactly one place a category becomes a status code
// and one place a call is instrumented, so both belong here rather than being
// restated by each service that mounts a handler.
//
// This file's job — carrying an error category to the client — is one GraphQL
// did poorly: a GraphQL execution returns HTTP 200 whatever happened, so the
// category had to travel in an `extensions` bag the handler never actually
// populated. On a typed transport the status code *is* the category.
//
// The mapping is total by construction: contracts.ErrorCategory is a closed
// vocabulary (ADR 0015's "does Platform code branch on it?" test — it does), so
// every category has exactly one code and an unrecognised value is Internal.
package rpc

import (
	"errors"

	"connectrpc.com/connect"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Code returns the Connect code for a Platform error category.
//
// Two of these are worth stating rather than reading off the table. Conflict is
// CodeAlreadyExists rather than CodeAborted because the Platform raises it for
// a uniqueness or referential violation, not a failed optimistic retry — the
// caller should not retry it unchanged. Unavailable is the one code a client is
// invited to retry, and is what the transports return for a surface that is
// wired but has nothing behind it yet.
func Code(category contracts.ErrorCategory) connect.Code {
	switch category {
	case contracts.InvalidArgument:
		return connect.CodeInvalidArgument
	case contracts.Unauthenticated:
		return connect.CodeUnauthenticated
	case contracts.PermissionDenied:
		return connect.CodePermissionDenied
	case contracts.NotFound:
		return connect.CodeNotFound
	case contracts.Conflict:
		return connect.CodeAlreadyExists
	case contracts.Unavailable:
		return connect.CodeUnavailable
	default:
		return connect.CodeInternal
	}
}

// Wrap converts a Platform error into a Connect error carrying the matching
// code. It returns nil for a nil error and passes an error that is already a
// *connect.Error through untouched, so a transport that built its own status
// (a malformed request, say) is not re-categorised on the way out.
//
// The message is carried verbatim. Platform errors are written by the Platform
// for a person to read, and every category above is a condition the caller is
// entitled to be told about — which is exactly why AuthenticateLocalUser
// collapses "no such user" and "wrong password" into one Unauthenticated
// "invalid credentials" at the command boundary rather than here.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	return connect.NewError(Code(contracts.CategoryOf(err)), err)
}
