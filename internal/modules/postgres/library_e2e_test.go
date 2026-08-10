// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	"github.com/mosaic-media/contracts/gen/mosaic/auth/v1/authv1connect"
	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/gen/mosaic/session/v1/sessionv1connect"
	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	authtransport "github.com/mosaic-media/platform/internal/transport/auth"
	"github.com/mosaic-media/platform/internal/transport/session"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// M2a's exit criterion, over the real wire and against real PostgreSQL
// (ADR 0104, roadmap M2.1–2.3).
//
// It is the acceptance baseline's end-to-end shape applied to this milestone:
// sign in over `AuthService`, spend the session on `SessionService`, and assert
// the *pushed content region* — not a service return value. That distinction is
// what makes it worth having. Every property here is already proved at the app
// layer against fakes; what this adds is that the actions a client can name
// reach those services, that their outcomes are rendered, and that the whole
// chain survives a real database, a real password hash and a real transport.
//
// The one thing it is not is a browser. It drives the transport the Shell
// drives and asserts the tree the Shell would render, which is the closest a
// test gets — a screen that has not been opened in a browser is still not
// verified, and this does not claim otherwise.

// e2eCatalogModule is a module that offers a catalog and materialises from it,
// deduping on the shared external id exactly as every real module does. The
// dedup is what the second run is asserted against.
type e2eCatalogModule struct {
	id     string
	titles []string
}

func (m *e2eCatalogModule) Manifest() v1.Manifest {
	return v1.Manifest{
		ID: m.id, Version: "0.0.1", Name: "E2E catalog",
		Provides: []v1.Role{v1.RoleCatalog, v1.RoleMetadata},
	}
}

func (m *e2eCatalogModule) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	found, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: req.Caller, Scheme: req.Ref.ExternalScheme, Value: req.Ref.ExternalID,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	for _, node := range found.Nodes {
		if node.IsRoot() {
			return v1.ImportResult{WorkID: node.ID, AlreadyKnown: true}, nil
		}
	}
	ids, _ := json.Marshal(map[string]string{req.Ref.ExternalScheme: req.Ref.ExternalID})
	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: req.Caller, MediaType: v1.MediaMovie, Title: req.Ref.NativeID, ExternalIDs: ids,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	return v1.ImportResult{WorkID: work.Work.ID}, nil
}

func (m *e2eCatalogModule) Catalogs(context.Context, v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	return v1.CatalogsResponse{Catalogs: []v1.Catalog{
		{ID: "top", NativeType: "movie", Name: "Top from " + m.id},
	}}, nil
}

func (m *e2eCatalogModule) CatalogItems(_ context.Context, req v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	if req.Skip >= len(m.titles) {
		return v1.CatalogItemsResponse{}, nil
	}
	items := make([]v1.CatalogItem, 0, len(m.titles))
	for _, title := range m.titles[req.Skip:] {
		items = append(items, v1.CatalogItem{
			Ref: v1.ContentRef{
				Provider: m.id, NativeID: title, NativeType: "movie", MediaType: v1.MediaMovie,
				ExternalScheme: "imdb", ExternalID: "tt-" + title,
			},
			Title: title,
		})
	}
	return v1.CatalogItemsResponse{Items: items}, nil
}

func (m *e2eCatalogModule) Metadata(_ context.Context, req v1.MetadataRequest) (v1.ContentMetadata, error) {
	return v1.ContentMetadata{Ref: req.Ref, Title: req.Ref.NativeID}, nil
}

