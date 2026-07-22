// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package health_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenImportPrefixes mirror internal/transport/auth's own boundary
// check: no transport may import the concrete Postgres module or a raw SQL
// driver — the same rule, generalized to every transport.
var forbiddenImportPrefixes = []string{
	"github.com/mosaic-media/platform/internal/modules/postgres",
	"github.com/jackc/pgx",
	"database/sql",
}

// TestHandoffDoesNotImportPostgresOrRawSQL parses every file's actual
// import declarations (go/parser, not a text grep), so it cannot be fooled
// by a comment or miss an import written differently than expected.
func TestHandoffDoesNotImportPostgresOrRawSQL(t *testing.T) {
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
					t.Errorf("%s: imports %q — the Supervisor handoff transport must call internal/platform/runtime only, never internal/modules/postgres or a raw SQL driver", entry.Name(), importPath)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no .go files were checked — the package directory resolution is broken")
	}
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's path")
	}
	return filepath.Dir(thisFile)
}
