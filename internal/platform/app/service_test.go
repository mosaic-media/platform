// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

var testNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func assertTrace(t *testing.T, tr *trace, want []string) {
	t.Helper()
	got := tr.snapshot()
	if len(got) != len(want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace = %v, want %v", got, want)
		}
	}
}

// seedAdminCaller seeds a session and an admin-role grant for the caller
// used throughout these tests to authenticate and authorize commands.
func seedAdminCaller(db *fakeDB, now time.Time) domain.SessionID {
	const adminSessionID = domain.SessionID("session-admin")
	const adminUserID = domain.UserID("user-admin")
	db.seedSession(adminSessionID, adminUserID, now)
	db.seedRole(adminUserID, adminRole())
	return adminSessionID
}

// --- CreateLocalUser ---

func TestCreateLocalUserFollowsCommandBoundaryOrder(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)

	result, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: adminSession,
		Username:        "new.user",
		Email:           "new.user@example.com",
		DisplayName:     "New User",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	if result.User.Username != "new.user" {
		t.Fatalf("result.User.Username = %q, want %q", result.User.Username, "new.user")
	}
	if result.User.ID == "" {
		t.Fatal("expected a generated user ID")
	}

	db.mu.Lock()
	_, persisted := db.usernames["new.user"]
	_, hasCredential := db.passwords[result.User.ID]
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	db.mu.Unlock()

	if !persisted {
		t.Fatal("expected new user to be persisted")
	}
	if !hasCredential {
		t.Fatal("expected a password credential to be persisted alongside the user")
	}
	if len(outbox) != 1 || outbox[0].Type != "user.created" {
		t.Fatalf("outbox = %+v, want exactly one user.created event", outbox)
	}

	// Proves the fixed command order: authenticate and policy
	// evaluation both happen before the UnitOfWork opens, and every write
	// (user, credential, outbox) happens inside that same transaction.
	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"uow.begin",
		"users.find_by_username",
		"users.create",
		"credentials.save_password",
		"outbox.append:user.created",
		"uow.committed",
	})
}

func TestCreateLocalUserDeniedByPolicyDoesNotMutateState(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	// A caller with a valid session but no role grants at all: the real
	// policy.Engine must deny by default.
	db.seedSession("session-nobody", "user-nobody", testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "session-nobody",
		Username:        "blocked.user",
		Email:           "blocked@example.com",
		Password:        "irrelevant",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	// This is the Policy gate: a denied action must not
	// mutate any state.
	db.mu.Lock()
	_, persisted := db.usernames["blocked.user"]
	userCount := len(db.users)
	credentialCount := len(db.passwords)
	outboxLen := len(db.outbox)
	db.mu.Unlock()
	if persisted {
		t.Fatal("expected no user to be persisted when policy denies")
	}
	if userCount != 0 || credentialCount != 0 || outboxLen != 0 {
		t.Fatalf("expected zero state mutation, got users=%d credentials=%d outbox=%d", userCount, credentialCount, outboxLen)
	}

	// The UnitOfWork must never open when authorization fails, and the
	// denial must be audited.
	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"events.publish:authorization.denied",
	})
}

func TestCreateLocalUserRejectsUnauthenticatedSession(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, testNow)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "does-not-exist",
		Username:        "new.user",
		Email:           "new.user@example.com",
		Password:        "irrelevant",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}

	// Policy must never be consulted for a caller that failed authentication.
	assertTrace(t, tr, []string{"sessions.find_by_id"})
}

func TestCreateLocalUserRejectsRevokedSession(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.mu.Lock()
	revokedAt := testNow.Add(-time.Minute)
	db.sessions["session-revoked"] = domain.Session{
		ID:        "session-revoked",
		UserID:    "user-admin",
		IssuedAt:  testNow.Add(-time.Hour),
		ExpiresAt: testNow.Add(time.Hour),
		RevokedAt: &revokedAt,
	}
	db.mu.Unlock()
	svc := newTestService(db, tr, testNow)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "session-revoked",
		Username:        "new.user",
		Email:           "new.user@example.com",
		Password:        "irrelevant",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
	assertTrace(t, tr, []string{"sessions.find_by_id"})
}

