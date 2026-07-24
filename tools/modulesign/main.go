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
//	modulesign build-index    -url <template> -out <index.json> <manifest.json>...
//	modulesign build-manifest -identity <id.json> -sdk-major <n> -out <manifest.json> <os/arch=binary>...
//
// build-manifest assembles a module's distribution manifest from the identity
// the module printed about itself (`mymodule --mosaic-manifest`) plus the SDK
// major and the built binaries, computing each binary's digest. It is the
// producer step a module's release runs: the module owns its id/version/name/
// roles, the build owns the SDK major and the bytes, and this joins them into
// the manifest.json a repository catalogues.
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
	"strconv"
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
	case "build-manifest":
		buildManifest(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: modulesign genkey -out <path> | digest <binary> | sign -key <path> <manifest.json> | sign-index -key <path> <index.json> | build-index -url <template> -out <index.json> <manifest.json>... | build-manifest -identity <json> -sdk-major <n> -out <manifest.json> <os/arch=binary>...")
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
// already carries its binaries, their digests and their download URLs — the
// module owns all three — so this only wraps them into the catalogue the
// repository signs. It does not compute URLs: a module's repository name need
// not match its id, so only the module knows where its bytes live.
func buildIndex(args []string) {
	out := flagValue(args, "-out")
	if out == "" {
		fail("build-index needs -out <path>, then manifest paths")
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
		idx.Modules = append(idx.Modules, extension.IndexModule{Manifest: m})
	}

	// Validate the round trip before writing: what is written must be what the
	// Platform parses, so a broken entry fails here rather than at a user's
	// install.
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

// buildManifest joins a module's self-declared identity with the SDK major and
// the built binaries into a distribution manifest.json. It is the one place the
// three sources of a manifest meet — the module (identity), the toolchain (SDK
// major) and the build (binaries and their digests) — so none of them is
// hand-copied into a workflow to drift.
func buildManifest(args []string) {
	identityPath := flagValue(args, "-identity")
	out := flagValue(args, "-out")
	sdkMajorStr := flagValue(args, "-sdk-major")
	urlTemplate := flagValue(args, "-url") // where this module hosts its binaries
	version := flagValue(args, "-version") // optional override of the identity's version
	if identityPath == "" || out == "" || sdkMajorStr == "" || urlTemplate == "" {
		fail("build-manifest needs -identity <json>, -sdk-major <n>, -url <template>, -out <path>, then os/arch=binary pairs")
	}
	sdkMajor, err := strconv.Atoi(sdkMajorStr)
	if err != nil {
		fail("-sdk-major must be a number: %v", err)
	}

	// The identity the module printed about itself.
	idData, err := os.ReadFile(identityPath) //nolint:gosec // the operator names their own file.
	if err != nil {
		fail("reading identity: %v", err)
	}
	var identity struct {
		ID       string   `json:"id"`
		Version  string   `json:"version"`
		Name     string   `json:"name"`
		Provides []string `json:"provides"`
	}
	if err := json.Unmarshal(idData, &identity); err != nil {
		fail("identity is not the JSON a module prints with --mosaic-manifest: %v", err)
	}
	if version != "" {
		identity.Version = version
	}

	// One binary per os/arch=path pair, digested with the same function the
	// Platform verifies against, and its download URL filled from the template.
	type binaryRef struct {
		OS     string `json:"os"`
		Arch   string `json:"arch"`
		Digest string `json:"digest"`
		URL    string `json:"url"`
	}
	var binaries []binaryRef
	for _, pair := range positionals(args) {
		platform, path, ok := strings.Cut(pair, "=")
		if !ok {
			fail("binary must be os/arch=path, got %q", pair)
		}
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok {
			fail("platform must be os/arch, got %q", platform)
		}
		digest, err := extension.FileDigest(path)
		if err != nil {
			fail("%v", err)
		}
		// {ext} is ".exe" on Windows and empty elsewhere, so one template covers a
		// matrix that includes Windows without the release having to special-case
		// the asset name — the same suffix Go's build gives the binary.
		ext := ""
		if goos == "windows" {
			ext = ".exe"
		}
		url := strings.NewReplacer(
			"{id}", identity.ID, "{version}", identity.Version,
			"{os}", goos, "{arch}", goarch, "{ext}", ext,
		).Replace(urlTemplate)
		binaries = append(binaries, binaryRef{OS: goos, Arch: goarch, Digest: digest, URL: url})
	}
	if len(binaries) == 0 {
		fail("build-manifest needs at least one os/arch=binary pair")
	}

	manifest := map[string]any{
		"schema":    extension.ManifestSchema,
		"id":        identity.ID,
		"version":   identity.Version,
		"name":      identity.Name,
		"sdk_major": sdkMajor,
		"provides":  identity.Provides,
		"binaries":  binaries,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail("marshalling manifest: %v", err)
	}
	// Validate before writing: a manifest the Platform cannot parse must fail at
	// the publisher, not at a user's install.
	if _, err := extension.ParseManifest(data); err != nil {
		fail("built a manifest that does not parse: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a manifest is public.
		fail("writing manifest: %v", err)
	}
	fmt.Printf("wrote %s for %s@%s (%d binaries)\n", out, identity.ID, identity.Version, len(binaries))
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
