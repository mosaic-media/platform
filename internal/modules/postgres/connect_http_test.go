// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	authtransport "github.com/mosaic-media/platform/internal/transport/auth"
	"github.com/mosaic-media/platform/internal/transport/session"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
	authv1 "github.com/mosaic-media/sdui/gen/mosaic/auth/v1"
	"github.com/mosaic-media/sdui/gen/mosaic/auth/v1/authv1connect"
	sessionv1 "github.com/mosaic-media/sdui/gen/mosaic/session/v1"
	"github.com/mosaic-media/sdui/gen/mosaic/session/v1/sessionv1connect"
)

// TestConnectHTTPSignsInAndRendersAScreen is the "make it runnable" proof end to
// end: a real app.Service over real PostgreSQL, served through the actual
// Connect handlers, driven only over HTTP the way the Shell does it. It signs in
// with a password (verified by the real Argon2id hasher) over AuthService, then
// spends that session on SessionService — subscribing to the push lane and
// navigating to a screen — and asserts the content it seeded comes back rendered.
//
// It replaces the GraphQL HTTP test ADR 0061 retired. That test proved the same
// stack through a transport no client used; this one proves it through the only
// transport there now is, which is why it drives the two lanes rather than
// posting queries.
func TestConnectHTTPSignsInAndRendersAScreen(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	hasher := crypto.NewPasswordHasher()

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: hasher,
		Capabilities:   nil, // no modules registered: this exercises the local library only
		ModuleSettings: cs.ModuleSettings,
	})

	// Seed an admin with a real password credential and the actions the flow
	// needs. This stands in for the bootstrap a running binary performs.
	const password = "correct horse battery staple"
	now := cs.Clock.Now()
	admin, err := cs.Users.Create(c, domain.User{ID: "admin", Username: "admin", Email: "admin@example.com", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := cs.Credentials.SavePassword(c, domain.PasswordCredential{UserID: admin.ID, Hash: hash, UpdatedAt: now}); err != nil {
		t.Fatalf("save password: %v", err)
	}
	if err := seedRoleGrant(c, pool, admin.ID, "Administrator", []domain.Permission{
		domain.Permission(app.ActionSessionCreate),
		domain.Permission(app.ActionContentCreate),
		domain.Permission(app.ActionContentRead),
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	// Both services on one mux, as the composition root mounts them. Plain
	// HTTP/1.1 is enough here: the Connect protocol carries a server stream over
	// chunked encoding, so the push lane works without h2c. The binary serves
	// h2c so the lanes *multiplex* onto one connection (ADR 0041), which is a
	// performance property, not a correctness one.
	mux := http.NewServeMux()
	authPath, authHandler := authv1connect.NewAuthServiceHandler(authtransport.NewHandler(svc))
	mux.Handle(authPath, authHandler)
	sessionHandler := session.NewHandler(svc, nil, nil, nil)
	sessionPath, sessionConnect := sessionv1connect.NewSessionServiceHandler(sessionHandler)
	mux.Handle(sessionPath, sessionConnect)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer sessionHandler.Manager().Shutdown()

	// 1. Sign in over HTTP to get a session — the one call made without one.
	authClient := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	signIn, err := authClient.SignIn(c, connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: password, DeviceId: "tv-1",
	}))
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	sessionID := signIn.Msg.GetSession().GetId()
	if sessionID == "" {
		t.Fatal("sign-in returned no session id")
	}
	if signIn.Msg.GetSession().GetUserId() != string(admin.ID) {
		t.Fatalf("session.user_id = %q, want %q", signIn.Msg.GetSession().GetUserId(), admin.ID)
	}

	// A wrong password must be refused with a status code, not a 200 carrying an
	// error — the property the GraphQL envelope could not express and the reason
	// the client can now branch on failure at all.
	_, err = authClient.SignIn(c, connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: "wrong", DeviceId: "tv-1",
	}))
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("SignIn(wrong password) code = %v, want %v", got, connect.CodeUnauthenticated)
	}

	// 2. Put something in the library, as the caller the sign-in just minted.
	// Content commands have no transport of their own since ADR 0061 — they are
	// reached through the session's Invoke actions or, as here, by the composed
	// service directly.
	const title = "Fullmetal Alchemist: Brotherhood"
	caller := v1.CallerFromSession(sessionID)
	work, err := svc.AddContentWork(c, v1.AddContentWorkCommand{
		Caller: caller, MediaType: v1.MediaAnimeSeries, Title: title,
		ExternalIDs: []byte(`{"anilist":"5114"}`),
	})
	if err != nil {
		t.Fatalf("AddContentWork: %v", err)
	}
	workID := string(work.Work.ID)

	// 3. Declare the route, then open the push lane. Attach first so the render
	// the stream performs on connect is the screen under test rather than home.
	sessionClient := sessionv1connect.NewSessionServiceClient(server.Client(), server.URL)
	if _, err := sessionClient.Attach(c, connect.NewRequest(&sessionv1.AttachRequest{
		Session: sessionID, Screen: "detail", Params: []byte(`{"nodeId":"` + workID + `"}`),
	})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	streamCtx, cancel := context.WithTimeout(c, 30*time.Second)
	defer cancel()
	stream, err := sessionClient.Subscribe(streamCtx, connect.NewRequest(&sessionv1.SubscribeRequest{
		Session: sessionID,
	}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	// 4. Read the push lane until the content region arrives, and assert the
	// screen the Platform rendered is the one holding the seeded work. Reading
	// until rather than asserting on the first message is deliberate: connect
	// pushes the definition library and the shell first (ADR 0024, ADR 0031),
	// and pinning that order here would make this test fail for a change it does
	// not cover.
	var sawDefinitions, sawShell bool
	var content string
	for stream.Receive() {
		switch body := stream.Msg().GetBody().(type) {
		case *sessionv1.ServerMessage_Event:
			if body.Event.GetType() == "sdui.definitions" {
				sawDefinitions = true
			}
		case *sessionv1.ServerMessage_Shell:
			sawShell = true
		case *sessionv1.ServerMessage_Region:
			if body.Region.GetRegion() != "content" {
				continue
			}
			node := body.Region.GetUiNode()
			if node == nil {
				t.Fatal("the content region arrived with no UINode")
			}
			rendered, err := protojson.Marshal(node)
			if err != nil {
				t.Fatalf("marshal the pushed UINode: %v", err)
			}
			content = string(rendered)
		}
		if content != "" {
			break
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Subscribe stream: %v", err)
	}
	if content == "" {
		t.Fatal("the stream ended before the content region was pushed")
	}
	if !sawDefinitions {
		t.Error("the definition library was not pushed before the content (ADR 0024)")
	}
	if !sawShell {
		t.Error("the app shell was not pushed before the content (ADR 0031)")
	}
	if !strings.Contains(content, title) {
		t.Errorf("the rendered detail screen does not contain %q:\n%s", title, content)
	}
}
