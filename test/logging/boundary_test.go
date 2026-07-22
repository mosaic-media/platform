// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package logging_test holds the standing gate behind ADR 0053: the Platform
// observes through internal/platform/telemetry, and unstructured printing does
// not come back.
//
// The rule needs to be executable or it decays. The good structured logger
// already existed and was called from three non-test places, while everything
// that actually reported what the process was doing used fmt.Printf and
// log.Printf — not from negligence, but because reaching the logger cost a
// constructor parameter and printing cost nothing. Ambient telemetry removes
// that asymmetry; this test is what keeps it removed.
package logging_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// scannedRoots are the trees this gate covers: the Platform's own code, the
// composition root, and the in-repo reference capability.
var scannedRoots = []string{"internal", "cmd", "capabilities"}

// forbiddenPrinters are the fmt functions that write to stdout or stderr.
// fmt.Sprintf and fmt.Errorf are absent on purpose — they format, they do not
// emit, and fmt.Errorf is how every contract error in the codebase is built.
var forbiddenPrinters = map[string]bool{
	"Print": true, "Printf": true, "Println": true,
	"Fprint": true, "Fprintf": true, "Fprintln": true,
}

// allowedFiles may still print.
//
// cmd/mosaic-platform/main.go is deliberately NOT here. It is where fifteen
// prints accumulated, so exempting it would exempt most of what this gate is
// for. Its one legitimate last-resort write — the fatal path, which may run
// when building telemetry is itself what failed — goes through
// os.Stderr.WriteString rather than fmt, so the file stays covered.
//
// That does leave a hole: a direct os.Stderr.WriteString anywhere would pass.
// Closing it fully would mean banning a raw io.Writer call, which is too broad
// to be useful. The gate's purpose is to make structured telemetry the path of
// least resistance, and a deliberate WriteString is at least visible in review
// in a way fmt.Printf never was.
var allowedFiles = map[string]bool{
	// The licence-header tool is a developer command line, not part of the
	// running Platform; its output is its user interface.
	filepath.Join("tools", "licenseheader", "main.go"): true,
	// The console sink formats a record into a strings.Builder. It is the one
	// place in the Platform whose job *is* rendering output, and it is what
	// every other call site defers to — so the rule cannot apply to it without
	// applying to the thing the rule points at.
	filepath.Join("internal", "platform", "telemetry", "sink.go"): true,
}

// TestPlatformCodeDoesNotPrintOrUseStandardLog parses real syntax trees rather
// than grepping, so it cannot be fooled by the words appearing in a comment or
// a string, and cannot miss a call written across several lines.
func TestPlatformCodeDoesNotPrintOrUseStandardLog(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	checked := 0

	for _, dir := range scannedRoots {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			// Tests may print: t.Logf is the wrong tool for a fixture dump, and
			// a test's output has no retention, redaction or access concern.
			if strings.HasSuffix(path, "_test.go") || allowedFiles[rel] {
				return nil
			}

			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			checked++
			checkImports(t, rel, file)
			checkCalls(t, fset, rel, file)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	// A gate that silently scanned nothing would pass forever. This has caught
	// a renamed directory before it caught a print.
	if checked < 100 {
		t.Fatalf("only %d files scanned; the walk roots are probably wrong", checked)
	}
}

// checkImports rejects the standard log package outright. Unlike fmt it has no
// legitimate non-emitting use, so the import alone is the violation and there
// is no need to inspect call sites.
func checkImports(t *testing.T, rel string, file *ast.File) {
	t.Helper()
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if path == "log" {
			t.Errorf("%s imports the standard log package; use internal/platform/telemetry via telemetry.From(ctx) (ADR 0053)", rel)
		}
	}
}

// checkCalls rejects fmt's emitting functions. It resolves the fmt identifier
// through the file's own import list rather than assuming the package is
// spelled "fmt", so an aliased import cannot slip past.
func checkCalls(t *testing.T, fset *token.FileSet, rel string, file *ast.File) {
	t.Helper()
	fmtNames := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "fmt" {
			continue
		}
		if imp.Name != nil {
			fmtNames[imp.Name.Name] = true
		} else {
			fmtNames["fmt"] = true
		}
	}
	if len(fmtNames) == 0 {
		return
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || !fmtNames[pkg.Name] {
			return true
		}
		if forbiddenPrinters[sel.Sel.Name] {
			t.Errorf("%s:%d calls %s.%s; emit through internal/platform/telemetry instead (ADR 0053)",
				rel, fset.Position(call.Pos()).Line, pkg.Name, sel.Sel.Name)
		}
		return true
	})
}

// repoRoot walks up from this file to the module root, so the test does not
// depend on the working directory `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../platform/test/logging/boundary_test.go -> .../platform
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}