func TestLibraryRulesFillTheLibraryOverTheWire(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	hasher := crypto.NewPasswordHasher()

	registry := app.NewCapabilityRegistry()
	registry.Register(&e2eCatalogModule{id: "alpha", titles: []string{"Arrival", "Dune"}})
	registry.Register(&e2eCatalogModule{id: "beta", titles: []string{"Contact"}})

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions,
		Tokens: cs.Tokens, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts,
		Clock: cs.Clock, IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: hasher,
		Capabilities: registry, ModuleSettings: cs.ModuleSettings,
		LibraryRules: cs.LibraryRules,
	})

	const password = "correct horse battery staple"
	now := cs.Clock.Now()
	admin, err := cs.Users.Create(c, domain.User{
		ID: "admin", Username: "admin", Email: "admin@example.com",
		Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := cs.Credentials.SavePassword(c, domain.PasswordCredential{
		UserID: admin.ID, Hash: hash, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save password: %v", err)
	}
	// The administrator preset, whole, because that is who manages rules — and
	// listing the actions by hand here would be a second, drifting copy of it.
	perms := make([]domain.Permission, 0, len(app.AdministratorActions()))
	for _, a := range app.AdministratorActions() {
		perms = append(perms, domain.Permission(a))
	}
	if err := seedRoleGrant(c, pool, admin.ID, "Administrator", perms); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	mux := http.NewServeMux()
	authPath, authHandler := authv1connect.NewAuthServiceHandler(authtransport.NewHandler(svc))
	mux.Handle(authPath, authHandler)
	sessionHandler := session.NewHandler(svc, nil, nil, nil)
	sessionPath, sessionConnect := sessionv1connect.NewSessionServiceHandler(sessionHandler)
	mux.Handle(sessionPath, sessionConnect)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer sessionHandler.Manager().Shutdown()

	authClient := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	signIn, err := authClient.SignIn(c, connect.NewRequest(&authv1.SignInRequest{
		Username: "admin", Password: password, DeviceId: "tv-1",
	}))
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	token := signIn.Msg.GetTokens().GetAccessToken()

	sessionClient := sessionv1connect.NewSessionServiceClient(server.Client(), server.URL)
	invoke := func(t *testing.T, action string, input map[string]any) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal %s input: %v", action, err)
		}
		if _, err := sessionClient.Invoke(c, connect.NewRequest(&sessionv1.InvokeRequest{
			Session: token, Action: action, Input: body,
		})); err != nil {
			t.Fatalf("Invoke(%s): %v", action, err)
		}
	}

	// The library starts empty, which is the baseline the rest is measured
	// against — and the first thing the Library screen has ever been able to say.
	if content := renderScreen(t, c, sessionClient, token, "library", nil); !strings.Contains(content, "Nothing in the library yet") {
		t.Fatalf("a fresh install's library screen said:\n%s", content)
	}

	// **Two rules**, created the way the settings panel creates them: an Invoke
	// carrying the catalog it is following, and the name from the form's scope.
	invoke(t, "createLibraryRule", map[string]any{
		"ruleName": "Alpha top", "addModule": "alpha", "addCatalog": "top", "nativeType": "movie",
	})
	invoke(t, "createLibraryRule", map[string]any{
		"ruleName": "Beta top", "addModule": "beta", "addCatalog": "top", "nativeType": "movie",
	})

	// Creating a rule materialises nothing. Saving states an intention; a run
	// acts on it, and that separation is what stops a Save from pulling a
	// hundred titles in.
	if content := renderScreen(t, c, sessionClient, token, "library", nil); !strings.Contains(content, "Nothing in the library yet") {
		t.Fatalf("creating two rules filled the library before anything ran:\n%s", content)
	}

	// The pass. Invoked here rather than waited for, because a six-hourly
	// schedule is not a thing a test can sit through — the scheduler and the
	// runner have their own tests, and this is about what the pass does.
	invoke(t, "runLibraryMaintenance", nil)

	// **New matches appear on the Library screen without anyone pressing Add.**
	content := renderScreen(t, c, sessionClient, token, "library", nil)
	for _, title := range []string{"Arrival", "Dune", "Contact"} {
		if !strings.Contains(content, title) {
			t.Errorf("the library screen does not show %q, which a rule added:\n%s", title, content)
		}
	}
	// The real total, which is the thing this screen can say and a
	// provider-backed one cannot.
	if !strings.Contains(content, "3 titles") {
		t.Errorf("the library screen did not state a total of 3:\n%s", content)
	}

	// **A second run adds no duplicates.**
	invoke(t, "runLibraryMaintenance", nil)
	content = renderScreen(t, c, sessionClient, token, "library", nil)
	if !strings.Contains(content, "3 titles") {
		t.Errorf("the second run changed the library's size:\n%s", content)
	}

	// **The run log says what happened**, on the rules themselves, where the
	// administrator who wrote them reads it.
	rules := renderScreen(t, c, sessionClient, token, "settings", map[string]any{"section": "library"})
	if !strings.Contains(rules, "Alpha top") || !strings.Contains(rules, "Beta top") {
		t.Fatalf("the rules panel does not list both rules:\n%s", rules)
	}
	// Two created on the first run, two refreshed on the second: the account
	// distinguishes "this arrived" from "this was already here and was topped
	// up", which is the difference a single success count would lose.
	if !strings.Contains(rules, "0 added · 2 refreshed · 0 skipped · 0 failed") {
		t.Errorf("Alpha's rule does not report two refreshed after the second run:\n%s", rules)
	}
	if !strings.Contains(rules, "0 added · 1 refreshed · 0 skipped · 0 failed") {
		t.Errorf("Beta's rule does not report one refreshed after the second run:\n%s", rules)
	}
}

// renderScreen declares a route and reads the pushed content region back — the
// two-lane round trip a client makes on every navigation (contracts#5).
//
// Attach then Subscribe, per navigation, rather than one long-lived stream: a
// test that held the stream open would be asserting on the *order* of pushes as
// much as on their content, and this is about what a screen says.
func renderScreen(t *testing.T, c context.Context, client sessionv1connect.SessionServiceClient, token, screen string, params map[string]any) string {
	t.Helper()

	raw := []byte("{}")
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = encoded
	}
	if _, err := client.Attach(c, connect.NewRequest(&sessionv1.AttachRequest{
		Session: token, Screen: screen, Params: raw,
	})); err != nil {
		t.Fatalf("Attach(%s): %v", screen, err)
	}

	streamCtx, cancel := context.WithTimeout(c, 30*time.Second)
	defer cancel()
	stream, err := client.Subscribe(streamCtx, connect.NewRequest(&sessionv1.SubscribeRequest{Session: token}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	for stream.Receive() {
		region, ok := stream.Msg().GetBody().(*sessionv1.ServerMessage_Region)
		if !ok || region.Region.GetRegion() != "content" {
			continue
		}
		node := region.Region.GetUiNode()
		if node == nil {
			t.Fatalf("the content region for %q arrived with no UINode", screen)
		}
		rendered, err := protojson.Marshal(node)
		if err != nil {
			t.Fatalf("marshal the pushed UINode: %v", err)
		}
		return string(rendered)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Subscribe stream: %v", err)
	}
	t.Fatalf("the stream ended before %q was pushed", screen)
	return ""
}
