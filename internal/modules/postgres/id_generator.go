// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// uuidGenerator is the runtime contracts.IDGenerator. It emits random
// version-4 UUID strings.
//
// As with the Clock, IDGenerator is a runtime generator rather than a
// PostgreSQL concern; it is bundled in the built-in module so the composition
// root has a complete driven-port set. The contract deliberately commits to no
// particular strategy, so the UUID choice lives entirely behind the port.
type uuidGenerator struct{}

// NewIDGenerator returns a UUIDv4-based IDGenerator.
func NewIDGenerator() contracts.IDGenerator {
	return uuidGenerator{}
}

func (uuidGenerator) NewID() domain.ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a catastrophic environment fault, not a
		// recoverable condition; identity generation cannot proceed safely.
		panic("postgres: id generator: " + err.Error())
	}
	// Set the version (4) and variant (RFC 4122) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return domain.ID(buf[:])
}
