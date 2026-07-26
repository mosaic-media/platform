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
	// Tokens is the bearer pair (ADR 0102). Its plaintext exists here and
	// nowhere else — nothing stores it, nothing logs it, and there is no way to
	// read it back, which is what makes a database read of the token tables
	// useless to whoever performed it.
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
	// 1. validate command shape.
	if err := validateAuthenticateLocalUserCommand(cmd); err != nil {
		return AuthenticateLocalUserResult{}, err
	}

	// 2. authenticate caller: verify the password credential.
	user, err := s.users.FindByUsername(ctx, cmd.Username)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return AuthenticateLocalUserResult{}, contracts.NewError(contracts.Unauthenticated, "invalid credentials")
		}
		return AuthenticateLocalUserResult{}, err
	}

	credential, err := s.credentials.FindPassword(ctx, user.ID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return AuthenticateLocalUserResult{}, contracts.NewError(contracts.Unauthenticated, "invalid credentials")
		}
		return AuthenticateLocalUserResult{}, err
	}

	verified, err := s.passwordVerifier.Verify(cmd.Password, credential.Hash)
	if err != nil {
		return AuthenticateLocalUserResult{}, contracts.WrapError(contracts.Internal, "verify password", err)
	}
	if !verified {
		// A failed authentication has no authenticated subject, so the actor
		// is empty; the attempted username travels in the payload.
		s.publishAuditEvent(ctx, "authentication.failed", []byte(cmd.Username), "")
		return AuthenticateLocalUserResult{}, contracts.NewError(contracts.Unauthenticated, "invalid credentials")
	}

	// A suspended account is refused **after** the credential is verified, and
	// with a different category.
	//
	// After, because refusing before would answer differently for a suspended
	// account and an unknown one against a wrong password, which is the
	// enumeration the collapsing above exists to prevent. Different, because
	// the two are different situations for the person in front of the screen:
	// "your password is wrong" sends them off to retype something that was
	// right, and there is nothing they can do about the other one but ask.
	if user.Status != domain.UserActive {
		s.publishAuditEvent(ctx, "authentication.refused", []byte(string(user.Status)), string(user.ID))
		return AuthenticateLocalUserResult{}, contracts.NewError(contracts.PermissionDenied,
			"this account cannot sign in")
	}

	// 3. authorize action through policy.
	subject := policy.Subject{UserID: user.ID, AuthStrength: domain.AuthStrengthPassword}
	resource := policy.Resource{Type: "user", ID: string(user.ID)}
	if err := s.authorize(ctx, subject, ActionSessionCreate, resource, policy.PolicyContext{}); err != nil {
		return AuthenticateLocalUserResult{}, err
	}

	var result AuthenticateLocalUserResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5/6. no further state to load: the new session is the direct
		// outcome of the verified credential, so issuing it is this
		// command's only domain rule. The session and its first pair of tokens
		// are written through the same Tx, because a session with no tokens is
		// a row nobody can use and tokens with no session are a credential
		// pointing at nothing (ADR 0102).
		session, pair, err := s.sessionManager.Issue(
			ctx, tx.Sessions(), tx.Tokens(), user.ID, cmd.DeviceID, domain.AuthStrengthPassword,
			// What this account may do, resolved now and stamped on the session
			// so a client can omit what it could not use (ADR 0036). Read
			// outside the delegation path deliberately: this is a projection for
			// drawing a screen, and every call the client then makes
			// re-authorises against the grants as they are at that moment.
			s.sessionCapabilities(ctx, user.ID))
		if err != nil {
			return err
		}

		// 7. persist state and outbox events in the same transaction.
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

	// 8. return a Platform result type.
	return result, nil
}
