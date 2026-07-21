// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// uuidV7Generator emits time-ordered version-7 UUIDs (RFC 9562 §5.7) for the
// content model (ADR 0013).
//
// It exists alongside the version-4 generator rather than replacing it. The
// infrastructure tables keep their random ids in text columns and are not
// migrated; the content tables need ordering. Random identifiers scatter btree
// inserts across the index, causing page splits and cache misses at the row
// counts the object graph targets, where time-ordered ones append near the
// right-hand edge.
//
// The layout is 48 bits of Unix milliseconds, 4 version bits, 12 random bits,
// 2 variant bits and 62 random bits. Monotonicity within a millisecond is not
// attempted: RFC 9562 makes the counter methods optional, and the property
// being bought here is btree locality, which millisecond ordering already
// delivers.
type uuidV7Generator struct {
	now func() time.Time
}

// NewUUIDv7Generator returns a UUIDv7-based IDGenerator for content-model
// identifiers.
func NewUUIDv7Generator() contracts.IDGenerator {
	return uuidV7Generator{now: time.Now}
}

// NewID returns a new time-ordered UUIDv7 content identifier.
func (g uuidV7Generator) NewID() domain.ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a catastrophic environment fault, not a
		// recoverable condition; identity generation cannot proceed safely.
		panic("postgres: uuidv7 generator: " + err.Error())
	}

	// 48-bit big-endian Unix milliseconds in the leading six bytes.
	ms := uint64(g.now().UTC().UnixMilli())
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], ms)
	copy(b[0:6], stamp[2:8])

	// Version 7 and the RFC 4122 variant; the remaining bits stay random.
	b[6] = (b[6] & 0x0f) | 0x70
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