func TestCreateLocalUserRejectsDuplicateUsernameAndRollsBack(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedUser(domain.User{ID: "user-existing", Username: "taken", Email: "taken@example.com"})
	svc := newTestService(db, tr, testNow)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: adminSession,
		Username:        "taken",
		Email:           "taken2@example.com",
		Password:        "irrelevant",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Conflict)
	}

	// Proves state and outbox events commit atomically: the domain rule
	// failure must leave neither a new user, credential, nor outbox event
	// behind.
	db.mu.Lock()
	userCount := len(db.users)
	credentialCount := len(db.passwords)
	outboxLen := len(db.outbox)
	db.mu.Unlock()
	if userCount != 1 {
		t.Fatalf("user count = %d, want 1 (no partial commit)", userCount)
	}
	if credentialCount != 0 || outboxLen != 0 {
		t.Fatalf("expected no partial commit, got credentials=%d outbox=%d", credentialCount, outboxLen)
	}

	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"uow.begin",
		"users.find_by_username",
		"uow.rolled_back",
	})
}

// --- AuthenticateLocalUser ---

func TestCreateLocalUserThenAuthenticateSucceeds(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)

	created, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: adminSession,
		Username:        "new.user",
		Email:           "new.user@example.com",
		DisplayName:     "New User",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}

	// A freshly created account still needs a role grant before it can do
	// anything policy-gated, including sign itself in — this slice does
	// not build a role-assignment command, so the test seeds it directly.
	db.seedRole(created.User.ID, adminRole())

	auth, err := svc.AuthenticateLocalUser(context.Background(), app.AuthenticateLocalUserCommand{
		Username: "new.user",
		Password: "correct horse battery staple",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("AuthenticateLocalUser() error = %v", err)
	}
	if auth.Session.UserID != created.User.ID {
		t.Fatalf("auth.Session.UserID = %q, want %q", auth.Session.UserID, created.User.ID)
	}
	if auth.Session.DeviceID != "device-1" {
		t.Fatalf("auth.Session.DeviceID = %q, want %q", auth.Session.DeviceID, "device-1")
	}
	if auth.Session.AuthStrength != domain.AuthStrengthPassword {
		t.Fatalf("auth.Session.AuthStrength = %q, want %q", auth.Session.AuthStrength, domain.AuthStrengthPassword)
	}
	if auth.Session.Revoked() {
		t.Fatal("a freshly issued session must not be revoked")
	}

	db.mu.Lock()
	_, sessionPersisted := db.sessions[auth.Session.ID]
	db.mu.Unlock()
	if !sessionPersisted {
		t.Fatal("expected the issued session to be persisted")
	}
}

