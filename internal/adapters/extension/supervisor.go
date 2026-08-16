// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"context"
	"sync"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Supervised owns a module process across its whole life: it launches it,
// watches whether it is still answering, restarts it with backoff when it is
// not, and gives up on a module that will not stay up — reporting a degraded
// capability rather than taking the Platform down with it (platform#39).
//
// platform#39 puts runtime supervision in the Platform for two reasons. A module
// crash must be a degraded capability rather than a Generation event — routing a
// restart through the Supervisor would make a wedged third-party process a
// host-lifecycle concern, which is the coupling the tier split exists to break.
// And the Platform is the only component that knows whether a module is
// answering as opposed to merely running, so detection and remedy belong
// together.
//
// Supervised is what the registry holds, and it holds it once, at composition. A
// restart replaces the process and the gRPC connection underneath it — a fresh
// [Module] with a fresh proxy — but the value the registry has does not change.
// Role resolution reads [Manifest], which is answered from a cached copy taken
// at the first successful launch, so a module's roles do not flicker while it is
// down. Without this, a restart would orphan the registry's reference and every
// call would hit a dead connection.
//
// A call made while the module is down or disabled returns Unavailable, which is
// exactly how [platform#23](0023-metadata-as-required-capability.md) and [platform#24](0024-capability-gated-affordances.md) expect an absent capability
// to read: the affordance that depends on it degrades, and nothing crashes.
// Runtime absence is a degraded state; composition-time absence stays fatal, and
// that check ran before the serve loop (platform#38).
type Supervised struct {
	cfg    Config
	policy RestartPolicy
	tel    v1.Telemetry

	mu       sync.RWMutex
	current  *Module
	manifest v1.Manifest // cached at first launch; stable across restarts
	state    State

	// consecutiveFailures counts crashes that happened before the module ever
	// became healthy again. It resets to zero once a launch stays up for
	// HealthyThreshold, and it is what auto-disable keys on.
	consecutiveFailures int

	stop chan struct{}
	done chan struct{}
}

// State is what an admin surface and a capability-gated affordance read to know
// whether a module is usable.
type State int

const (
	// StateStarting is the initial launch, before the first success.
	StateStarting State = iota
	// StateRunning is a module that is up and answering.
	StateRunning
	// StateRestarting is a module that died and is being brought back, possibly
	// after a backoff delay.
	StateRestarting
	// StateDisabled is a module that crash-looped past the policy's limit. The
	// Platform has stopped trying, the capability is degraded, and bringing it
	// back is an operator action rather than an automatic one — which is the
	// honest state for code that cannot stay up, and is never a Platform exit.
	StateDisabled
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateRestarting:
		return "restarting"
	case StateDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// RestartPolicy is the crash-loop policy platform#39 left open (backoff ceiling,
// auto-disable, how an admin is told). The defaults are deliberately
// conservative: a module that crashes should come back quickly the first time
// and slowly the tenth, and one that never stays up should stop consuming
// restarts rather than spin forever.
type RestartPolicy struct {
	// InitialBackoff is the wait before the first restart. Doubles each
	// consecutive failure up to MaxBackoff.
	InitialBackoff time.Duration
	// MaxBackoff is the ceiling on the doubling.
	MaxBackoff time.Duration
	// HealthyThreshold is how long a launch must stay up before it counts as a
	// recovery: past it, the backoff and the failure counter reset. Without it a
	// module that crashes every HealthyThreshold+1 seconds would never be seen
	// as crash-looping.
	HealthyThreshold time.Duration
	// MaxConsecutiveFailures is how many crashes-before-healthy are tolerated
	// before the module is disabled. Zero means never disable — keep restarting
	// forever — which is a choice an operator can make but is not the default.
	MaxConsecutiveFailures int
	// ProbeInterval is how often the health monitor checks the process is still
	// answering.
	ProbeInterval time.Duration
	// ProbeTimeout bounds one health probe, so a wedged module is detected rather
	// than making the monitor hang with it.
	ProbeTimeout time.Duration
}

// DefaultRestartPolicy is the production policy.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{
		InitialBackoff:         1 * time.Second,
		MaxBackoff:             30 * time.Second,
		HealthyThreshold:       60 * time.Second,
		MaxConsecutiveFailures: 5,
		ProbeInterval:          5 * time.Second,
		ProbeTimeout:           3 * time.Second,
	}
}

