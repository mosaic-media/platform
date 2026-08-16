// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package filesystem

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Adopting the Supervisor's findings (platform#74).
//
// The Supervisor writes what went wrong to a file and the Platform takes it
// over when it comes up — because the findings worth having most are the ones
// made while the Platform is not there to be written to: a Platform that will
// not start, a Generation that was rolled back.
//
// The dependency does not invert. Nothing here is needed for the Platform to
// function; a missing file, an unreadable one and an empty one are all the same
// ordinary answer, which is "nothing to adopt".
//
// The file is untrusted input: it is written by one binary and read by another
// that may have been upgraded — or downgraded — independently, so a line that
// cannot be parsed is skipped rather than fatal. A boot that refused to proceed
// because the diagnostics were malformed would defeat what they are for.

// SpooledFinding is one line of the Supervisor's file, in its own shape.
//
// Deliberately not the Platform's domain.Issue: this is a wire format between
// two independently-versioned binaries, and mapping it at the boundary is what
// keeps a change to either side from being a change to both.
type SpooledFinding struct {
	Type      string    `json:"type"`
	Context   string    `json:"context"`
	Reference string    `json:"reference"`
	Detail    string    `json:"detail"`
	At        time.Time `json:"at"`
}

// ReadSpool takes over the Supervisor's findings: it reads them and removes the
// file, so a finding is adopted once rather than re-raised on every boot.
//
// The file is renamed before it is read, which is what makes the handover safe
// while the Supervisor is still running: the Supervisor appends to the original
// path and would otherwise be writing into a file this is about to delete,
// losing whatever it wrote in between. A rename on one filesystem is atomic, so
// the Supervisor's next append creates a fresh file and nothing falls between
// the two.
//
// A missing file is not an error. It is the ordinary state of an install that
// has had nothing go wrong, and of every install with no Supervisor at all.
func ReadSpool(path string) ([]SpooledFinding, error) {
	if path == "" {
		return nil, nil
	}

	taken := path + ".adopting"
	if err := os.Rename(path, taken); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("taking over the supervisor's findings: %w", err)
	}
	// Removed whatever happens below. A file left behind would be adopted again
	// on the next boot, turning one rollback into a finding that reappears
	// forever — and the rows are already in the register by then, so nothing
	// would look wrong.
	defer func() { _ = os.Remove(taken) }()

	f, err := os.Open(taken)
	if err != nil {
		return nil, fmt.Errorf("reading the supervisor's findings: %w", err)
	}
	defer f.Close()

	var out []SpooledFinding
	scanner := bufio.NewScanner(f)
	// A generous line bound. These are short records, and the cap exists so a
	// corrupt file cannot be read into memory rather than to constrain a real
	// one.
	scanner.Buffer(make([]byte, 0, 8<<10), 64<<10)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var finding SpooledFinding
		if err := json.Unmarshal(line, &finding); err != nil {
			// Skipped, not fatal. See the file comment: this is untrusted input
			// from another binary's version of the format.
			continue
		}
		if finding.Type == "" {
			continue
		}
		out = append(out, finding)
	}
	if err := scanner.Err(); err != nil {
		// Whatever was read before the error is still worth having: a truncated
		// file — the Supervisor was killed mid-write — has good lines in front
		// of the bad one, and those are the ones describing what happened.
		return out, nil
	}
	return out, nil
}
