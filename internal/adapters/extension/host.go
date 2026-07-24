// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package extension launches an out-of-process module and adapts it into a
// v1.Capability the rest of the Platform holds like any other (ADR 0064,
// ADR 0077).
//
// It lives under internal/adapters/ rather than internal/modules/ because of
// what it is: internal/modules/ holds built-in modules that *implement* a
// Platform contract in this process (Postgres implements StorageAdapter), and
// this implements none — it adapts an external process into a published
// contract, which is what an adapter does. It is worth stating because the
// package-tier model in CLAUDE.md names three tiers and this is the first thing
// that is host *of* a tier rather than a member of one.
//
// Nothing above the capability registry knows this package exists. That is the
// property ADR 0064 is arranged around: ImportContent, provider resolution and
// capability-gated affordances are unchanged, because what they hold is a
// v1.Capability either way.
package extension

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"

	goplugin "github.com/hashicorp/go-plugin"
	"github.com/mosaic-media/sdk/host"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Config is what the Platform needs to launch one module.
type Config struct {
	// BinaryPath is the module executable. The Supervisor installs it and
	// verifies its signature and digest before the Platform is ever handed the
	// path (ADR 0065); this package assumes that has happened and does not
	// re-check, because a second unverified check would suggest it is the gate.
	BinaryPath string

	// DeclaredManifest is what the module's *manifest file* claimed, read
	// without executing the binary (ADR 0065). The running binary is checked
	// against it at connect time — see [Launch].
	//
	// The zero value skips the check, which is correct only where there is no
	// manifest file to compare against: today's dev and test paths, before the
	// Supervisor exists. It is not a way to opt out in production.
	DeclaredManifest v1.Manifest

	// Content and Telemetry are what the module calls back into. Every call it
	// makes re-authorises as the invoking user (ADR 0017) — this package grants
	// no authority of its own.
	Content   v1.ContentService
	Telemetry v1.Telemetry
}

// Module is a launched module process and the capability it serves.
type Module struct {
	// Capability is what the registry holds. It is a proxy, and nothing above
	// the registry can tell.
	Capability v1.Capability

	client *goplugin.Client
}

// Close terminates the module process. It is safe to call more than once.
//
// The Platform owns the process rather than the Supervisor (ADR 0064): a module
// crash must be a degraded capability rather than a Generation event, and the
// Platform is the only component that knows whether a module is *answering* as
// opposed to merely running.
func (m *Module) Close() {
	if m.client != nil {
		m.client.Kill()
	}
}

// Launch starts the module process, completes the handshake, and returns a
// capability.
//
// Two checks run before it returns, in the order ADR 0064 requires — declaration
// first, then enforcement:
//
//  1. **go-plugin's handshake**, which refuses a binary that is not a Mosaic
//     module or was built against a different SDK major version.
//  2. **The manifest check**: the running binary's manifest must agree with what
//     the manifest file declared, including that every declared role is one the
//     binary actually serves. A mismatch refuses the connection rather than
//     leaving a module registered under an identity it does not have.
//
// The second matters more than it looks, because the registry resolves roles
// from the manifest (a proxy satisfies every provider interface, so a type
// assertion cannot be the test). An unchecked manifest would therefore be an
// unchecked routing table.
func Launch(cfg Config) (*Module, error) {
	if cfg.BinaryPath == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "extension: no binary path")
	}

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: host.Handshake,
		Plugins:         host.ClientPluginMap(cfg.Content, cfg.Telemetry, categoryOf),
		Cmd:             exec.Command(cfg.BinaryPath), //nolint:gosec // the path is the Supervisor's, verified before we see it.
		AllowedProtocols: []goplugin.Protocol{
			// gRPC only. net/rpc is Go-specific and would close the door on a
			// module written in another language (ADR 0077).
			goplugin.ProtocolGRPC,
		},
		// A Unix socket rather than a loopback TCP port: no port allocation, no
		// accidental network exposure, and filesystem permissions as the access
		// control (ADR 0064).
		UnixSocketConfig: &goplugin.UnixSocketConfig{},
		// go-plugin logs the child's stderr through this. Left at its default
		// for now; wiring it into the telemetry plane is ADR 0077's open
		// question, and guessing at it here would be inventing a mapping.
		Managed: true,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, contracts.WrapError(contracts.Unavailable,
			fmt.Sprintf("extension: connecting to %s", cfg.BinaryPath), err)
	}

	raw, err := rpcClient.Dispense(host.PluginName)
	if err != nil {
		client.Kill()
		return nil, contracts.WrapError(contracts.Unavailable,
			fmt.Sprintf("extension: dispensing %s", cfg.BinaryPath), err)
	}

	capability, ok := raw.(v1.Capability)
	if !ok {
		client.Kill()
		return nil, contracts.NewError(contracts.Internal,
			fmt.Sprintf("extension: %s served %T, which is not a v1.Capability", cfg.BinaryPath, raw))
	}

	if err := checkManifest(cfg.DeclaredManifest, capability.Manifest()); err != nil {
		client.Kill()
		return nil, err
	}

	return &Module{Capability: capability, client: client}, nil
}

// checkManifest compares what the manifest file declared against what the
// running binary reports. An empty declared id means there was no manifest file
// to check against, which is the dev and test path today.
func checkManifest(declared, running v1.Manifest) error {
	if running.ID == "" {
		return contracts.NewError(contracts.Unavailable,
			"extension: the module reported no manifest id")
	}
	if declared.ID == "" {
		return nil
	}

	if declared.ID != running.ID {
		return contracts.NewError(contracts.Conflict, fmt.Sprintf(
			"extension: manifest declares id %q but the binary reports %q", declared.ID, running.ID))
	}
	if declared.Version != "" && declared.Version != running.Version {
		return contracts.NewError(contracts.Conflict, fmt.Sprintf(
			"extension: manifest declares version %q but the binary reports %q",
			declared.Version, running.Version))
	}

	// Every role the manifest file declared must be one the binary also
	// declares. The reverse is allowed: a binary reporting *fewer* roles than
	// its manifest claimed is the failure this catches, while a binary
	// reporting more is a manifest that is merely out of date, and refusing
	// that would make a module unlaunchable over a documentation lag.
	runningRoles := make(map[v1.Role]bool, len(running.Provides))
	for _, r := range running.Provides {
		runningRoles[r] = true
	}
	var missing []string
	for _, r := range declared.Provides {
		if !runningRoles[r] {
			missing = append(missing, string(r))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return contracts.NewError(contracts.Conflict, fmt.Sprintf(
			"extension: manifest declares role(s) %v that %q does not serve", missing, running.ID))
	}
	return nil
}

// categoryOf lets sdk/host name the category of a Platform error without
// importing the Platform's internal vocabulary. It is the injection point
// described in that package: the harness compiles against the SDK, where these
// categories are deliberately not published (ADR 0016).
func categoryOf(err error) string {
	if err == nil {
		return ""
	}
	// CategoryOf returns Internal for anything uncategorised, so an error that
	// is not a Platform error is reported as internal rather than as nothing.
	// That matches what a caller in this process would see.
	var perr *contracts.Error
	if !errors.As(err, &perr) {
		return ""
	}
	return string(contracts.CategoryOf(err))
}

var _ = context.Background // reserved: lifecycle takes a context in slice 2.4.
