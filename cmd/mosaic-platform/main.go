// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Command mosaic-platform is the Platform process entry point. Its
// responsibility is dependency bootstrap only; it must stay free of
// business logic.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	aiostreams "github.com/mosaic-media/module-aiostreams"
	cinemeta "github.com/mosaic-media/module-cinemeta"
	fanarttv "github.com/mosaic-media/module-fanart-tv"
	remoteplayback "github.com/mosaic-media/module-remote-playback"
	stremio "github.com/mosaic-media/module-stremio-addons"
	tmdb "github.com/mosaic-media/module-tmdb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/mosaic-media/contracts/gen/mosaic/auth/v1/authv1connect"
	"github.com/mosaic-media/contracts/gen/mosaic/session/v1/sessionv1connect"
	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/composition/bootstrap"
	"github.com/mosaic-media/platform/internal/composition/builtin"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/events"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/runtime"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/artwork"
	authtransport "github.com/mosaic-media/platform/internal/transport/auth"
	"github.com/mosaic-media/platform/internal/transport/health"
	"github.com/mosaic-media/platform/internal/transport/netguard"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/rpc"
	"github.com/mosaic-media/platform/internal/transport/session"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// postgresDSNEnv names the environment variable carrying the PostgreSQL
// connection string. Reading storage configuration from the environment is
// a bridge until a config-loading pipeline reads it from an Active
// ConfigVersion instead; it is recorded here so that work can replace it
// deliberately.
const postgresDSNEnv = "MOSAIC_POSTGRES_DSN"

// healthAddrEnv names the environment variable carrying the address the
// Supervisor handoff HTTP surface listens on.
const healthAddrEnv = "MOSAIC_HEALTH_ADDR"

const defaultHealthAddr = ":8080"

// apiAddrEnv names the environment variable carrying the address the
// client-facing Connect API listens on. It is a separate surface from the
// Supervisor handoff: the handoff is operational (readiness, liveness), the
// API is where a client — or a compiled-in capability's caller — reaches the
// application services. Read from the environment for the same bridging
// reason as the DSN above.
const apiAddrEnv = "MOSAIC_API_ADDR"

const defaultAPIAddr = ":8081"

// bootstrapAdminUserEnv and bootstrapAdminPasswordEnv name the credentials for
// the optional first-run administrator. Both must be set for the bootstrap to
// run; it is idempotent once that user exists.
const (
	bootstrapAdminUserEnv     = "MOSAIC_BOOTSTRAP_ADMIN_USERNAME"
	bootstrapAdminPasswordEnv = "MOSAIC_BOOTSTRAP_ADMIN_PASSWORD"
)

// logLevelEnv names the environment variable carrying the minimum level this
// process records. It is read from the environment for the same bridging
// reason as the DSN above: the level is a Hot-class configuration field once
// the config pipeline owns it, and this stands in until then.
const logLevelEnv = "MOSAIC_LOG_LEVEL"

// moduleSelectionEnv names the environment variable carrying the module
// selection (ADR 0063): a comma-separated list of core module ids to wire in.
// Unset means every module the binary carries, so an unconfigured deployment
// gets the full set. Set-but-empty selects nothing, which
// RequireComposedRoleClasses then refuses if it leaves a required class empty.
// Selection is Generation-class configuration; this env read is the bridge
// until the Supervisor activates a Generation with a selection, the same bridge
// the DSN and log level take.
const moduleSelectionEnv = "MOSAIC_MODULES"

// extensionsDirEnv names the directory the Platform stores installed extension
// modules under (ADR 0081): their verified binaries and cached manifests, read
// back at boot to re-adopt what a user installed. It must be a persistent,
// writable location for installs to survive a restart; the default is relative
// to the working directory, which a container deployment overrides with a
// mounted volume. Like the DSN, it is an infrastructure path read from the
// environment rather than a versioned config field.
const extensionsDirEnv = "MOSAIC_EXTENSIONS_DIR"

const defaultExtensionsDir = "mosaic-extensions"

// serviceName and serviceVersion identify this process on every record it
// emits. Mosaic is one host running more than one process — this one, the
// Supervisor when it exists — so which process spoke is a required dimension
// rather than a decoration (ADR 0053).
const (
	serviceName    = "mosaic-platform"
	serviceVersion = "0.1.0"
)

// telemetryLogPath is the durable local sink. It is the one that survives a
// crash and keeps working when PostgreSQL does not, which is the case it
// exists for (ADR 0058).
const telemetryLogPath = "logs/mosaic-platform.log"

// telemetryPartitionsAhead is how many days of telemetry partitions to keep
// created in advance. Generous on purpose: partition creation belongs in a
// scheduled job, the jobs runner does not exist (ADR 0058), and a process that
// runs for a fortnight without restarting must not reach a midnight with
// nowhere to put its records.
const telemetryPartitionsAhead = 14

