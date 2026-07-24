// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package sdkboundary_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestExtensionProbeImportsOnlyThePublishedSurface holds test/extprobe to what a
// third-party extension module can actually import: the SDK contract and the
// harness, and nothing else (ADR 0016, ADR 0064).
//
// **This is a weaker guarantee than the one next door, and deliberately so.**
// test/sdkprobe is its own Go module, so Go itself refuses an internal/ import
// and the compiler is the check. test/extprobe is inside the Platform module,
// which means nothing structural stops it reaching internal/ — so a parse of its
// imports is what stands in.
//
// The trade is worth naming rather than leaving to be discovered. Making
// extprobe a separate module would need `replace` directives to two nested
// working trees and would buy a property the Platform's own build already
// demonstrates, since the Platform depends on the published sdk/host. What
// matters here is that the probe stays an honest example of what a module
// author writes, and an import that crept in would make it a dishonest one —
// the probe would still pass its tests while no longer demonstrating anything.
func TestExtensionProbeImportsOnlyThePublishedSurface(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "extprobe"))
	if err != nil {
		t.Fatalf("resolve probe dir: %v", err)
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing test/extprobe: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("test/extprobe has no Go packages — the probe is what proves the boundary works")
	}

	// Everything a module author is allowed to reach. The standard library is
	// permitted; anything under github.com/mosaic-media/ that is not the SDK or
	// its harness is not.
	allowed := map[string]bool{
		"github.com/mosaic-media/sdk/contracts/platform/v1": true,
		"github.com/mosaic-media/sdk/host":                  true,
	}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, imp := range file.Imports {
				spec, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("%s: unquoting import %s: %v", path, imp.Path.Value, err)
				}
				// The standard library has no dot in its first path element.
				if !strings.Contains(strings.SplitN(spec, "/", 2)[0], ".") {
					continue
				}
				if allowed[spec] {
					continue
				}
				t.Errorf("%s imports %q.\n"+
					"test/extprobe stands in for a module written by someone outside this "+
					"repository, so it may import only the published SDK and its harness. "+
					"An import beyond those makes the probe an example nobody can follow.",
					filepath.Base(path), spec)
			}
		}
	}
}
