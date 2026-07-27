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
		// Already provisioned? Then all that remains is keeping this account's
		// authority current — the common case on every start after the first.
		if existing, err := tx.Users().FindByUsername(ctx, seed.Username); err == nil {
			return reconcileSuperuser(ctx, tx, existing.ID, seed.Permissions)
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

		// **Find the role before creating it**, because the role name is unique
		// and an install claimed through the setup wizard already has one.
		//
		// Creating it unconditionally worked for the case this bootstrap was
		// written for — an unclaimed server, seeded by environment variables
		// before anybody has touched it — and failed the whole boot with a
		// duplicate-key error on the case that turns out to matter more: adding
		// an administrator to an install that already has one. That is the way
		// back into a deployment whose only login has been lost, and it is worth
		// having precisely when nothing else is available.
		//
		// The existing role's permissions are left exactly as they are.
		// ReconcileOwnerRole below is what maintains them, on every boot and
		// however the owner was created; widening them from here would let a
		// bootstrap silently re-grant authority an administrator had deliberately
		// narrowed.
		role, err := tx.Permissions().FindRoleByName(ctx, superuserRoleName)
		if contracts.CategoryOf(err) == contracts.NotFound {
			role, err = tx.Permissions().CreateRole(ctx, domain.Role{
				ID: domain.RoleID(ids.NewID()), Name: superuserRoleName, Permissions: seed.Permissions,
			})
		}
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

// ReconcileOwnerRole brings the install owner's role back in line with the
// Platform's current action set, on every boot, however the owner was created.
//
// **It exists because claiming made the old reconciliation unreachable.** That
// one runs inside EnsureAdmin, which runs only when the environment-variable
// bootstrap is configured — so a server claimed through the setup wizard has an
// owner whose role is a snapshot taken at the moment of claiming and never
// touched again. Upgrade that install, and the owner cannot grant the action
// the upgrade added: not to anybody, ever, because an authority the root of
// every grant does not hold can never be delegated. It is the same defect
// reconcileSuperuser was written for, arriving by a door that did not exist
// when it was written.
//
// It matches the role by **name**, which the structural check below deliberately
// does not. The two are answering different questions: that one is repairing an
// install whose role may be called anything, because it was seeded by a build
// that named it differently; this one is maintaining a role the claim itself
// created and named, one boot ago or a hundred. A name is safe to match on when
// you are the thing that wrote it.
//
// Writes nothing when nothing changed, so an ordinary boot costs one read.
func ReconcileOwnerRole(ctx context.Context, uow contracts.UnitOfWork, perms []domain.Permission) (bool, error) {
	changed := false
	err := uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		role, err := tx.Permissions().FindRoleByName(ctx, superuserRoleName)
		if err != nil {
			// No owner role means an unclaimed server, or one seeded by a build
			// that named it something else — neither is this function's business
			// and neither is an error.
			if contracts.CategoryOf(err) == contracts.NotFound {
				return nil
			}
			return err
		}
		if samePermissions(role.Permissions, perms) {
			return nil
		}
		changed = true
		return tx.Permissions().SetRolePermissions(ctx, role.ID, perms)
	})
	return changed, err
}

// reconcileSuperuser brings the owner account's role back in line with the
// Platform's current action set.
//
// A preset is *snapshotted* into a role row when the role is created, so adding
// an action to the Platform never reaches an account that already exists. That
// is correct for every other role — an administrator's authority should not
// silently widen because the software was upgraded — and wrong for exactly one:
// the superuser is the root of every other grant, and an authority it does not
// hold can never be given to anyone. An install would quietly become unable to
// delegate a permission it now has.
//
// It was found the way these things are: a new action was added, the tests
// passed, and playback progress silently failed to record on a running install
// whose admin had been seeded before that action existed.
//
// **It matches on "this account holds exactly one role", not on the role's
// name.** Matching by name was the first attempt and it failed against the very
// install that motivated this: that account's role is called "Administrator",
// because it was seeded by a build that named it differently. A name is a label
// someone may have changed; holding a single role is the structural signature of
// an account nobody has reshaped. An account holding several has been arranged
// deliberately, and re-granting everything to it would undo that arrangement on
// the next restart — a worse failure than the one this fixes.
func reconcileSuperuser(ctx context.Context, tx contracts.Tx, userID domain.UserID, perms []domain.Permission) error {
	roles, err := tx.Permissions().RolesForUser(ctx, userID)
	if err != nil {
		return err
	}
	if len(roles) != 1 {
		return nil
	}
	if samePermissions(roles[0].Permissions, perms) {
		return nil
	}
	return tx.Permissions().SetRolePermissions(ctx, roles[0].ID, perms)
}

// samePermissions compares two permission sets irrespective of order, so a boot
// that changes nothing writes nothing.
func samePermissions(a, b []domain.Permission) bool {
	if len(a) != len(b) {
		return false
	}
	have := make(map[domain.Permission]bool, len(a))
	for _, p := range a {
		have[p] = true
	}
	for _, p := range b {
		if !have[p] {
			return false
		}
	}
	return true
}
