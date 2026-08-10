// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"crypto/rand"
	"encoding/base64"
	"sync"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// invocations is the table of live module invocations — the mechanism that
// makes serializing a Caller safe (platform#39).
//
// # The problem it solves
//
// In process, a v1.Caller is meaningless outside the invocation that produced
// it: it is a value on a stack, and when the call returns nothing holds it.
// Serialized across a process boundary it becomes something a module *has* — a
// string in another process's memory, which that process can keep, write to
// disk, or use later.
//
// # Why a handle and not a token
//
// platform#39 rejected a signed short-TTL bearer token, and the reason is worth
// keeping next to the code: **a TTL is a window in which a retained value still
// works**, and retention is the entire risk. A five-minute token means five
// minutes during which a module that kept it can act as the user who invoked
// it, for work that user never asked for.
//
// A handle that stops resolving the instant the invocation returns has no
// window at all. There is nothing to tune, nothing to get wrong, and no
// clock-skew story.
//
// # What it is not
//
// It is not authorisation. Resolving a handle yields the Caller the Platform
// minted it for, and the application service then authenticates and authorises
// that Caller exactly as it would for any other request (platform#13). A live
// handle grants no more than the session behind it; it only says *which*
// session, and only for as long as the invocation lasts.
type invocations struct {
	mu   sync.Mutex
	live map[string]v1.Caller
}

func newInvocations() *invocations {
	return &invocations{live: make(map[string]v1.Caller)}
}

// mint registers caller for the duration of one invocation and returns the
// handle to hand across, plus the revoke to call when the invocation returns.
//
// The revoke is returned rather than exposed as a method taking a handle, so
// the only way to obtain one is to have minted it — a call site cannot revoke
// somebody else's invocation, and `defer revoke()` is the shape that makes
// forgetting hard.
func (i *invocations) mint(caller v1.Caller) (handle string, revoke func()) {
	handle = newHandle()

	i.mu.Lock()
	i.live[handle] = caller
	i.mu.Unlock()

	return handle, func() {
		i.mu.Lock()
		delete(i.live, handle)
		i.mu.Unlock()
	}
}

// resolve exchanges a handle a module presented for the Caller it was minted
// for. An unknown handle is PermissionDenied rather than NotFound: the module
// is attempting to act as somebody, and the honest category is that it may not,
// not that a lookup missed.
func (i *invocations) resolve(handle string) (v1.Caller, error) {
	if handle == "" {
		return v1.Caller{}, contracts.NewError(contracts.PermissionDenied,
			"extension: a module called back with no invocation handle")
	}

	i.mu.Lock()
	caller, ok := i.live[handle]
	i.mu.Unlock()

	if !ok {
		// The message deliberately does not echo the handle. It is not secret,
		// but a rejected handle in a log is noise, and the useful signal —
		// which module, doing what — is the span the boundary already opens.
		return v1.Caller{}, contracts.NewError(contracts.PermissionDenied,
			"extension: a module called back with a handle that is not live; "+
				"a handle is valid only for the invocation it was minted for")
	}
	return caller, nil
}

// count reports how many invocations are live. It exists for tests: the
// property worth asserting is that the table empties, and a leak is otherwise
// invisible until it is large.
func (i *invocations) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.live)
}

// newHandle returns an unguessable handle.
//
// Unguessability is not the primary control — the table is, and a handle that
// is not in it fails whatever it looks like. It matters because the alternative
// is a counter, and a counter is guessable *and* replayable across restarts,
// which would make a stale handle from a previous boot occasionally resolve to
// somebody else's invocation.
//
// rand.Read from crypto/rand cannot fail in a way worth handling: since Go 1.24
// it panics rather than returning an error, because a machine with no entropy
// source has no safe path forward.
func newHandle() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
