// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package bootstrap performs first-run seeding the composition root needs
// before the Platform is usable — today, ensuring an initial administrator
// exists. It is composition wiring, not an application service: it writes
// through the store contracts directly rather than through a command, because
// there is no authenticated caller yet to authorise the very first grant.
//
// This is a deliberate bridge (ADR 0018). The eventual owner of first-admin
// setup is Supervisor onboarding; EnsureAdmin is the seam that flow will drive,
// with a credential channel better than a plaintext env var. Expect this to be
// superseded, not to live here forever.
package bootstrap

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// AdminSeed is the first user to provision: its login and the permissions its
// Superuser role carries (ADR 0069). Username and Password are grouped
// into a named pair so the two same-typed strings cannot be transposed at the
// call site — a swap that would otherwise create the admin with the password as
// its username, and vice versa, without a compile error.
type AdminSeed struct {
	Username    string
	Password    string
	Permissions []domain.Permission
}

// superuserRoleName is what the first user's role is called. It is the string
// app.RoleNameSuperuser, repeated here rather than imported: this package is
// composition wiring that writes through store contracts, and it has never
// depended on the application services (ADR 0018). One constant is a smaller
// price than that dependency.
const superuserRoleName = "Superuser"

// EnsureAdmin creates the first user — a user with a password credential, a
// Superuser role carrying seed.Permissions, and a grant binding them — unless a
// user with the username already exists.
//
// The account it creates is the superuser (ADR 0069): the one privileged
// account established out-of-band, from which all other authority is
// allocated. Every later account is created *by* this one and starts with
// less. It is idempotent: an existing
// user is left untouched and Created is false.
//
// The whole seeding runs in one transaction, so a partial admin (a user with
// no role, say) can never be left behind for a later run to skip over.
func EnsureAdmin(
	ctx context.Context,
	uow contracts.UnitOfWork,
	hasher domain.PasswordVerifier,
	clock contracts.Clock,
	ids contracts.IDGenerator,
	seed AdminSeed,
) (created bool, err error) {
	if seed.Username == "" || seed.Password == "" {
		return false, contracts.NewError(contracts.InvalidArgument, "bootstrap admin requires a username and password")
	}

	err = uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// Already provisioned? Then this is a no-op — the common case on every
		// start after the first.
		if _, err := tx.Users().FindByUsername(ctx, seed.Username); err == nil {
			return nil
		} else if contracts.CategoryOf(err) != contracts.NotFound {
			return err
		}

		now := clock.Now()
		user := domain.User{
			ID:          domain.UserID(ids.NewID()),
			Username:    seed.Username,
			DisplayName: seed.Username,
			Status:      domain.UserActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if _, err := tx.Users().Create(ctx, user); err != nil {
			return err
		}

		hash, err := hasher.Hash(seed.Password)
		if err != nil {
			return contracts.WrapError(contracts.Internal, "hash bootstrap password", err)
		}
		if err := tx.Credentials().SavePassword(ctx, domain.PasswordCredential{
			UserID: user.ID, Hash: hash, UpdatedAt: now,
		}); err != nil {
			return err
		}

		role, err := tx.Permissions().CreateRole(ctx, domain.Role{
			ID: domain.RoleID(ids.NewID()), Name: superuserRoleName, Permissions: seed.Permissions,
		})
		if err != nil {
			return err
		}
		if err := tx.Permissions().GrantRole(ctx, domain.Grant{UserID: user.ID, RoleID: role.ID}); err != nil {
			return err
		}

		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}