func TestAuthenticateLocalUserRejectsWrongPassword(t *testing.T) {
	db := newFakeDB()
	setupTrace := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	setupSvc := newTestService(db, setupTrace, testNow)

	created, err := setupSvc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: adminSession,
		Username:        "new.user",
		Email:           "new.user@example.com",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	db.seedRole(created.User.ID, adminRole())

	// A fresh trace and Service isolate the assertions below to just the
	// login attempt, not the setup above.
	tr := &trace{}
	svc := newTestService(db, tr, testNow)

	_, err = svc.AuthenticateLocalUser(context.Background(), app.AuthenticateLocalUserCommand{
		Username: "new.user",
		Password: "wrong password",
		DeviceID: "device-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}

	db.mu.Lock()
	sessionForUser := false
	for _, session := range db.sessions {
		if session.UserID == created.User.ID {
			sessionForUser = true
		}
	}
	db.mu.Unlock()
	if sessionForUser {
		t.Fatal("expected no session to be issued for a failed login")
	}

	assertTrace(t, tr, []string{
		"users.find_by_username",
		"credentials.find_password",
		"events.publish:authentication.failed",
	})
}

func TestAuthenticateLocalUserRejectsUnknownUsername(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, testNow)

	_, err := svc.AuthenticateLocalUser(context.Background(), app.AuthenticateLocalUserCommand{
		Username: "does.not.exist",
		Password: "irrelevant",
		DeviceID: "device-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

// --- Session issue / validate / revoke ---

func TestSessionIssuedValidatedAndRevoked(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)

	created, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: adminSession,
		Username:        "new.user",
		Email:           "new.user@example.com",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	db.seedRole(created.User.ID, adminRole())

	// Issue: sign in.
	auth, err := svc.AuthenticateLocalUser(context.Background(), app.AuthenticateLocalUserCommand{
		Username: "new.user",
		Password: "correct horse battery staple",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("AuthenticateLocalUser() error = %v", err)
	}

	// Validate: the new session can be used to authenticate a subsequent
	// call (GetUserByID's own authenticate step).
	queried, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: auth.Session.ID,
		UserID:          created.User.ID,
	})
	if err != nil {
		t.Fatalf("GetUserByID() with freshly issued session error = %v", err)
	}
	if queried.User.ID != created.User.ID {
		t.Fatalf("queried.User.ID = %q, want %q", queried.User.ID, created.User.ID)
	}

	// Revoke: an authorized caller revokes the session server-side.
	if _, err := svc.RevokeSession(context.Background(), app.RevokeSessionCommand{
		CallerSessionID: adminSession,
		TargetSessionID: auth.Session.ID,
	}); err != nil {
		t.Fatalf("RevokeSession() error = %v", err)
	}

	db.mu.Lock()
	revokedSession := db.sessions[auth.Session.ID]
	db.mu.Unlock()
	if !revokedSession.Revoked() {
		t.Fatal("expected the session to be marked revoked")
	}

	// A revoked session must fail validation: using it again must be
	// rejected as Unauthenticated, not silently accepted.
	_, err = svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: auth.Session.ID,
		UserID:          created.User.ID,
	})
	if err == nil {
		t.Fatal("expected error using a revoked session, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

func TestRevokeSessionDeniedByPolicyDoesNotMutateState(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	db.seedSession("session-target", "user-target", testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.RevokeSession(context.Background(), app.RevokeSessionCommand{
		CallerSessionID: "session-nobody",
		TargetSessionID: "session-target",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	db.mu.Lock()
	target := db.sessions["session-target"]
	db.mu.Unlock()
	if target.Revoked() {
		t.Fatal("expected the target session to remain unrevoked when policy denies")
	}

	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"events.publish:authorization.denied",
	})
}

// --- GetUserByID ---

func TestGetUserByIDReturnsUser(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Email: "target@example.com"})
	svc := newTestService(db, tr, testNow)

	result, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: adminSession,
		UserID:          "user-target",
	})
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if result.User.Username != "target" {
		t.Fatalf("result.User.Username = %q, want %q", result.User.Username, "target")
	}

	// Proves the query boundary still authenticates and authorizes before
	// reading state, even though it never opens a UnitOfWork.
	assertTrace(t, tr, []string{"sessions.find_by_id", "permissions.roles_for_user", "users.find_by_id"})
}

func TestGetUserByIDDeniesWhenPolicyRejects(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target"})
	svc := newTestService(db, tr, testNow)

	_, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: "session-nobody",
		UserID:          "user-target",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	// The store must never be read when authorization fails.
	assertTrace(t, tr, []string{"sessions.find_by_id", "permissions.roles_for_user", "events.publish:authorization.denied"})
}

func TestGetUserByIDReturnsNotFoundForMissingUser(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: adminSession,
		UserID:          "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.NotFound)
	}
}