// telemetryMaintenanceInterval is how often partitions are extended and
// expired ones dropped. Hourly is far more often than a daily boundary needs,
// which is the point: it means a missed tick costs nothing.
const telemetryMaintenanceInterval = time.Hour

// superuserPermissions is the authority the first user receives: every action
// the application services check (ADR 0069).
//
// The set lives in the app package beside the action constants, so adding an
// action is a compile-time decision about which tier holds it rather than
// something a reader of this file has to notice was omitted.
func superuserPermissions() []domain.Permission {
	actions := app.SuperuserActions()
	perms := make([]domain.Permission, len(actions))
	for i, a := range actions {
		perms[i] = domain.Permission(a)
	}
	return perms
}

// registerCapabilities wires the module capabilities compiled into this binary
// into the registry the Platform resolves through. It is the one place that
// names concrete modules — the composition-root equivalent of the Build
// Pipeline's generated imports (ADR 0007). Modules land here as they are added;
// the Stremio addon-source module was the first.
//
// Three of the six are *core* modules under ADR 0062's guarantee clause —
// Cinemeta and TMDB back the metadata/search class ADR 0035 requires, remote
// playback backs the consumer class without which the library is inert — and
// three, Stremio, AIOStreams and fanart.tv, are extension modules. Nothing here
// distinguishes them by tier, and that is the design: the tier is a delivery and
// coupling decision, not a contract decision, so all six implement the same SDK
// interfaces and register identically.
//
// What *does* distinguish them now is selection (ADR 0063). A module the
// selection does not name is never constructed — its New is not called, no
// resource it holds is opened, and the registry never sees it. The default is
// every module, so an unconfigured deployment is unchanged; a selection is how
// an admin drops one. The Supervisor will drive this by activating a Generation
// with a different selection; until it exists the selection is read from the
// environment, the same bridge the DSN and log level take.
//
// fanart.tv is the first module here that fills neither a source role nor a
// consumer role (ADR 0075) — it enriches content another module already
// identified, reached only through the artwork enrichment pass rather than
// through any ContentRef, so its registration looks identical to the others
// while its invocation path does not.
func registerCapabilities(reg *app.CapabilityRegistry, sel app.Selection, httpClient *http.Client, boot *telemetry.Logger) {
	for _, d := range moduleDescriptors(httpClient) {
		if !sel.Selected(d.id) {
			boot.Info("module not selected; not constructed", telemetry.String("module", d.id))
			continue
		}
		reg.Register(d.construct())
	}
}

// moduleDescriptor pairs a module's stable id with a thunk that constructs it.
// The thunk is what makes "not selected, not constructed" real: an unselected
// module's New is never called, so nothing it holds is opened.
type moduleDescriptor struct {
	id        string
	construct func() v1.Capability
}

// moduleDescriptors is the list the selection is applied to — the one place that
// names concrete modules, the composition-root equivalent of the Build
// Pipeline's generated imports (ADR 0007).
func moduleDescriptors(httpClient *http.Client) []moduleDescriptor {
	return []moduleDescriptor{
		// The Cinemeta metadata module — the zero-configuration floor under the
		// required metadata/search class (ADR 0072). It is what makes a fresh
		// install work with nothing set: no key, no URL, no settings document at
		// all, so there is nothing about it a deployment can get wrong. This is
		// why the default selection includes everything — dropping it from the
		// default would boot an inert Mosaic.
		//
		// It sorts before TMDB in id order, which the registry resolves in, and
		// "cinemeta" ahead of "tmdb" is an accident rather than a policy — which
		// provider wins for a given field is an open seam neither ordering
		// answers.
		{cinemeta.CapabilityID, func() v1.Capability { return cinemeta.New(httpClient) }},
		// The TMDB metadata module — the richer provider of the same class, for a
		// deployment willing to hold an API key. It needs one, set through its own
		// settings screen (ADR 0038), and every role reports that plainly until
		// one exists; Cinemeta is what keeps the class satisfied in the meantime.
		{tmdb.CapabilityID, func() v1.Capability { return tmdb.New(httpClient) }},
		// The Stremio addon-source module. The addons it sources from are
		// user-managed settings (ADR 0021), set at runtime through configureModule
		// rather than baked in at composition, so the module is available even
		// before any addon is configured.
		//
		// Handed the Platform's client rather than nil (ADR 0055, seam 9): it
		// spans every outbound call and, more importantly, routes through
		// netguard's dial guard. A module builds its own client when given nil,
		// which bypassed the SSRF protection entirely for the one caller that
		// fetches URLs a user supplied.
		{stremio.CapabilityID, func() v1.Capability { return stremio.New(httpClient) }},
		// The AIOStreams module — a stream source for one named upstream, beside
		// the open-ended addon list above. It is the answer to a question that
		// module cannot answer: a Stremio addon is community-made and unreviewed,
		// and Mosaic has no access-control story that makes an arbitrary addon
		// list safe to recommend, so an install that only wants streams should not
		// have to adopt that whole surface. AIOStreams is itself an aggregator, so
		// the breadth survives and the trust decision becomes one instance URL.
		//
		// It sorts before Stremio in id order, which the stream-enrichment fan-out
		// reads as precedence: it is asked first and stops the search when it
		// answers (ADR 0073). That is the intended order and it is alphabetical
		// accident, the same seam as the cinemeta/tmdb ordering above.
		//
		// Handed the Platform's client for the same reason Stremio is: the
		// instance URL is text a user typed, and only that client routes through
		// netguard's dial guard.
		{aiostreams.CapabilityID, func() v1.Capability { return aiostreams.New(httpClient) }},
		// The remote playback module — the first *consumer* capability (ADR 0045).
		// Registering it is what stops the library being inert: the Stremio module
		// above snapshots a stream location at import, and this is what can turn
		// that location back into playable bytes.
		{remoteplayback.CapabilityID, func() v1.Capability { return remoteplayback.New() }},
		// The fanart.tv artwork module — the first module reached only through
		// enrichment rather than through any ContentRef (ADR 0075). It fills
		// RoleArtwork alone: it illustrates content another module identified and
		// must never declare RoleMetadata, RoleSearch or RoleCatalog, which its
		// own boundary test asserts. Registering it costs nothing when it is
		// unconfigured or unaddressable for a title — the artwork enrichment pass
		// is best-effort, and a deployment with no artwork provider sees exactly
		// what it saw before this module existed.
		{fanarttv.CapabilityID, func() v1.Capability { return fanarttv.New(httpClient) }},
	}
}

