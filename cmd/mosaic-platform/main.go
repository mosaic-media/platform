// Command mosaic-platform is the Platform process entry point. Its
// responsibility is dependency bootstrap only (MEG-015 §02); it must stay
// free of business logic.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/adapters/crypto"
	"github.com/mosaic-media/mosaic-platform/internal/composition/builtin"
	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
	"github.com/mosaic-media/mosaic-platform/internal/platform/diagnostics"
	"github.com/mosaic-media/mosaic-platform/internal/platform/events"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	"github.com/mosaic-media/mosaic-platform/internal/platform/runtime"
	graphqltransport "github.com/mosaic-media/mosaic-platform/internal/transport/graphql"
	"github.com/mosaic-media/mosaic-platform/internal/transport/health"
)

// postgresDSNEnv names the environment variable carrying the PostgreSQL
// connection string. Reading storage configuration from the environment is
// a bridge until a config-loading pipeline reads it from an Active
// ConfigVersion instead; it is recorded here so that work can replace it
// deliberately.
const postgresDSNEnv = "MOSAIC_POSTGRES_DSN"

// healthAddrEnv names the environment variable carrying the address the
// MEG-015 §10 Supervisor handoff HTTP surface listens on.
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
	// discovered (MEG-006). Postgres is the first, required storage module.
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
	// is the startup gate that refuses to run against a mismatched database
	// (MEG-015 §05, MEG-007 §10 — "Migration failures MUST prevent Runtime
	// startup"). Bracketing it with migrations.Begin/Complete is what makes
	// MEG-015 §10's migration status endpoint reflect real state — Running
	// while this is in flight, Complete or Failed once it returns — rather
	// than a value invented for the endpoint alone.
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
	// committed outbox rows into it (MEG-015 §06).
	bus := events.NewBus()
	worker := events.NewWorker(set.Outbox, bus, "outbox-worker")

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	published, err := worker.RunOnce(drainCtx)
	drainCancel()
	if err != nil {
		return fmt.Errorf("outbox drain failed: %w", err)
	}
	fmt.Printf("mosaic-platform: outbox worker drained %d event(s)\n", published)

	// Aggregate real component health (MEG-015 §09 — Diagnostics Model),
	// backing both the local structured log and the /readyz endpoint below.
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
	svc := app.NewService(
		set.UnitOfWork, set.Sessions, set.Users, set.Credentials, set.Config, set.Permissions,
		set.Nodes, set.Clock, set.IDs, set.ContentIDs,
		policy.NewEngine(set.Permissions), bus, crypto.NewPasswordHasher(),
	)
	schema, err := graphqltransport.NewSchema(svc)
	if err != nil {
		return fmt.Errorf("build graphql schema failed: %w", err)
	}

	// From here on the process is a genuine long-running Supervisor
	// candidate (MEG-015 §10 — Activation Sequence: "Start runtime
	// components" -> "Readiness probe" -> "Activate candidate"), not a
	// boot-and-exit scaffold: the outbox worker's background poll loop
	// runs, and the Supervisor handoff HTTP surface serves readiness,
	// liveness, metadata, migration and config activation status until a
	// shutdown signal arrives.
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
	apiMux := http.NewServeMux()
	apiMux.Handle("/graphql", graphqltransport.Handler(schema))
	apiServer := &http.Server{Addr: apiAddr, Handler: apiMux}

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
	// tick and Stop is checkpointed before the process exits — MEG-015
	// §10's "outbox checkpointing"). Rollback, if the Supervisor decides to
	// activate an earlier Generation instead of this one, means activating
	// that other binary — this process must never and does not attempt to
	// reverse any database mutation itself (MEG-015 §10 — Rollback
	// Boundary).
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
