// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mosaic-media/platform/internal/transport/netguard"
)

// httpFetcher retrieves repository artefacts over HTTPS, through netguard's dial
// guard — the same protection every other outbound Platform fetch uses. A
// repository index or a module binary is content the Platform downloads on a
// user's behalf, so it is exactly the SSRF surface netguard exists for: a
// repository URL that resolved into the host's own network would otherwise let
// an attacker point "install a module" at an internal service.
type httpFetcher struct {
	client *http.Client
}

// NewHTTPFetcher returns a Fetcher backed by a guarded HTTP client. It is what a
// serving Platform wires into an [Installer]; tests wire a fake instead.
func NewHTTPFetcher() Fetcher {
	return &httpFetcher{
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext:         netguard.DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

func (f *httpFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: status %d", url, resp.StatusCode)
	}
	// A module binary is a few tens of megabytes; a bound keeps a hostile or
	// broken repository from streaming forever. 256 MiB is generous headroom
	// over any real module and still finite.
	const maxArtefact = 256 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtefact))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return data, nil
}
