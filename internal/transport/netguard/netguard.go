// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package netguard holds the dial guard the Platform's outbound proxies share.
//
// Any handler that fetches a URL on a client's behalf opens an SSRF hole unless
// something stops it reaching the host's own network. The guard closes it at
// connect time — after DNS, with the concrete address in hand — so a hostname
// that resolves (or re-resolves) into a private range is refused rather than
// dialled.
//
// It lives here rather than in one proxy because there is now more than one:
// the artwork proxy (platform#20) and the playback origin (platform#25) fetch
// third-party URLs on the same terms, and a second copy of security-critical
// code is a second copy to get wrong.
package netguard

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned by the dial guard for a non-routable target.
var ErrBlockedAddress = errors.New("netguard: refusing to connect to a non-public address")

// DialContext dials only public addresses. The Control hook runs after DNS with
// the concrete address about to be connected, which is what closes the
// DNS-rebinding variant a hostname check alone would leave open.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || Blocked(ip) {
				return ErrBlockedAddress
			}
			return nil
		},
	}
	return d.DialContext(ctx, network, address)
}

// Blocked reports whether an IP is one an outbound proxy must not reach:
// loopback, private, link-local (including the cloud metadata address
// 169.254.169.254), unspecified, or otherwise not global-unicast.
func Blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || !ip.IsGlobalUnicast()
}
