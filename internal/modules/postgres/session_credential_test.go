// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/sessions"
)

// seedCredential mints an access token for a seeded session whose plaintext is
// the session id.
//
// Since ADR 0102 a caller presents an access token rather than a session id, so
// an integration fixture that creates only the session would make every call in
// these tests Unauthenticated. Issuing a credential equal to the id keeps the
// existing call sites — which pass a seeded session id as the caller — saying
// what they always said, while the code under test goes through the real token
// lookup rather than a special case for tests.
//
// It writes through the real store, so a schema or query mistake in the token
// table surfaces here rather than being hidden by an in-memory stand-in.
func seedCredential(t *testing.T, ctx context.Context, cs *postgres.ContractSet, session domain.Session) domain.Session {
	t.Helper()
	if err := cs.Tokens.SaveAccess(ctx, domain.AccessToken{
		Hash:      sessions.HashToken(string(session.ID)),
		SessionID: session.ID,
		IssuedAt:  session.IssuedAt,
		ExpiresAt: session.ExpiresAt,
	}); err != nil {
		t.Fatalf("seed session credential: %v", err)
	}
	return session
}
