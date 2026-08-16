// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// ActionSessionCreate is the policy action evaluated when a caller signs
// in and a new session is about to be issued.
const ActionSessionCreate policy.Action = "session.create"

// AuthenticateLocalUserCommand signs a local user in with a password
// credential and issues a new session. Remote identity providers (Apple,
// Google, ...) are explicitly out of scope for the Platform foundation.
type AuthenticateLocalUserCommand struct {
	Username string
	Password string
	DeviceID domain.DeviceID
}

// AuthenticateLocalUserResult is the Platform result type returned once a
// session has been issued.
type AuthenticateLocalUserResult struct {
	Session domain.Session
	// Tokens is the bearer pair (platform#58). Its plaintext exists here and
	// nowhere else — nothing stores it, nothing logs it, and there is no way to
	// read it back, which is what makes a database read of the token tables
	// useless. Do not log or persist it.
	Tokens domain.TokenPair
}

func validateAuthenticateLocalUserCommand(cmd AuthenticateLocalUserCommand) error {
	if cmd.Username == "" {
		return contracts.NewError(contracts.InvalidArgument, "username is required")
	}
	if cmd.Password == "" {
		return contracts.NewError(contracts.InvalidArgument, "password is required")
	}
	if cmd.DeviceID == "" {
		return contracts.NewError(contracts.InvalidArgument, "device id is required")
	}
	return nil
}

// AuthenticateLocalUser is the one command boundary where step 2
// ("authenticate caller") cannot be a caller-session lookup — there is no
// session yet. Verifying the password credential plays that role instead:
// it is what establishes the identity the remaining steps authorize and
// act on. Username lookup and credential mismatches both fail identically
// (Unauthenticated, "invalid credentials") so a caller cannot use this
// command to discover which usernames exist.
func (s *Service) AuthenticateLocalUser(ctx context.Context, cmd AuthenticateLocalUserCommand) (AuthenticateLocalUserResult, error) {
	if err := validateAuthenticateLocalUserCommand(cmd); err != nil {
		return AuthenticateLocalUserResult{}, err
	}

	user, err := s.authenticatePassword(ctx, cmd.Username, cmd.Password)
	if err != nil {
		return AuthenticateLocalUserResult{}, err
	}

	subject := policy.Subject{UserID: user.ID, AuthStrength: domain.AuthStrengthPassword}
	resource := policy.Resource{Type: "user", ID: string(user.ID)}
	if err := s.authorize(ctx, subject, ActionSessionCreate, resource, policy.PolicyContext{}); err != nil {
		return AuthenticateLocalUserResult{}, err
	}

	var result AuthenticateLocalUserResult

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// There is no further state to load: the new session is the direct
		// outcome of the verified credential, so issuing it is this
		// command's only domain rule. The session and its first pair of tokens
		// are written through the same Tx, because a session with no tokens is
		// a row nobody can use and tokens with no session are a credential
		// pointing at nothing (platform#58).
		session, pair, err := s.sessionManager.Issue(
			ctx, tx.Sessions(), tx.Tokens(), user.ID, cmd.DeviceID, domain.AuthStrengthPassword,
			// What this account may do, resolved now and stamped on the session
			// so a client can omit what it could not use (platform#24). It is a
			// projection for drawing a screen, not an authority: every call the
			// client makes re-authorises against the grants as they then are.
			s.sessionCapabilities(ctx, user.ID))
		if err != nil {
			return err
		}

		event := domain.OutboxEvent{Event: s.newEvent(ctx, "authentication.succeeded", []byte(cmd.Username), string(user.ID))}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}

		result = AuthenticateLocalUserResult{Session: session, Tokens: pair}
		return nil
	})
	if err != nil {
		return AuthenticateLocalUserResult{}, err
	}

	return result, nil
}

// authenticatePassword verifies the password credential and returns the user it
// identifies. It stands in for the caller-session lookup this boundary cannot
// do — see AuthenticateLocalUser.
//
// A suspended account is refused after the credential is verified, and with a
// different category. After, because refusing before would answer differently
// for a suspended account and an unknown one against a wrong password — the
// username enumeration the shared "invalid credentials" refusal exists to
// prevent. Different, because "your password is wrong" sends somebody off to
// retype something that was right.
func (s *Service) authenticatePassword(ctx context.Context, username, password string) (domain.User, error) {
	user, err := s.users.FindByUsername(ctx, username)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.User{}, contracts.NewError(contracts.Unauthenticated, "invalid credentials")
		}
		return domain.User{}, err
	}

	credential, err := s.credentials.FindPassword(ctx, user.ID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.User{}, contracts.NewError(contracts.Unauthenticated, "invalid credentials")
		}
		return domain.User{}, err
	}

	verified, err := s.passwordVerifier.Verify(password, credential.Hash)
	if err != nil {
		return domain.User{}, contracts.WrapError(contracts.Internal, "verify password", err)
	}
	if !verified {
		// A failed authentication has no authenticated subject, so the actor
		// is empty; the attempted username travels in the payload.
		s.publishAuditEvent(ctx, "authentication.failed", []byte(username), "")
		return domain.User{}, contracts.NewError(contracts.Unauthenticated, "invalid credentials")
	}

	if user.Status != domain.UserActive {
		s.publishAuditEvent(ctx, "authentication.refused", []byte(string(user.Status)), string(user.ID))
		return domain.User{}, contracts.NewError(contracts.PermissionDenied,
			"this account cannot sign in")
	}
	return user, nil
}
