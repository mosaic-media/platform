// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package sessions_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/sessions"
)

// The session credential is a bearer pair (platform#58). What is worth testing
// hardest is the half that makes a long-lived credential defensible rather than
// merely convenient: rotation, reuse detection, the device binding, and the two
// expiries sitting one inside the other. A manager that issued tokens and never
// noticed a replay would pass every "does it work" test there is.

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeIDGenerator struct{ n int }

func (g *fakeIDGenerator) NewID() domain.ID {
	g.n++
	return domain.ID("id-" + strconv.Itoa(g.n))
}

type fakeSessionStore struct {
	sessions map[domain.SessionID]domain.Session
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{sessions: make(map[domain.SessionID]domain.Session)}
}

func (s *fakeSessionStore) Create(_ context.Context, session domain.Session) (domain.Session, error) {
	s.sessions[session.ID] = session
	return session, nil
}

func (s *fakeSessionStore) FindByID(_ context.Context, id domain.SessionID) (domain.Session, error) {
	session, ok := s.sessions[id]
	if !ok {
		return domain.Session{}, contracts.NewError(contracts.NotFound, "session not found")
	}
	return session, nil
}

func (s *fakeSessionStore) Revoke(_ context.Context, id domain.SessionID) error {
	session, ok := s.sessions[id]
	if !ok {
		return contracts.NewError(contracts.NotFound, "session not found")
	}
	revokedAt := time.Now()
	session.RevokedAt = &revokedAt
	s.sessions[id] = session
	return nil
}

func (s *fakeSessionStore) Touch(_ context.Context, id domain.SessionID, at time.Time) (domain.Session, error) {
	session, ok := s.sessions[id]
	if !ok {
		return domain.Session{}, contracts.NewError(contracts.NotFound, "session not found")
	}
	session.LastSeenAt = at
	s.sessions[id] = session
	return session, nil
}

func (s *fakeSessionStore) ListForUser(_ context.Context, userID domain.UserID, now time.Time) ([]domain.Session, error) {
	var out []domain.Session
	for _, session := range s.sessions {
		if session.UserID == userID && !session.Revoked() && !session.ExpiredAt(now) {
			out = append(out, session)
		}
	}
	return out, nil
}

// fakeTokenStore holds tokens in memory with the same exactly-one-winner
// spending the conditional UPDATE gives. A fake that let two callers both spend
// one token would make the reuse-detection test pass without testing it.
type fakeTokenStore struct {
	access  map[string]domain.AccessToken
	refresh map[string]domain.RefreshToken
}

func newFakeTokenStore() *fakeTokenStore {
	return &fakeTokenStore{
		access:  map[string]domain.AccessToken{},
		refresh: map[string]domain.RefreshToken{},
	}
}

func (s *fakeTokenStore) SaveAccess(_ context.Context, t domain.AccessToken) error {
	s.access[t.Hash] = t
	return nil
}

func (s *fakeTokenStore) FindAccess(_ context.Context, hash string) (domain.AccessToken, error) {
	t, ok := s.access[hash]
	if !ok {
		return domain.AccessToken{}, contracts.NewError(contracts.NotFound, "access token not found")
	}
	return t, nil
}

func (s *fakeTokenStore) SaveRefresh(_ context.Context, t domain.RefreshToken) error {
	s.refresh[t.Hash] = t
	return nil
}

func (s *fakeTokenStore) FindRefresh(_ context.Context, hash string) (domain.RefreshToken, error) {
	t, ok := s.refresh[hash]
	if !ok {
		return domain.RefreshToken{}, contracts.NewError(contracts.NotFound, "refresh token not found")
	}
	return t, nil
}

func (s *fakeTokenStore) MarkRefreshUsed(_ context.Context, hash string, at time.Time) (bool, error) {
	t, ok := s.refresh[hash]
	if !ok || t.UsedAt != nil || t.RevokedAt != nil {
		return false, nil
	}
	used := at
	t.UsedAt = &used
	s.refresh[hash] = t
	return true, nil
}

func (s *fakeTokenStore) RevokeChain(_ context.Context, chainID domain.ID, at time.Time) error {
	touched := map[domain.SessionID]bool{}
	for hash, t := range s.refresh {
		if t.ChainID != chainID {
			continue
		}
		touched[t.SessionID] = true
		if t.RevokedAt == nil {
			revoked := at
			t.RevokedAt = &revoked
			s.refresh[hash] = t
		}
	}
	for hash, t := range s.access {
		if touched[t.SessionID] {
			delete(s.access, hash)
		}
	}
	return nil
}

