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
	remoteplayback "github.com/mosaic-media/module-remote-playback"
	stremio "github.com/mosaic-media/module-stremio-addons"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
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
	graphqltransport "github.com/mosaic-media/platform/internal/transport/graphql"
	"github.com/mosaic-media/platform/internal/transport/health"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/session"
	"github.com/mosaic-media/sdui/gen/mosaic/session/v1/sessionv1connect"
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
// client-facing GraphQL API listens on. It is a separate surface from the
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

// telemetryLogRetention is how long queryable log records are kept before
// their partition is dropped. It is the default for
// telemetry.retention.logs_days, which is a Hot config field an administrator
// can change — reading it from Active config is the work that lands with the
// expert-mode surface; until then this constant is the value in force.
const telemetryLogRetention = 14 * 24 * time.Hour

// telemetryMaintenanceInterval is how often partitions are extended and
// expired ones dropped. Hourly is far more often than a daily boundary needs,
// which is the point: it means a missed tick costs nothing.
const telemetryMaintenanceInterval = time.Hour

// adminPermissions is the authority the bootstrapped administrator receives:
// every action the application services check. It is assembled from the app
// package's action constants rather than string literals so a new action is a
// compile-time addition here, not a silently missing grant.
func adminPermissions() []domain.Permission {
	actions := []policy.Action{
		app.ActionUserCreate, app.ActionUserRead, app.ActionUserList, app.ActionUserStatusUpdate,
		app.ActionSessionCreate, app.ActionSessionRevoke,
		app.ActionPermissionRead, app.ActionRoleCreate, app.ActionRoleGrant,
		app.ActionConfigDraft, app.ActionConfigValidate, app.ActionConfigActivate, app.ActionConfigRead,
		app.ActionContentCreate, app.ActionContentRead, app.ActionContentRelate,
		app.ActionContentBind, app.ActionContentResolve, app.ActionContentImport,
		app.ActionModuleConfigure, app.ActionModuleRead,
	}
	perms := make([]domain.Permission, len(actions))
	for i, a := range actions {
		perms[i] = domain.Permission(a)
	}
	return perms
}

