// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	"github.com/mosaic-media/contracts/gen/mosaic/auth/v1/authv1connect"
	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/gen/mosaic/session/v1/sessionv1connect"
	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/composition/bootstrap"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	authtransport "github.com/mosaic-media/platform/internal/transport/auth"
	"github.com/mosaic-media/platform/internal/transport/session"
	"net/http"
)

// The bearer pair over the real wire (ADR 0102).
//
// This is the test the browser had to find first. An expired access token used
// to reach the screen builders and come back as an error *rendered into the
// content region*, so the client saw a successful call carrying a picture of a
// failure — nothing to retry on, and a page that said "Platform unavailable"
// about a Platform that was perfectly available. The transport authenticates
// now, and this is what says so.

func bearerFixture(t *testing.T) (*httptest.Server, *postgres.ContractSet, context.Context) {
	t.Helper()
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Tokens: cs.Tokens,
		Users: cs.Users, Credentials: cs.Credentials, Config: cs.Config,
		Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts,
		Clock: cs.Clock, IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{},
		PasswordVerifier: crypto.NewPasswordHasher(),
		UserPreferences:  cs.UserPreferences,
	})

	perms := make([]domain.Permission, 0, len(app.SuperuserActions()))
	for _, a := range app.SuperuserActions() {
		perms = append(perms, domain.Permission(a))
	}
	if _, err := bootstrap.EnsureAdmin(ctx, cs.UnitOfWork, crypto.NewPasswordHasher(), cs.Clock, cs.IDs,
		bootstrap.AdminSeed{Username: "admin", Password: "correct horse battery staple", Permissions: perms}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	mux := http.NewServeMux()
	authPath, authHandler := authv1connect.NewAuthServiceHandler(authtransport.NewHandler(svc))
	mux.Handle(authPath, authHandler)
	sessionHandler := session.NewHandler(svc, nil, nil, nil)
	sessionPath, sessionConnect := sessionv1connect.NewSessionServiceHandler(sessionHandler)
	mux.Handle(sessionPath, sessionConnect)

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	return server, cs, ctx
}

func TestAnExpiredAccessTokenIsRefusedByTheTransportRatherThanRendered(t *testing.T) {
	server, cs, ctx := bearerFixture(t)

	authClient := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	in, err := authClient.SignIn(ctx, connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: "correct horse battery staple", DeviceId: "browser-1",
	}))
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	pair := in.Msg.GetTokens()
	if pair.GetAccessToken() == "" || pair.GetRefreshToken() == "" {
		t.Fatal("sign-in issued no pair")
	}

	sessionClient := sessionv1connect.NewSessionServiceClient(server.Client(), server.URL)

	// It works while the credential is live.
	if _, err := sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
		Session: pair.GetAccessToken(), Screen: "home",
	})); err != nil {
		t.Fatalf("Attach with a live credential: %v", err)
	}

	// Twenty-five hours later — past the fixed lifetime a session used to have
	// — the access token has expired and the refresh token has not.
	if _, err := cs.Pool.Exec(ctx,
		`UPDATE session_access_tokens SET expires_at = expires_at - interval '25 hours'`); err != nil {
		t.Fatalf("expire the access token: %v", err)
	}

	_, err = sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
		Session: pair.GetAccessToken(), Screen: "home",
	}))
	if err == nil {
		t.Fatal("an expired credential was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want Unauthenticated — a client can only implement "+
			"retry-on-Unauthenticated if the transport says so", got)
	}

	// And the client's answer to that is a refresh, after which it is signed in
	// again with no re-authentication. This is the exit criterion: come back
	// past the old twenty-four hours and still be signed in.
	out, err := authClient.Refresh(ctx, connect.NewRequest(&authv1.RefreshRequest{
		RefreshToken: pair.GetRefreshToken(), DeviceId: "browser-1",
	}))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	next := out.Msg.GetTokens()
	if next.GetAccessToken() == pair.GetAccessToken() {
		t.Fatal("the refresh did not rotate the access token")
	}
	if next.GetRefreshToken() == pair.GetRefreshToken() {
		t.Fatal("the refresh did not rotate the refresh token")
	}
	if _, err := sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
		Session: next.GetAccessToken(), Screen: "home",
	})); err != nil {
		t.Fatalf("Attach after refreshing: %v", err)
	}

	// The session is the same one throughout — a rotation is not a new session,
	// so a device list does not grow a row every ten minutes.
	if out.Msg.GetSession().GetId() != in.Msg.GetSession().GetId() {
		t.Fatalf("refresh minted a new session (%q, was %q)",
			out.Msg.GetSession().GetId(), in.Msg.GetSession().GetId())
	}
	var sessionRows int
	if err := cs.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&sessionRows); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionRows != 1 {
		t.Fatalf("%d session rows after one sign-in and one refresh, want 1", sessionRows)
	}
}

