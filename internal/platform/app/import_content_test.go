// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// ImportContent materialises a virtual result by invoking the capability its ref
// names. These tests prove the command boundary around the invocation —
// validate, authenticate, authorise, resolve the capability from the ref,
// forward the caller — using a recording fake capability, so they check the
// routing rather than any particular module's behaviour.

// recordingCapability is a fake v1.Capability that records what it was handed
// and returns a canned result (or an error).
type recordingCapability struct {
	id          string
	err         error
	gotRef      v1.ContentRef
	gotCaller   v1.Caller
	gotService  bool
	gotSettings []byte
}

func (c *recordingCapability) Manifest() v1.Manifest {
	return v1.Manifest{ID: c.id, Version: "0.0.1", Name: "Recording"}
}

func (c *recordingCapability) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	c.gotRef = req.Ref
	c.gotCaller = req.Caller
	c.gotService = svc != nil
	c.gotSettings = req.Settings
	if c.err != nil {
		return v1.ImportResult{}, c.err
	}
	return v1.ImportResult{WorkID: v1.NodeID("work-" + req.Ref.NativeID), Items: 2, Parts: 1}, nil
}

// testRef builds the ContentRef the Platform materialises — as a search or
// catalog browse would have produced it, naming the provider that owns it.
func testRef(provider, nativeType, nativeID string) v1.ContentRef {
	return v1.ContentRef{Provider: provider, NativeID: nativeID, NativeType: nativeType, MediaType: v1.MediaMovie}
}

func importFixture(t *testing.T, caps ...*recordingCapability) (*app.Service, *fakeDB, *trace, domain.SessionID) {
	t.Helper()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	tr := &trace{}
	registry := app.NewCapabilityRegistry()
	for _, c := range caps {
		registry.Register(c)
	}
	svc := newTestServiceWithCapabilities(db, tr, now, registry)
	db.seedUser(domain.User{ID: "u-1", Username: "curator", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	return svc, db, tr, "s-1"
}

// traced reports whether the trace recorded the given step.
func traced(tr *trace, step string) bool {
	for _, s := range tr.snapshot() {
		if s == step {
			return true
		}
	}
	return false
}

func TestImportContent(t *testing.T) {
	ctx := context.Background()

	t.Run("invokes the capability the ref names, forwarding the caller and the service", func(t *testing.T) {
		cap := &recordingCapability{id: "stremio"}
		svc, _, tr, session := importFixture(t, cap)

		result, err := svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: string(session)}, Ref: testRef("stremio", "movie", "tt1254207"),
		})
		if err != nil {
			t.Fatalf("ImportContent: %v", err)
		}
		if result.WorkID != v1.NodeID("work-tt1254207") {
			t.Fatalf("WorkID = %q, want the capability's result", result.WorkID)
		}
		if cap.gotRef.NativeID != "tt1254207" {
			t.Fatalf("capability saw ref native id %q", cap.gotRef.NativeID)
		}
		if cap.gotCaller.Session != string(session) {
			t.Fatalf("capability saw caller %q, want the invoking session forwarded", cap.gotCaller.Session)
		}
		if !cap.gotService {
			t.Fatal("capability was handed a nil ContentService")
		}
		if string(cap.gotSettings) != "{}" {
			t.Fatalf("capability saw settings %q, want an empty document when none configured", cap.gotSettings)
		}
		if !traced(tr, "events.publish:content.import.invoked") {
			t.Fatalf("missing the import audit event: %v", tr.snapshot())
		}
	})

	t.Run("a ref naming an unregistered provider is NotFound", func(t *testing.T) {
		svc, _, _, session := importFixture(t)
		_, err := svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: string(session)}, Ref: testRef("nope", "movie", "x"),
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want NotFound", got)
		}
	})

	t.Run("a missing provider or native id is InvalidArgument", func(t *testing.T) {
		svc, _, _, session := importFixture(t, &recordingCapability{id: "stremio"})
		_, err := svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: string(session)}, Ref: v1.ContentRef{NativeID: "x", NativeType: "movie"},
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("missing provider: category = %s", got)
		}
		_, err = svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: string(session)}, Ref: v1.ContentRef{Provider: "stremio"},
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("missing native id/type: category = %s", got)
		}
	})

	t.Run("an unauthenticated caller cannot import", func(t *testing.T) {
		cap := &recordingCapability{id: "stremio"}
		svc, _, _, _ := importFixture(t, cap)
		_, err := svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: "not-a-session"}, Ref: testRef("stremio", "movie", "x"),
		})
		if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
			t.Fatalf("category = %s, want Unauthenticated", got)
		}
		if cap.gotRef.NativeID != "" {
			t.Fatal("the capability must not be invoked for an unauthenticated caller")
		}
	})

	t.Run("an unauthorised caller cannot import and the capability is never invoked", func(t *testing.T) {
		now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
		db := newFakeDB()
		cap := &recordingCapability{id: "stremio"}
		registry := app.NewCapabilityRegistry()
		registry.Register(cap)
		svc := newTestServiceWithCapabilities(db, &trace{}, now, registry)
		// A caller with a session but no role granting content.import.
		db.seedUser(domain.User{ID: "u-2", Username: "viewer", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
		db.seedSession("s-2", "u-2", now)

		_, err := svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: "s-2"}, Ref: testRef("stremio", "movie", "x"),
		})
		if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
			t.Fatalf("category = %s, want PermissionDenied", got)
		}
		if cap.gotRef.NativeID != "" {
			t.Fatal("the capability must not be invoked for an unauthorised caller")
		}
	})

	t.Run("a capability error propagates", func(t *testing.T) {
		cap := &recordingCapability{id: "stremio", err: errors.New("provider unreachable")}
		svc, _, _, session := importFixture(t, cap)
		_, err := svc.ImportContent(ctx, app.ImportContentCommand{
			Caller: v1.Caller{Session: string(session)}, Ref: testRef("stremio", "movie", "x"),
		})
		if err == nil {
			t.Fatal("expected the capability error to propagate")
		}
	})
}
