// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Repository is a source of extension modules (ADR 0065): a signed index over
// HTTPS and the key that signs it.
//
// Trust is per repository, not per module and not global (ADR 0065). Adding a
// repository *is* the trust decision — the moment a user takes on the risk that
// its modules run with the Platform's authority — which is why consent attaches
// here and not at each install. Mosaic's own repository is trusted by default; a
// third-party one is added with explicit informed consent (ADR 0079 puts that
// surface in the Platform).
//
// One key vouches for everything the repository distributes: it signs the index,
// and the same key is what a module from this repository is verified against.
// That collapses "the repository is trusted" and "this module is authentic" onto
// the one decision a user actually made — trusting the repository — rather than
// asking them to reason about a separate publisher key per module. A future
// separation of publisher key from repository key is possible; it is not what
// consent-per-repository asks for.
type Repository struct {
	// Name identifies the repository in provenance and in the admin surface. It
	// is what an admin sees beside a module, so it is human-facing.
	Name string
	// URL is the HTTPS base the index and binaries are fetched from.
	URL string
	// Key signs this repository's index and vouches for its modules.
	Key ed25519.PublicKey
	// Official marks Mosaic's own repository, trusted by default. A user who
	// never touches repository settings only ever installs from an Official one.
	Official bool
}

// keyring returns a keyring trusting only this repository's key, for verifying
// its index and modules.
func (r Repository) keyring() *Keyring {
	k := NewKeyring()
	_ = k.Trust(r.Name, r.Key)
	return k
}

// Registry is the set of configured repositories. It is populated at composition
// (the official repository) and by an admin adding one (ADR 0065's consent). A
// repository added here is one whose modules the user has agreed may run.
type Registry struct {
	repos map[string]Repository
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{repos: make(map[string]Repository)}
}

// Add registers a repository. For a user-added one, calling Add is the recorded
// consent; the caller is responsible for having obtained it (the admin surface).
// A repeated name replaces, so re-adding is how a repository's key rotates.
func (r *Registry) Add(repo Repository) error {
	if repo.Name == "" {
		return fmt.Errorf("extension: a repository needs a name")
	}
	if len(repo.Key) != ed25519.PublicKeySize {
		return fmt.Errorf("extension: repository %q has no valid signing key", repo.Name)
	}
	r.repos[repo.Name] = repo
	return nil
}

// Lookup returns a configured repository by name.
func (r *Registry) Lookup(name string) (Repository, bool) {
	repo, ok := r.repos[name]
	return repo, ok
}

// ─── The signed index ───────────────────────────────────────────────────────

// Index is a repository's catalogue: the modules it offers, each with its
// manifest and where to download each platform's binary. The whole index is
// signed by the repository's key, so a client can trust that this catalogue —
// and every manifest in it — came from the repository unaltered.
//
// The manifests are carried *inline* rather than referenced, so verifying the
// index's one signature authenticates the whole catalogue at once. A module's
// binary is still checked against the digest its inline manifest declares, so a
// tampered binary is caught even though the index signature covers the manifest,
// not the bytes on a mirror.
type Index struct {
	Schema  string        `json:"schema"`
	Modules []IndexModule `json:"modules"`
}

// IndexSchema is the only index schema this build understands.
const IndexSchema = "mosaic.module.index/v1"

// IndexModule is one module the repository offers: its manifest, inline. The
// manifest carries everything needed to install — the binaries' download URLs
// and their digests — so an index entry is the manifest and nothing beside it.
// The manifest's digests are what a downloaded binary is checked against.
type IndexModule struct {
	Manifest Manifest `json:"manifest"`
}

// ParseIndex validates an index's structure — not its signature, which is a
// separate step. It refuses an unknown schema rather than guessing at a
// catalogue format it does not understand.
func ParseIndex(data []byte) (Index, error) {
	var idx Index
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&idx); err != nil {
		return Index{}, fmt.Errorf("extension: parsing index: %w", err)
	}
	if idx.Schema != IndexSchema {
		return Index{}, fmt.Errorf("extension: unknown index schema %q (this build understands %q)", idx.Schema, IndexSchema)
	}
	return idx, nil
}

// module returns the entry for a module id, or false.
func (idx Index) module(id string) (IndexModule, bool) {
	for _, m := range idx.Modules {
		if m.Manifest.ID == id {
			return m, true
		}
	}
	return IndexModule{}, false
}

// verifyIndex checks the index bytes were signed by the repository's key and
// parses them. A repository that cannot prove it produced its own catalogue is
// refused — the index signature is the "this came from the repository" check
// that the per-module digest cannot make.
func (r Repository) verifyIndex(data, signature []byte) (Index, error) {
	if _, ok := r.keyring().verify(data, signature); !ok {
		return Index{}, contracts.NewError(contracts.PermissionDenied,
			fmt.Sprintf("extension: index from %q does not verify against its key", r.Name))
	}
	idx, err := ParseIndex(data)
	if err != nil {
		return Index{}, contracts.WrapError(contracts.InvalidArgument, "extension: index", err)
	}
	return idx, nil
}
