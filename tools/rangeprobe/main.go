// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Command rangeprobe measures whether a resolved stream URL honours HTTP Range.
//
// It exists because M3 slice 4 branches on the answer and nothing in this
// repository knows it. The roadmap says a remuxed stream cannot be seeked, which
// is true and is a statement about *this origin's* implementation — ffmpeg's
// fragmented MP4 goes down a pipe, so there is no index to range over. It says
// nothing about the upstream, and the upstream is what decides which of two
// incompatible segmenter designs is buildable:
//
//   - **Upstream honours Range.** ffmpeg can seek the source over HTTP, so a
//     segment can be produced with `-ss` at a keyframe for the cost of one
//     ranged fetch. Restart-at-offset is cheap, and the reference designs that
//     assume a local seekable file port across more or less intact.
//   - **Upstream ignores Range.** Every `-ss` re-downloads from byte zero. A
//     restart-per-seek design is then catastrophic rather than merely wasteful,
//     and the only workable shape is a file-backed cache that accumulates bytes
//     once and lets readers seek within what has landed.
//
// The same answer settles a second question the roadmap treats as known: the
// direct-relay path already forwards Range upstream and relays Content-Range and
// the 206 back, so "a directly relayed stream is seekable" is *conditional* on
// the upstream, and has never been checked against a real debrid CDN.
//
// This is deliberately a tool and not a test. It reaches a live third-party CDN
// using a credential that exists only on a developer's machine, so it must never
// run in CI, and a test that skips when unconfigured is the fail-soft shape this
// repository's gates exist to avoid.
//
// Usage:
//
//	export MOSAIC_DEV_AIOSTREAMS_MANIFEST="https://…/manifest.json"
//	go run ./tools/rangeprobe                    # defaults to a well-seeded film
//	go run ./tools/rangeprobe -imdb tt0133093
//	go run ./tools/rangeprobe -url https://…     # skip resolution, probe a URL
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// The AIOStreams manifest URL is a credential: its path carries the profile id
// and the encrypted debrid configuration behind it. It is read from the
// environment rather than a flag so it does not land in shell history, and it is
// never printed — only its host is (ADR 0056, ADR 0059).
const manifestEnv = "MOSAIC_DEV_AIOSTREAMS_MANIFEST"

func main() {
	imdb := flag.String("imdb", "tt0133093", "IMDB id to resolve streams for")
	kind := flag.String("type", "movie", "Stremio content type: movie or series")
	direct := flag.String("url", "", "probe this URL directly and skip resolution")
	flag.Parse()

	client := &http.Client{Timeout: 60 * time.Second}

	target := *direct
	if target == "" {
		manifest := os.Getenv(manifestEnv)
		if manifest == "" {
			fmt.Fprintf(os.Stderr, `%s is not set.

This measurement needs a live AIOStreams instance backed by a debrid service.
The instance URL carries the profile id and the encrypted debrid config, so it
is a credential and cannot be synthesised or borrowed from a fixture — which is
the whole point, since a fixture is what would answer the question wrongly.

Put it in platform/.env (the slot is already documented in .env.example) or
export it, then re-run. Alternatively pass -url to probe a resolved URL you
already have.
`, manifestEnv)
			os.Exit(2)
		}
		var err error
		target, err = resolve(client, manifest, *kind, *imdb)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolving a stream: %v\n", err)
			os.Exit(1)
		}
	}

	if err := probe(client, target); err != nil {
		fmt.Fprintf(os.Stderr, "probing: %v\n", err)
		os.Exit(1)
	}
}