// moduleIDs is the set of core module ids the binary carries, for validating a
// selection names only modules that exist.
func moduleIDs(httpClient *http.Client) []string {
	descs := moduleDescriptors(httpClient)
	ids := make([]string, len(descs))
	for i, d := range descs {
		ids[i] = d.id
	}
	return ids
}

func main() {
	if err := run(); err != nil {
		// The last resort, and the only write in this process that does not go
		// through telemetry. It runs when run() failed — possibly because
		// building telemetry itself is what failed — so there may be nothing
		// structured left to write to. Written directly rather than through fmt
		// so the standing gate in test/logging stays able to cover this file
		// rather than having to exempt it.
		_, _ = os.Stderr.WriteString("mosaic-platform: " + err.Error() + "\n")
		os.Exit(1)
	}
}

// telemetryMaintenance extends the partition window and drops expired
// partitions on a ticker until ctx ends.
//
// This is a scheduled job wearing a goroutine, and it says so: it wants the
// jobs runner, a scheduler and the system principal (ADR 0017, ADR 0058),
// none of which exist. Running it here is the honest interim rather than
// leaving retention unenforced and calling the phase done — but it means
// retention runs only while the process does, and a Platform that is down for
// a month comes back with a month of records it intended to have dropped.
func telemetryMaintenance(ctx context.Context, store *postgres.TelemetryStore, svc *app.Service, lg *telemetry.Logger) {
	ticker := time.NewTicker(telemetryMaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := store.EnsurePartitions(ctx, now.UTC(), telemetryPartitionsAhead); err != nil {
				lg.Error("extend telemetry partitions failed", telemetry.Err(err))
			}
			// Read per sweep rather than cached at boot, which is what makes
			// the retention fields Hot (ADR 0058): an administrator shortening
			// retention should take effect on the next sweep, not the next
			// restart.
			r := svc.TelemetryRetention(ctx)
			retention := postgres.Retention{Logs: r.Logs, Spans: r.Spans}
			dropped, err := store.DropExpiredPartitions(ctx, now.UTC(), retention)
			if err != nil {
				lg.Error("drop expired telemetry partitions failed", telemetry.Err(err))
				continue
			}
			if dropped > 0 {
				lg.Info("dropped expired telemetry partitions",
					telemetry.Int("partitions", dropped),
					telemetry.Duration("log_retention", retention.Logs),
					telemetry.Duration("span_retention", retention.Spans))
			}
		}
	}
}

