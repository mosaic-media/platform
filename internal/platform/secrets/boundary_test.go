package secrets_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenFileReadPatterns are direct file-read APIs MEG-015 §08 says
// application services and Modules must not use to read secret/credential
// material — they must go through the Secret Broker (internal/platform/
// secrets) instead.
var forbiddenFileReadPatterns = []string{
	"os.ReadFile(",
	"os.Open(",
	"os.OpenFile(",
	"ioutil.ReadFile(",
}

// scannedRoots are the package families MEG-015 §08 names explicitly:
// "Application services and Modules must not read secret files directly."
// internal/transport is included too, defensively, though not named by that
// sentence — a transport reading a credential file directly would be just
// as wrong.
var scannedRoots = []string{
	filepath.Join("internal", "platform", "app"),
	filepath.Join("internal", "modules"),
	filepath.Join("internal", "transport"),
}

// TestApplicationServicesAndModulesDoNotReadFilesDirectly is the MEG-015
// §12 static check for the Secret broker exit criterion: no package outside
// internal/platform/secrets (and the internal/adapters/filesystem helper it
// uses privately) reads credential files directly. It is a coarse,
// text-level scan rather than an AST/import-level check, but it is a real
// regression guard, not a tautology: today nothing in the scanned roots'
// production code performs ANY direct file read at all (migrations use
// go:embed, config versions live in a ConfigStore, ...), so a future
// change that adds one will be caught here. _test.go files are excluded:
// the rule is about production code paths that could run against real
// credentials, not a test reading back a fixture (a temp support bundle,
// a temp log file) it wrote itself.
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
					t.Errorf("%s: contains %q — application services, Modules and transports must access secrets through the Secret Broker (internal/platform/secrets), never by reading files directly (MEG-015 §08)", rel, pattern)
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
