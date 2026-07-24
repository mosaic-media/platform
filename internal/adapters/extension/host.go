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
	"path/filepath"
	"sort"

	goplugin "github.com/hashicorp/go-plugin"
	"github.com/mosaic-media/sdk/host"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/transport/moduleproxy"
)

// Config is what the Platform needs to launch one module.
type Config struct {
	// BinaryPath is the module executable. The Platform installs it and verifies
	// its signature and digest before it reaches Launch (ADR 0065's mechanism,
	// Platform-side per ADR 0079); this function is the spawn step, downstream of
	// that verification, and does not re-check — the check belongs where the
	// download lands, not at every launch of an already-verified binary.
	BinaryPath string

	// DeclaredManifest is what the module's *manifest file* claimed, read
	// without executing the binary (ADR 0065). The running binary is checked
	// against it at connect time — see [Launch].
	//
	// The zero value skips the check, which is correct only where there is no
	// manifest file to compare against: today's dev and test paths, before the
	// Platform's install-and-verify path exists. It is not a way to opt out in
	// production.
	DeclaredManifest v1.Manifest

	// Content and Telemetry are what the module calls back into. Every call it
	// makes re-authorises as the invoking user (ADR 0017) — this package grants
	// no authority of its own.
	Content   v1.ContentService
	Telemetry v1.Telemetry

	// Env is extra environment for the module process, appended to the
	// Platform's own. It exists for the lifecycle tests to drive a controlled
	// crash; a production launch leaves it empty, and the egress design (ADR
	// 0064) deliberately does not pass module *configuration* this way — module
	// settings are the opaque document (ADR 0021), not the environment. The
	// egress proxy address below is the exception, and it is Platform plumbing
	// rather than module configuration.
	Env []string

	// AllowPrivateEgress is the operator override on the egress proxy (ADR
	// 0064): the module may reach loopback, RFC1918 and link-local targets. It
	// defaults off, so a module fetching a user-supplied URL cannot reach the
	// host's own network. A test whose fake upstream is on loopback sets it, the
	// same override an operator sets for a service on their LAN.
	//
	// The module's egress proxy shares Config.Telemetry above for its per-host
	// attribution (seam 9): host only, never content.
	AllowPrivateEgress bool
}

// Module is a launched module process and the capability it serves.
type Module struct {
	// Capability is what the registry holds. It is a proxy, and nothing above
	// the registry can tell.
	Capability v1.Capability

	client      *goplugin.Client
	rpcClient   goplugin.ClientProtocol
	invocations *invocations
	proxy       *moduleproxy.Proxy
}

// alive reports whether the module process is still answering, not merely still
// running (ADR 0064: the Platform is the only component that can tell the
// difference, which is why lifecycle lives here). The gRPC ping is one round
// trip to the child, so a wedged-but-not-exited process fails it; deadline is
// the caller's to set.
func (m *Module) alive(ctx context.Context) bool {
	if m.client.Exited() {
		return false
	}
	// Ping has no context parameter, so a hung process would block it forever.
	// Bounding it is the whole point of an active probe over a bare Exited()
	// check, so the ping runs in a goroutine the deadline can abandon.
	done := make(chan error, 1)
	go func() { done <- m.rpcClient.Ping() }()
	select {
	case err := <-done:
		return err == nil
	case <-ctx.Done():
		return false
	}
}

// LiveInvocations reports how many invocation handles are currently valid. It
// exists so a test can assert the table empties — a handle that outlives its
// invocation is the failure this whole mechanism prevents, and a leak is
// otherwise invisible until it is large.
func (m *Module) LiveInvocations() int {
	if m.invocations == nil {
		return 0
	}
	return m.invocations.count()
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
	if m.proxy != nil {
		m.proxy.Close()
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

	// The invocation table, and the two wrappers that use it (ADR 0064). The
	// module never sees a real session reference: it is handed a handle that
	// stops resolving the moment the invocation returns, and the ContentService
	// it calls back into exchanges that handle for the Caller it was minted
	// for. Both halves are needed — minting without resolving would hand out
	// meaningless strings, and resolving without minting would have nothing to
	// resolve.
	inv := newInvocations()

	// The egress proxy (ADR 0064). Every outbound call the module makes routes
	// through it — HTTP_PROXY/HTTPS_PROXY below point the module's HTTP client at
	// it — so the deny list that guarded the in-process client's dials guards
	// this one's too, and every host the module contacts is attributed to it.
	// One proxy per module makes that attribution inherent.
	label := cfg.DeclaredManifest.ID
	if label == "" {
		label = filepath.Base(cfg.BinaryPath)
	}
	proxy, err := moduleproxy.Start(moduleproxy.Options{
		ModuleID:     label,
		AllowPrivate: cfg.AllowPrivateEgress,
		Log:          cfg.Telemetry,
	})
	if err != nil {
		return nil, contracts.WrapError(contracts.Unavailable, "extension: starting egress proxy", err)
	}
	// The proxy outlives Launch only on success. Every error path below closes
	// it here rather than at each return, so a failed launch cannot leak a
	// listener.
	launched := false
	defer func() {
		if !launched {
			proxy.Close()
		}
	}()

	cmd := exec.Command(cfg.BinaryPath) //nolint:gosec // the path is the Platform's own, verified at install before we see it (ADR 0079).
	// Route the module's HTTP client through the proxy. Go's default transport
	// reads these, so a module using an ordinary client — as every module does —
	// routes through without any change to its code. NO_PROXY is cleared so
	// nothing is exempted: the loopback the proxy itself listens on is reached by
	// the module dialling the proxy, not by the module being allowed to bypass it
	// for loopback targets.
	cmd.Env = append(cmd.Environ(),
		// The Mosaic-specific variable sdk/host reads to force *all* egress
		// through the proxy, loopback included — which the standard variables
		// below cannot do, because Go's ProxyFromEnvironment excludes loopback
		// (ADR 0064; sdk/host's egress.go carries the detail).
		host.EgressProxyEnv+"="+proxy.Addr(),
		// The standard variables too, as defence in depth: a module not built
		// against sdk/host, or one deliberately using ProxyFromEnvironment, still
		// routes its non-loopback egress through the proxy.
		"HTTP_PROXY="+proxy.Addr(),
		"HTTPS_PROXY="+proxy.Addr(),
		"NO_PROXY=",
	)
	if len(cfg.Env) > 0 {
		cmd.Env = append(cmd.Env, cfg.Env...)
	}

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: host.Handshake,
		Plugins: host.ClientPluginMap(
			&resolvingContent{inner: cfg.Content, inv: inv},
			cfg.Telemetry,
			categoryOf,
		),
		Cmd: cmd,
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

	launched = true
	return &Module{
		Capability:  &guardedCapability{inner: capability, inv: inv},
		client:      client,
		rpcClient:   rpcClient,
		invocations: inv,
		proxy:       proxy,
	}, nil
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
	// Version is deliberately not compared. The manifest's version is the release
	// tag the publisher stamped (`build-manifest -version` overrides the build's
	// own, so the catalogue reads `v0.24.0`), whereas the binary self-reports its
	// build-graph version (`0.24.0+dirty`, `(devel)`), which is formatted
	// differently and carries a VCS-dirty marker a clean tag never will. They name
	// the same release by two conventions, so equality is the wrong test. Identity
	// is the id and roles below; that the running bytes ARE the ones the manifest
	// vouched for is the digest check at install, which no self-reported string can
	// add to. A live install caught this: a correctly published module was refused
	// only because its self-reported version was spelled differently from its tag.

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
