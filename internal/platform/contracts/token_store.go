// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// TokenStore persists the two halves of a session's bearer pair (platform#58).
//
// It is on Tx as well as reachable directly, and both are load-bearing.
// Directly, because validating an access token happens on every call and must
// not open a transaction. On Tx, because issuing a pair and creating the
// session it belongs to have to commit together — a session with no tokens is a
// row nobody can use, and tokens with no session are a credential pointing at
// nothing.
type TokenStore interface {
	// SaveAccess stores an issued access token by hash.
	SaveAccess(ctx context.Context, token domain.AccessToken) error
	// FindAccess resolves a hash to its token, NotFound when there is none.
	FindAccess(ctx context.Context, hash string) (domain.AccessToken, error)

	// SaveRefresh stores an issued refresh token by hash.
	SaveRefresh(ctx context.Context, token domain.RefreshToken) error
	// FindRefresh resolves a hash to its token, NotFound when there is none.
	FindRefresh(ctx context.Context, hash string) (domain.RefreshToken, error)
	// MarkRefreshUsed records that a token was exchanged, and reports whether
	// it was this call that spent it.
	//
	// The boolean is the reuse detection, and it has to be answered by the
	// **store** rather than by a read followed by a write: two refreshes
	// arriving together would both read an unspent token and both proceed,
	// which is a race that hands out two live chains from one credential. A
	// conditional update makes exactly one of them the winner.
	MarkRefreshUsed(ctx context.Context, hash string, at time.Time) (spentHere bool, err error)

	// RevokeChain revokes every unspent token in a chain and every access
	// token of the sessions behind it. This is what a detected replay costs:
	// the whole chain, because there is no way to tell the thief's descendant
	// from the legitimate client's.
	RevokeChain(ctx context.Context, chainID domain.ID, at time.Time) error
	// RevokeSession revokes every token belonging to one session — what
	// signing out, and ending a device from another device, both do.
	RevokeSession(ctx context.Context, id domain.SessionID, at time.Time) error

	// DeleteExpired removes tokens that are past their lifetime, returning how
	// many rows went. Access tokens are minutes-lived and issued on every
	// refresh, so this table grows faster than anything else in the schema and
	// nothing else will ever clean it.
	DeleteExpired(ctx context.Context, before time.Time) (int, error)
}
