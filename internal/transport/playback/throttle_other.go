// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build !unix

package playback

import "os"

// Windows has no SIGSTOP, and suspending a process there needs the undocumented
// NtSuspendProcess or a job object — neither of which is worth carrying for a
// platform Mosaic does not ship a server for.
//
// **The consequence is stated rather than hidden:** on a non-unix host the
// transcode is not throttled, so a playback's segment directory grows to the
// whole release instead of a window. Eviction behind the playhead still runs, so
// it is bounded by how far ahead the encoder gets rather than unbounded — but it
// is not the window the unix build has.
const (
	pauseSignal  = os.Interrupt
	resumeSignal = os.Interrupt
)

// signalProcess does nothing here. Sending Interrupt to pause would terminate
// the transcode, which is worse than not throttling it.
func signalProcess(p *os.Process, sig os.Signal) {}
