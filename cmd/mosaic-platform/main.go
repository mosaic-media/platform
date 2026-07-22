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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
		fmt.Fprintln(os.Stderr, "mosaic-platform:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}
	fmt.Printf("mosaic-platform: booting (environment=%s)\n", cfg.Environment)

	// Register built-in modules the same way an external Module would be
	// discovered. Postgres is the first, required storage module.
	moduleRegistry := builtin.NewRegistry()
	moduleRegistry.Register(postgres.New())
	for _, m := range moduleRegistry.Manifests() {
		fmt.Printf("mosaic-platform: registered built-in module %s@%s (fulfills %d contracts)\n",
			m.ID, m.Version, len(m.Fulfills))
	}
	generationMetadata := runtime.BuildGenerationMetadata(moduleRegistry)

	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		// Storage is not configured yet. Keep the scaffold's boot-and-exit
		// behaviour rather than failing, so the process still starts before
		// a DSN is provided by another means.
		fmt.Printf("mosaic-platform: %s not set; skipping storage bootstrap\n", postgresDSNEnv)
		fmt.Println("mosaic-platform: exiting cleanly")
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
	fmt.Printf("mosaic-platform: storage ready (%s: %s — %s)\n",
		storageHealth.Component, storageHealth.State, storageHealth.Detail)

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
	fmt.Printf("mosaic-platform: outbox worker drained %d event(s)\n", published)

	// Aggregate real component health per the diagnostics model, backing
	// both the local structured log and the /readyz endpoint below.
	diagRegistry := diagnostics.NewRegistry()
	diagRegistry.Register("postgres", set.HealthReporter)
	diagRegistry.Register("event-bus", bus)
	diagRegistry.Register("outbox-worker", worker, "postgres", "event-bus")

	logger, err := diagnostics.NewFileLogger("logs/mosaic-platform.log")
	if err != nil {
		return fmt.Errorf("open diagnostics log failed: %w", err)
	}
	defer logger.Close()

	logSnapshot := func(ctx context.Context, label string) {
		for _, h := range diagRegistry.Snapshot(ctx) {
			logger.Info(h.Component, label, diagnostics.ComponentHealthFields(h)...)
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
		fmt.Printf("mosaic-platform: registered capability %s@%s (%s) — provides %v\n", m.ID, m.Version, m.Name, m.Provides)
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
		fmt.Println("mosaic-platform: ffmpeg + ffprobe found; per-stream playback decisions enabled (ADR 0050)")
	} else {
		fmt.Println("mosaic-platform: ffmpeg/ffprobe not found; playback relays unprobed, so a release whose audio the client cannot decode will play silently")
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
		if created {
			fmt.Printf("mosaic-platform: bootstrapped administrator %q\n", adminUser)
		} else {
			fmt.Printf("mosaic-platform: administrator %q already present\n", adminUser)
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

	worker.Start(serveCtx)

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
	httpServer := &http.Server{Addr: healthAddr, Handler: handoff.Mux()}

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
	sessionPath, sessionConnect := sessionv1connect.NewSessionServiceHandler(sessionHandler)

	apiMux := http.NewServeMux()
	apiMux.Handle("/graphql", graphqltransport.Handler(schema))
	apiMux.Handle("/artwork", artwork.Handler(artworkSigner, artwork.GuardedClient()))
	apiMux.Handle("/playback/", playback.Handler(playbackSealer, playback.Client(), playbackRemuxer))
	apiMux.Handle(sessionPath, sessionConnect)
	// Serve the API over h2c (cleartext HTTP/2) so the two session lanes —
	// concurrent unary intents and the long-lived Subscribe stream — multiplex
	// onto one connection (ADR 0041); Connect still degrades to HTTP/1.1 for the
	// GraphQL and artwork handlers.
	apiServer := &http.Server{Addr: apiAddr, Handler: h2c.NewHandler(apiMux, &http2.Server{})}
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
	fmt.Printf("mosaic-platform: serving Supervisor handoff surface on %s\n", healthAddr)
	fmt.Printf("mosaic-platform: serving GraphQL API on %s/graphql\n", apiAddr)
	fmt.Printf("mosaic-platform: serving session transport on %s%s* (Connect two-lane, ADR 0041)\n", apiAddr, sessionPath)
	fmt.Println("mosaic-platform: ready")

	var serveErr error
	select {
	case <-serveCtx.Done():
		fmt.Println("mosaic-platform: shutdown signal received")
	case serveErr = <-serveErrCh:
		if serveErr != nil {
			fmt.Fprintf(os.Stderr, "mosaic-platform: health server error: %v\n", serveErr)
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
		fmt.Fprintf(os.Stderr, "mosaic-platform: final outbox drain failed: %v\n", result.FinalDrainErr)
	} else {
		fmt.Printf("mosaic-platform: final outbox drain published %d event(s)\n", result.FinalDrainPublished)
	}
	logSnapshot(context.Background(), "shutdown health check")

	fmt.Println("mosaic-platform: exiting cleanly")
	return serveErr
}
