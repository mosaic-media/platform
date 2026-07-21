// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenImportPrefixes are exactly what the GraphQL resolver rule
// forbids resolvers from reaching for: the concrete Postgres module
// and any raw SQL driver/connection package. Reaching contracts/domain/app
// is fine and expected — those are how a resolver is SUPPOSED to reach
// state.
var forbiddenImportPrefixes = []string{
	"github.com/mosaic-media/platform/internal/modules/postgres",
	"github.com/jackc/pgx",
	"database/sql",
}

// TestResolversDoNotImportPostgresOrRawSQL is the import boundary check: no
// file in this package imports internal/modules/postgres or a raw SQL
// driver. Unlike a
// text grep, this parses each file's actual import declarations (go/parser
// with ImportsOnly), so it cannot be fooled by a comment or a substring
// match and cannot miss an import written differently than expected.
func TestResolversDoNotImportPostgresOrRawSQL(t *testing.T) {
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
					t.Errorf("%s: imports %q — GraphQL resolvers must call application services only, never internal/modules/postgres or a raw SQL driver", entry.Name(), importPath)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no .go files were checked — the package directory resolution is broken")
	}
}

// packageDir locates this test file's own directory via runtime.Caller, so
// the check works regardless of the working directory `go test` was
// invoked from.
func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's path")
	}
	return filepath.Dir(thisFile)
}
