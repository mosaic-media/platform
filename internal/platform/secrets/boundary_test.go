// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package secrets_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenFileReadPatterns are direct file-read APIs application services
// and Modules must not use to read secret/credential material — they must
// go through the Secret Broker (internal/platform/secrets) instead.
var forbiddenFileReadPatterns = []string{
	"os.ReadFile(",
	"os.Open(",
	"os.OpenFile(",
	"ioutil.ReadFile(",
}

// scannedRoots are the package families the Secret Broker rule names
// explicitly: "Application services and Modules must not read secret files
// directly."
// internal/transport is included too, defensively, though not named by that
// sentence — a transport reading a credential file directly would be just
// as wrong.
var scannedRoots = []string{
	filepath.Join("internal", "platform", "app"),
	filepath.Join("internal", "modules"),
	filepath.Join("internal", "transport"),
}

// TestApplicationServicesAndModulesDoNotReadFilesDirectly asserts that no
// package outside internal/platform/secrets (and the
// internal/adapters/filesystem helper it uses privately) reads credential
// files directly.
//
// It is a text-level scan rather than an AST or import-level check, so it can
// only match the spellings in forbiddenFileReadPatterns. Nothing in the
// scanned roots' production code performs any direct file read today —
// migrations use go:embed, config versions live in a ConfigStore — so the
// scan has no exemptions to carry and a change that adds one is caught.
//
// _test.go files are excluded: the rule is about production code paths that
// could run against real credentials, not a test reading back a fixture it
// wrote itself.
func TestApplicationServicesAndModulesDoNotReadFilesDirectly(t *testing.T) {
	root := moduleRoot(t)

	for _, relRoot := range scannedRoots {
		dir := filepath.Join(root, relRoot)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, pattern := range forbiddenFileReadPatterns {
				if strings.Contains(string(contents), pattern) {
					t.Errorf("%s: contains %q — application services, Modules and transports must access secrets through the Secret Broker (internal/platform/secrets), never by reading files directly", rel, pattern)
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// moduleRoot locates the repository root from this test file's own path,
// so the check works regardless of the working directory `go test` was
// invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's path")
	}
	// thisFile is .../internal/platform/secrets/boundary_test.go.
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}
