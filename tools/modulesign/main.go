// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Command modulesign is the publisher side of extension-module signing (ADR
// 0065): it generates a signing key, computes a binary's digest in the exact
// format the Platform verifies against, and signs a manifest. A module's release
// workflow runs it; the Platform never does — the Platform only verifies.
//
// The digest it prints comes from the same function the Platform hashes with
// (extension.FileDigest), so there is no second definition of the format to
// drift. Getting that format wrong is the one mistake that fails silently at the
// publisher and only surfaces as "signature does not verify" on the far side,
// which is why the tool owns it rather than a README.
//
//	modulesign genkey      -out <path>                  # writes <path> and <path>.pub
//	modulesign digest      <binary>                     # prints sha256:<hex>
//	modulesign sign        -key <path> <manifest.json>  # writes <manifest.json>.sig
//	modulesign sign-index  -key <path> <index.json>     # writes <index.json>.sig
//	modulesign build-index -url <template> -out <index.json> <manifest.json>...
//
// build-index assembles a repository index from module manifests, computing each
// binary's download URL from a template with {id} {version} {os} {arch}
// placeholders — so a repository hosted on GitHub points its binaries at release
// download URLs and its index at Pages, with no bespoke script to get the format
// wrong. The output is signed with sign-index. The repository model is
// hosting-agnostic (ADR 0065); GitHub is one HTTPS host, and an untrusted one —
// the signature and digests are what protect a download, not the host.
//
// The private key is raw ed25519 seed bytes; the public key is the raw public
// key. Neither is armoured — a module publisher's key custody is their concern,
// and a raw key is the least ambiguous thing to hand a secret store.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "genkey":
		genkey(os.Args[2:])
	case "digest":
		digest(os.Args[2:])
	case "sign":
		signFile(os.Args[2:], "manifest", func(data []byte) error {
			_, err := extension.ParseManifest(data)
			return err
		})
	case "sign-index":
		signFile(os.Args[2:], "index", func(data []byte) error {
			_, err := extension.ParseIndex(data)
			return err
		})
	case "build-index":
		buildIndex(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: modulesign genkey -out <path> | digest <binary> | sign -key <path> <manifest.json> | sign-index -key <path> <index.json> | build-index -url <template> -out <index.json> <manifest.json>...")
	os.Exit(2)
}

func genkey(args []string) {
	out := flagValue(args, "-out")
	if out == "" {
		fail("genkey needs -out <path>")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail("generating key: %v", err)
	}
	// The private key file holds the seed, from which the full key is derived.
	// 0o600 because it is a secret; the tool refuses to be casual about that.
	if err := os.WriteFile(out, priv.Seed(), 0o600); err != nil {
		fail("writing private key: %v", err)
	}
	if err := os.WriteFile(out+".pub", pub, 0o644); err != nil { //nolint:gosec // a public key is public.
		fail("writing public key: %v", err)
	}
	fmt.Printf("wrote %s (private, keep secret) and %s.pub (trust this in the Platform)\n", out, out)
}

func digest(args []string) {
	if len(args) != 1 {
		fail("digest takes exactly one binary path")
	}
	d, err := extension.FileDigest(args[0])
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(d)
}

// signFile signs any document after validating it parses as the kind named, so
// the same signing path serves manifests and indexes and neither can be signed
// as garbage — a valid signature over an unparseable file fails far away with no
// clue why.
func signFile(args []string, kind string, validate func([]byte) error) {
	key := flagValue(args, "-key")
	path := lastNonFlag(args)
	if key == "" || path == "" {
		fail("sign needs -key <path> and a %s path", kind)
	}

	seed, err := os.ReadFile(key) //nolint:gosec // the operator names their own key file.
	if err != nil {
		fail("reading key: %v", err)
	}
	if len(seed) != ed25519.SeedSize {
		fail("key is not an ed25519 seed (%d bytes, want %d)", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	data, err := os.ReadFile(path) //nolint:gosec // the operator names their own file.
	if err != nil {
		fail("reading %s: %v", kind, err)
	}
	if err := validate(data); err != nil {
		fail("refusing to sign an invalid %s: %v", kind, err)
	}

	sig := ed25519.Sign(priv, data)
	out := path + ".sig"
	if err := os.WriteFile(out, sig, 0o644); err != nil { //nolint:gosec // a signature is public.
		fail("writing signature: %v", err)
	}
	fmt.Printf("wrote %s\n", out)
}

// buildIndex assembles a repository index from module manifests. Each manifest
// already carries its binaries and their digests (computed with `digest`); this
// adds the download URL for each, from a template, and emits the index a
// repository serves.
func buildIndex(args []string) {
	template := flagValue(args, "-url")
	out := flagValue(args, "-out")
	if template == "" || out == "" {
		fail("build-index needs -url <template> and -out <path>, then manifest paths")
	}
	manifests := positionals(args)
	if len(manifests) == 0 {
		fail("build-index needs at least one manifest path")
	}

	idx := extension.Index{Schema: extension.IndexSchema}
	for _, path := range manifests {
		data, err := os.ReadFile(path) //nolint:gosec // the operator names their own manifests.
		if err != nil {
			fail("reading %s: %v", path, err)
		}
		m, err := extension.ParseManifest(data)
		if err != nil {
			fail("%s: %v", path, err)
		}
		urls := make(map[string]string, len(m.Binaries))
		for _, b := range m.Binaries {
			urls[b.OS+"/"+b.Arch] = expand(template, m, b)
		}
		idx.Modules = append(idx.Modules, extension.IndexModule{Manifest: m, BinaryURLs: urls})
	}

	// Validate the round trip before writing: what is written must be what the
	// Platform parses, so a template that produced nonsense fails here rather
	// than at a user's install.
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		fail("marshalling index: %v", err)
	}
	if _, err := extension.ParseIndex(data); err != nil {
		fail("built an index that does not parse: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // an index is public.
		fail("writing index: %v", err)
	}
	fmt.Printf("wrote %s with %d module(s); sign it with: modulesign sign-index -key <key> %s\n", out, len(idx.Modules), out)
}

// expand fills a URL template's {id} {version} {os} {arch} placeholders.
func expand(template string, m extension.Manifest, b extension.BinaryRef) string {
	r := strings.NewReplacer(
		"{id}", m.ID,
		"{version}", m.Version,
		"{os}", b.OS,
		"{arch}", b.Arch,
	)
	return r.Replace(template)
}

// positionals returns the arguments that are not flags or flag values.
func positionals(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			i++ // skip the flag's value
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// flagValue returns the argument following name, or "".
func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

// lastNonFlag returns the last argument that is not a flag or a flag's value —
// the positional manifest path.
func lastNonFlag(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		if i > 0 && args[i-1] == "-key" {
			continue
		}
		if args[i] == "-key" {
			continue
		}
		return args[i]
	}
	return ""
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "modulesign: "+format+"\n", a...)
	os.Exit(1)
}
