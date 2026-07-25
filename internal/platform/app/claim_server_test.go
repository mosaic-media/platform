// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// A server nobody owns can be claimed, and the claim signs the owner in — the
// point of it being one call rather than a create followed by a sign-in with a
// password set thirty seconds earlier.
func TestClaimServerCreatesTheOwnerAndSignsThemIn(t *testing.T) {
	db := newFakeDB()
	svc := newTestService(db, &trace{}, testNow)

	res, err := svc.ClaimServer(context.Background(), app.ClaimServerCommand{
		Username: "alex", Password: "correct horse", DisplayName: "Alex Rivera", DeviceID: "d-1",
	})
	if err != nil {
		t.Fatalf("ClaimServer: %v", err)
	}
	if res.User.Username != "alex" || res.User.DisplayName != "Alex Rivera" {
		t.Fatalf("owner = %+v, want alex/Alex Rivera", res.User)
	}
	if res.Session.ID == "" {
		t.Fatal("claiming a server must return a session; otherwise the owner types the password they just set")
	}
	// And the owner is an owner: the session carries superuser authority, not an
	// account that exists and can do nothing.
	if !svc.CallerCan(context.Background(), v1.Caller{Session: string(res.Session.ID)}, app.ActionUserRead, "user") {
		t.Error("the claimed owner does not hold superuser authority")
	}
}

// The gate. A second claim is a conflict, not a second owner — and the refusal
// says nothing about who owns it.
func TestClaimServerRefusesAServerThatHasOne(t *testing.T) {
	db := newFakeDB()
	svc := newTestService(db, &trace{}, testNow)
	ctx := context.Background()

	if _, err := svc.ClaimServer(ctx, app.ClaimServerCommand{
		Username: "alex", Password: "correct horse", DeviceID: "d-1",
	}); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	_, err := svc.ClaimServer(ctx, app.ClaimServerCommand{
		Username: "mallory", Password: "hunter2", DeviceID: "d-2",
	})
	if err == nil {
		t.Fatal("a claimed server must refuse a second claim")
	}
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Conflict)
	}
	if msg := err.Error(); msg != "" && contains(msg, "alex") {
		t.Errorf("the refusal names the owner (%q); an unauthenticated caller must not learn that", msg)
	}
}

// ServerClaimed is what the pre-session endpoint branches on, and it fails
// closed: the cost of wrongly saying "claimed" is a sign-in screen, and the cost
// of wrongly saying "unclaimed" is offering the server to a stranger.
func TestServerClaimedIsFalseOnlyOnAnEmptyServer(t *testing.T) {
	db := newFakeDB()
	svc := newTestService(db, &trace{}, testNow)
	ctx := context.Background()

	if svc.ServerClaimed(ctx) {
		t.Fatal("a server with no users is unclaimed")
	}
	if _, err := svc.ClaimServer(ctx, app.ClaimServerCommand{
		Username: "alex", Password: "correct horse", DeviceID: "d-1",
	}); err != nil {
		t.Fatalf("ClaimServer: %v", err)
	}
	if !svc.ServerClaimed(ctx) {
		t.Fatal("a server with an owner is claimed")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}

var _ = domain.UserActive
