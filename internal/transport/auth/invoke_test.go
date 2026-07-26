// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/transport/auth"
)

// The pre-session action lane (ADR 0101's lane, carrying ADR 0098's claim).
//
// What is worth testing hardest here is not that a claim works — a handler that
// created a user would also do that — but the properties that would be
// catastrophic to lose: a claimed server refuses a second claim, a refusal
// lands on the fields it came from rather than in a sentence beside them, and
// signing in does not become a way to learn which usernames exist.

func invoke(t *testing.T, h *auth.Handler, action string, input any) (*authv1.InvokeResponse, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	res, err := h.Invoke(context.Background(), connect.NewRequest(&authv1.InvokeRequest{
		Action: action, Input: raw, DeviceId: "device-1",
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// A well-formed claim on an empty server creates the owner and signs them in,
// which is the whole of ADR 0098's decision: the person who claimed the server
// is signed in by the act of claiming it.
func TestClaimingAnUnclaimedServerCreatesTheOwnerAndSignsThemIn(t *testing.T) {
	db := newFakeDB()
	h := auth.NewHandler(newTestService(db, testNow))

	msg, err := invoke(t, h, "claimServer", map[string]any{
		"serverName":      "The living room box",
		"displayName":     "Alex",
		"username":        "alex",
		"password":        "correct horse",
		"confirmPassword": "correct horse",
	})
	if err != nil {
		t.Fatalf("claimServer: %v", err)
	}

	signed := msg.GetSigned()
	if signed == nil {
		t.Fatalf("claiming did not sign anyone in: %+v", msg.GetOutcome())
	}
	if signed.GetTokens().GetAccessToken() == "" || signed.GetTokens().GetRefreshToken() == "" {
		t.Error("the claim returned a session with no bearer pair, so the client holds nothing")
	}

	// The owner holds everything, because it is the root of every other grant
	// (ADR 0069): an authority withheld here could never be given to anybody.
	caps := signed.GetSession().GetCapabilities()
	if len(caps) == 0 {
		t.Fatal("the owner's session carries no capability set")
	}
	for _, want := range []string{"user.create", "role.create", "role.grant", "content.read"} {
		if !containsString(caps, want) {
			t.Errorf("the owner's session does not carry %q", want)
		}
	}
}

// A second claim is a Conflict, not a second owner. It answers with the door
// rather than with an error, because the person looking at a stale setup page
// needs somewhere to go and "conflict" is not it.
func TestASecondClaimIsRefusedAndAnswersWithTheDoor(t *testing.T) {
	db := newFakeDB()
	h := auth.NewHandler(newTestService(db, testNow))

	claim := map[string]any{
		"serverName": "One", "username": "first",
		"password": "correct horse", "confirmPassword": "correct horse",
	}
	if _, err := invoke(t, h, "claimServer", claim); err != nil {
		t.Fatalf("the first claim failed: %v", err)
	}

	claim["username"] = "second"
	msg, err := invoke(t, h, "claimServer", claim)
	if err != nil {
		t.Fatalf("the second claim errored rather than answering: %v", err)
	}
	if msg.GetSigned() != nil {
		t.Fatal("a second claim produced a second owner")
	}
	door := msg.GetDoorway()
	if door == nil {
		t.Fatalf("a refused claim answered with %+v rather than a door", msg.GetOutcome())
	}
	if got := treeText(door.GetUiNode()); !strings.Contains(got, "Sign in") {
		t.Errorf("the door served after a refused claim is not the sign-in one: %s", got)
	}
	if len(door.GetDefinitions()) == 0 {
		t.Error("the replacement door carries no definitions, so it would draw as placeholders")
	}
}

// contracts.RejectFields has been routed and never called since validation
// landed. This is the first screen that produces one, so this is the test that
// it arrives where it belongs.
func TestAClaimIsRefusedOnTheFieldsThatWereWrong(t *testing.T) {
	h := auth.NewHandler(newTestService(newFakeDB(), testNow))

	msg, err := invoke(t, h, "claimServer", map[string]any{
		"serverName":      "",
		"username":        "two words",
		"password":        "short",
		"confirmPassword": "short",
	})
	if err != nil {
		t.Fatalf("a rejected claim errored rather than answering: %v", err)
	}
	errs := msg.GetFieldErrors()
	if errs == nil {
		t.Fatalf("a rejected claim answered with %+v rather than field errors", msg.GetOutcome())
	}

	got := map[string]string{}
	for _, f := range errs.GetErrors() {
		got[f.GetField()] = f.GetMessage()
	}
	for _, field := range []string{"serverName", "username", "password"} {
		if got[field] == "" {
			t.Errorf("nothing was said about %q, so the form cannot mark it", field)
		}
	}
	// The confirmation is not complained about while the password itself is
	// refused: two complaints about one mistake is how a four-field form comes
	// back looking entirely wrong.
	if _, ok := got["confirmPassword"]; ok {
		t.Error("the confirmation was rejected alongside a password that was itself refused")
	}
}

// A wrong password is one message on the form, never a mark on a field.
// Attaching it to "username" would say a username exists as surely as a
// sentence would, which is exactly the enumeration AuthenticateLocalUser
// collapses its errors to prevent.
func TestASignInRefusalDoesNotSayWhichHalfWasWrong(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	h := auth.NewHandler(newTestService(db, testNow))

	wrongPassword, err := invoke(t, h, "signIn", map[string]any{
		"username": "admin", "password": "not it",
	})
	if err != nil {
		t.Fatalf("a refused sign-in errored rather than answering: %v", err)
	}
	noSuchUser, err := invoke(t, h, "signIn", map[string]any{
		"username": "nobody", "password": "not it",
	})
	if err != nil {
		t.Fatalf("a refused sign-in errored rather than answering: %v", err)
	}

	for _, msg := range []*authv1.InvokeResponse{wrongPassword, noSuchUser} {
		errs := msg.GetFieldErrors()
		if errs == nil {
			t.Fatalf("a refused sign-in answered with %+v", msg.GetOutcome())
		}
		if len(errs.GetErrors()) != 0 {
			t.Errorf("the refusal marked %d field(s); it must mark none", len(errs.GetErrors()))
		}
		if errs.GetFormError() == "" {
			t.Error("the refusal said nothing at all")
		}
	}
	if wrongPassword.GetFieldErrors().GetFormError() != noSuchUser.GetFieldErrors().GetFormError() {
		t.Error("a wrong password and an unknown username are told apart, which enumerates accounts")
	}
}

// Signing in through the door is the same command the direct RPC calls, so it
// must mint the same thing — including the capability set, which is what a
// client gates its affordances on (ADR 0036).
func TestSigningInThroughTheDoorMintsASessionWithItsCapabilities(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	h := auth.NewHandler(newTestService(db, testNow))

	msg, err := invoke(t, h, "signIn", map[string]any{"username": "admin", "password": "hunter2"})
	if err != nil {
		t.Fatalf("signIn: %v", err)
	}
	signed := msg.GetSigned()
	if signed == nil {
		t.Fatalf("the door did not sign anyone in: %+v", msg.GetOutcome())
	}
	if signed.GetSession().GetUserId() == "" {
		t.Error("the minted session names no user")
	}
	if len(signed.GetSession().GetCapabilities()) == 0 {
		t.Error("the minted session carries no capability set, so a client cannot gate anything")
	}
}

// The stream source the household picked is installed, and the repository it
// comes from is the Platform's answer rather than the caller's.
//
// **This is the test the browser wrote.** The repository was resolved after the
// claim committed, from a catalogue read that is deliberately gated on the
// server still being claimable — so it answered "Mosaic no longer offers
// aiostreams" on the very claim that had just chosen it. Every test passed:
// each one either had no extension manager or never reached the install.
func TestClaimingInstallsTheStreamSourceThatWasChosen(t *testing.T) {
	ext := &fakeExtensions{available: []app.ExtensionCatalogueEntry{
		{Repository: "mosaic-official", ModuleID: "aiostreams", Provides: []string{"stream"}},
		{Repository: "mosaic-official", ModuleID: "fanart-tv", Provides: []string{"artwork"}},
	}}
	h := auth.NewHandler(newTestServiceWithExtensions(newFakeDB(), testNow, ext))

	msg, err := invoke(t, h, "claimServer", map[string]any{
		"serverName": "One", "username": "alex",
		"password": "correct horse", "confirmPassword": "correct horse",
		"streamSource": "aiostreams",
	})
	if err != nil {
		t.Fatalf("claimServer: %v", err)
	}
	if msg.GetSigned() == nil {
		t.Fatalf("claiming did not sign anyone in: %+v", msg.GetOutcome())
	}

	ext.mu.Lock()
	defer ext.mu.Unlock()
	if len(ext.installed) != 1 || ext.installed[0] != "mosaic-official/aiostreams" {
		t.Fatalf("installed %v, want the chosen source from the repository that offers it", ext.installed)
	}
}

// A module the catalogue does not offer as a stream source is not installed,
// whatever the request says. The claim still succeeds: the account is the part
// that had to work, and a source can be added from Settings.
func TestAClaimWillNotInstallSomethingTheCatalogueDidNotOffer(t *testing.T) {
	ext := &fakeExtensions{available: []app.ExtensionCatalogueEntry{
		{Repository: "mosaic-official", ModuleID: "fanart-tv", Provides: []string{"artwork"}},
	}}
	h := auth.NewHandler(newTestServiceWithExtensions(newFakeDB(), testNow, ext))

	msg, err := invoke(t, h, "claimServer", map[string]any{
		"serverName": "One", "username": "alex",
		"password": "correct horse", "confirmPassword": "correct horse",
		"streamSource": "fanart-tv",
	})
	if err != nil {
		t.Fatalf("claimServer: %v", err)
	}
	if msg.GetSigned() == nil {
		t.Fatal("a claim naming an unofferable source failed outright; the account is not the optional half")
	}
	ext.mu.Lock()
	defer ext.mu.Unlock()
	if len(ext.installed) != 0 {
		t.Fatalf("installed %v from an unauthenticated request", ext.installed)
	}
}

// The switch is the complete enumeration of what an unauthenticated caller can
// ask for. An action it does not name does not exist.
func TestAnUnknownDoorwayActionIsRefused(t *testing.T) {
	h := auth.NewHandler(newTestService(newFakeDB(), testNow))
	_, err := invoke(t, h, "deleteEverything", map[string]any{})
	if err == nil {
		t.Fatal("an unnamed action was dispatched")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("CodeOf(err) = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// A suspended account's password is still right, and telling them to check it
// would send them off to do something that cannot help.
func TestASuspendedAccountIsNotToldToCheckItsPassword(t *testing.T) {
	db := newFakeDB()
	userID := signedInUser(db)
	db.seedUser(domain.User{
		ID: userID, Username: "admin", Email: "admin@example.com",
		Status: domain.UserSuspended, CreatedAt: testNow, UpdatedAt: testNow,
	})
	h := auth.NewHandler(newTestService(db, testNow))

	msg, err := invoke(t, h, "signIn", map[string]any{"username": "admin", "password": "hunter2"})
	if err != nil {
		t.Fatalf("a suspended sign-in errored rather than answering: %v", err)
	}
	errs := msg.GetFieldErrors()
	if errs == nil {
		t.Fatalf("a suspended sign-in answered with %+v", msg.GetOutcome())
	}
	if strings.Contains(strings.ToLower(errs.GetFormError()), "password") {
		t.Errorf("a suspended account was told to check its password: %q", errs.GetFormError())
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
