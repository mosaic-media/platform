package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
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

func TestCreateLocalUserFollowsCommandBoundaryOrder(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-admin", "user-admin", testNow)
	svc := newTestService(db, tr, testNow, true)

	result, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "session-admin",
		Username:        "new.user",
		Email:           "new.user@example.com",
		DisplayName:     "New User",
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
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	db.mu.Unlock()

	if !persisted {
		t.Fatal("expected new user to be persisted")
	}
	if len(outbox) != 1 {
		t.Fatalf("outbox length = %d, want 1", len(outbox))
	}
	if outbox[0].Type != "user.created" {
		t.Fatalf("outbox event type = %q, want %q", outbox[0].Type, "user.created")
	}

	// Proves the fixed MEG-015 §04 order: authenticate and authorize both
	// happen before the UnitOfWork opens, and the outbox append happens
	// inside the same transaction as the user write.
	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"policy.authorize",
		"uow.begin",
		"users.find_by_username",
		"users.create",
		"outbox.append",
		"uow.committed",
	})
}

func TestCreateLocalUserDeniesWhenPolicyRejects(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-admin", "user-admin", testNow)
	svc := newTestService(db, tr, testNow, false)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "session-admin",
		Username:        "blocked.user",
		Email:           "blocked@example.com",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	db.mu.Lock()
	_, persisted := db.usernames["blocked.user"]
	db.mu.Unlock()
	if persisted {
		t.Fatal("expected no user to be persisted when policy denies")
	}

	// The UnitOfWork must never open when authorization fails.
	assertTrace(t, tr, []string{"sessions.find_by_id", "policy.authorize"})
}

func TestCreateLocalUserRejectsUnauthenticatedSession(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, testNow, true)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "does-not-exist",
		Username:        "new.user",
		Email:           "new.user@example.com",
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
	svc := newTestService(db, tr, testNow, true)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "session-revoked",
		Username:        "new.user",
		Email:           "new.user@example.com",
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
	db.seedSession("session-admin", "user-admin", testNow)
	db.seedUser(domain.User{ID: "user-existing", Username: "taken", Email: "taken@example.com"})
	svc := newTestService(db, tr, testNow, true)

	_, err := svc.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{
		CallerSessionID: "session-admin",
		Username:        "taken",
		Email:           "taken2@example.com",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Conflict)
	}

	// Proves state and outbox events commit atomically: the domain rule
	// failure must leave neither a new user nor an outbox event behind.
	db.mu.Lock()
	userCount := len(db.users)
	outboxLen := len(db.outbox)
	db.mu.Unlock()
	if userCount != 1 {
		t.Fatalf("user count = %d, want 1 (no partial commit)", userCount)
	}
	if outboxLen != 0 {
		t.Fatalf("outbox length = %d, want 0 (no partial commit)", outboxLen)
	}

	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"policy.authorize",
		"uow.begin",
		"users.find_by_username",
		"uow.rolled_back",
	})
}

func TestGetUserByIDReturnsUser(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-admin", "user-admin", testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Email: "target@example.com"})
	svc := newTestService(db, tr, testNow, true)

	result, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: "session-admin",
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
	assertTrace(t, tr, []string{"sessions.find_by_id", "policy.authorize", "users.find_by_id"})
}

func TestGetUserByIDDeniesWhenPolicyRejects(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-admin", "user-admin", testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target"})
	svc := newTestService(db, tr, testNow, false)

	_, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: "session-admin",
		UserID:          "user-target",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	// The store must never be read when authorization fails.
	assertTrace(t, tr, []string{"sessions.find_by_id", "policy.authorize"})
}

func TestGetUserByIDReturnsNotFoundForMissingUser(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-admin", "user-admin", testNow)
	svc := newTestService(db, tr, testNow, true)

	_, err := svc.GetUserByID(context.Background(), app.GetUserByIDQuery{
		CallerSessionID: "session-admin",
		UserID:          "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.NotFound)
	}
}
