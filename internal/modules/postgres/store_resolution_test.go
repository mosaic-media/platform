package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// TestStoreResolutionIsTransactionEquivalentToAccessors is the real-database
// analogue of slice 13's fake-backed equivalence proof. Against a real
// PostgreSQL transaction it shows that, within a single WithinTx call, a write
// through contracts.Store[UserStore](tx) is visible to a read through
// tx.Users() and vice versa — the two resolution paths address the same
// transaction.
//
// Note the guarantee proven here is transaction-equivalence, not pointer
// identity. Slice 13's fake cached one store instance per accessor, so Store[T]
// and the accessor returned the identical pointer. The PostgreSQL stores are
// cheap value-wrappers over the transaction handle (t.q): each accessor call
// constructs a fresh &userStore{q: t.q}, so Store[UserStore](tx) and tx.Users()
// are distinct instances that nonetheless read and write the same transaction.
// Transaction-equivalence is the property that actually matters for atomicity
// and is what MAD-001 §04 ("one transaction, one storage adapter") requires;
// pointer identity is a fake-only artifact.
func TestStoreResolutionIsTransactionEquivalentToAccessors(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	uow := postgres.NewUnitOfWork(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	err := uow.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
		storeUsers, err := contracts.Store[contracts.UserStore](tx)
		if err != nil {
			return fmt.Errorf("resolve Store[UserStore]: %w", err)
		}

		// Direction 1: write through Store[UserStore](tx), read through tx.Users().
		if _, err := storeUsers.Create(c, domain.User{
			ID: "u-store", Username: "via-store", Email: "via-store@example.com",
			Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("create via Store[T]: %w", err)
		}
		got, err := tx.Users().FindByID(c, "u-store")
		if err != nil {
			return fmt.Errorf("named accessor could not see the Store[T] write: %w", err)
		}
		if got.Username != "via-store" {
			t.Errorf("accessor read username = %q, want via-store", got.Username)
		}

		// Direction 2: write through tx.Users(), read through Store[UserStore](tx).
		if _, err := tx.Users().Create(c, domain.User{
			ID: "u-accessor", Username: "via-accessor", Email: "via-accessor@example.com",
			Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("create via accessor: %w", err)
		}
		got2, err := storeUsers.FindByID(c, "u-accessor")
		if err != nil {
			return fmt.Errorf("Store[T]-resolved store could not see the accessor write: %w", err)
		}
		if got2.Username != "via-accessor" {
			t.Errorf("Store[T] read username = %q, want via-accessor", got2.Username)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	// Both rows committed together, confirming the cross-resolution writes
	// shared one transaction that committed as a unit.
	var count int
	if err := pool.QueryRow(c,
		`SELECT count(*) FROM users WHERE id IN ('u-store', 'u-accessor')`,
	).Scan(&count); err != nil {
		t.Fatalf("count committed users: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected both cross-resolution writes committed, found %d", count)
	}
}