// resolve asks the instance for streams and returns the first result carrying a
// direct URL. A magnet-only result is skipped: there is nothing to range over
// until a debrid service has turned it into a link, and an instance returning
// only magnets is itself the finding.
func resolve(client *http.Client, manifest, kind, imdb string) (string, error) {
	base := strings.TrimSuffix(manifest, "/manifest.json")
	endpoint := base + "/stream/" + url.PathEscape(kind) + "/" + url.PathEscape(imdb) + ".json"

	if u, err := url.Parse(base); err == nil {
		fmt.Printf("instance host   %s\n", u.Host)
	}

	resp, err := client.Get(endpoint) //nolint:noctx // a one-shot developer tool.
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("instance answered %s — an unconfigured instance declares no stream resource, which looks identical to a broken one", resp.Status)
	}

	var body struct {
		Streams []struct {
			Name          string `json:"name"`
			Title         string `json:"title"`
			URL           string `json:"url"`
			InfoHash      string `json:"infoHash"`
			BehaviorHints struct {
				Filename string `json:"filename"`
			} `json:"behaviorHints"`
		} `json:"streams"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if len(body.Streams) == 0 {
		return "", fmt.Errorf("the instance returned no streams for %s — try another id, or check the profile has sources enabled", imdb)
	}

	magnets := 0
	for _, s := range body.Streams {
		if s.URL == "" {
			magnets++
			continue
		}
		name := s.BehaviorHints.Filename
		if name == "" {
			name = s.Title
		}
		fmt.Printf("candidates      %d (%d magnet-only)\n", len(body.Streams), magnets)
		fmt.Printf("release         %s\n", firstLine(name))
		fmt.Printf("container       %s\n", containerOf(s.URL, s.BehaviorHints.Filename))
		return s.URL, nil
	}
	return "", fmt.Errorf("all %d results were magnet-only, so there is no URL to range over — the profile has no debrid service resolving them", len(body.Streams))
}

// probe is the measurement. Three requests, and the third is the one that
// matters.
//
// A CDN that does not implement Range does not usually say so — it answers 200
// with the whole body, which a naive check reads as success because the bytes
// arrive. So the mid-file range is compared against the head of the file: if the
// two are equal, the server ignored the range and started from zero, whatever
// its status line claimed.
func probe(client *http.Client, target string) error {
	if u, err := url.Parse(target); err == nil {
		fmt.Printf("upstream host   %s\n", u.Host)
	}
	fmt.Println()

	// 1. HEAD — what the server advertises before being asked for anything.
	head, err := request(client, http.MethodHead, target, "")
	if err != nil {
		return err
	}
	size := head.length
	fmt.Printf("HEAD            %s  Accept-Ranges: %s  Content-Length: %s\n",
		head.status, orNone(head.acceptRanges), humanBytes(size))

	// A server may refuse HEAD and still range perfectly well, so a failure here
	// is reported and not fatal.
	if size <= 0 {
		fmt.Println("                no Content-Length from HEAD; falling back to the ranged responses")
	}

	// 2. The first kilobyte. This is what a player asks for first, and what a
	// container sniff needs.
	first, err := request(client, http.MethodGet, target, "bytes=0-1023")
	if err != nil {
		return err
	}
	fmt.Printf("GET 0-1023      %s  Content-Range: %s  (%d bytes)\n",
		first.status, orNone(first.contentRange), len(first.body))

	// 3. A range from the middle of the file — the seek a player actually
	// performs, and the request a non-compliant CDN answers from byte zero.
	// Half way in when the size is known, and only then a fixed floor — asking
	// past the end earns a 416, which is a correct answer to a wrong question
	// and reads as an inconclusive probe. A real film is tens of gigabytes so
	// this never bit in production; a 1 MB sample is what found it.
	var offset int64
	switch {
	case size > 2048:
		offset = size / 2
	case size > 0:
		return fmt.Errorf("the upstream is %d bytes — too small to seek within, so this cannot measure anything", size)
	default:
		offset = 1 << 20
	}
	mid, err := request(client, http.MethodGet, target,
		"bytes="+strconv.FormatInt(offset, 10)+"-"+strconv.FormatInt(offset+1023, 10))
	if err != nil {
		return err
	}
	fmt.Printf("GET mid-file    %s  Content-Range: %s  (%d bytes)\n",
		mid.status, orNone(mid.contentRange), len(mid.body))

	fmt.Println()

	// A failed fetch is not a measurement. An expired debrid link, a dead host
	// or a typo'd id all answer non-2xx, and reporting that as "does not honour
	// Range" would be the tool producing exactly the confident wrong answer it
	// exists to prevent — the reading would then argue for the more expensive of
	// two architectures on the strength of a 404.
	if first.code >= 300 || mid.code >= 300 {
		fmt.Println("RESULT          INCONCLUSIVE — the upstream did not serve the bytes.")
		fmt.Println()
		fmt.Printf("  The ranged requests answered %s and %s, so nothing here is\n", first.status, mid.status)
		fmt.Println("  a statement about Range support. A resolved debrid link is short-lived;")
		fmt.Println("  re-resolve and probe again rather than reading this as an answer.")
		return fmt.Errorf("upstream returned %s — no measurement was made", mid.status)
	}

	honours := mid.code == http.StatusPartialContent &&
		mid.contentRange != "" &&
		len(mid.body) > 0 &&
		!bytes.Equal(mid.body, first.body)

	switch {
	case honours:
		fmt.Println("RESULT          the upstream HONOURS Range.")
		fmt.Println()
		fmt.Println("  A directly relayed stream is genuinely seekable: the origin already")
		fmt.Println("  forwards Range and relays the 206 back, so that path needs nothing.")
		fmt.Println("  A segmenter for the MSE-incompatible containers can seek the source")
		fmt.Println("  with ffmpeg -ss over HTTP, so keyframe-aligned segments cost one")
		fmt.Println("  ranged fetch each rather than a re-download from zero.")
	case mid.code == http.StatusPartialContent && bytes.Equal(mid.body, first.body):
		fmt.Println("RESULT          the upstream CLAIMS 206 and IGNORES the range.")
		fmt.Println()
		fmt.Println("  The mid-file bytes are identical to the first kilobyte, so the server")
		fmt.Println("  answered from byte zero while reporting partial content. This is the")
		fmt.Println("  failure a status-code check reads as success. Treat it as no Range.")
	default:
		fmt.Println("RESULT          the upstream DOES NOT honour Range.")
		fmt.Println()
		fmt.Println("  Seeking a directly relayed stream is silently broken today, which is a")
		fmt.Println("  larger finding than this slice. A segmenter cannot restart ffmpeg at an")
		fmt.Println("  offset — every seek would re-download from zero — so the only workable")
		fmt.Println("  shape is a file-backed cache that accumulates the source once.")
	}
	return nil
}

type result struct {
	status       string
	code         int
	acceptRanges string
	contentRange string
	length       int64
	body         []byte
}

func request(client *http.Client, method, target, rng string) (result, error) {
	req, err := http.NewRequest(method, target, nil) //nolint:noctx // a one-shot developer tool.
	if err != nil {
		return result{}, err
	}
	// The same User-Agent the origin sends, because a CDN may vary its behaviour
	// on it and the measurement must describe what the origin will actually see.
	req.Header.Set("User-Agent", "mosaic-platform-playback/1")
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := client.Do(req)
	if err != nil {
		return result{}, err
	}
	defer resp.Body.Close()

	out := result{
		status:       resp.Status,
		code:         resp.StatusCode,
		acceptRanges: resp.Header.Get("Accept-Ranges"),
		contentRange: resp.Header.Get("Content-Range"),
		length:       resp.ContentLength,
	}
	if method == http.MethodGet {
		// Bounded: a server ignoring the range would otherwise stream the whole
		// film into this tool.
		out.body, err = io.ReadAll(io.LimitReader(resp.Body, 1024))
		if err != nil {
			return out, err
		}
	}
	if out.length <= 0 && out.contentRange != "" {
		if i := strings.LastIndex(out.contentRange, "/"); i >= 0 {
			if total, convErr := strconv.ParseInt(out.contentRange[i+1:], 10, 64); convErr == nil {
				out.length = total
			}
		}
	}
	return out, nil
}

// containerOf reports the container extension the origin's own heuristic would
// see. It reads the filename first and the URL path second, for the reason
// ShouldRemux records: a URL need not carry a truthful extension, and a
// container hint has already been found hiding in a query parameter.
func containerOf(rawURL, filename string) string {
	if ext := strings.ToLower(path.Ext(filename)); ext != "" {
		return ext + " (from filename)"
	}
	clean := rawURL
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	if ext := strings.ToLower(path.Ext(clean)); ext != "" {
		return ext + " (from url path)"
	}
	return "unknown — the origin would relay this unchanged"
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "(absent)"
	}
	return s
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "(unknown)"
	}
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGT"[exp])
}
