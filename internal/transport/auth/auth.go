// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package auth is the Connect transport that mints and revokes sessions
// (ADR 0061). It is the only first-party surface a caller reaches without
// already holding a session, and it is what a client calls before it can open
// the two-lane SessionService of ADR 0041.
//
// It replaced the GraphQL signIn/signOut mutations. Like every transport in
// this repository it is a projection surface only: each method calls exactly
// one application command and translates its result — boundary_test.go
// enforces that it never reaches a store or a module directly.
package auth

import (
	"context"

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
}

// Compile-time proof the handler satisfies the generated service contract.
var _ authv1connect.AuthServiceHandler = (*Handler)(nil)

// NewHandler wires the auth transport over the application services.
func NewHandler(svc *app.Service) *Handler { return &Handler{svc: svc} }

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
	return connect.NewResponse(&authv1.SignInResponse{Session: sessionMessage(result.Session)}), nil
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
// Two fields of domain.Session are deliberately not on the contract. RevokedAt,
// because a client only ever receives a session it was just issued, so it would
// be nil in every message this transport can produce — whether a session is
// still valid is answered by the next call failing Unauthenticated, not by a
// cached timestamp. And Capabilities, because nothing populates it at issue
// time; see the note on mosaic.auth.v1.Session.
func sessionMessage(s domain.Session) *authv1.Session {
	return &authv1.Session{
		Id:           string(s.ID),
		UserId:       string(s.UserID),
		DeviceId:     string(s.DeviceID),
		IssuedAt:     timestamppb.New(s.IssuedAt),
		LastSeenAt:   timestamppb.New(s.LastSeenAt),
		ExpiresAt:    timestamppb.New(s.ExpiresAt),
		AuthStrength: string(s.AuthStrength),
	}
}
