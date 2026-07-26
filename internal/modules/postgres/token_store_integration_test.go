// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The half of the bearer pair PostgreSQL decides rather than the Platform: the
// conditional UPDATE that makes spending a refresh token exactly-one-winner,
// and the foreign key that takes credentials with the session they belong to.
// Neither can be demonstrated against a fake — a fake would only prove the test
// agrees with itself about what the SQL means.

func tokenFixture(t *testing.T) (*postgres.ContractSet, domain.Session, context.Context) {
	t.Helper()
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var mod postgres.Module
	cs := mod.Bind(pool)
	now := cs.Clock.Now()

	user, err := cs.Users.Create(ctx, domain.User{
		ID: "viewer", Username: "viewer", Email: "viewer@example.com",
		Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	session, err := cs.Sessions.Create(ctx, domain.Session{
		ID: "session-1", UserID: user.ID, DeviceID: "device-1",
		IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour),
		AuthStrength: domain.AuthStrengthPassword,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return cs, session, ctx
}

// Reuse detection rests entirely on this: two refreshes arriving together must
// not both proceed, or one credential hands out two live chains. A read
// followed by a write would let both win, which is why spending is a
// conditional UPDATE and why this is tested concurrently against a real engine.
func TestSpendingARefreshTokenHasExactlyOneWinner(t *testing.T) {
	cs, session, ctx := tokenFixture(t)
	now := cs.Clock.Now()

	if err := cs.Tokens.SaveRefresh(ctx, domain.RefreshToken{
		Hash: "hash-1", SessionID: session.ID, ChainID: "chain-1", DeviceID: session.DeviceID,
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveRefresh: %v", err)
	}

	const racers = 8
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		fails []error
	)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			spent, err := cs.Tokens.MarkRefreshUsed(ctx, "hash-1", now)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, err)
				return
			}
			if spent {
				wins++
			}
		}()
	}
	wg.Wait()

	if len(fails) > 0 {
		t.Fatalf("MarkRefreshUsed errored: %v", fails[0])
	}
	if wins != 1 {
		t.Fatalf("%d of %d concurrent callers spent the same token, want exactly 1", wins, racers)
	}
}

