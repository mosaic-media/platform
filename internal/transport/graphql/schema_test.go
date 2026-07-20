// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	graphqllib "github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	graphql "github.com/mosaic-media/mosaic-platform/internal/transport/graphql"
)

var testNow = time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

// exec runs query against a fresh schema built over svc and fails the test
// if the top-level execution reports errors, returning the raw Data map.
func exec(t *testing.T, svc *app.Service, query string) map[string]interface{} {
	t.Helper()
	schema, err := graphql.NewSchema(svc, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	result := graphqllib.Do(graphqllib.Params{Schema: schema, RequestString: query, Context: context.Background()})
	if len(result.Errors) != 0 {
		t.Fatalf("query %s: unexpected errors: %v", query, result.Errors)
	}
	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("query %s: Data is not a map: %#v", query, result.Data)
	}
	return data
}

// execExpectError runs query and returns the first error message, failing
// the test if the query unexpectedly succeeded.
func execExpectError(t *testing.T, svc *app.Service, query string) string {
	t.Helper()
	schema, err := graphql.NewSchema(svc, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	result := graphqllib.Do(graphqllib.Params{Schema: schema, RequestString: query, Context: context.Background()})
	if len(result.Errors) == 0 {
		t.Fatalf("query %s: expected an error, got none (data: %#v)", query, result.Data)
	}
	return result.Errors[0].Message
}

func seedAdmin(db *fakeDB) domain.SessionID {
	const sessionID = domain.SessionID("session-admin")
	const userID = domain.UserID("user-admin")
	db.seedUser(domain.User{ID: userID, Username: "admin", Email: "admin@example.com", Status: domain.UserActive, CreatedAt: testNow, UpdatedAt: testNow})
	db.seedSession(sessionID, userID, testNow)
	db.seedRole(userID, adminRole())
	return sessionID
}

// --- Auth ---

func TestSignInThenSignOut(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	db.mu.Lock()
	db.passwords["user-admin"] = domain.PasswordCredential{UserID: "user-admin", Hash: "insecure-test-hash:hunter2", UpdatedAt: testNow}
	db.mu.Unlock()
	svc := newTestService(db, testNow)

	signIn := exec(t, svc, fmt.Sprintf(`mutation {
		signIn(username: "admin", password: "hunter2", deviceId: "device-1") {
			session { id userId deviceId authStrength }
		}
	}`))
	session := signIn["signIn"].(map[string]interface{})["session"].(map[string]interface{})
	if session["userId"] != "user-admin" {
		t.Fatalf("session.userId = %v, want user-admin", session["userId"])
	}
	if session["deviceId"] != "device-1" {
		t.Fatalf("session.deviceId = %v, want device-1", session["deviceId"])
	}
	sessionID := session["id"].(string)

	signOut := exec(t, svc, fmt.Sprintf(`mutation {
		signOut(callerSessionId: "%s", targetSessionId: "%s") { sessionId }
	}`, adminSession, sessionID))
	if signOut["signOut"].(map[string]interface{})["sessionId"] != sessionID {
		t.Fatalf("signOut result = %+v", signOut["signOut"])
	}
}

func TestSignInWithWrongPasswordSurfacesError(t *testing.T) {
	db := newFakeDB()
	seedAdmin(db)
	db.mu.Lock()
	db.passwords["user-admin"] = domain.PasswordCredential{UserID: "user-admin", Hash: "insecure-test-hash:hunter2", UpdatedAt: testNow}
	db.mu.Unlock()
	svc := newTestService(db, testNow)

	msg := execExpectError(t, svc, `mutation {
		signIn(username: "admin", password: "wrong", deviceId: "device-1") { session { id } }
	}`)
	if msg == "" {
		t.Fatal("expected a non-empty error message")
	}
}

func TestGapFieldsReturnFlaggedNotImplementedErrors(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	svc := newTestService(db, testNow)

	cases := []string{
		fmt.Sprintf(`query { jobs(callerSessionId: "%s") { id } }`, adminSession),
		fmt.Sprintf(`query { job(callerSessionId: "%s", id: "j-1") { id } }`, adminSession),
		fmt.Sprintf(`query { jobLogs(callerSessionId: "%s", jobId: "j-1") { message } }`, adminSession),
		fmt.Sprintf(`mutation { retryJob(callerSessionId: "%s", jobId: "j-1") { id } }`, adminSession),
		fmt.Sprintf(`query { componentHealth(callerSessionId: "%s") { component } }`, adminSession),
		fmt.Sprintf(`query { remoteSignInChallengeStatus(callerSessionId: "%s", challengeId: "c-1") }`, adminSession),
		fmt.Sprintf(`mutation { refreshSession(callerSessionId: "%s", sessionId: "s-1") { id } }`, adminSession),
	}
	for _, query := range cases {
		msg := execExpectError(t, svc, query)
		if msg == "" {
			t.Fatalf("query %s: expected a flagged error message, got empty", query)
		}
	}
}

// --- Users ---

func TestUsersListAndDetail(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Email: "target@example.com", Status: domain.UserActive, CreatedAt: testNow, UpdatedAt: testNow})
	svc := newTestService(db, testNow)

	list := exec(t, svc, fmt.Sprintf(`query { users(callerSessionId: "%s") { id username } }`, adminSession))
	users := list["users"].([]interface{})
	if len(users) != 2 {
		t.Fatalf("len(users) = %d, want 2", len(users))
	}

	detail := exec(t, svc, fmt.Sprintf(`query { user(callerSessionId: "%s", id: "user-target") { username status } }`, adminSession))
	user := detail["user"].(map[string]interface{})
	if user["username"] != "target" || user["status"] != "active" {
		t.Fatalf("user = %+v", user)
	}
}

