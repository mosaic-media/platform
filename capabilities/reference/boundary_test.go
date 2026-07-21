// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package reference_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCapabilityImportsOnlyTheSDK is the stop point made executable: the
// reference capability must use only the published SDK (the mosaic-sdk module)
// and the standard library. A private Platform import here would mean the
// contracts are not ready to publish (the roadmap's stop point, ADR 0016).
//
// Since the surface was extracted into its own module, Go itself would also
// reject a Platform-internal import — but this parse keeps the intent explicit
// and catches a third-party dependency creeping in too, in the same style as
// the GraphQL and health boundary tests.
func TestCapabilityImportsOnlyTheSDK(t *testing.T) {
	const (
		sdkPrefix      = "github.com/mosaic-media/sdk/"
		platformPrefix = "github.com/mosaic-media/platform/"
	)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", name, err)
			}
			switch {
			// Standard-library imports have no dot in their first segment.
			case !strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
			case strings.HasPrefix(path, sdkPrefix):
				// The published SDK — the one dependency a capability may have.
			case strings.HasPrefix(path, platformPrefix):
				t.Errorf("%s imports private Platform package %q; a capability may import only the SDK", name, path)
			default:
				t.Errorf("%s imports third-party package %q; the reference capability may use only the SDK and the standard library", name, path)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no non-test source files were checked; the boundary test is not looking at anything")
	}
}
