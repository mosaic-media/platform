// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package filesystem_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/filesystem"
)

// The spool is untrusted input from an independently-versioned binary, so the
// tests worth having are the ones about surviving it.

func writeSpool(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "findings.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatalf("write spool: %v", err)
	}
	return path
}

// A bad line is skipped and the good ones survive it. A boot that refused to
// proceed because the diagnostics were malformed would defeat what they are for,
// and a truncated file is the ordinary shape of one whose writer was killed —
// exactly when it has something to say.
func TestAnUnreadableLineDoesNotCostTheReadableOnes(t *testing.T) {
	path := writeSpool(t, `{"type":"child_unrecoverable","context":"child","reference":"platform"}
this is not json
{"type":"generation_rolled_back","context":"generation","reference":"v0.4.0"}
{"truncated":
`)
	findings, err := filesystem.ReadSpool(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("adopted %d findings, want the 2 readable ones: %+v", len(findings), findings)
	}
	if findings[0].Reference != "platform" || findings[1].Reference != "v0.4.0" {
		t.Errorf("the readable findings are not the ones adopted: %+v", findings)
	}
}

// A finding is adopted once. A file left behind would be re-read on every boot,
// turning one rollback into a finding that reappears forever — and by then the
// rows are in the register, so nothing would look wrong.
func TestTheSpoolIsConsumed(t *testing.T) {
	path := writeSpool(t, `{"type":"provision_failed","context":"host"}`+"\n")

	first, err := filesystem.ReadSpool(path)
	if err != nil || len(first) != 1 {
		t.Fatalf("first read: %d findings, %v", len(first), err)
	}
	second, err := filesystem.ReadSpool(path)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("the spool was adopted twice: %+v", second)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the spool file is still there, so the next boot adopts it again")
	}
	// And the working file it was renamed to is gone too.
	if _, err := os.Stat(path + ".adopting"); !os.IsNotExist(err) {
		t.Error("the taken-over file was left behind")
	}
}

// No file is the ordinary state of an install with nothing wrong, and of every
// install with no Supervisor. It must not be an error, or a plain docker run of
// the Platform would log one on every boot.
func TestNoSpoolIsNotAProblem(t *testing.T) {
	findings, err := filesystem.ReadSpool(filepath.Join(t.TempDir(), "nothing-here.jsonl"))
	if err != nil {
		t.Errorf("a missing spool is an error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a missing spool produced findings: %+v", findings)
	}
	if findings, err := filesystem.ReadSpool(""); err != nil || findings != nil {
		t.Errorf("an unconfigured spool is an error: %v", err)
	}
}

// A line with no type is skipped: the Platform owns the closed vocabulary, and
// a finding it cannot name is one it cannot render or offer a suggestion for.
func TestATypelessLineIsSkipped(t *testing.T) {
	path := writeSpool(t, `{"context":"host","detail":"something"}`+"\n")
	findings, err := filesystem.ReadSpool(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a typeless finding was adopted: %+v", findings)
	}
}
