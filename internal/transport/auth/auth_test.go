// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/transport/auth"
)

var testNow = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

// signedInUser seeds a user who can sign in and revoke, and returns their id.
func signedInUser(db *fakeDB) domain.UserID {
	const userID = domain.UserID("user-admin")
	db.seedUser(domain.User{
		ID: userID, Username: "admin", Email: "admin@example.com",
		Status: domain.UserActive, CreatedAt: testNow, UpdatedAt: testNow,
	})
	db.seedPassword(userID, "hunter2")
	db.seedRole(userID, authRole())
	return userID
}

// TestSignInIssuesASession is the whole reason this transport exists: a client
// with nothing but a username and password ends up holding the opaque session
// ref every SessionService call requires (ADR 0061).
func TestSignInIssuesASession(t *testing.T) {
	db := newFakeDB()
	userID := signedInUser(db)
	handler := auth.NewHandler(newTestService(db, testNow))

	res, err := handler.SignIn(context.Background(), connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: "hunter2", DeviceId: "device-1",
	}))
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}

	session := res.Msg.GetSession()
	if session.GetId() == "" {
		t.Fatal("SignIn returned an empty session id")
	}
	if session.GetUserId() != string(userID) {
		t.Errorf("session.user_id = %q, want %q", session.GetUserId(), userID)
	}
	if session.GetDeviceId() != "device-1" {
		t.Errorf("session.device_id = %q, want device-1", session.GetDeviceId())
	}
	if session.GetAuthStrength() != string(domain.AuthStrengthPassword) {
		t.Errorf("session.auth_strength = %q, want %q", session.GetAuthStrength(), domain.AuthStrengthPassword)
	}
	// The timestamps must survive the projection as real values: a client uses
	// expires_at to decide when to re-authenticate, and a zero-valued
	// google.protobuf.Timestamp would read as 1970 and expire it immediately.
	if !session.GetExpiresAt().AsTime().After(testNow) {
		t.Errorf("session.expires_at = %v, want a time after %v", session.GetExpiresAt().AsTime(), testNow)
	}
	if got := session.GetIssuedAt().AsTime(); !got.Equal(testNow) {
		t.Errorf("session.issued_at = %v, want %v", got, testNow)
	}

	// The issued session is real: it is in the store and usable as a caller.
	if _, ok := db.session(domain.SessionID(session.GetId())); !ok {
		t.Error("the issued session was not persisted")
	}
}

// TestSignInFailuresAreUnauthenticated pins the mapping the client depends on
// now that GraphQL's always-200 envelope is gone: a rejected credential is an
// UNAUTHENTICATED status code, not a success carrying an error object.
//
// It also pins the non-enumeration property — an unknown username and a wrong
// password are indistinguishable from outside, in both code and message.
func TestSignInFailuresAreUnauthenticated(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	handler := auth.NewHandler(newTestService(db, testNow))

	wrongPassword := signInError(t, handler, &authv1.SignInRequest{
		Username: "admin", Password: "wrong", DeviceId: "device-1",
	})
	unknownUser := signInError(t, handler, &authv1.SignInRequest{
		Username: "nobody", Password: "hunter2", DeviceId: "device-1",
	})

	for name, err := range map[string]*connect.Error{"wrong password": wrongPassword, "unknown user": unknownUser} {
		if err.Code() != connect.CodeUnauthenticated {
			t.Errorf("%s: code = %v, want %v", name, err.Code(), connect.CodeUnauthenticated)
		}
	}
	if wrongPassword.Message() != unknownUser.Message() {
		t.Errorf("a wrong password (%q) is distinguishable from an unknown user (%q) — sign-in must not reveal which usernames exist",
			wrongPassword.Message(), unknownUser.Message())
	}
}

// TestSignInRejectsAMalformedCommand proves the command's shape validation
// reaches the wire as INVALID_ARGUMENT rather than as an opaque unknown error.
func TestSignInRejectsAMalformedCommand(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	handler := auth.NewHandler(newTestService(db, testNow))

	// A session belongs to a device, so an unnamed device is not signable-in.
	err := signInError(t, handler, &authv1.SignInRequest{Username: "admin", Password: "hunter2"})
	if err.Code() != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want %v", err.Code(), connect.CodeInvalidArgument)
	}
}

// TestSignOutRevokesTheSession completes the round trip: the session SignIn
// issued is revoked, and the store agrees.
func TestSignOutRevokesTheSession(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	handler := auth.NewHandler(newTestService(db, testNow))

	in, err := handler.SignIn(context.Background(), connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: "hunter2", DeviceId: "device-1",
	}))
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	id := in.Msg.GetSession().GetId()

	out, err := handler.SignOut(context.Background(), connect.NewRequest(&authv1.SignOutRequest{
		CallerSession: id, TargetSession: id,
	}))
	if err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if out.Msg.GetSession() != id {
		t.Errorf("SignOut echoed %q, want %q", out.Msg.GetSession(), id)
	}

	session, ok := db.session(domain.SessionID(id))
	if !ok {
		t.Fatal("the session vanished from the store rather than being revoked")
	}
	if !session.Revoked() {
		t.Error("the session is not revoked after SignOut")
	}
}

// TestSignOutWithoutACallerIsUnauthenticated guards the one check this
// transport makes itself. It is here rather than in the command because an
// empty caller is a malformed *request*, and answering it InvalidArgument would
// tell an unauthenticated caller that the field shape was the only problem.
func TestSignOutWithoutACallerIsUnauthenticated(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	handler := auth.NewHandler(newTestService(db, testNow))

	_, err := handler.SignOut(context.Background(), connect.NewRequest(&authv1.SignOutRequest{
		TargetSession: "session-1",
	}))
	if err == nil {
		t.Fatal("SignOut with no caller session succeeded, want Unauthenticated")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want %v", got, connect.CodeUnauthenticated)
	}
}

// signInError asserts SignIn failed and returns the Connect error it failed
// with.
func signInError(t *testing.T, handler *auth.Handler, req *authv1.SignInRequest) *connect.Error {
	t.Helper()
	_, err := handler.SignIn(context.Background(), connect.NewRequest(req))
	if err == nil {
		t.Fatalf("SignIn(%q) succeeded, want an error", req.GetUsername())
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("SignIn returned %T (%v), want a *connect.Error — the category never reached the wire", err, err)
	}
	return ce
}
