// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package filesystem

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A segment directory is where one transcode writes its HLS segments and the
// origin reads them back from (platform#64, platform#66).
//
// It sits here rather than in the transport for the same reason the spool does:
// the Secret Broker's boundary check is a static scan that forbids any transport
// opening a file directly, deliberately coarsely, so honouring it is cheaper
// than arguing with it and it puts the filesystem behind a port either way.
//
// **The directory is bounded, not accumulated.** A transcode is not kept: the
// caller deletes segments behind the playhead and the whole directory goes when
// the session ends, so a playback costs a window of seconds rather than the
// size of the release. That is not tidiness — a release can be tens of
// gigabytes and the deployments this targets have far less disk than that.

// ErrBadSegmentName reports a name that is not a plain file in the directory.
var ErrBadSegmentName = errors.New("filesystem: segment name is not a bare filename")

// SegmentDir is a temporary directory holding one transcode's output.
type SegmentDir interface {
	// Path is where ffmpeg is told to write. A process that is handed a
	// directory needs a real one; there is no abstraction to pass it.
	Path() string
	// Has reports whether a completed segment of this name exists. With ffmpeg's
	// temp_file flag a segment appears at its final name only once written, so
	// presence is completion rather than a guess — verified: no .tmp files
	// survive a run.
	Has(name string) bool
	// Open reads a segment back.
	Open(name string) (io.ReadCloser, int64, error)
	// Remove deletes one segment, for eviction behind the playhead.
	Remove(name string) error
	// Names lists the segments currently present.
	Names() ([]string, error)
	// Close removes the directory and everything in it.
	Close() error
}

// NewSegmentDir creates the backing store for one transcode session.
type NewSegmentDir func() (SegmentDir, error)

// TempSegmentDir creates a segment directory under the system temp directory.
//
// The pattern names Mosaic so an operator finding one after a crash knows what
// left it, which is the same reason the spool's does — and matters more here,
// because this one holds media rather than a single file.
func TempSegmentDir(pattern string) (SegmentDir, error) {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return nil, err
	}
	return &tempSegmentDir{dir: dir}, nil
}

type tempSegmentDir struct{ dir string }

func (d *tempSegmentDir) Path() string { return d.dir }

// resolve rejects anything that is not a bare filename.
//
// **The name reaches this from a URL path**, so traversal is a live concern
// rather than a theoretical one: without this, `..%2f..%2fetc%2fpasswd` reads
// whatever the process can. The handler validates too; this is the layer that
// owns the filesystem and so is the layer that must not be talked out of it.
func (d *tempSegmentDir) resolve(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", ErrBadSegmentName
	}
	return filepath.Join(d.dir, name), nil
}

func (d *tempSegmentDir) Has(name string) bool {
	path, err := d.resolve(name)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (d *tempSegmentDir) Open(name string) (io.ReadCloser, int64, error) {
	path, err := d.resolve(name)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (d *tempSegmentDir) Remove(name string) error {
	path, err := d.resolve(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (d *tempSegmentDir) Names() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (d *tempSegmentDir) Close() error { return os.RemoveAll(d.dir) }