// Revoking a chain must take the access tokens with it, or a thief keeps
// working for the rest of an access token's lifetime — the one window the short
// lifetime exists to bound.
func TestRevokingAChainTakesItsAccessTokensWithIt(t *testing.T) {
	cs, session, ctx := tokenFixture(t)
	now := cs.Clock.Now()

	if err := cs.Tokens.SaveRefresh(ctx, domain.RefreshToken{
		Hash: "refresh-1", SessionID: session.ID, ChainID: "chain-1", DeviceID: session.DeviceID,
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveRefresh: %v", err)
	}
	if err := cs.Tokens.SaveAccess(ctx, domain.AccessToken{
		Hash: "access-1", SessionID: session.ID, IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}

	if err := cs.Tokens.RevokeChain(ctx, "chain-1", now); err != nil {
		t.Fatalf("RevokeChain: %v", err)
	}

	refresh, err := cs.Tokens.FindRefresh(ctx, "refresh-1")
	if err != nil {
		t.Fatalf("FindRefresh: %v", err)
	}
	if !refresh.Revoked() {
		t.Error("the refresh token survived its chain being revoked")
	}
	if _, err := cs.Tokens.FindAccess(ctx, "access-1"); contracts.CategoryOf(err) != contracts.NotFound {
		t.Errorf("the access token survived: err = %v", err)
	}
}

// A token is stored by hash and looked up by hash, so a database read hands
// nobody a usable credential. This is the property the whole storage choice
// rests on, asserted against the real column rather than against a fake's map.
func TestTokensAreOnlyEverStoredByHash(t *testing.T) {
	cs, session, ctx := tokenFixture(t)
	now := cs.Clock.Now()

	if err := cs.Tokens.SaveAccess(ctx, domain.AccessToken{
		Hash: "the-hash", SessionID: session.ID, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	var count int
	if err := cs.Pool.QueryRow(ctx,
		`SELECT count(*) FROM session_access_tokens WHERE token_hash = $1`, "the-hash").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("the token was not stored under its hash (%d rows)", count)
	}
	// The table holds the hash and the session it belongs to, and nothing that
	// could be presented as a credential.
	rows, err := cs.Pool.Query(ctx,
		`SELECT column_name FROM information_schema.columns WHERE table_name = 'session_access_tokens'`)
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "token" || name == "plaintext" || name == "secret" {
			t.Fatalf("session_access_tokens carries a %q column — a plaintext credential at rest", name)
		}
	}
}

// The foreign key is what makes deleting a user or a session leave no orphan
// credential behind, which nothing in Go would enforce.
func TestDeletingASessionTakesItsTokens(t *testing.T) {
	cs, session, ctx := tokenFixture(t)
	now := cs.Clock.Now()

	if err := cs.Tokens.SaveAccess(ctx, domain.AccessToken{
		Hash: "access-1", SessionID: session.ID, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	if _, err := cs.Pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, string(session.ID)); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := cs.Tokens.FindAccess(ctx, "access-1"); contracts.CategoryOf(err) != contracts.NotFound {
		t.Fatalf("an access token outlived its session row: err = %v", err)
	}
}

func TestDeleteExpiredRemovesOnlyWhatIsPast(t *testing.T) {
	cs, session, ctx := tokenFixture(t)
	now := cs.Clock.Now()

	if err := cs.Tokens.SaveAccess(ctx, domain.AccessToken{
		Hash: "stale", SessionID: session.ID, IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	if err := cs.Tokens.SaveAccess(ctx, domain.AccessToken{
		Hash: "live", SessionID: session.ID, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}

	deleted, err := cs.Tokens.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows, want 1", deleted)
	}
	if _, err := cs.Tokens.FindAccess(ctx, "live"); err != nil {
		t.Fatalf("the live token was swept: %v", err)
	}
}

// ListForUser is the device list. It shows what a person can act on, so a
// revoked device is not on it — a list of devices somebody can no longer be
// signed in on is a list of things they cannot end.
func TestListForUserShowsLiveDevicesOnly(t *testing.T) {
	cs, phone, ctx := tokenFixture(t)
	now := cs.Clock.Now()

	if _, err := cs.Sessions.Create(ctx, domain.Session{
		ID: "session-tv", UserID: phone.UserID, DeviceID: "device-tv",
		IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour),
		AuthStrength: domain.AuthStrengthPassword,
	}); err != nil {
		t.Fatalf("seed second session: %v", err)
	}

	live, err := cs.Sessions.ListForUser(ctx, phone.UserID, now)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("%d devices listed, want 2", len(live))
	}

	if err := cs.Sessions.Revoke(ctx, phone.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	live, err = cs.Sessions.ListForUser(ctx, phone.UserID, now)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(live) != 1 || live[0].ID != "session-tv" {
		t.Fatalf("after revoking one device the list is %+v", live)
	}
}

func TestTouchMovesTheIdleClock(t *testing.T) {
	cs, session, ctx := tokenFixture(t)
	later := session.LastSeenAt.Add(3 * time.Hour)

	touched, err := cs.Sessions.Touch(ctx, session.ID, later)
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	// Compared to the microsecond, which is timestamptz's resolution: Go's
	// nanoseconds do not survive the round trip, and an exact comparison here
	// would be a test about PostgreSQL's storage rather than about the touch.
	const tolerance = time.Microsecond
	if d := touched.LastSeenAt.Sub(later.UTC()); d > tolerance || d < -tolerance {
		t.Fatalf("LastSeenAt = %v, want %v", touched.LastSeenAt, later.UTC())
	}
	reread, err := cs.Sessions.FindByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if d := reread.LastSeenAt.Sub(later.UTC()); d > tolerance || d < -tolerance {
		t.Fatalf("the touch was not persisted: %v", reread.LastSeenAt)
	}
}