func run() error {
	// Telemetry is constructed before anything else, because every line after
	// this one has something worth saying and the only alternative available
	// to it would be fmt.Printf (ADR 0053). Two sinks, neither optional
	// (ADR 0058): the console keeps the boot narration legible to a human at a
	// terminal, and the file is the durable record that survives a crash and
	// still works when the database does not.
	resource := telemetry.NewResource(serviceName, serviceVersion)
	fileSink, err := telemetry.NewFileSink(telemetryLogPath)
	if err != nil {
		return fmt.Errorf("open telemetry log failed: %w", err)
	}
	defer fileSink.Close()
	root := telemetry.New(
		telemetry.MultiSink{telemetry.NewConsoleSink(os.Stdout), fileSink},
		resource,
		telemetry.ParseLevel(os.Getenv(logLevelEnv)),
	)
	boot := root.For("composition-root")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}
	// The boot id names this start of the process. It is minted here today and
	// adopted from the environment when something started us and supplied one
	// — which is the single piece of ADR 0060 that can exist before the
	// Supervisor does, so there is something to hand over to when it arrives.
	boot.Info("booting",
		telemetry.String("environment", cfg.Environment),
		telemetry.String("boot_id", resource.BootID),
		telemetry.String("version", serviceVersion))

	// Register built-in modules the same way an external Module would be
	// discovered. Postgres is the first, required storage module.
	moduleRegistry := builtin.NewRegistry()
	moduleRegistry.Register(postgres.New())
	for _, m := range moduleRegistry.Manifests() {
		boot.Info("registered built-in module",
			telemetry.String("module", m.ID),
			telemetry.String("version", m.Version),
			telemetry.Int("fulfills", len(m.Fulfills)))
	}
	generationMetadata := runtime.BuildGenerationMetadata(moduleRegistry)

	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		// Storage is not configured yet. Keep the scaffold's boot-and-exit
		// behaviour rather than failing, so the process still starts before
		// a DSN is provided by another means.
		// This exits 0, which is indistinguishable from a healthy boot to
		// anything watching exit codes — so it must at least be unmistakable in
		// the log. ADR 0060 names this as one of the failures only a
		// supervising process can properly report.
		boot.Warn("storage not configured; skipping storage bootstrap",
			telemetry.String("expected_env", postgresDSNEnv))
		boot.Info("exiting cleanly")
		return nil
	}

	lifecycle := runtime.NewLifecycle()
	migrations := runtime.NewMigrationTracker()

	// Bootstrap phase, bounded: connect, then run migrations. Migrate fails
	// fast on a missing, incompatible or partially applied schema, so this
	// is the startup gate that refuses to run against a mismatched database:
	// migration failures prevent Runtime startup. Bracketing it with
	// migrations.Begin/Complete is what makes the migration status endpoint
	// reflect real state — Running while this is in flight, Complete or Failed
	// once it returns — rather than a value invented for the endpoint alone.
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 60*time.Second)
	pool, err := postgres.Connect(bootCtx, dsn)
	if err != nil {
		bootCancel()
		return fmt.Errorf("storage connect failed: %w", err)
	}

	migrations.Begin()
	migrateErr := postgres.Migrate(bootCtx, pool)
	migrations.Complete(migrateErr)
	bootCancel()
	if migrateErr != nil {
		pool.Close()
		return fmt.Errorf("storage migration failed: %w", migrateErr)
	}

	set := postgres.New().Bind(pool)
	defer set.Pool.Close()

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 30*time.Second)
	storageHealth, err := set.Health.Check(healthCtx)
	healthCancel()
	if err != nil {
		return fmt.Errorf("storage health check failed: %w", err)
	}
	// Detail is free text a reporter attached, so it is classified rather than
	// printed: it is exactly where a DSN with a password in it would surface.
	boot.Info("storage ready",
		telemetry.String("component", storageHealth.Component),
		telemetry.String("state", string(storageHealth.State)),
		telemetry.Sensitive("detail", storageHealth.Detail))

	// Wire the in-process Event Bus and the outbox worker that drains
	// committed outbox rows into it.
	bus := events.NewBus()
	worker := events.NewWorker(set.Outbox, bus, "outbox-worker")

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	published, err := worker.RunOnce(drainCtx)
	drainCancel()
	if err != nil {
		return fmt.Errorf("outbox drain failed: %w", err)
	}
	boot.Info("outbox drained at boot", telemetry.Int("events", published))

	// Aggregate real component health per the diagnostics model, backing
	// both the local structured log and the /readyz endpoint below.
	diagRegistry := diagnostics.NewRegistry()
	diagRegistry.Register("postgres", set.HealthReporter)
	diagRegistry.Register("event-bus", bus)
	diagRegistry.Register("outbox-worker", worker, "postgres", "event-bus")

	// The second sink (ADR 0058). The file above is durable and survives the
	// database; this one makes records *queryable*, which is what the
	// expert-mode viewer needs and what a flat file cannot serve. Neither is
	// optional and neither replaces the other.
	//
	// It is deliberately attached only now, after storage is up: everything
	// logged before this point describes bringing the database up, and is
	// exactly the narration that must not depend on the database existing.
	telemetryStore := postgres.NewTelemetryStore(set.Pool)
	partitionCtx, partitionCancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = telemetryStore.EnsurePartitions(partitionCtx, time.Now().UTC(), telemetryPartitionsAhead)
	partitionCancel()
	if err != nil {
		// Not fatal. A Platform that refuses to start because it cannot store
		// its own logs has turned an observability problem into an outage, and
		// the file sink still has everything.
		boot.Error("could not create telemetry partitions; the queryable sink will drop records", telemetry.Err(err))
	}
	bufferedSink := telemetry.NewBufferedSink[telemetry.Record](telemetryStore, 0, 0, 0)
	root = root.WithSink(telemetry.MultiSink{
		telemetry.NewConsoleSink(os.Stdout), fileSink, bufferedSink,
	})
	boot = root.For("composition-root")

	// Spans go only to the queryable sink (ADR 0055). A span is a shape — a
	// tree with durations — and rendering one into a flat log file produces
	// noise a human cannot reassemble, so unlike log records there is nothing
	// gained by a second copy on disk. A trace that matters is reconstructed
	// from the database; if the database is gone, the log file still holds the
	// records, which is the part that was ever readable by eye.
	spanSink := telemetry.NewBufferedSink[telemetry.SpanRecord](telemetryStore.Spans(), 0, 0, 0)

	logSnapshot := func(ctx context.Context, label string) {
		for _, h := range diagRegistry.Snapshot(ctx) {
			root.For(h.Component).Info(label, telemetry.ComponentHealthFields(h)...)
		}
	}
	logSnapshot(context.Background(), "boot-time health check")

	// Assemble the application services over the wired contracts. This is the
	// first time the composition root constructs app.Service: every dependency
	// it needs is already on the ContractSet, plus the ABAC policy engine, the
	// event bus as the audit publisher, and the Argon2id password hasher.
	// Which modules to wire in (ADR 0063). Read from the environment, the same
	// bridge the DSN and log level take until a config pipeline owns it, and
	// unset means every module — so an unconfigured deployment gets the full set
	// and works out of the box. A selection is validated against what the binary
	// carries before anything is constructed, so a typo names the mistake rather
	// than silently dropping the module it misspelled.
	moduleClient := netguard.ModuleClient()
	selection := app.SelectAll()
	if spec, ok := os.LookupEnv(moduleSelectionEnv); ok {
		selection = app.ParseSelection(spec)
	}
	if err := selection.Validate(moduleIDs(moduleClient)); err != nil {
		return fmt.Errorf("invalid %s: %w", moduleSelectionEnv, err)
	}

	// Register the selected module capabilities the Platform can invoke. This is
	// the composition-root stand-in for ADR 0007's build-time module selection:
	// modules are registered here explicitly rather than discovered, until the
	// Supervisor activates a Generation with a selection.
	capRegistry := app.NewCapabilityRegistry()
	registerCapabilities(capRegistry, selection, moduleClient, boot)
	// Fail boot if a capability declares a provider role it does not implement
	// (ADR 0027): a role named but unbacked would otherwise surface as a nil
	// provider at invocation, not at composition.
	if err := capRegistry.Verify(); err != nil {
		return fmt.Errorf("capability registry invalid: %w", err)
	}
	// Every required role class must be filled over the composed set — core and
	// extension together (ADR 0063 re-expressing ADR 0035). A Mosaic that cannot
	// identify or find content is inert rather than degraded, so this is the same
	// class of fatal startup error as a missing required built-in module. The
	// required roles come from the role-class table (app.RoleClasses) rather than
	// a hand-kept list here, so adding a required class is a table edit and not a
	// second place to remember. It binds the serving composition and not the
	// app.Service constructor, so tests that build a service directly are
	// unaffected.
	if err := capRegistry.RequireComposedRoleClasses(); err != nil {
		return fmt.Errorf("required capability missing: %w", err)
	}
	for _, m := range capRegistry.Manifests() {
		boot.Info("registered capability",
			telemetry.String("module", m.ID),
			telemetry.String("version", m.Version),
			telemetry.String("name", m.Name),
			telemetry.String("provides", fmt.Sprint(m.Provides)))
	}

	// Establish the default extension-module trust: the official repository,
	// trusted by default with the key compiled into this binary (ADR 0065,
	// ADR 0079). Building it here validates the embedded key at boot — a corrupt
	// or empty key fails now rather than at a user's first install — and records
	// which repository the deployment trusts. Nothing installs from it yet: the
	// runtime install trigger is an admin action whose surface does not exist, so
	// this is the trust anchor standing ready, not an install path in the serve
	// loop.
	extRegistry, err := extension.DefaultRegistry()
	if err != nil {
		return fmt.Errorf("default module repository trust: %w", err)
	}
	if repo, ok := extRegistry.Lookup(extension.OfficialRepositoryName); ok {
		boot.Info("trusting extension repository",
			telemetry.String("repository", repo.Name),
			telemetry.String("url", repo.URL),
			telemetry.Bool("official", repo.Official))
	}

	svc := app.NewService(app.Deps{
		UnitOfWork:       set.UnitOfWork,
		Sessions:         set.Sessions,
		Users:            set.Users,
		Credentials:      set.Credentials,
		Config:           set.Config,
		Permissions:      set.Permissions,
		Nodes:            set.Nodes,
		Parts:            set.Parts,
		Clock:            set.Clock,
		IDs:              set.IDs,
		ContentIDs:       set.ContentIDs,
		Policy:           policy.NewEngine(set.Permissions),
		Events:           bus,
		PasswordVerifier: crypto.NewPasswordHasher(),
		Capabilities:     capRegistry,
		ModuleSettings:   set.ModuleSettings,
		UserPreferences:  set.UserPreferences,
		TelemetryQueries: set.TelemetryQueries,

		PlaybackResolutions: set.PlaybackResolutions,
		PlaybackStates:      set.PlaybackStates,
	})

	// Re-adopt the extension modules a user has installed (ADR 0081). The
	// installed set is durable Platform state and default-empty: a fresh install
	// composes only its core modules and reaches this loop with nothing to do.
	// Each installed record is brought up the way an install brings one up — the
	// verified binary re-checked against its cached manifest and spawned — so a
	// restart reconstructs exactly the set the user last installed, without an
	// operator action and without re-consent.
	//
	// It runs after the Service is built because a module's callbacks re-enter the
	// Service as the invoking user (ADR 0017): the spawned module is handed svc as
	// its ContentService. The Supervised capability it produces registers into the
	// same registry a compiled-in module does, and nothing above the registry can
	// tell the difference. Registration is safe here without a lock because it
	// happens before the serve loop; runtime install/uninstall while serving is a
	// later slice that makes the registry concurrent.
	//
	// An extension that fails to adopt is a degraded capability — logged and
	// skipped, never a boot failure. Extensions fill no required role class (that
	// is core's guarantee, ADR 0035/0072), so one being absent is the ordinary
	// degraded state, not the inert Platform RequireComposedRoleClasses refuses.
	extensionsDir := os.Getenv(extensionsDirEnv)
	if extensionsDir == "" {
		extensionsDir = defaultExtensionsDir
	}
	installer, err := extension.NewOfficialInstaller(extensionsDir)
	if err != nil {
		return fmt.Errorf("extension installer: %w", err)
	}
	adoptCtx, adoptCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer adoptCancel()
	installedExtensions, err := set.InstalledExtensions.List(adoptCtx)
	if err != nil {
		return fmt.Errorf("reading installed extensions: %w", err)
	}
	var adoptedModules []*extension.Supervised
	for _, rec := range installedExtensions {
		// Attribution is the Platform's, fixed here per module: the long-lived
		// telemetry surface the module observes through (ADR 0059), and the egress
		// proxy's per-host attribution both key on this id.
		mtel := app.NewModuleTelemetry(root.For("module."+rec.ModuleID), rec.ModuleID)
		adopted, adoptErr := installer.Adopt(adoptCtx, rec.Repository, rec.ModuleID)
		if adoptErr != nil {
			boot.Error("installed extension could not be adopted; capability degraded",
				telemetry.String("module", rec.ModuleID),
				telemetry.String("repository", rec.Repository),
				telemetry.Err(adoptErr))
			continue
		}
		adopted.Config.Content = svc
		adopted.Config.Telemetry = mtel
		sup, superviseErr := extension.Supervise(adopted.Config, extension.DefaultRestartPolicy(), mtel)
		if superviseErr != nil {
			boot.Error("installed extension could not be launched; capability degraded",
				telemetry.String("module", rec.ModuleID),
				telemetry.Err(superviseErr))
			continue
		}
		capRegistry.Register(sup)
		adoptedModules = append(adoptedModules, sup)
		m := sup.Manifest()
		boot.Info("adopted extension module",
			telemetry.String("module", m.ID),
			telemetry.String("version", m.Version),
			telemetry.String("repository", rec.Repository),
			telemetry.String("provides", fmt.Sprint(m.Provides)))
	}
	// Stop the adopted module processes when the Platform stops, so a module
	// process never outlives the Platform that spawned it. Registered after a
	// successful adoption; the empty default registers nothing.
	defer func() {
		for _, sup := range adoptedModules {
			sup.Close()
		}
	}()

	// The artwork proxy (ADR 0030) re-serves remote poster/backdrop images from
	// the Platform's origin, so a client gets same-origin (CORS-clean) artwork.
	// Its signing key is process-scoped: emitted screens are re-fetched, so a
	// signature need not outlive the process.
	artworkKey := make([]byte, 32)
	if _, err := rand.Read(artworkKey); err != nil {
		return fmt.Errorf("generate artwork key failed: %w", err)
	}
	artworkSigner := artwork.NewSigner(artworkKey)

	// The playback origin (ADR 0045) relays a resolved stream from the
	// Platform's own origin, so a client never holds the upstream URL — which
	// for a debrid link carries a credential. Its key is process-scoped for the
	// same reason the artwork signer's is, and more so: a ticket is minted when
	// playback starts and a restart re-resolves rather than replaying a stale
	// upstream.
	playbackKey := make([]byte, 32)
	if _, err := rand.Read(playbackKey); err != nil {
		return fmt.Errorf("generate playback key failed: %w", err)
	}
	playbackSealer, err := playback.NewSealer(playbackKey)
	if err != nil {
		return fmt.Errorf("build playback sealer failed: %w", err)
	}
	// Stream-copy remux (ADR 0048). MSE takes only fMP4/WebM, so a Matroska
	// release is unplayable in a browser whatever codec is inside; rewriting the
	// container costs almost nothing since the streams are copied, not encoded.
	// ffmpeg is optional — without it the Platform still boots and direct-plays,
	// and a release needing a remux says so rather than failing obscurely.
	playbackRemuxer := playback.NewRemuxer()
	playbackProber := playback.NewProber()
	if playbackRemuxer.Available() && playbackProber.Available() {
		boot.Info("ffmpeg and ffprobe found; per-stream playback decisions enabled")
	} else {
		boot.Warn("ffmpeg/ffprobe not found; playback relays unprobed, so a release whose audio the client cannot decode will play silently")
	}

	// Optionally seed the first administrator. There is no in-band way to
	// grant the very first authority — every command that could is itself
	// policy-gated — so this bridges that gap for initial setup, gated on both
	// env vars being present and idempotent once the user exists. The password
	// is read once and never logged.
	if adminUser := os.Getenv(bootstrapAdminUserEnv); adminUser != "" {
		bootCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		created, err := bootstrap.EnsureAdmin(
			bootCtx, set.UnitOfWork, crypto.NewPasswordHasher(), set.Clock, set.IDs,
			bootstrap.AdminSeed{
				Username:    adminUser,
				Password:    os.Getenv(bootstrapAdminPasswordEnv),
				Permissions: superuserPermissions(),
			},
		)
		cancel()
		if err != nil {
			return fmt.Errorf("bootstrap admin failed: %w", err)
		}
		// The username is a person, so it is classified rather than printed —
		// and Identifier rather than Sensitive, so two records about the same
		// administrator can still be tied together without the log holding who
		// they are.
		if created {
			boot.Info("bootstrapped administrator", telemetry.Identifier("username", adminUser))
		} else {
			boot.Info("administrator already present", telemetry.Identifier("username", adminUser))
		}
	}

	// From here on the process is a genuine long-running Supervisor
	// candidate — start runtime components, pass the readiness probe, be
	// activated — not a boot-and-exit scaffold: the outbox worker's
	// background poll loop runs, and the Supervisor handoff HTTP surface
	// serves readiness, liveness, metadata, migration and config activation
	// status until a shutdown signal arrives.
	serveCtx, stopServe := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopServe()
	// Seed the root logger into the serving context. This is the composition
	// root doing its one job as an edge (ADR 0053): everything started from
	// serveCtx — the outbox worker's poll loop, the session reaper, and every
	// request handler beneath them — reaches telemetry through the context it
	// already receives, and nothing below needs a logger parameter.
	serveCtx = telemetry.Into(serveCtx, root)

	worker.Start(serveCtx)
	// Drain the queryable sink in the background. Started here rather than at
	// construction so records buffered during boot are flushed by the same
	// loop, and stopped in the shutdown block below so the last second of
	// records is not silently discarded.
	bufferedSink.Start(serveCtx)
	spanSink.Start(serveCtx)
	go telemetryMaintenance(serveCtx, telemetryStore, svc, root.For("telemetry"))

	handoff := &health.Handoff{
		Metadata:    generationMetadata,
		Registry:    diagRegistry,
		Lifecycle:   lifecycle,
		Migrations:  migrations,
		ConfigStore: set.Config,
	}
	healthAddr := os.Getenv(healthAddrEnv)
	if healthAddr == "" {
		healthAddr = defaultHealthAddr
	}
	// Request contexts do NOT descend from serveCtx — net/http builds them from
	// BaseContext, which defaults to context.Background(). Without this, every
	// handler's telemetry.From(ctx) returns the no-op logger and the whole
	// edge-seeding story silently writes nothing. It is deliberately a separate
	// context from serveCtx rather than serveCtx itself: serveCtx is cancelled
	// by the shutdown signal, and using it here would abort in-flight requests
	// at exactly the moment graceful shutdown is trying to let them finish.
	requestCtx := telemetry.WithSpanSink(telemetry.Into(context.Background(), root), spanSink)
	baseContext := func(net.Listener) context.Context { return requestCtx }

	httpServer := &http.Server{
		Addr:        healthAddr,
		Handler:     telemetry.HTTPMuxMiddleware("handoff", handoff.Mux()),
		BaseContext: baseContext,
	}

	apiAddr := os.Getenv(apiAddrEnv)
	if apiAddr == "" {
		apiAddr = defaultAPIAddr
	}
	// The client API is Connect, and only Connect (ADR 0061). Two services:
	// AuthService mints the session, then the two-lane SessionService (ADR 0041)
	// carries everything else — unary intents (Navigate/Invoke/SubmitInput/
	// Attach) and one server-streaming Subscribe per session for push. Between
	// them they are the whole surface a client speaks; the GraphQL transport
	// that used to sit beside them is gone.
	authHandler := authtransport.NewHandler(svc)
	authPath, authConnect := authv1connect.NewAuthServiceHandler(
		authHandler,
		connect.WithInterceptors(rpc.TelemetryInterceptor("auth")),
	)

	sessionHandler := session.NewHandler(svc, artworkSigner.Rewrite, playbackSealer, playbackProber)
	sessionHandler.Manager().StartReaper(serveCtx)
	// The session interceptor is the first and most important edge seam
	// (ADR 0055): it is where a user's action enters the Platform, so it is
	// where the Shell's trace is continued and where everything downstream —
	// handlers, application services, modules — inherits its telemetry.
	sessionPath, sessionConnect := sessionv1connect.NewSessionServiceHandler(
		sessionHandler,
		connect.WithInterceptors(rpc.TelemetryInterceptor("session")),
	)

	apiMux := http.NewServeMux()
	// Each plain-HTTP surface is wrapped at its own seam and names itself, so a
	// record says which surface produced it rather than only that it was HTTP.
	apiMux.Handle("/artwork", telemetry.HTTPMiddleware("artwork", artwork.Handler(artworkSigner, artwork.GuardedClient())))
	apiMux.Handle("/playback/", telemetry.HTTPMiddleware("playback", playback.Handler(playbackSealer, playback.Client(), playbackRemuxer)))
	apiMux.Handle(authPath, authConnect)
	apiMux.Handle(sessionPath, sessionConnect)
	// Serve the API over h2c (cleartext HTTP/2) so the two session lanes —
	// concurrent unary intents and the long-lived Subscribe stream — multiplex
	// onto one connection (ADR 0041); Connect still degrades to HTTP/1.1 for the
	// artwork and playback handlers.
	apiServer := &http.Server{
		Addr:        apiAddr,
		Handler:     h2c.NewHandler(apiMux, &http2.Server{}),
		BaseContext: baseContext,
	}
	// On graceful shutdown, close every session so its Subscribe stream ends and
	// the client reconnects rather than erroring (ADR 0041 stream resume, as
	// ADR 0032's "going away" close did for the socket).
	apiServer.RegisterOnShutdown(sessionHandler.Manager().Shutdown)

	// Both servers feed one error channel; whichever fails first ends the
	// serve phase and both are shut down together below.
	serveErrCh := make(chan error, 2)
	serve := func(s *http.Server) {
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErrCh <- err
			return
		}
		serveErrCh <- nil
	}
	go serve(httpServer)
	go serve(apiServer)

	lifecycle.MarkRunning()
	boot.Info("serving supervisor handoff surface", telemetry.String("addr", healthAddr))
	boot.Info("serving auth transport", telemetry.String("addr", apiAddr+authPath))
	boot.Info("serving session transport",
		telemetry.String("addr", apiAddr+sessionPath),
		telemetry.String("transport", "connect-two-lane"))
	boot.Info("ready")

	var serveErr error
	select {
	case <-serveCtx.Done():
		boot.Info("shutdown signal received")
	case serveErr = <-serveErrCh:
		if serveErr != nil {
			boot.Error("http server failed", telemetry.Err(serveErr))
		}
	}

	// Graceful shutdown: stop accepting new HTTP requests, then drain the
	// outbox worker (stop its poll loop and perform one final synchronous
	// RunOnce so any event that became deliverable between the last poll
	// tick and Stop is checkpointed before the process exits). Rollback, if
	// the Supervisor decides to activate an earlier Generation instead of
	// this one, means activating that other binary — this process must never
	// and does not attempt to reverse any database mutation itself.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	_ = apiServer.Shutdown(shutdownCtx)
	_ = httpServer.Shutdown(shutdownCtx)

	result := runtime.Shutdown(shutdownCtx, lifecycle, worker)
	if result.FinalDrainErr != nil {
		boot.Error("final outbox drain failed", telemetry.Err(result.FinalDrainErr))
	} else {
		boot.Info("final outbox drain complete", telemetry.Int("events", result.FinalDrainPublished))
	}
	logSnapshot(context.Background(), "shutdown health check")

	// Flush the queryable sink last, after the shutdown health snapshot above,
	// so the records describing shutdown are themselves stored rather than
	// lost to the thing that stores them closing first. The file sink has had
	// them all along; this is about the queryable copy agreeing.
	if dropped, failed := bufferedSink.Dropped(), bufferedSink.Failed(); dropped > 0 || failed > 0 {
		// Reported before the flush, since reporting it is itself a record.
		// A sink that silently loses records looks exactly like a quiet
		// system, and the difference matters most during whatever caused it.
		boot.Warn("telemetry records were lost",
			telemetry.Int64("dropped_buffer_full", int64(dropped)),
			telemetry.Int64("failed_write", int64(failed)))
	}
	_ = spanSink.Close()
	_ = bufferedSink.Close()

	boot.Info("exiting cleanly")
	return serveErr
}
