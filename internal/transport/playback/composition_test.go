// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// The serving composition is checked here because nothing else can check it.
//
// **Reap and Close were written, tested, and called from nowhere.** Every unit
// test passed; the package's own tests exercised both directly. What was missing
// was a caller, and a missing caller is invisible to a test of the thing that
// should have been called — the transcode simply outlived every viewer, because
// startSession detaches the process from the request with
// context.WithoutCancel and the ticker that was supposed to be its counterpart
// did not exist.
//
// So this parses the composition root the way internal/transport/health parses
// imports: with go/parser rather than a grep, so a comment cannot satisfy it and
// a rename cannot silently drop it.

// TestTheServingCompositionOwnsItsTranscodes asserts that main.go holds the
// session registry, reaps it, and stops it on shutdown.
func TestTheServingCompositionOwnsItsTranscodes(t *testing.T) {
	file, path := parseCompositionRoot(t)

	// Two passes rather than one, because the registry's methods may be called
	// anywhere in the file and a single walk would miss any use the AST reaches
	// before the assignment that names it.
	sessionsIdent := ""
	ast.Inspect(file, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok {
			if name, ok := boundToNewSessions(assign); ok {
				sessionsIdent = name
			}
		}
		return true
	})
	if sessionsIdent == "" {
		t.Fatalf("%s never calls playback.NewSegmentSessions, so it holds no registry and can reap nothing", path)
	}

	// Which playback.X the composition calls. Handler must not be among them: it
	// builds a registry nobody can reach, so a Platform wired through it starts
	// transcodes it has no way to end.
	pkgCalls := map[string]bool{}
	// Which methods are called on the registry, whatever it was named.
	methods := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch recv.Name {
		case "playback":
			pkgCalls[sel.Sel.Name] = true
		case sessionsIdent:
			methods[sel.Sel.Name] = true
		}
		return true
	})

	if !pkgCalls["HandlerWithSessions"] {
		t.Errorf("%s does not call playback.HandlerWithSessions — see its doc for what holding no reference costs", path)
	}
	if pkgCalls["Handler"] {
		t.Errorf("%s calls playback.Handler; it keeps a registry nothing else can reach, so an abandoned playback leaves ffmpeg running and its spool on disk", path)
	}
	if !methods["StartReaper"] {
		t.Errorf("%s starts no transcode reaper — a viewer who closes the tab leaves ffmpeg pulling the whole release from the upstream", path)
	}
	if !methods["Close"] {
		t.Errorf("%s never stops the running transcodes — a restart leaves ffmpeg processes holding upstream connections open", path)
	}
}

// boundToNewSessions reports the identifier an assignment binds
// playback.NewSegmentSessions(...) to.
func boundToNewSessions(assign *ast.AssignStmt) (string, bool) {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewSegmentSessions" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "playback" {
		return "", false
	}
	name, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	return name.Name, true
}

func parseCompositionRoot(t *testing.T) (*ast.File, string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's path")
	}
	// internal/transport/playback -> the repository root -> the composition root.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	path := filepath.Join(root, "cmd", "mosaic-platform", "main.go")

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v — if the composition root moved, move this check with it", path, err)
	}
	return file, path
}