func (s *fakeTokenStore) RevokeSession(_ context.Context, id domain.SessionID, at time.Time) error {
	for hash, t := range s.refresh {
		if t.SessionID == id && t.RevokedAt == nil {
			revoked := at
			t.RevokedAt = &revoked
			s.refresh[hash] = t
		}
	}
	for hash, t := range s.access {
		if t.SessionID == id {
			delete(s.access, hash)
		}
	}
	return nil
}

func (s *fakeTokenStore) DeleteExpired(_ context.Context, before time.Time) (int, error) {
	n := 0
	for hash, t := range s.access {
		if t.ExpiresAt.Before(before) {
			delete(s.access, hash)
			n++
		}
	}
	for hash, t := range s.refresh {
		if t.ExpiresAt.Before(before) {
			delete(s.refresh, hash)
			n++
		}
	}
	return n, nil
}

var _ contracts.TokenStore = (*fakeTokenStore)(nil)

var testNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

type harness struct {
	manager  *sessions.Manager
	sessions *fakeSessionStore
	tokens   *fakeTokenStore
	clock    *fakeClock
}

func newHarness() *harness {
	clock := &fakeClock{now: testNow}
	return &harness{
		manager:  sessions.NewManager(clock, &fakeIDGenerator{}),
		sessions: newFakeSessionStore(),
		tokens:   newFakeTokenStore(),
		clock:    clock,
	}
}

func (h *harness) issue(t *testing.T) (domain.Session, domain.TokenPair) {
	t.Helper()
	session, pair, err := h.manager.Issue(context.Background(), h.sessions, h.tokens,
		"user-1", "device-1", domain.AuthStrengthPassword,
		[]domain.Permission{"content.read", "playback.write"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return session, pair
}

func TestIssueCreatesASessionAndItsFirstPair(t *testing.T) {
	h := newHarness()
	session, pair := h.issue(t)

	if session.UserID != "user-1" || session.DeviceID != "device-1" {
		t.Fatalf("session = %+v", session)
	}
	// The capability set is stamped on at issue time (platform#24).
	if len(session.Capabilities) != 2 {
		t.Errorf("session.Capabilities = %v, want the two it was issued with", session.Capabilities)
	}
	// A new session expires at the absolute lifetime.
	if want := testNow.Add(sessions.AbsoluteLifetime); !session.ExpiresAt.Equal(want) {
		t.Fatalf("session.ExpiresAt = %v, want %v", session.ExpiresAt, want)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("Issue returned an incomplete pair")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("the two halves of the pair are the same value")
	}
	if want := testNow.Add(sessions.AccessLifetime); !pair.AccessExpiresAt.Equal(want) {
		t.Fatalf("access expiry = %v, want %v", pair.AccessExpiresAt, want)
	}

	// Only hashes are stored. A database read must not hand anybody a usable
	// credential, and this is the assertion that says so.
	if _, ok := h.tokens.access[pair.AccessToken]; ok {
		t.Fatal("the access token plaintext is in the store")
	}
	if _, ok := h.tokens.access[sessions.HashToken(pair.AccessToken)]; !ok {
		t.Fatal("the access token was not stored under its hash")
	}
	if _, ok := h.tokens.refresh[sessions.HashToken(pair.RefreshToken)]; !ok {
		t.Fatal("the refresh token was not stored under its hash")
	}
}

func TestValidateResolvesAnAccessTokenToItsSession(t *testing.T) {
	h := newHarness()
	session, pair := h.issue(t)

	got, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(pair.AccessToken))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.ID != session.ID {
		t.Fatalf("resolved to %q, want %q", got.ID, session.ID)
	}

	// The session id is not a credential any more, which is the whole point of
	// the pair. Presenting it must authenticate as nothing.
	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(session.ID)); err == nil {
		t.Fatal("the session id was accepted as a credential")
	}
}

func TestAnExpiredAccessTokenIsRefusedWhileItsSessionLivesOn(t *testing.T) {
	h := newHarness()
	_, pair := h.issue(t)

	h.clock.now = testNow.Add(sessions.AccessLifetime + time.Second)
	_, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(pair.AccessToken))
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}

	// And the refresh token still works — the short access lifetime is a
	// rotation cadence, not a sign-out.
	if _, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens,
		pair.RefreshToken, "device-1"); err != nil {
		t.Fatalf("Refresh after the access token expired: %v", err)
	}
}

