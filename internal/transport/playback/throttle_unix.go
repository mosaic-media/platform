// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build unix

package playback

import (
	"os"
	"syscall"
)

// Holding a transcode back (platform#82).
//
// **Stopping the process is the only throttle available**, because the pressure
// has to reach ffmpeg rather than the origin. ffmpeg writes its segments to a
// directory on its own schedule; nothing reads them through a pipe the origin
// could simply stop draining, so there is no backpressure to apply and the only
// lever is the scheduler's.
//
// SIGSTOP and SIGCONT rather than a kill and a restart: a restart costs a fresh
// upstream connection and a re-seek, and the encoder is going to be wanted again
// in a few seconds. This is what `remux` does for the same reason.
const (
	pauseSignal  = syscall.SIGSTOP
	resumeSignal = syscall.SIGCONT
)

// signalProcess sends sig, ignoring the error a process that has already exited
// returns — which is an ordinary race here rather than a fault, since the
// transcode may finish between the frontier check and the signal.
func signalProcess(p *os.Process, sig os.Signal) {
	if p == nil {
		return
	}
	_ = p.Signal(sig)
}
