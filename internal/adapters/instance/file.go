// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package instance stores this install's identity in a file on disk
// (ADR 0098).
//
// It is in internal/adapters rather than internal/modules because it is not
// module-shaped: it implements one small port over the filesystem, it has no
// manifest and no lifecycle, and nothing would ever want a second
// implementation of it. That is the distinction the package tier model draws,
// and this is the filesystem-helper case it names.
//
// **Why a file at all**, when there is a perfectly good database: because the
// question this answers is "which server is this" and the moment it is asked
// most urgently is when the database cannot answer anything. A server name held
// only in PostgreSQL vanishes exactly when a person needs it to identify the
// machine that has stopped working.
package instance

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// File is an InstanceIdentityStore backed by one JSON document.
type File struct{ path string }

// Compile-time proof this satisfies the port it exists for.
var _ contracts.InstanceIdentityStore = (*File)(nil)

// NewFile builds a store over path. The file is not read or created here: a
// Platform that has never been claimed has no identity to read, and creating an
// empty one at construction would make "unclaimed" indistinguishable from
// "claimed and empty" for every later reader.
func NewFile(path string) *File { return &File{path: path} }

// document is the on-disk shape. It is its own type rather than the domain
// struct so that a field renamed in the domain does not silently change the
// format of a file already written on somebody's disk — the two are allowed to
// drift, and the mapping below is where the drift has to be dealt with.
type document struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ClaimedAt string `json:"claimedAt"`
}

// Read returns the stored identity, NotFound when the file does not exist.
func (f *File) Read(_ context.Context) (domain.InstanceIdentity, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.InstanceIdentity{}, contracts.NewError(contracts.NotFound, "this server has no recorded identity")
		}
		return domain.InstanceIdentity{}, contracts.WrapError(contracts.Unavailable, "read the instance identity file", err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		// A corrupt file is Unavailable rather than NotFound, deliberately. They
		// differ in what happens next: NotFound invites a claim, and a server
		// whose identity file will not parse has almost certainly been claimed
		// already. Offering to claim it again would be the one destructive
		// answer available here.
		return domain.InstanceIdentity{}, contracts.WrapError(contracts.Unavailable, "parse the instance identity file", err)
	}
	identity := domain.InstanceIdentity{ID: doc.ID, Name: doc.Name}
	if doc.ClaimedAt != "" {
		if at, err := parseTime(doc.ClaimedAt); err == nil {
			identity.ClaimedAt = at
		}
	}
	return identity, nil
}

// Write records the identity, creating the directory if it is missing.
//
// It writes to a temporary file in the same directory and renames over the
// target, so a process that dies mid-write leaves either the old document or
// the new one and never half of either. Same directory rather than a temp dir
// because a rename across filesystems is a copy, and a copy is not atomic.
func (f *File) Write(_ context.Context, identity domain.InstanceIdentity) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return contracts.WrapError(contracts.Unavailable, "create the instance identity directory", err)
	}
	doc := document{ID: identity.ID, Name: identity.Name}
	if !identity.ClaimedAt.IsZero() {
		doc.ClaimedAt = formatTime(identity.ClaimedAt)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return contracts.WrapError(contracts.Internal, "encode the instance identity", err)
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(dir, ".instance-*.json")
	if err != nil {
		return contracts.WrapError(contracts.Unavailable, "create a temporary instance identity file", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return contracts.WrapError(contracts.Unavailable, "write the instance identity", err)
	}
	// Synced before the rename. Without it the rename can land while the
	// contents have not, which on a power loss produces the one outcome this
	// whole file exists to prevent: an identity file that is present and empty.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return contracts.WrapError(contracts.Unavailable, "flush the instance identity", err)
	}
	if err := tmp.Close(); err != nil {
		return contracts.WrapError(contracts.Unavailable, "close the instance identity file", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		return contracts.WrapError(contracts.Unavailable, "replace the instance identity file", err)
	}
	return nil
}

// parseTime and formatTime keep the on-disk timestamp in RFC 3339, which is
// what every other Mosaic document uses and what a person reading the file with
// `cat` can make sense of.
func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