// registerCapabilities wires the optional-module capabilities compiled into
// this binary into the registry the Platform resolves through. It is the one
// place that names concrete modules — the composition-root equivalent of the
// Build Pipeline's generated imports (ADR 0007). Modules land here as they are
// added; the Stremio addon-source module is the first.
func registerCapabilities(reg *app.CapabilityRegistry) {
	// The Stremio addon-source module. It is always registered: the addons it
	// sources from are user-managed settings (ADR 0021), set at runtime through
	// configureModule rather than baked in at composition, so the module is
	// available even before any addon is configured.
	reg.Register(stremio.New(nil))
	// The remote playback module — the first *consumer* capability (ADR 0045).
	// Registering it is what stops the library being inert: the Stremio module
	// above snapshots a stream location at import, and this is what can turn
	// that location back into playable bytes.
	reg.Register(remoteplayback.New())
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
func telemetryMaintenance(ctx context.Context, store *postgres.TelemetryStore, lg *telemetry.Logger) {
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
			dropped, err := store.DropExpiredPartitions(ctx, now.UTC(), telemetryLogRetention)
			if err != nil {
				lg.Error("drop expired telemetry partitions failed", telemetry.Err(err))
				continue
			}
			if dropped > 0 {
				lg.Info("dropped expired telemetry partitions",
					telemetry.Int("partitions", dropped),
					telemetry.Duration("retention", telemetryLogRetention))
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
	bufferedSink := telemetry.NewBufferedSink(telemetryStore, 0, 0, 0)
	root = root.WithSink(telemetry.MultiSink{
		telemetry.NewConsoleSink(os.Stdout), fileSink, bufferedSink,
	})
	boot = root.For("composition-root")

	logSnapshot := func(ctx context.Context, label string) {
		for _, h := range diagRegistry.Snapshot(ctx) {
			root.For(h.Component).Info(label, telemetry.ComponentHealthFields(h)...)
		}
	}
	logSnapshot(context.Background(), "boot-time health check")

	// Assemble the application services over the wired contracts, and build
	// the executable GraphQL schema that projects them. This is the first
	// time the composition root constructs app.Service: every dependency it
	// needs is already on the ContractSet, plus the ABAC policy engine, the
	// event bus as the audit publisher, and the Argon2id password hasher.
	// Register the optional-module capabilities the Platform can invoke. This
	// is the composition-root stand-in for ADR 0007's build-time module
	// selection: modules are registered here explicitly rather than discovered,
	// until the Supervisor's Build Pipeline generates the imports.
	capRegistry := app.NewCapabilityRegistry()
	registerCapabilities(capRegistry)
	// Fail boot if a capability declares a provider role it does not implement
	// (ADR 0027): a role named but unbacked would otherwise surface as a nil
	// provider at invocation, not at composition.
	if err := capRegistry.Verify(); err != nil {
		return fmt.Errorf("capability registry invalid: %w", err)
	}
	for _, m := range capRegistry.Manifests() {
		boot.Info("registered capability",
			telemetry.String("module", m.ID),
			telemetry.String("version", m.Version),
			telemetry.String("name", m.Name),
			telemetry.String("provides", fmt.Sprint(m.Provides)))
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
	})
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

	schema, err := graphqltransport.NewSchema(svc, artworkSigner.Rewrite)
	if err != nil {
		return fmt.Errorf("build graphql schema failed: %w", err)
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
				Permissions: adminPermissions(),
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
	go telemetryMaintenance(serveCtx, telemetryStore, root.For("telemetry"))

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
	requestCtx := telemetry.Into(context.Background(), root)
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
	// The first-party client session is the Connect two-lane RPC surface
	// (ADR 0041): unary intents (Navigate/Invoke/SubmitInput/Attach) and one
	// server-streaming Subscribe per session for push. It supersedes the ADR 0032
	// WebSocket. GraphQL is retained only as the external/tooling surface, not on
	// the hot client path.
	sessionHandler := session.NewHandler(svc, artworkSigner.Rewrite, playbackSealer, playbackProber)
	sessionHandler.Manager().StartReaper(serveCtx)
	// The session interceptor is the first and most important edge seam
	// (ADR 0055): it is where a user's action enters the Platform, so it is
	// where the Shell's trace is continued and where everything downstream —
	// handlers, application services, modules — inherits its telemetry.
	sessionPath, sessionConnect := sessionv1connect.NewSessionServiceHandler(
		sessionHandler,
		connect.WithInterceptors(session.TelemetryInterceptor()),
	)

	apiMux := http.NewServeMux()
	// Each plain-HTTP surface is wrapped at its own seam and names itself, so a
	// record says which surface produced it rather than only that it was HTTP.
	apiMux.Handle("/graphql", telemetry.HTTPMiddleware("graphql", graphqltransport.Handler(schema)))
	apiMux.Handle("/artwork", telemetry.HTTPMiddleware("artwork", artwork.Handler(artworkSigner, artwork.GuardedClient())))
	apiMux.Handle("/playback/", telemetry.HTTPMiddleware("playback", playback.Handler(playbackSealer, playback.Client(), playbackRemuxer)))
	apiMux.Handle(sessionPath, sessionConnect)
	// Serve the API over h2c (cleartext HTTP/2) so the two session lanes —
	// concurrent unary intents and the long-lived Subscribe stream — multiplex
	// onto one connection (ADR 0041); Connect still degrades to HTTP/1.1 for the
	// GraphQL and artwork handlers.
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
	boot.Info("serving graphql api", telemetry.String("addr", apiAddr+"/graphql"))
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
	_ = bufferedSink.Close()

	boot.Info("exiting cleanly")
	return serveErr
}