// A client that keeps refreshing stays signed in far beyond a single day.
func TestASessionSurvivesFarPastTheOldTwentyFourHourLifetime(t *testing.T) {
	h := newHarness()
	_, pair := h.issue(t)

	h.clock.now = testNow.Add(14 * 24 * time.Hour)
	_, next, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, pair.RefreshToken, "device-1")
	if err != nil {
		t.Fatalf("Refresh a fortnight later: %v", err)
	}
	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(next.AccessToken)); err != nil {
		t.Fatalf("the refreshed credential does not work: %v", err)
	}
}

func TestRefreshRotatesAndSpendsTheOldToken(t *testing.T) {
	h := newHarness()
	_, first := h.issue(t)

	h.clock.now = testNow.Add(time.Minute)
	_, second, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, first.RefreshToken, "device-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("the refresh token was not rotated — rotation is the load-bearing part")
	}
	if second.AccessToken == first.AccessToken {
		t.Fatal("the access token was not rotated")
	}
	// The new one works.
	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(second.AccessToken)); err != nil {
		t.Fatalf("the rotated access token does not work: %v", err)
	}
}

// Reuse detection is what makes theft detectable rather than silent, and the
// cost is that the whole chain goes.
func TestARefreshTokenPresentedTwiceRevokesTheWholeChain(t *testing.T) {
	h := newHarness()
	_, first := h.issue(t)

	h.clock.now = testNow.Add(time.Minute)
	_, second, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, first.RefreshToken, "device-1")
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// The replay. The manager reports the compromise rather than acting on it:
	// the exchange runs inside a transaction, so a revocation written here
	// would be rolled back by the very error reporting it. Revoking is the
	// caller's, outside that transaction — see app.RefreshSession.
	h.clock.now = testNow.Add(2 * time.Minute)
	_, _, err = h.manager.Refresh(context.Background(), h.sessions, h.tokens, first.RefreshToken, "device-1")
	if err == nil {
		t.Fatal("a spent refresh token was accepted")
	}
	compromise, ok := sessions.CompromiseOf(err)
	if !ok {
		t.Fatalf("a replay was reported as %v, want a Compromise the caller can act on", err)
	}

	// And when the caller acts on it, the legitimate client's descendant is
	// dead too — there is no way to tell it from the thief's.
	if err := h.tokens.RevokeChain(context.Background(), compromise.ChainID, h.clock.now); err != nil {
		t.Fatalf("RevokeChain: %v", err)
	}
	if _, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, second.RefreshToken, "device-1"); err == nil {
		t.Fatal("the rest of the chain survived a detected replay")
	}
	// The access token the chain issued goes with it, or the thief keeps
	// working for the rest of its lifetime — the one window the short lifetime
	// exists to bound.
	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(second.AccessToken)); err == nil {
		t.Fatal("an access token survived its chain being revoked")
	}
}

func TestARefreshFromAnotherDeviceIsRefusedAndRevokesTheChain(t *testing.T) {
	h := newHarness()
	_, pair := h.issue(t)

	_, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens,
		pair.RefreshToken, "some-other-device")
	if err == nil {
		t.Fatal("a refresh token was honoured from a device it was not issued to")
	}
	compromise, ok := sessions.CompromiseOf(err)
	if !ok {
		t.Fatalf("a device mismatch was reported as %v, want a Compromise", err)
	}

	// The right device does not rescue it once the caller has acted: the
	// credential is compromised, and treating a device mismatch as a retryable
	// mistake would make the binding decorative.
	if err := h.tokens.RevokeChain(context.Background(), compromise.ChainID, h.clock.now); err != nil {
		t.Fatalf("RevokeChain: %v", err)
	}
	if _, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens,
		pair.RefreshToken, "device-1"); err == nil {
		t.Fatal("the chain survived a refresh from the wrong device")
	}
}

// Idle expiry sits inside absolute expiry: a device somebody stopped using
// stops working long before one they use every day.
func TestASessionIdleForTooLongStopsRefreshing(t *testing.T) {
	h := newHarness()
	_, pair := h.issue(t)

	h.clock.now = testNow.Add(sessions.IdleLifetime + time.Hour)
	_, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, pair.RefreshToken, "device-1")
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
	// Still inside the absolute lifetime, which is the point: the two are
	// different questions and the shorter one bit first.
	if h.clock.now.After(testNow.Add(sessions.AbsoluteLifetime)) {
		t.Fatal("the test did not exercise idle expiry — it ran past the absolute lifetime")
	}
}