// Supervise launches a module and keeps it running under policy. It returns once
// the first launch has either succeeded or failed; the monitor then runs in the
// background until [Supervised.Close].
//
// A first-launch failure is returned rather than retried, because a module that
// cannot start even once is more likely misconfigured than crash-looping, and
// the caller should see that plainly rather than have it buried in a backoff.
// Re-acquiring a module that will not start at all — re-download, re-verify — is
// the extension-management layer's job (platform#49), not this monitor's.
func Supervise(cfg Config, policy RestartPolicy, tel v1.Telemetry) (*Supervised, error) {
	m, err := Launch(cfg)
	if err != nil {
		return nil, err
	}

	s := &Supervised{
		cfg:      cfg,
		policy:   policy,
		tel:      tel,
		current:  m,
		manifest: m.Capability.Manifest(),
		state:    StateRunning,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go s.monitor()
	return s, nil
}

// Close stops the monitor and kills the module process.
func (s *Supervised) Close() {
	close(s.stop)
	<-s.done
}

// State reports the current lifecycle state, for an admin surface or a
// capability-gated affordance.
func (s *Supervised) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// monitor watches the current process and restarts it when it stops answering,
// until Close or until the crash-loop limit disables the module.
func (s *Supervised) monitor() {
	defer close(s.done)

	ticker := time.NewTicker(s.policy.ProbeInterval)
	defer ticker.Stop()

	// launchedAt marks when the current process came up, so the monitor can tell
	// a recovery (stayed up past HealthyThreshold) from a crash loop.
	launchedAt := s.now()

	for {
		select {
		case <-s.stop:
			s.mu.Lock()
			if s.current != nil {
				s.current.Close()
				s.current = nil
			}
			s.mu.Unlock()
			return

		case <-ticker.C:
			s.mu.RLock()
			m := s.current
			s.mu.RUnlock()
			if m == nil {
				continue // disabled; nothing to probe.
			}

			ctx, cancel := context.WithTimeout(context.Background(), s.policy.ProbeTimeout)
			alive := m.alive(ctx)
			cancel()
			if alive {
				// A module that has been up long enough has recovered: forgive
				// its past crashes so a module that dies once a week is never
				// mistaken for one that dies once a second.
				if s.now().Sub(launchedAt) >= s.policy.HealthyThreshold {
					s.mu.Lock()
					s.consecutiveFailures = 0
					s.mu.Unlock()
				}
				continue
			}

			// The module stopped answering. Restart it, unless the policy says
			// it has crash-looped past forgiveness.
			if relaunchedAt, ok := s.restart(); ok {
				launchedAt = relaunchedAt
			} else {
				return // disabled; the monitor's work is done.
			}
		}
	}
}

// restart brings the module back after a backoff, or disables it. It returns the
// time the replacement came up and true, or false when the module was disabled.
func (s *Supervised) restart() (time.Time, bool) {
	s.mu.Lock()
	s.consecutiveFailures++
	failures := s.consecutiveFailures
	s.state = StateRestarting
	old := s.current
	s.current = nil
	s.mu.Unlock()

	if old != nil {
		old.Close()
	}

	if s.policy.MaxConsecutiveFailures > 0 && failures > s.policy.MaxConsecutiveFailures {
		s.mu.Lock()
		s.state = StateDisabled
		s.mu.Unlock()
		s.emit(v1.RedactionNone, true, "module disabled after crash-looping",
			v1.Int("consecutive_failures", failures))
		return time.Time{}, false
	}

	backoff := s.backoffFor(failures)
	s.emit(v1.RedactionNone, false, "module stopped answering; restarting after backoff",
		v1.Int("consecutive_failures", failures),
		v1.String("backoff", backoff.String()))

	select {
	case <-s.stop:
		return time.Time{}, false
	case <-time.After(backoff):
	}

	m, err := Launch(s.cfg)
	if err != nil {
		// A failed relaunch is itself a failure: loop back through restart so the
		// backoff grows and the crash-loop limit still applies, rather than
		// retrying instantly.
		s.emit(v1.RedactionNone, true, "module failed to relaunch",
			v1.String("error", err.Error()))
		return s.restart()
	}

	s.mu.Lock()
	s.current = m
	s.state = StateRunning
	s.mu.Unlock()
	s.emit(v1.RedactionNone, false, "module restarted")
	return s.now(), true
}

// backoffFor returns the delay before the nth consecutive restart: exponential
// from InitialBackoff, capped at MaxBackoff.
func (s *Supervised) backoffFor(failures int) time.Duration {
	backoff := s.policy.InitialBackoff
	for i := 1; i < failures; i++ {
		backoff *= 2
		if backoff >= s.policy.MaxBackoff {
			return s.policy.MaxBackoff
		}
	}
	if backoff > s.policy.MaxBackoff {
		return s.policy.MaxBackoff
	}
	return backoff
}

func (s *Supervised) now() time.Time { return time.Now() }

// emit reports a lifecycle event through telemetry. It is how an admin is told,
// which is the third of platform#39's open crash-loop questions: a module going
// down and coming back is Warn, a module being disabled is Error, and both
// carry the module id so a reader knows which one.
func (s *Supervised) emit(_ v1.RedactionClass, isError bool, msg string, fields ...v1.Field) {
	if s.tel == nil {
		return
	}
	fields = append(fields, v1.String("module_id", s.manifest.ID))
	if isError {
		s.tel.Error(msg, fields...)
	} else {
		s.tel.Warn(msg, fields...)
	}
}

// live returns the running module, or nil when it is down or disabled.
func (s *Supervised) live() *Module {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// unavailable is the error a call gets when the module is not currently usable.
// It is Unavailable rather than Internal because a down module is a transient,
// expected state under this design, not a Platform fault.
func (s *Supervised) unavailable() error {
	return contracts.NewError(contracts.Unavailable,
		"extension: module "+s.manifest.ID+" is not currently available ("+s.State().String()+")")
}