func TestSetUserStatusMutatesStatus(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	db.seedUser(domain.User{ID: "user-target", Username: "target", Status: domain.UserActive, CreatedAt: testNow, UpdatedAt: testNow})
	svc := newTestService(db, testNow)

	result := exec(t, svc, fmt.Sprintf(`mutation {
		setUserStatus(callerSessionId: "%s", userId: "user-target", status: "suspended") { status }
	}`, adminSession))
	if result["setUserStatus"].(map[string]interface{})["status"] != "suspended" {
		t.Fatalf("setUserStatus result = %+v", result["setUserStatus"])
	}
}

func TestUsersDeniedByPolicySurfacesError(t *testing.T) {
	db := newFakeDB()
	db.seedSession("session-nobody", "user-nobody", testNow)
	svc := newTestService(db, testNow)

	msg := execExpectError(t, svc, `query { users(callerSessionId: "session-nobody") { id } }`)
	if msg == "" {
		t.Fatal("expected a non-empty error message")
	}
}

// --- Permissions ---

func TestPermissionsQueries(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	svc := newTestService(db, testNow)

	roles := exec(t, svc, fmt.Sprintf(`query { rolesForUser(callerSessionId: "%s", userId: "user-admin") { name } }`, adminSession))
	roleList := roles["rolesForUser"].([]interface{})
	if len(roleList) != 1 || roleList[0].(map[string]interface{})["name"] != "Administrator" {
		t.Fatalf("rolesForUser = %+v", roleList)
	}

	grants := exec(t, svc, fmt.Sprintf(`query { grantsForUser(callerSessionId: "%s", userId: "user-admin") { roleId } }`, adminSession))
	grantList := grants["grantsForUser"].([]interface{})
	if len(grantList) != 1 || grantList[0].(map[string]interface{})["roleId"] != "role-admin" {
		t.Fatalf("grantsForUser = %+v", grantList)
	}

	effective := exec(t, svc, fmt.Sprintf(`query { effectivePermissions(callerSessionId: "%s", userId: "user-admin") }`, adminSession))
	perms := effective["effectivePermissions"].([]interface{})
	if len(perms) == 0 {
		t.Fatal("expected a non-empty effective permission list")
	}
}

// --- Configuration ---

func TestConfigurationDraftValidateActivateAndQuery(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	svc := newTestService(db, testNow)

	drafted := exec(t, svc, fmt.Sprintf(`mutation {
		draftConfigVersion(callerSessionId: "%s", payload: "{\"runtime.log_level\":\"debug\"}") { id status }
	}`, adminSession))
	draft := drafted["draftConfigVersion"].(map[string]interface{})
	if draft["status"] != "draft" {
		t.Fatalf("draft.status = %v, want draft", draft["status"])
	}
	versionID := draft["id"].(string)

	validated := exec(t, svc, fmt.Sprintf(`mutation {
		validateConfigVersion(callerSessionId: "%s", configVersionId: "%s") { status }
	}`, adminSession, versionID))
	if validated["validateConfigVersion"].(map[string]interface{})["status"] != "validated" {
		t.Fatalf("validate result = %+v", validated["validateConfigVersion"])
	}

	activated := exec(t, svc, fmt.Sprintf(`mutation {
		activateConfigVersion(callerSessionId: "%s", configVersionId: "%s") {
			activated reloadClass version { status }
		}
	}`, adminSession, versionID))
	activatePayload := activated["activateConfigVersion"].(map[string]interface{})
	if activatePayload["activated"] != true || activatePayload["reloadClass"] != "hot" {
		t.Fatalf("activate result = %+v", activatePayload)
	}

	active := exec(t, svc, fmt.Sprintf(`query { activeConfigVersion(callerSessionId: "%s") { id status } }`, adminSession))
	activeVersion := active["activeConfigVersion"].(map[string]interface{})
	if activeVersion["id"] != versionID || activeVersion["status"] != "active" {
		t.Fatalf("activeConfigVersion = %+v", activeVersion)
	}

	byID := exec(t, svc, fmt.Sprintf(`query { configVersion(callerSessionId: "%s", id: "%s") { status } }`, adminSession, versionID))
	if byID["configVersion"].(map[string]interface{})["status"] != "active" {
		t.Fatalf("configVersion = %+v", byID["configVersion"])
	}
}

func TestActivateConfigVersionDefersGenerationClassChange(t *testing.T) {
	db := newFakeDB()
	adminSession := seedAdmin(db)
	svc := newTestService(db, testNow)

	drafted := exec(t, svc, fmt.Sprintf(`mutation {
		draftConfigVersion(callerSessionId: "%s", payload: "{\"composition.modules\":[\"postgres\"]}") { id }
	}`, adminSession))
	versionID := drafted["draftConfigVersion"].(map[string]interface{})["id"].(string)

	exec(t, svc, fmt.Sprintf(`mutation { validateConfigVersion(callerSessionId: "%s", configVersionId: "%s") { id } }`, adminSession, versionID))

	activated := exec(t, svc, fmt.Sprintf(`mutation {
		activateConfigVersion(callerSessionId: "%s", configVersionId: "%s") { activated reloadClass }
	}`, adminSession, versionID))
	payload := activated["activateConfigVersion"].(map[string]interface{})
	if payload["activated"] != false || payload["reloadClass"] != "generation" {
		t.Fatalf("expected a deferred Generation-class activation, got %+v", payload)
	}
}
