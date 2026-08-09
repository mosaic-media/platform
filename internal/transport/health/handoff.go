// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package health is the Supervisor handoff HTTP transport:
// readiness, liveness, Generation metadata, migration status and config
// activation status. Every handler's entire body calls into
// internal/platform/runtime — the same "transport calls Platform-tier
// logic only, never a database or Module directly" rule
// internal/transport/auth already enforces, generalized to every
// transport. boundary_test.go statically enforces it.
// The shutdown hook itself is not an HTTP endpoint — it is a hook, and a
// process-level signal handler (the composition root) is what actually
// invokes runtime.Shutdown; see cmd/mosaic-platform/main.go.
package health

import (
	"encoding/json"
	"net/http"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/runtime"
)

// Handoff wires the Supervisor handoff surface to a real, executable
// HTTP mux. It holds no PostgreSQL or Module-specific types —
// only the adapter-agnostic pieces internal/platform/runtime already
// defines — so this package stays a pure transport, testable without a
// database.
type Handoff struct {
	Metadata    runtime.GenerationMetadata
	Registry    *diagnostics.Registry
	Lifecycle   *runtime.Lifecycle
	Migrations  *runtime.MigrationTracker
	ConfigStore contracts.ConfigStore
	// UpgradeStore is where a request for a version waits (ADR 0129). Nil is a
	// build with no upgrade path, which reports nothing pending.
	UpgradeStore contracts.UpgradeStore
}

// Mux builds the Supervisor handoff HTTP surface:
//   - GET /metadata   — Generation metadata (Platform version, contract
//     version, built-in Modules, assets)
//   - GET /readyz     — readiness (200 when safe to activate, 503 otherwise)
//   - GET /healthz    — liveness (200 while the process should keep
//     running, 503 once it has begun graceful shutdown)
//   - GET /migrations — migration status (required/running/complete/failed)
//   - GET /config     — active configuration version and reload class
//   - GET /upgrade    — the version somebody asked the Supervisor to install
func (h *Handoff) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata", h.handleMetadata)
	mux.HandleFunc("/readyz", h.handleReadiness)
	mux.HandleFunc("/healthz", h.handleLiveness)
	mux.HandleFunc("/migrations", h.handleMigrations)
	mux.HandleFunc("/config", h.handleConfigActivation)
	mux.HandleFunc("/upgrade", h.handleUpgrade)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (h *Handoff) handleMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.Metadata)
}

func (h *Handoff) handleReadiness(w http.ResponseWriter, r *http.Request) {
	result := runtime.CheckReadiness(r.Context(), h.Registry)
	status := http.StatusOK
	if !result.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, result)
}

func (h *Handoff) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	result := runtime.CheckLiveness(h.Lifecycle)
	status := http.StatusOK
	if !result.Alive {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, result)
}

func (h *Handoff) handleMigrations(w http.ResponseWriter, _ *http.Request) {
	status := h.Migrations.Status()
	httpStatus := http.StatusOK
	if status.Phase == runtime.MigrationFailed {
		httpStatus = http.StatusServiceUnavailable
	}
	writeJSON(w, httpStatus, status)
}

// handleUpgrade reports what somebody asked for, which is the one remedy on the
// resolution register the Platform cannot perform itself (ADR 0129).
//
// Always 200, including "nothing pending": the Supervisor polls this every few
// seconds and a status code carrying meaning would make an absent request
// indistinguishable from a Platform that is unwell.
func (h *Handoff) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, runtime.CheckUpgrade(r.Context(), h.UpgradeStore))
}

func (h *Handoff) handleConfigActivation(w http.ResponseWriter, r *http.Request) {
	result := runtime.CheckConfigActivation(r.Context(), h.ConfigStore)
	writeJSON(w, http.StatusOK, result)
}
