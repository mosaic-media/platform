// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/adapters/crypto"
	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	graphqltransport "github.com/mosaic-media/mosaic-platform/internal/transport/graphql"
)

// TestGraphQLHTTPImportsAndQueriesContent is the "make it runnable" proof end
// to end: a real app.Service over real PostgreSQL, served through the actual
// GraphQL HTTP handler, driven only over HTTP the way a client would. It signs
// in with a password (verified by the real Argon2id hasher), imports a work
// and a season, then finds them again by search and by external id.
func TestGraphQLHTTPImportsAndQueriesContent(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	hasher := crypto.NewPasswordHasher()

	svc := app.NewService(
		cs.UnitOfWork, cs.Sessions, cs.Users, cs.Credentials, cs.Config, cs.Permissions,
		cs.Nodes, cs.Clock, cs.IDs, cs.ContentIDs,
		policy.NewEngine(cs.Permissions), noopPublisher{}, hasher,
		nil, // no capabilities registered in this GraphQL HTTP test
		cs.ModuleSettings,
	)

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

	schema, err := graphqltransport.NewSchema(svc, nil)
	if err != nil {
		t.Fatalf("build schema: %v", err)
	}
	server := httptest.NewServer(graphqltransport.Handler(schema))
	defer server.Close()

	post := func(t *testing.T, query string, vars map[string]any) map[string]any {
		t.Helper()
		return gqlPost(t, server.URL, query, vars)
	}

	// 1. Sign in over HTTP to get a session.
	signIn := post(t, `mutation($u:String!,$p:String!,$d:String!){
		signIn(username:$u,password:$p,deviceId:$d){ session{ id userId } }
	}`, map[string]any{"u": "admin", "p": password, "d": "tv-1"})
	session := signIn["signIn"].(map[string]any)["session"].(map[string]any)
	sessionID, _ := session["id"].(string)
	if sessionID == "" {
		t.Fatalf("sign-in returned no session: %v", signIn)
	}

	// 2. Import a work and a season through the content mutations.
	work := post(t, `mutation($s:String!){
		addContentWork(callerSessionId:$s, mediaType:"anime_series", title:"Fullmetal Alchemist: Brotherhood", externalIds:"{\"anilist\":\"5114\"}"){ id kind mediaType }
	}`, map[string]any{"s": sessionID})
	added := work["addContentWork"].(map[string]any)
	workID, _ := added["id"].(string)
	if workID == "" || added["kind"] != "work" || added["mediaType"] != "anime_series" {
		t.Fatalf("addContentWork returned %v", added)
	}

	season := post(t, `mutation($s:String!,$parent:String!){
		addContentChild(callerSessionId:$s, parentId:$parent, kind:"container", containerType:"season", title:"Season 1", naturalOrder:1){ id kind containerType }
	}`, map[string]any{"s": sessionID, "parent": workID})
	child := season["addContentChild"].(map[string]any)
	if child["kind"] != "container" || child["containerType"] != "season" {
		t.Fatalf("addContentChild returned %v", child)
	}

	// 3. Find it again by search and by external id, over HTTP.
	search := post(t, `query($s:String!){
		searchContent(callerSessionId:$s, title:"alchemist", kind:"work"){ id title }
	}`, map[string]any{"s": sessionID})
	nodes, _ := search["searchContent"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["id"] != workID {
		t.Fatalf("searchContent = %v, want the imported work", nodes)
	}

	byID := post(t, `query($s:String!){
		contentByExternalId(callerSessionId:$s, scheme:"anilist", value:"5114"){ id }
	}`, map[string]any{"s": sessionID})
	found, _ := byID["contentByExternalId"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["id"] != workID {
		t.Fatalf("contentByExternalId = %v, want the imported work", found)
	}

	// 4. The tree reads back through the node query with its child.
	node := post(t, `query($s:String!,$id:String!){
		contentNode(callerSessionId:$s, id:$id, withChildren:true){ node{ title } children{ kind } }
	}`, map[string]any{"s": sessionID, "id": workID})
	payload := node["contentNode"].(map[string]any)
	children, _ := payload["children"].([]any)
	if len(children) != 1 || children[0].(map[string]any)["kind"] != "container" {
		t.Fatalf("contentNode children = %v, want one season", children)
	}
}

// gqlPost sends one GraphQL request and returns its data, failing on a
// transport error or any GraphQL error in the response.
func gqlPost(t *testing.T, url, query string, vars map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Errors) > 0 {
		t.Fatalf("graphql errors: %v", out.Errors)
	}
	return out.Data
}