// And a session in daily use is not timed out by the idle ceiling, because a
// refresh is itself use.
func TestRefreshingKeepsASessionOutOfIdleExpiry(t *testing.T) {
	h := newHarness()
	_, pair := h.issue(t)

	for day := 1; day <= 45; day++ {
		h.clock.now = testNow.Add(time.Duration(day) * 24 * time.Hour)
		_, next, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, pair.RefreshToken, "device-1")
		if err != nil {
			t.Fatalf("refresh on day %d: %v", day, err)
		}
		pair = next
	}
}

// Past the absolute lifetime nothing extends it, however much it was used.
func TestNoRefreshExtendsASessionPastItsAbsoluteExpiry(t *testing.T) {
	h := newHarness()
	session, pair := h.issue(t)

	// Kept in use all the way to the ceiling, because idle expiry would
	// otherwise end it first and this test would pass for the wrong reason.
	for at := 20 * 24 * time.Hour; at < sessions.AbsoluteLifetime; at += 20 * 24 * time.Hour {
		h.clock.now = testNow.Add(at)
		_, next, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, pair.RefreshToken, "device-1")
		if err != nil {
			t.Fatalf("keeping the session in use at %v: %v", at, err)
		}
		pair = next
	}

	// A refresh minted right at the ceiling must not carry a longer expiry
	// than the session it belongs to.
	h.clock.now = testNow.Add(sessions.AbsoluteLifetime - time.Hour)
	_, late, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, pair.RefreshToken, "device-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if late.RefreshExpiresAt.After(session.ExpiresAt) {
		t.Fatalf("a rotation extended the session: %v past %v", late.RefreshExpiresAt, session.ExpiresAt)
	}

	h.clock.now = testNow.Add(sessions.AbsoluteLifetime + time.Hour)
	if _, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens, late.RefreshToken, "device-1"); err == nil {
		t.Fatal("a session was refreshed past its absolute expiry")
	}
}

// Revocation is per device and it takes effect at once, which is what makes a
// bearer pair defensible rather than merely convenient.
func TestRevokingASessionEndsEveryCredentialBehindItImmediately(t *testing.T) {
	h := newHarness()
	session, pair := h.issue(t)

	if err := h.manager.Revoke(context.Background(), h.sessions, h.tokens, session.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(pair.AccessToken)); err == nil {
		t.Fatal("an access token outlived the session it belonged to")
	}
	if _, _, err := h.manager.Refresh(context.Background(), h.sessions, h.tokens,
		pair.RefreshToken, "device-1"); err == nil {
		t.Fatal("a revoked session could still be refreshed")
	}
}

// Revoking one device leaves the others alone — the whole reason revocation is
// per device rather than per account.
func TestRevokingOneDeviceLeavesTheOthersSignedIn(t *testing.T) {
	h := newHarness()
	phone, phonePair := h.issue(t)

	tv, tvPair, err := h.manager.Issue(context.Background(), h.sessions, h.tokens,
		"user-1", "device-tv", domain.AuthStrengthPassword, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := h.manager.Revoke(context.Background(), h.sessions, h.tokens, phone.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(phonePair.AccessToken)); err == nil {
		t.Fatal("the revoked device still works")
	}
	got, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(tvPair.AccessToken))
	if err != nil {
		t.Fatalf("revoking one device ended another: %v", err)
	}
	if got.ID != tv.ID {
		t.Fatalf("resolved to %q, want %q", got.ID, tv.ID)
	}
}

func TestAnEmptyCredentialIsUnauthenticatedRatherThanALookup(t *testing.T) {
	h := newHarness()
	_, err := h.manager.Validate(context.Background(), h.sessions, h.tokens, "")
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

// Every failure is one category, deliberately: the differences are exactly what
// an attacker would like told apart, and a legitimate client does the same
// thing in every case — it refreshes, or it signs in.
func TestEveryRefusalIsUnauthenticated(t *testing.T) {
	h := newHarness()
	_, pair := h.issue(t)

	for name, credential := range map[string]string{
		"unknown token": "not-a-token",
		"session id":    "id-1",
		"empty":         "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
				domain.SessionCredential(credential))
			if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
				t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
			}
		})
	}

	// And the one that should not be refused, so this test cannot pass by
	// refusing everything.
	if _, err := h.manager.Validate(context.Background(), h.sessions, h.tokens,
		domain.SessionCredential(pair.AccessToken)); err != nil {
		t.Fatalf("a valid credential was refused: %v", err)
	}
}
