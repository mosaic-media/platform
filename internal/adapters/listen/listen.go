// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package listen

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Package listen binds a listener whose transport is decided by the shape of
// its address. It is not module-shaped and implements no contract, which is
// what puts it in `adapters` rather than `modules`.
//
// On binds the address: an absolute path is a Unix socket, anything else is
// TCP.
//
// Both of the Platform's listeners are sockets in the shipped install, so the
// Supervisor is the only way to reach either (ADR 0120). One setting decides
// the transport and the address together, rather than an address beside a mode
// flag that can contradict it.
func On(addr string) (net.Listener, error) {
	if !IsSocket(addr) {
		return net.Listen("tcp", addr)
	}

	// A socket file outlives the process that made it. A SIGKILL — which is
	// exactly what a Supervisor sends a child that ignored SIGTERM — leaves
	// one behind, and refusing to bind because the last stop was unclean
	// would turn a single crash into an outage that needs a human.
	if err := removeStaleSocket(addr); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(addr), 0o700); err != nil {
		return nil, fmt.Errorf("creating the socket directory: %w", err)
	}

	listener, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	// The socket's own mode is the access control ADR 0120 rests on. Set
	// after binding, because the umask applies to the bind and would
	// otherwise decide this.
	if err := os.Chmod(addr, 0o600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("securing the socket: %w", err)
	}
	return listener, nil
}

// IsSocket reports whether an address names a Unix socket rather than a
// host:port. It is exported because callers need the same answer for reasons
// beyond binding — whether a forwarded header may be believed turns on it
// (ADR 0120), and asking the question twice with two rules is how the two
// answers come to disagree.
func IsSocket(addr string) bool { return strings.HasPrefix(addr, "/") }

// removeStaleSocket unlinks a leftover socket, and refuses to touch anything
// that is not one — a path that has become a regular file is a
// misconfiguration, and deleting a user's file to bind over it would be a
// far worse answer than failing to start.
func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking the socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket; refusing to remove it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing a stale socket: %w", err)
	}
	return nil
}
