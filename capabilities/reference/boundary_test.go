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

// TestCapabilityImportsOnlyPublishedContracts is the stop point made
// executable: the reference capability must use only the published contract
// surface (contracts/platform/v1) and the standard library. A private
// Platform import here would mean the contracts are not ready to publish
// (the roadmap's stop point, ADR 0016).
//
// It parses this package's non-test files rather than trusting review, in the
// same style as the GraphQL and health boundary tests.
func TestCapabilityImportsOnlyPublishedContracts(t *testing.T) {
	const (
		modulePrefix = "github.com/mosaic-media/mosaic-platform/"
		allowed      = "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
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
			// Standard-library imports have no dot in their first segment.
			if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				continue
			}
			if !strings.HasPrefix(path, modulePrefix) {
				t.Errorf("%s imports third-party package %q; the reference capability may use only v1 and the standard library", name, path)
				continue
			}
			if path != allowed {
				t.Errorf("%s imports private Platform package %q; the capability may import only %q", name, path, allowed)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no non-test source files were checked; the boundary test is not looking at anything")
	}
}
