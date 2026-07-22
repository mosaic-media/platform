// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

// Resource is the process identity stamped on every record. Mosaic is a
// single-host system with more than one process — the Platform, the Supervisor
// when it exists (ADR 0060), and a module's own process if modules ever leave
// this one — so *which process said this* is a required dimension, not a
// decoration. The field names follow OpenTelemetry's resource conventions so
// an OTLP export later carries them unchanged.
type Resource struct {
	// ServiceName is the component's stable name: "mosaic-platform",
	// "mosaic-supervisor".
	ServiceName string
	// ServiceVersion is the build's version.
	ServiceVersion string
	// InstanceID distinguishes two processes of the same service.
	InstanceID string
	// GenerationID names the Supervisor Generation this process belongs to,
	// empty when the process was started directly rather than activated.
	GenerationID string
	// BootID names one start of the process. The Supervisor mints it and hands
	// it over so its own records and the Platform's stitch into one timeline
	// (ADR 0060); when nothing hands one over, the process mints its own so a
	// boot is always nameable in the logs.
	BootID string
}

// bootIDEnv names the environment variable a Supervisor uses to hand its boot
// id to the process it is starting. This is the one piece of ADR 0060 that can
// exist before the Supervisor does: the Platform adopts an inbound id when
// given one, so there is something to hand over to when the Supervisor arrives.
const bootIDEnv = "MOSAIC_BOOT_ID"

// NewResource builds the identity for this process. BootID is taken from the
// environment when a supervising process supplied one and minted otherwise.
func NewResource(serviceName, serviceVersion string) Resource {
	return Resource{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		InstanceID:     randomID(),
		GenerationID:   os.Getenv("MOSAIC_GENERATION_ID"),
		BootID:         bootID(),
	}
}

// bootID returns the inbound boot id, or a fresh one when the process was not
// started by something that supplied it.
func bootID() string {
	if id := os.Getenv(bootIDEnv); id != "" {
		return id
	}
	return randomID()
}

// randomID returns a short random hex identifier. A failure to read entropy
// yields an empty id rather than taking the process down: an unnamed boot is a
// degraded log, not a reason to refuse to start.
func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
