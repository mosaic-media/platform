// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package auth is the Connect transport that mints and revokes sessions
// (ADR 0061). It is the only first-party surface a caller reaches without
// already holding a session, and it is what a client calls before it can open
// the two-lane SessionService of contracts#5.
//
// It replaced the GraphQL signIn/signOut mutations. Like every transport in
// this repository it is a projection surface only: each method calls exactly
// one application command and translates its result — boundary_test.go
// enforces that it never reaches a store or a module directly.
package auth

import (
	"context"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	"github.com/mosaic-media/contracts/gen/mosaic/auth/v1/authv1connect"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/transport/rpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements the AuthService. Construct once and mount its Connect
// handler on the API mux.
type Handler struct {
	svc *app.Service
	// bootstrapLimit bounds the one call reachable before authentication
	// (ADR 0101). It is per-handler rather than global because there is one
	// handler per process; a second would be a second Platform.
	bootstrapLimit *limiter
	// now is the clock the limiter reads, injectable so a test can spend a
	// bucket and watch it refill without sleeping.
	now func() time.Time
}

// Compile-time proof the handler satisfies the generated service contract.
var _ authv1connect.AuthServiceHandler = (*Handler)(nil)

// NewHandler wires the auth transport over the application services.
func NewHandler(svc *app.Service) *Handler {
	return &Handler{
		svc:            svc,
		bootstrapLimit: newLimiter(bootstrapPerMinute, bootstrapBurst),
		now:            time.Now,
	}
}

// SignIn authenticates a local user and issues a session.
//
// Every failure mode below the command boundary — unknown username, wrong
// password — arrives here as one Unauthenticated "invalid credentials", by
// design: AuthenticateLocalUser collapses them so this endpoint cannot be used
// to enumerate which usernames exist. This method must not un-collapse it by
// adding detail of its own.
func (h *Handler) SignIn(ctx context.Context, req *connect.Request[authv1.SignInRequest]) (*connect.Response[authv1.SignInResponse], error) {
	r := req.Msg
	result, err := h.svc.AuthenticateLocalUser(ctx, app.AuthenticateLocalUserCommand{
		Username: r.GetUsername(),
		Password: r.GetPassword(),
		DeviceID: domain.DeviceID(r.GetDeviceId()),
	})
	if err != nil {
		return nil, rpc.Wrap(err)
	}
	return connect.NewResponse(&authv1.SignInResponse{
		Session: sessionMessage(result.Session),
		Tokens:  tokenPairMessage(result.Tokens),
	}), nil
}

// SignOut revokes a session. The caller and the target are named separately
// because revoking another device's session is an authorised act the policy
// layer decides on, not something the transport can assume from "they asked".
func (h *Handler) SignOut(ctx context.Context, req *connect.Request[authv1.SignOutRequest]) (*connect.Response[authv1.SignOutResponse], error) {
	r := req.Msg
	if r.GetCallerSession() == "" {
		return nil, rpc.Wrap(contracts.NewError(contracts.Unauthenticated, "a caller session is required"))
	}
	result, err := h.svc.RevokeSession(ctx, app.RevokeSessionCommand{
		CallerSessionID: domain.SessionID(r.GetCallerSession()),
		TargetSessionID: domain.SessionID(r.GetTargetSession()),
	})
	if err != nil {
		return nil, rpc.Wrap(err)
	}
	return connect.NewResponse(&authv1.SignOutResponse{Session: string(result.SessionID)}), nil
}

// sessionMessage projects the domain session onto the wire.
//
// One field of domain.Session is deliberately not on the contract: RevokedAt,
// because a client only ever receives a session it was just issued, so it would
// be nil in every message this transport can produce — whether a session is
// still valid is answered by the next call failing Unauthenticated, not by a
// cached timestamp.
func sessionMessage(s domain.Session) *authv1.Session {
	return &authv1.Session{
		Id:           string(s.ID),
		UserId:       string(s.UserID),
		DeviceId:     string(s.DeviceID),
		IssuedAt:     timestamppb.New(s.IssuedAt),
		LastSeenAt:   timestamppb.New(s.LastSeenAt),
		ExpiresAt:    timestamppb.New(s.ExpiresAt),
		AuthStrength: string(s.AuthStrength),
		Capabilities: capabilityStrings(s.Capabilities),
	}
}

// capabilityStrings projects the session's capability set onto the wire
// (ADR 0036).
//
// nil for an empty set rather than an empty slice, so "this Platform did not
// resolve a capability set" and "this account holds nothing" are the same
// absent field. They are the same thing to a client: neither is a statement it
// may gate on, because gating is the server's and this is only what lets a
// client omit an affordance it would have been refused anyway.
func capabilityStrings(perms []domain.Permission) []string {
	if len(perms) == 0 {
		return nil
	}
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		out = append(out, string(p))
	}
	return out
}

// Refresh exchanges a refresh token for a new pair, rotating it (ADR 0102).
//
// It is unauthenticated in the session sense and authenticated in every sense
// that matters: the refresh token is the credential, and presenting a spent one
// revokes the whole chain rather than merely being refused. That is what makes
// theft detectable instead of silent, and it is why a client must store the new
// pair and discard the old one the instant this returns.
func (h *Handler) Refresh(ctx context.Context, req *connect.Request[authv1.RefreshRequest]) (*connect.Response[authv1.RefreshResponse], error) {
	r := req.Msg
	result, err := h.svc.RefreshSession(ctx, app.RefreshSessionCommand{
		RefreshToken: r.GetRefreshToken(),
		DeviceID:     domain.DeviceID(r.GetDeviceId()),
	})
	if err != nil {
		return nil, rpc.Wrap(err)
	}
	return connect.NewResponse(&authv1.RefreshResponse{
		Session: sessionMessage(result.Session),
		Tokens:  tokenPairMessage(result.Tokens),
	}), nil
}

// tokenPairMessage projects the issued pair onto the wire.
//
// This is the only place either plaintext exists outside the moment it was
// generated. Nothing stores it, nothing logs it, and the store holds hashes —
// so a client that loses the pair cannot be sent it again, only a new one.
func tokenPairMessage(pair domain.TokenPair) *authv1.TokenPair {
	return &authv1.TokenPair{
		AccessToken:      pair.AccessToken,
		AccessExpiresAt:  timestamppb.New(pair.AccessExpiresAt),
		RefreshToken:     pair.RefreshToken,
		RefreshExpiresAt: timestamppb.New(pair.RefreshExpiresAt),
	}
}
