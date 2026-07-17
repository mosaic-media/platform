// Command mosaic-platform is the Platform process entry point. Its
// responsibility is dependency bootstrap only (MEG-015 §02); it must stay
// free of business logic.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/composition/builtin"
	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
	"github.com/mosaic-media/mosaic-platform/internal/platform/events"
)

// postgresDSNEnv names the environment variable carrying the PostgreSQL
// connection string. Reading storage configuration from the environment is
// a bridge until the Configuration and secret broker slice (MEG-015 §08)
// lands; it is recorded here so that slice can replace it deliberately.
const postgresDSNEnv = "MOSAIC_POSTGRES_DSN"

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
	registry := builtin.NewRegistry()
	registry.Register(postgres.New())
	for _, m := range registry.Manifests() {
		fmt.Printf("mosaic-platform: registered built-in module %s@%s (fulfills %d contracts)\n",
			m.ID, m.Version, len(m.Fulfills))
	}

	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		// Storage is not configured yet. Keep the scaffold's boot-and-exit
		// behaviour rather than failing, so the process still starts before
		// the configuration slice provides a DSN by another means.
		fmt.Printf("mosaic-platform: %s not set; skipping storage bootstrap\n", postgresDSNEnv)
		fmt.Println("mosaic-platform: exiting cleanly")
		return nil
	}

	// Open the storage module: connect, then run migrations. Migrate fails
	// fast on a missing, incompatible or partially applied schema, so this
	// call is the startup gate that refuses to run against a mismatched
	// database (MEG-015 §05, MEG-007 §10 — "Migration failures MUST prevent
	// Runtime startup").
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	set, err := postgres.New().Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("storage bootstrap failed: %w", err)
	}
	defer set.Pool.Close()

	health, err := set.Health.Check(ctx)
	if err != nil {
		return fmt.Errorf("storage health check failed: %w", err)
	}
	fmt.Printf("mosaic-platform: storage ready (%s: %s — %s)\n",
		health.Component, health.State, health.Detail)

	// Wire the in-process Event Bus and the outbox worker that drains
	// committed outbox rows into it (MEG-015 §06). There is no long-running
	// serve loop yet — that arrives with Supervisor handoff — so this
	// process does not Start() the worker's background poll loop; it drains
	// whatever is currently deliverable once at boot, proving the wiring
	// works end to end against the real database.
	bus := events.NewBus()
	worker := events.NewWorker(set.Outbox, bus, "outbox-worker")
	published, err := worker.RunOnce(ctx)
	if err != nil {
		return fmt.Errorf("outbox drain failed: %w", err)
	}
	fmt.Printf("mosaic-platform: outbox worker drained %d event(s)\n", published)

	fmt.Println("mosaic-platform: exiting cleanly")
	return nil
}
