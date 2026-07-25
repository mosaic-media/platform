// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package session

import (
	"errors"
	"fmt"
	"testing"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// A rejection that names fields goes to the fields. A toast saying "that
// username is taken" is a sentence floating beside the form rather than a mark
// on the box that is wrong — and on a form with four inputs it does not say
// which.
func TestARejectionNamingFieldsBecomesAFieldErrorsPush(t *testing.T) {
	err := contracts.RejectFields("Could not create the account",
		contracts.FieldRejection{Field: "username", Message: "Already taken"},
		contracts.FieldRejection{Field: "password", Message: "Too weak"},
	)
	msg, ok := fieldErrorsMsg(err)
	if !ok {
		t.Fatal("a rejection naming fields did not produce a push")
	}
	body := msg.GetFieldErrors()
	if body.GetFormError() != "Could not create the account" {
		t.Errorf("form error = %q", body.GetFormError())
	}
	if len(body.GetErrors()) != 2 {
		t.Fatalf("errors = %d", len(body.GetErrors()))
	}
	if body.GetErrors()[0].GetField() != "username" || body.GetErrors()[0].GetMessage() != "Already taken" {
		t.Errorf("first error = %+v", body.GetErrors()[0])
	}
}

// It survives wrapping. A command's rejection travels up through the layers that
// add context to it, and an envelope that only worked on an unwrapped error
// would work in a test and nowhere else.
func TestAWrappedRejectionIsStillFound(t *testing.T) {
	inner := contracts.RejectFields("bad", contracts.FieldRejection{Field: "key", Message: "Invalid"})
	wrapped := fmt.Errorf("configure module: %w", inner)
	if _, ok := fieldErrorsMsg(wrapped); !ok {
		t.Error("a wrapped rejection was not recognised")
	}
}

// Everything else stays a toast. Nearly every error the Platform produces is not
// about a submission, and routing them all through a form-shaped envelope would
// put "the database is unreachable" underneath a text box.
func TestAnOrdinaryErrorIsNotAFieldRejection(t *testing.T) {
	for _, err := range []error{
		errors.New("plain"),
		contracts.NewError(contracts.Unavailable, "upstream is down"),
		contracts.NewError(contracts.InvalidArgument, "malformed input"),
		contracts.RejectFields("named nothing"), // no fields — the boundary case
	} {
		if _, ok := fieldErrorsMsg(err); ok {
			t.Errorf("%v was routed to the fields", err)
		}
	}
}

// The push is the same envelope the client's own validators fill.
func TestTheEnvelopeIsTheContractsOwn(t *testing.T) {
	msg, _ := fieldErrorsMsg(contracts.RejectFields("x", contracts.FieldRejection{Field: "f", Message: "m"}))
	var _ *sessionv1.FieldErrors = msg.GetFieldErrors()
	if msg.GetToast() != nil || msg.GetRegion() != nil {
		t.Error("a field rejection was also sent as something else")
	}
}
