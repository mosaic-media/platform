package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// --- ListUsers ---

func TestListUsersReturnsEveryUser(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedUser(domain.User{ID: "user-a", Username: "alice", Status: domain.UserActive})
	db.seedUser(domain.User{ID: "user-b", Username: "bob", Status: domain.UserActive})
	svc := newTestService(db, tr, testNow)

	result, err := svc.ListUsers(context.Background(), app.ListUsersQuery{CallerSessionID: adminSession})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(result.Users) != 2 {
		t.Fatalf("len(result.Users) = %d, want 2", len(result.Users))
	}
}

func TestListUsersDeniedByPolicy(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.ListUsers(context.Background(), app.ListUsersQuery{CallerSessionID: "session-nobody"})
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}
}

// --- SetUserStatus ---

func TestSetUserStatusSuspendsUser(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Status: domain.UserActive})
	svc := newTestService(db, tr, testNow)

	result, err := svc.SetUserStatus(context.Background(), app.SetUserStatusCommand{
		CallerSessionID: adminSession,
		TargetUserID:    "user-target",
		Status:          domain.UserSuspended,
	})
	if err != nil {
		t.Fatalf("SetUserStatus() error = %v", err)
	}
	if result.User.Status != domain.UserSuspended {
		t.Fatalf("result.User.Status = %q, want %q", result.User.Status, domain.UserSuspended)
	}

	db.mu.Lock()
	stored := db.users["user-target"]
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	db.mu.Unlock()
	if stored.Status != domain.UserSuspended {
		t.Fatalf("persisted status = %q, want %q", stored.Status, domain.UserSuspended)
	}
	if len(outbox) != 1 || outbox[0].Type != "user.status_changed" {
		t.Fatalf("outbox = %+v, want exactly one user.status_changed event", outbox)
	}
}

func TestSetUserStatusRejectsInvalidStatus(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Status: domain.UserActive})
	svc := newTestService(db, tr, testNow)

	_, err := svc.SetUserStatus(context.Background(), app.SetUserStatusCommand{
		CallerSessionID: adminSession,
		TargetUserID:    "user-target",
		Status:          "not-a-real-status",
	})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.InvalidArgument)
	}
}

func TestSetUserStatusDeniedByPolicyDoesNotMutateState(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Status: domain.UserActive})
	svc := newTestService(db, tr, testNow)

	_, err := svc.SetUserStatus(context.Background(), app.SetUserStatusCommand{
		CallerSessionID: "session-nobody",
		TargetUserID:    "user-target",
		Status:          domain.UserSuspended,
	})
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	db.mu.Lock()
	stored := db.users["user-target"]
	db.mu.Unlock()
	if stored.Status != domain.UserActive {
		t.Fatal("expected the target user's status to remain unchanged when policy denies")
	}
}

// --- GetRolesForUser / GetGrantsForUser / GetEffectivePermissions ---

func TestGetRolesForUserReturnsSeededRole(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedRole("user-target", adminRole())
	svc := newTestService(db, tr, testNow)

	result, err := svc.GetRolesForUser(context.Background(), app.GetRolesForUserQuery{
		CallerSessionID: adminSession,
		TargetUserID:    "user-target",
	})
	if err != nil {
		t.Fatalf("GetRolesForUser() error = %v", err)
	}
	if len(result.Roles) != 1 || result.Roles[0].Name != "Administrator" {
		t.Fatalf("result.Roles = %+v, want the seeded Administrator role", result.Roles)
	}
}

func TestGetGrantsForUserDeniedByPolicy(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.GetGrantsForUser(context.Background(), app.GetGrantsForUserQuery{
		CallerSessionID: "session-nobody",
		TargetUserID:    "user-target",
	})
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}
}

func TestGetEffectivePermissionsFlattensAndDedupsRoles(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	db.seedRole("user-target", domain.Role{
		ID: "role-a", Name: "A",
		Permissions: []domain.Permission{"x.read", "x.write"},
	})
	db.seedRole("user-target", domain.Role{
		ID: "role-b", Name: "B",
		Permissions: []domain.Permission{"x.write", "y.read"},
	})
	svc := newTestService(db, tr, testNow)

	result, err := svc.GetEffectivePermissions(context.Background(), app.GetEffectivePermissionsQuery{
		CallerSessionID: adminSession,
		TargetUserID:    "user-target",
	})
	if err != nil {
		t.Fatalf("GetEffectivePermissions() error = %v", err)
	}
	want := []domain.Permission{"x.read", "x.write", "y.read"}
	if len(result.Permissions) != len(want) {
		t.Fatalf("result.Permissions = %v, want %v", result.Permissions, want)
	}
	for i, p := range want {
		if result.Permissions[i] != p {
			t.Fatalf("result.Permissions = %v, want %v", result.Permissions, want)
		}
	}
}