// Revoking a device ends it at once — the other half of the exit criterion.
func TestRevokingADeviceEndsItImmediatelyOverTheWire(t *testing.T) {
	server, cs, ctx := bearerFixture(t)
	authClient := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	sessionClient := sessionv1connect.NewSessionServiceClient(server.Client(), server.URL)

	signIn := func(device string) *authv1.SignInResponse {
		t.Helper()
		res, err := authClient.SignIn(ctx, connect.NewRequest(&authv1.SignInRequest{
			Username: "admin", Password: "correct horse battery staple", DeviceId: device,
		}))
		if err != nil {
			t.Fatalf("SignIn(%s): %v", device, err)
		}
		return res.Msg
	}

	phone := signIn("phone")
	desk := signIn("desk")

	// Both work.
	for name, msg := range map[string]*authv1.SignInResponse{"phone": phone, "desk": desk} {
		if _, err := sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
			Session: msg.GetTokens().GetAccessToken(), Screen: "home",
		})); err != nil {
			t.Fatalf("Attach(%s): %v", name, err)
		}
	}

	// The desk ends the phone, naming its session id — which is what a device
	// list shows and is not itself a credential.
	if _, err := authClient.SignOut(ctx, connect.NewRequest(&authv1.SignOutRequest{
		CallerSession: desk.GetTokens().GetAccessToken(),
		TargetSession: phone.GetSession().GetId(),
	})); err != nil {
		t.Fatalf("SignOut: %v", err)
	}

	// At once, not when an access token happens to expire.
	if _, err := sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
		Session: phone.GetTokens().GetAccessToken(), Screen: "home",
	})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the revoked device still works: err = %v", err)
	}
	// And it cannot refresh its way back in.
	if _, err := authClient.Refresh(ctx, connect.NewRequest(&authv1.RefreshRequest{
		RefreshToken: phone.GetTokens().GetRefreshToken(), DeviceId: "phone",
	})); err == nil {
		t.Fatal("a revoked device refreshed itself back in")
	}
	// The device that did the revoking is untouched, which is the whole reason
	// revocation is per device rather than per account.
	if _, err := sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
		Session: desk.GetTokens().GetAccessToken(), Screen: "home",
	})); err != nil {
		t.Fatalf("revoking one device ended another: %v", err)
	}

	live, err := cs.Sessions.ListForUser(ctx, domain.UserID(desk.GetSession().GetUserId()), time.Now().UTC())
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(live) != 1 || live[0].DeviceID != "desk" {
		t.Fatalf("the device list is %+v, want just the desk", live)
	}
}

// A refresh token presented twice takes the chain with it, over the wire.
func TestAReplayedRefreshTokenEndsTheChainOverTheWire(t *testing.T) {
	server, _, ctx := bearerFixture(t)
	authClient := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	sessionClient := sessionv1connect.NewSessionServiceClient(server.Client(), server.URL)

	in, err := authClient.SignIn(ctx, connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: "correct horse battery staple", DeviceId: "browser-1",
	}))
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	first := in.Msg.GetTokens()

	out, err := authClient.Refresh(ctx, connect.NewRequest(&authv1.RefreshRequest{
		RefreshToken: first.GetRefreshToken(), DeviceId: "browser-1",
	}))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	second := out.Msg.GetTokens()

	// The replay.
	if _, err := authClient.Refresh(ctx, connect.NewRequest(&authv1.RefreshRequest{
		RefreshToken: first.GetRefreshToken(), DeviceId: "browser-1",
	})); err == nil {
		t.Fatal("a spent refresh token was accepted")
	}

	// The legitimate client goes with it. That is the intended behaviour and the
	// cost of making theft detectable: by the time a replay is seen, the thief
	// and the user hold descendants of the same original.
	if _, err := authClient.Refresh(ctx, connect.NewRequest(&authv1.RefreshRequest{
		RefreshToken: second.GetRefreshToken(), DeviceId: "browser-1",
	})); err == nil {
		t.Fatal("the chain survived a detected replay")
	}
	if _, err := sessionClient.Attach(ctx, connect.NewRequest(&sessionv1.AttachRequest{
		Session: second.GetAccessToken(), Screen: "home",
	})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an access token survived its chain being revoked: err = %v", err)
	}
}
