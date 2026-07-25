// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// ClaimServerCommand creates the first administrator of a server that has none.
type ClaimServerCommand struct {
	Username    string
	Password    string
	DisplayName string
	Email       string
	// DeviceID names the client claiming the server, because the session this
	// returns belongs to a device like every other.
	DeviceID domain.DeviceID
}

// ClaimServerResult carries the owner and the session they are signed in with.
type ClaimServerResult struct {
	User    domain.User
	Session domain.Session
}

// ClaimServer creates the first administrator and signs them in (ADR 0098).
//
// **It is the only unauthenticated write the Platform accepts, and it is a
// deliberate one.** Every command that could grant the first authority is
// itself policy-gated, so a server with no users has no in-band way to acquire
// one — which is why the first administrator has until now been an environment
// variable read at start-up, and why there has been no setup experience.
//
// It refuses the moment any user exists. That check and the create happen in one
// transaction, so two clients racing to claim the same server cannot both
// succeed: the second finds a user and gets Conflict.
//
// The threat this accepts is that whoever reaches an unclaimed server first owns
// it. ADR 0098 states it plainly rather than burying it, along with the two
// mitigations that were considered and not taken — a console claim token, and a
// claim window that closes some minutes after start-up.
func (s *Service) ClaimServer(ctx context.Context, cmd ClaimServerCommand) (ClaimServerResult, error) {
	if cmd.Username == "" || cmd.Password == "" {
		return ClaimServerResult{}, contracts.NewError(contracts.InvalidArgument, "a username and password are required")
	}
	if cmd.DeviceID == "" {
		return ClaimServerResult{}, contracts.NewError(contracts.InvalidArgument, "a device id is required")
	}

	var owner domain.User
	err := s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// The gate. Inside the transaction on purpose: read-then-write outside
		// one is a race with a second claimant, and the thing being raced for is
		// ownership of the server.
		existing, err := tx.Users().List(ctx)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			// Deliberately says nothing about who owns it. An unauthenticated
			// caller learns that the server is claimed, which they can tell from
			// being shown a sign-in screen anyway, and nothing else.
			return contracts.NewError(contracts.Conflict, "this server has already been set up")
		}

		now := s.clock.Now()
		display := cmd.DisplayName
		if display == "" {
			display = cmd.Username
		}
		user := domain.User{
			ID:          domain.UserID(s.ids.NewID()),
			Username:    cmd.Username,
			Email:       cmd.Email,
			DisplayName: display,
			Status:      domain.UserActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		created, err := tx.Users().Create(ctx, user)
		if err != nil {
			return err
		}

		hash, err := s.passwordVerifier.Hash(cmd.Password)
		if err != nil {
			return contracts.WrapError(contracts.Internal, "hash the owner's password", err)
		}
		if err := tx.Credentials().SavePassword(ctx, domain.PasswordCredential{
			UserID: created.ID, Hash: hash, UpdatedAt: now,
		}); err != nil {
			return err
		}

		actions := SuperuserActions()
		perms := make([]domain.Permission, len(actions))
		for i, a := range actions {
			perms[i] = domain.Permission(a)
		}
		role, err := tx.Permissions().CreateRole(ctx, domain.Role{
			ID: domain.RoleID(s.ids.NewID()), Name: "superuser", Permissions: perms,
		})
		if err != nil {
			return err
		}
		if err := tx.Permissions().GrantRole(ctx, domain.Grant{UserID: created.ID, RoleID: role.ID}); err != nil {
			return err
		}

		// The claim is worth a record of its own. It is the one write here that
		// nobody had to authenticate for, and an operator who later finds a
		// server already claimed should be able to see when it happened.
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "server.claimed", []byte(created.Username), string(created.ID)),
		}); err != nil {
			return err
		}

		owner = created
		return nil
	})
	if err != nil {
		return ClaimServerResult{}, err
	}

	// Signing in by the act of claiming, rather than making somebody type the
	// password they set thirty seconds ago into the screen that replaces this
	// one.
	auth, err := s.AuthenticateLocalUser(ctx, AuthenticateLocalUserCommand{
		Username: cmd.Username, Password: cmd.Password, DeviceID: cmd.DeviceID,
	})
	if err != nil {
		return ClaimServerResult{}, err
	}
	return ClaimServerResult{User: owner, Session: auth.Session}, nil
}

// ServerClaimed reports whether this server has an administrator yet, so the
// pre-session screen endpoint can serve the setup tree or the sign-in one.
//
// It answers a boolean and nothing else. "Is this server set up" is already
// observable — the screen you are shown says so — and the user list behind it is
// not.
func (s *Service) ServerClaimed(ctx context.Context) bool {
	users, err := s.users.List(ctx)
	// A read that fails answers "claimed", which fails closed: the cost of being
	// wrong that way is a sign-in screen on a server that has none, and the cost
	// of being wrong the other way is offering to hand the server to a stranger.
	if err != nil {
		return true
	}
	return len(users) > 0
}
