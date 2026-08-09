// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/composition/builtin"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/runtime"
	health "github.com/mosaic-media/platform/internal/transport/health"
)

// fakeReporter is a contracts.ComponentHealthReporter stand-in.
type fakeReporter struct{ health domain.ComponentHealth }

func (f fakeReporter) ReportHealth(context.Context) domain.ComponentHealth { return f.health }

// fakeConfigStore is a minimal in-memory contracts.ConfigStore.
type fakeConfigStore struct {
	active  *domain.ConfigVersion
	pending *domain.ConfigVersion
}

func (s *fakeConfigStore) Save(_ context.Context, v domain.ConfigVersion) (domain.ConfigVersion, error) {
	return v, nil
}
func (s *fakeConfigStore) Latest(context.Context) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
}
func (s *fakeConfigStore) FindByID(context.Context, domain.ConfigVersionID) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
}
func (s *fakeConfigStore) FindActive(context.Context) (domain.ConfigVersion, error) {
	if s.active == nil {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no active config version")
	}
	return *s.active, nil
}
func (s *fakeConfigStore) FindPending(context.Context) (domain.ConfigVersion, error) {
	if s.pending == nil {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version awaiting escalation")
	}
	return *s.pending, nil
}
func (s *fakeConfigStore) UpdateStatus(_ context.Context, v domain.ConfigVersion) (domain.ConfigVersion, error) {
	return v, nil
}

func newTestHandoff() (*health.Handoff, *diagnostics.Registry, *runtime.Lifecycle, *runtime.MigrationTracker, *fakeConfigStore) {
	registry := diagnostics.NewRegistry()
	registry.Register("postgres", fakeReporter{health: domain.ComponentHealth{Component: "postgres", Health: domain.HealthHealthy}})
	lifecycle := runtime.NewLifecycle()
	lifecycle.MarkRunning()
	migrations := runtime.NewMigrationTracker()
	migrations.Begin()
	migrations.Complete(nil)
	configStore := &fakeConfigStore{}

	h := &health.Handoff{
		Metadata:    runtime.BuildGenerationMetadata(builtin.NewRegistry()),
		Registry:    registry,
		Lifecycle:   lifecycle,
		Migrations:  migrations,
		ConfigStore: configStore,
	}
	return h, registry, lifecycle, migrations, configStore
}

func getJSON(t *testing.T, url string, out any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp
}

// TestHandoffEndpointsAgainstARunningInstance is the Supervisor gate
// proven directly: every required endpoint is exercised
// over real HTTP against a real running httptest.Server, not called as a
// bare Go function.
func TestHandoffEndpointsAgainstARunningInstance(t *testing.T) {
	h, _, _, _, _ := newTestHandoff()
	server := httptest.NewServer(h.Mux())
	defer server.Close()

	t.Run("metadata", func(t *testing.T) {
		var got runtime.GenerationMetadata
		resp := getJSON(t, server.URL+"/metadata", &got)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got.ContractID != contracts.ContractID {
			t.Fatalf("ContractID = %q, want %q", got.ContractID, contracts.ContractID)
		}
	})

	t.Run("readyz healthy", func(t *testing.T) {
		var got runtime.ReadinessResult
		resp := getJSON(t, server.URL+"/readyz", &got)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if !got.Ready {
			t.Fatal("expected Ready = true")
		}
	})

	t.Run("healthz alive", func(t *testing.T) {
		var got runtime.LivenessResult
		resp := getJSON(t, server.URL+"/healthz", &got)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if !got.Alive {
			t.Fatal("expected Alive = true")
		}
	})

	t.Run("migrations complete", func(t *testing.T) {
		var got runtime.MigrationStatus
		resp := getJSON(t, server.URL+"/migrations", &got)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got.Phase != runtime.MigrationComplete {
			t.Fatalf("Phase = %q, want %q", got.Phase, runtime.MigrationComplete)
		}
	})

	t.Run("config no active version", func(t *testing.T) {
		var got runtime.ConfigActivationStatus
		resp := getJSON(t, server.URL+"/config", &got)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got.HasActiveVersion {
			t.Fatal("expected HasActiveVersion = false")
		}
	})
}

