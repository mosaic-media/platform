// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"connectrpc.com/connect"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/rpc"
	"github.com/mosaic-media/platform/internal/transport/screens"
	"github.com/mosaic-media/platform/internal/transport/vocabulary"
)

// Invoke runs a doorway action (ADR 0101's lane, carrying ADR 0098's claim).
//
// The switch below is the complete enumeration of what a client with no session
// can ask this Platform to do. That is the same property the session
// transport's dispatch has and it matters more here, because everything in it
// runs unauthenticated: an action a client can name and this function cannot
// map does not exist, and adding one is a deliberate act in a place a reader
// will find.
//
// Two cases, and they are the two doors ADR 0098 named. Neither returns
// anything about the other's outcome — a claim on a claimed server and a
// sign-in with a wrong password both answer with the door, not with a
// description of why.
func (h *Handler) Invoke(ctx context.Context, req *connect.Request[authv1.InvokeRequest]) (*connect.Response[authv1.InvokeResponse], error) {
	// Shares the bootstrap's bucket rather than having one of its own. They are
	// the same surface from an abuser's point of view — the pre-authentication
	// one — and two independent limits would each be reachable in full, which is
	// twice the ceiling nobody chose.
	if !h.bootstrapLimit.allow(peerOf(ctx, req), h.now()) {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many requests; try again shortly"))
	}

	r := req.Msg
	client := vocabulary.From(r.GetVocabulary())
	switch r.GetAction() {
	case claimServerAction:
		return h.claimServer(ctx, r, client)
	case signInAction:
		return h.doorwaySignIn(ctx, r, client)
	default:
		// Named actions only. An unknown one is a bad request rather than a
		// silently empty answer, because the only thing that can produce one is a
		// client out of step with the door it was served.
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown action"))
	}
}

// The two doorway actions, spelled once. They match the constants the doorway
// builder emits — the screens package owns the names because it is what writes
// them into the tree, and this reads them back rather than keeping a second
// copy that could drift.
const (
	claimServerAction = screens.ClaimServerAction
	signInAction      = screens.SignInAction
)

// claimEnvelope is what the setup wizard's submit collects. The keys are the
// field names the wizard's State scope declares, which is also what a
// field-level rejection travels back under.
type claimEnvelope struct {
	ServerName      string `json:"serverName"`
	DisplayName     string `json:"displayName"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	// StreamSource is the picker's value: the module id, and nothing else. The
	// repository it comes from is resolved server-side from the catalogue the
	// step was drawn from, so this endpoint cannot be asked to install something
	// from somewhere the Platform did not offer.
	StreamSource string `json:"streamSource"`
}

// claimServer takes ownership of an unclaimed server and signs the claimant in.
func (h *Handler) claimServer(ctx context.Context, r *authv1.InvokeRequest, client vocabulary.Client) (*connect.Response[authv1.InvokeResponse], error) {
	var env claimEnvelope
	if err := decodeInput(r.GetInput(), &env); err != nil {
		return nil, err
	}
	result, err := h.svc.ClaimServer(ctx, app.ClaimServerCommand{
		ServerName:           env.ServerName,
		Username:             env.Username,
		DisplayName:          env.DisplayName,
		Password:             env.Password,
		ConfirmPassword:      env.ConfirmPassword,
		DeviceID:             domain.DeviceID(r.GetDeviceId()),
		StreamSourceModuleID: strings.TrimSpace(env.StreamSource),
	})
	if err != nil {
		if out, ok := fieldErrorsOutcome(err); ok {
			return out, nil
		}
		// A claim that lost a race is not an error the person can act on — the
		// server now has an owner, and what they need is the sign-in door. Every
		// other failure is a real one and travels as a Connect error.
		if contracts.CategoryOf(err) == contracts.Conflict {
			return h.doorwayOutcome(ctx, client)
		}
		return nil, rpc.Wrap(err)
	}

	// The two best-effort steps report themselves rather than disappearing. A
	// household that picked a stream source and did not get one would otherwise
	// find out by pressing play on something a week later.
	if result.IdentityProblem != "" || result.StreamSourceProblem != "" {
		telemetry.From(ctx).For("auth").Warn("the server was claimed with something left undone",
			telemetry.String("identity_problem", result.IdentityProblem),
			telemetry.String("stream_source_problem", result.StreamSourceProblem))
	}

	return signedOutcome(result.Session, result.Tokens), nil
}

// doorwaySignIn is the sign-in form's action.
//
// It is the same command SignIn calls, deliberately: the door and the direct
// RPC must not be able to authenticate differently. What differs is only the
// shape of the answer — a refusal here lands on the form rather than becoming a
// Connect error, because there is a form to land it on.
func (h *Handler) doorwaySignIn(ctx context.Context, r *authv1.InvokeRequest, client vocabulary.Client) (*connect.Response[authv1.InvokeResponse], error) {
	var env struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeInput(r.GetInput(), &env); err != nil {
		return nil, err
	}

	result, err := h.svc.AuthenticateLocalUser(ctx, app.AuthenticateLocalUserCommand{
		Username: env.Username,
		Password: env.Password,
		DeviceID: domain.DeviceID(r.GetDeviceId()),
	})
	if err != nil {
		switch contracts.CategoryOf(err) {
		case contracts.Unauthenticated:
			// **One message, on the form and not on a field.** Saying which of
			// the two was wrong is exactly the enumeration
			// AuthenticateLocalUser collapses its errors to prevent, and a
			// rejection attached to "username" would say a username exists as
			// surely as a sentence would.
			return connect.NewResponse(&authv1.InvokeResponse{
				Outcome: &authv1.InvokeResponse_FieldErrors{FieldErrors: formError(
					"That username and password do not match an account on this server.")},
			}), nil
		case contracts.PermissionDenied:
			// A suspended account authenticates and is then refused the session
			// (ADR 0069). It is a different sentence because it is a different
			// situation: the credentials were right and there is nothing to
			// retype, which "check your password" would send somebody off to do.
			return connect.NewResponse(&authv1.InvokeResponse{
				Outcome: &authv1.InvokeResponse_FieldErrors{FieldErrors: formError(
					"This account cannot sign in. Ask whoever administers this server.")},
			}), nil
		case contracts.Conflict:
			return h.doorwayOutcome(ctx, client)
		default:
			return nil, rpc.Wrap(err)
		}
	}
	return signedOutcome(result.Session, result.Tokens), nil
}

// signedOutcome is the response for an action that minted a session.
func signedOutcome(session domain.Session, tokens domain.TokenPair) *connect.Response[authv1.InvokeResponse] {
	return connect.NewResponse(&authv1.InvokeResponse{
		Outcome: &authv1.InvokeResponse_Signed{Signed: &authv1.Signed{
			Session: sessionMessage(session),
			Tokens:  tokenPairMessage(tokens),
		}},
	})
}

// formError is a rejection that belongs to the submission rather than to any
// one field — the FieldErrors envelope with nothing in its list.
func formError(message string) *sessionv1.FieldErrors {
	return &sessionv1.FieldErrors{FormError: message}
}

// decodeInput reads an action envelope. An empty input decodes to the zero
// value rather than failing: a form with nothing filled in submits nothing, and
// the command's own validation is what should say so, per field.
func decodeInput(input []byte, into any) error {
	if len(input) == 0 {
		return nil
	}
	if err := json.Unmarshal(input, into); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("input is not valid JSON"))
	}
	return nil
}
