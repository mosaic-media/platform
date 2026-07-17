package filesystem

import (
	"os"
	"path/filepath"
)

// Exists reports whether a regular file exists at path.
func Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ReadFile reads the whole file at path. This helper — and the package it
// lives in — is the deliberate exception to "no direct file reads": it is
// the low-level utility internal/platform/secrets' encrypted local vault
// uses to read its own ciphertext file. Application services and Modules
// must never call it (or any other direct file API) to read credential
// material themselves; they go through the Secret Broker instead
// (MEG-015 §08).
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFileAtomic writes data to path by writing a temporary file in the
// same directory and renaming it into place, so a crash or a concurrent
// reader never observes a partially written file. The temporary file (and
// the final file, via the same permissions) is created 0600 — readable
// only by the owner — appropriate for the vault ciphertext this backs.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
