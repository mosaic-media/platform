// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenImportPrefixes are what the transport rule forbids: the concrete
// Postgres module and any raw SQL driver. Reaching app/contracts/domain is fine
// and expected — those are how a transport is SUPPOSED to reach state.
//
// This check used to live in internal/transport/graphql, which was the
// Platform's original transport and so the place the rule was first written
// down. ADR 0061 retired that package; the rule is unchanged and it is stated
// here now, alongside the sibling copy in internal/transport/health.
var forbiddenImportPrefixes = []string{
	"github.com/mosaic-media/platform/internal/modules/postgres",
	"github.com/jackc/pgx",
	"database/sql",
}

// TestAuthTransportDoesNotImportPostgresOrRawSQL parses each file's actual
// import declarations (go/parser with ImportsOnly), so it cannot be fooled by a
// comment or a substring match and cannot miss an import written differently
// than expected.
func TestAuthTransportDoesNotImportPostgresOrRawSQL(t *testing.T) {
	dir := packageDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		checked++
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			for _, forbidden := range forbiddenImportPrefixes {
				if strings.HasPrefix(importPath, forbidden) {
					t.Errorf("%s: imports %q — a transport must call application services only, never internal/modules/postgres or a raw SQL driver", entry.Name(), importPath)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no .go files were checked — the package directory resolution is broken")
	}
}

// packageDir locates this test file's own directory via runtime.Caller, so the
// check works regardless of the working directory `go test` was invoked from.
func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's path")
	}
	return filepath.Dir(thisFile)
}