func TestReadyzReturns503WhenAComponentIsUnavailable(t *testing.T) {
	registry := diagnostics.NewRegistry()
	registry.Register("postgres", fakeReporter{health: domain.ComponentHealth{Component: "postgres", Health: domain.HealthUnavailable}})
	h := &health.Handoff{
		Metadata:    runtime.BuildGenerationMetadata(builtin.NewRegistry()),
		Registry:    registry,
		Lifecycle:   runtime.NewLifecycle(),
		Migrations:  runtime.NewMigrationTracker(),
		ConfigStore: &fakeConfigStore{},
	}
	server := httptest.NewServer(h.Mux())
	defer server.Close()

	var got runtime.ReadinessResult
	resp := getJSON(t, server.URL+"/readyz", &got)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got.Ready {
		t.Fatal("expected Ready = false")
	}
}

func TestHealthzReturns503OnceLifecycleIsStopping(t *testing.T) {
	h, _, lifecycle, _, _ := newTestHandoff()
	server := httptest.NewServer(h.Mux())
	defer server.Close()

	lifecycle.MarkStopping()

	var got runtime.LivenessResult
	resp := getJSON(t, server.URL+"/healthz", &got)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got.Alive {
		t.Fatal("expected Alive = false once Stopping")
	}
}

func TestMigrationsReturns503WhenFailed(t *testing.T) {
	h, _, _, migrations, _ := newTestHandoff()
	migrations.Begin()
	migrations.Complete(errors.New("schema behind, migration required"))
	server := httptest.NewServer(h.Mux())
	defer server.Close()

	var got runtime.MigrationStatus
	resp := getJSON(t, server.URL+"/migrations", &got)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got.Phase != runtime.MigrationFailed {
		t.Fatalf("Phase = %q, want %q", got.Phase, runtime.MigrationFailed)
	}
	if got.Detail == "" {
		t.Fatal("expected a non-empty Detail explaining the failure")
	}
}

func TestConfigReflectsActiveVersion(t *testing.T) {
	h, _, _, _, configStore := newTestHandoff()
	configStore.active = &domain.ConfigVersion{ID: "cv-1", Status: domain.ConfigActive}
	server := httptest.NewServer(h.Mux())
	defer server.Close()

	var got runtime.ConfigActivationStatus
	resp := getJSON(t, server.URL+"/config", &got)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !got.HasActiveVersion || got.VersionID != "cv-1" {
		t.Fatalf("got = %+v", got)
	}
}

// The two binaries have to agree on these three keys, and nothing structurally
// holds them together (ADR 0129).
//
// **This is the same hazard the Supervisor's telemetry had**: a shape written by
// one process and read by another, where a key quietly renamed on one side reads
// as an absent value on the other rather than as an error. The Supervisor's
// reader is `UpgradeRequest` in its own repository; this pins what it is reading.
func TestTheUpgradeHandoffKeysAreTheOnesTheSupervisorReads(t *testing.T) {
	handoff := &health.Handoff{UpgradeStore: &fakePendingUpgrades{
		version: "v0.4.0", at: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
	}}
	recorder := httptest.NewRecorder()
	handoff.Mux().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/upgrade", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — a pending request is not an error condition", recorder.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "pending,requestedAt,version" {
		t.Fatalf("keys are %v, want pending,requestedAt,version", keys)
	}
	if got["pending"] != true || got["version"] != "v0.4.0" {
		t.Fatalf("body is %v", got)
	}
}

// Nothing pending answers 200 with pending:false rather than a status code
// carrying meaning: the Supervisor polls this every few seconds, and an absent
// request must not be indistinguishable from a Platform that is unwell.
func TestNoPendingUpgradeStillAnswersOK(t *testing.T) {
	handoff := &health.Handoff{UpgradeStore: &fakePendingUpgrades{}}
	recorder := httptest.NewRecorder()
	handoff.Mux().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/upgrade", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"pending":false`) {
		t.Fatalf("body is %s", recorder.Body.String())
	}
}

type fakePendingUpgrades struct {
	version string
	at      time.Time
}

func (f *fakePendingUpgrades) Request(context.Context, domain.UpgradeRequest) error { return nil }
func (f *fakePendingUpgrades) Settle(context.Context) error                         { return nil }
func (f *fakePendingUpgrades) Pending(context.Context) (domain.UpgradeRequest, error) {
	if f.version == "" {
		return domain.UpgradeRequest{}, contracts.NewError(contracts.NotFound, "nothing requested")
	}
	return domain.UpgradeRequest{Version: f.version, RequestedAt: f.at}, nil
}
