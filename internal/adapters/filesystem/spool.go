// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package filesystem

import "os"

// A spool is a temporary file written once and read many times while it is
// still growing — the shape a live transcode needs, where one process appends
// and several range requests read behind the frontier.
//
// It lives here rather than in the transport that uses it because the Secret
// Broker's rule is enforced by a static scan: no application service, module or
// transport opens a file directly (internal/platform/secrets' boundary test).
// The rule is about credential material and this is a video spool, but the check
// is deliberately coarse — its own comment says nothing in those roots performs
// any direct file read, so the next one is caught. Honouring that is cheaper
// than arguing with it, and it puts the filesystem behind a port where the
// hexagonal placement wanted it anyway.

// Spool is a growable temporary file. Writes append at an explicit offset and
// reads may lag behind them; Close removes the backing file.
type Spool interface {
	WriteAt(p []byte, off int64) (int, error)
	ReadAt(p []byte, off int64) (int, error)
	Close() error
}

// TempSpool creates a spool backed by a file in the system temp directory.
//
// The pattern names Mosaic so an operator finding one after a crash knows what
// left it, and the file is removed on Close — a transcode that ends normally
// leaves nothing behind, and one killed with the process is the operator's to
// sweep, which the name is what makes possible.
func TempSpool(pattern string) (Spool, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	return &tempSpool{file: f}, nil
}

type tempSpool struct{ file *os.File }

func (s *tempSpool) WriteAt(p []byte, off int64) (int, error) { return s.file.WriteAt(p, off) }
func (s *tempSpool) ReadAt(p []byte, off int64) (int, error)  { return s.file.ReadAt(p, off) }

func (s *tempSpool) Close() error {
	name := s.file.Name()
	err := s.file.Close()
	if rmErr := os.Remove(name); err == nil {
		err = rmErr
	}
	return err
}
