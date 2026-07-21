// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package sessions_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/sessions"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeIDGenerator struct{ id string }

func (g fakeIDGenerator) NewID() domain.ID { return domain.ID(g.id) }

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

var testNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func TestManagerIssueCreatesSessionWithExpectedFields(t *testing.T) {
	store := newFakeSessionStore()
	manager := sessions.NewManager(fakeClock{now: testNow}, fakeIDGenerator{id: "session-1"})

	session, err := manager.Issue(context.Background(), store, "user-1", "device-1", domain.AuthStrengthPassword)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.ID != "session-1" {
		t.Errorf("session.ID = %q, want %q", session.ID, "session-1")
	}
	if session.UserID != "user-1" {
		t.Errorf("session.UserID = %q, want %q", session.UserID, "user-1")
	}
	if session.DeviceID != "device-1" {
		t.Errorf("session.DeviceID = %q, want %q", session.DeviceID, "device-1")
	}
	if session.AuthStrength != domain.AuthStrengthPassword {
		t.Errorf("session.AuthStrength = %q, want %q", session.AuthStrength, domain.AuthStrengthPassword)
	}
	if !session.IssuedAt.Equal(testNow) {
		t.Errorf("session.IssuedAt = %v, want %v", session.IssuedAt, testNow)
	}
	if !session.ExpiresAt.After(session.IssuedAt) {
		t.Error("session.ExpiresAt must be after IssuedAt")
	}
	if session.Revoked() {
		t.Error("a freshly issued session must not be revoked")
	}
}

func TestManagerValidateAcceptsFreshSession(t *testing.T) {
	store := newFakeSessionStore()
	manager := sessions.NewManager(fakeClock{now: testNow}, fakeIDGenerator{id: "session-1"})
	issued, err := manager.Issue(context.Background(), store, "user-1", "device-1", domain.AuthStrengthPassword)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	validated, err := manager.Validate(context.Background(), store, issued.ID)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if validated.UserID != "user-1" {
		t.Errorf("validated.UserID = %q, want %q", validated.UserID, "user-1")
	}
}

func TestManagerValidateRejectsUnknownSession(t *testing.T) {
	store := newFakeSessionStore()
	manager := sessions.NewManager(fakeClock{now: testNow}, fakeIDGenerator{id: "session-1"})

	_, err := manager.Validate(context.Background(), store, "does-not-exist")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

func TestManagerValidateRejectsExpiredSession(t *testing.T) {
	store := newFakeSessionStore()
	manager := sessions.NewManager(fakeClock{now: testNow}, fakeIDGenerator{id: "session-1"})
	issued, err := manager.Issue(context.Background(), store, "user-1", "device-1", domain.AuthStrengthPassword)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	afterExpiry := sessions.NewManager(fakeClock{now: issued.ExpiresAt.Add(time.Second)}, fakeIDGenerator{})
	_, err = afterExpiry.Validate(context.Background(), store, issued.ID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

func TestManagerRevokeCausesValidateToFail(t *testing.T) {
	store := newFakeSessionStore()
	manager := sessions.NewManager(fakeClock{now: testNow}, fakeIDGenerator{id: "session-1"})
	issued, err := manager.Issue(context.Background(), store, "user-1", "device-1", domain.AuthStrengthPassword)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if err := manager.Revoke(context.Background(), store, issued.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	_, err = manager.Validate(context.Background(), store, issued.ID)
	if err == nil {
		t.Fatal("expected error for revoked session, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}
