// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Command extprobe is a trivial out-of-process module, and the first thing to
// cross the extension boundary for real (platform#39's build order, step 1: "a
// trivial in-repo module implementing one role — nothing user-visible;
// establishes the wire, the handshake and the handle").
//
// It is deliberately not a useful module. What it proves is that the mechanism
// works end to end where the in-package tests could not reach:
//
//   - go-plugin's handshake over a real Unix socket, in a real child process
//   - a manifest read back across the boundary
//   - Import calling back into the Platform's ContentService, within the
//     invocation, carrying the Caller handle it was given
//   - three provider roles, so role dispatch is exercised, and the five it does
//     not fill, so the refusal path is too
//
// Its whole main is the line every module author writes. If that stops being
// true, this file is where it shows.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
	"github.com/mosaic-media/sdk/host"
)

// probeSettings is how a test asks the probe to do more work. It rides the
// ordinary settings document (platform#17), which the Platform stores and hands
// back uninterpreted — so using it here costs no special mechanism.
type probeSettings struct {
	// Children makes Import add this many child nodes after the work. It exists
	// for the callback-cost measurement platform#39 requires before the protocol is
	// fixed: a tree import's cost is (calls per import) × (cost per call), and
	// this is what lets the second be measured without the first being guessed.
	Children int `json:"children"`

	// FetchURL makes the probe issue an HTTP GET during Import, using an ordinary
	// default-transport client — the shape a real module uses. It exists for the
	// egress tests (platform#39): the fetch must go through the Platform's proxy, so
	// a URL resolving to a private address is refused unless the operator override
	// is on. The probe records the outcome in the import's returned counts.
	FetchURL string `json:"fetch_url"`
}

// probe fills three of the eight roles and no more. The registry resolves roles
// from the manifest rather than by type assertion — a proxy satisfies every
// provider interface — so declaring exactly what is implemented, and leaving
// five unfilled, is what makes this a usable test of both directions.
//
// Search proves dispatch. Stream and subtitles are here for a narrower reason:
// they are the two roles carrying fields that a module.proto without them drops
// silently, and the only way to show a field crossing for real is a role served
// by a real child process.
type probe struct{}

func (probe) Manifest() v1.Manifest {
	return v1.Manifest{
		ID:       "extprobe",
		Version:  "v0.1.0",
		Name:     "Extension Probe",
		Provides: []v1.Role{v1.RoleSearch, v1.RoleStream, v1.RoleSubtitles},
	}
}

// Import writes one Work through the ContentService it reaches over the
// callback stream, acting as the Caller it was handed (platform#13). The write is
// the point: it is the only way to prove the callback direction works, and the
// counts it returns are what the test asserts against.
func (probe) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	// Telemetry is reached ambiently off the context, exactly as in process
	// (sdk#5) — a module never holds one.
	v1.TelemetryFrom(ctx).Info("extprobe import",
		v1.String("native_id", req.Ref.NativeID),
		v1.Int("settings_bytes", len(req.Settings)),
	)

	out, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller:    req.Caller,
		MediaType: req.Ref.MediaType,
		Title:     "probe: " + req.Ref.NativeID,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}

	// A tree import is many calls, not one. This is what makes the probe able
	// to stand in for one — a season of episodes is exactly this shape.
	var settings probeSettings
	if len(req.Settings) > 0 {
		// A settings document the module cannot parse is the user's, not an
		// error to fail an import over: unknown or malformed fields mean the
		// defaults, which is how every other module here treats them.
		_ = json.Unmarshal(req.Settings, &settings)
	}

	// An HTTP fetch through an ordinary client, for the egress tests. The result
	// is signalled through the import: Parts=1 means the fetch succeeded (the
	// proxy allowed it), Parts=0 means it failed (the proxy denied it or the host
	// was unreachable). A test reads that rather than the bytes, since what is
	// under test is whether the call was permitted, not what it returned.
	fetchOK := 0
	if settings.FetchURL != "" {
		resp, err := http.Get(settings.FetchURL) //nolint:gosec,noctx // the URL is the test's; egress is what is under test.
		if err == nil {
			// A denied fetch does not error: for plain HTTP the proxy answers
			// with a 403 rather than tunnelling, so err is nil and the status is
			// what says whether the call was permitted. Only a real 2xx from the
			// target counts as reaching it.
			if resp.StatusCode < 400 {
				fetchOK = 1
			}
			_ = resp.Body.Close()
		}
	}

	for i := 0; i < settings.Children; i++ {
		if _, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller:       req.Caller,
			ParentID:     out.Work.ID,
			Kind:         v1.NodeItem,
			ItemType:     v1.ItemEpisode,
			Title:        "episode",
			NaturalOrder: float64(i),
		}); err != nil {
			return v1.ImportResult{}, err
		}
	}

	return v1.ImportResult{WorkID: out.Work.ID, Items: 1 + settings.Children, Parts: fetchOK}, nil
}

// Search echoes its query back as one result. It exists so role dispatch is
// exercised in both directions — the request converted on the way out, the
// result on the way back.
func (probe) Search(_ context.Context, req v1.SearchRequest) (v1.SearchResponse, error) {
	return v1.SearchResponse{Results: []v1.SearchResult{{
		Ref: v1.ContentRef{
			Provider:       "extprobe",
			NativeID:       req.Text,
			NativeType:     "movie",
			MediaType:      v1.MediaMovie,
			ExternalScheme: "probe",
			ExternalID:     req.Text,
		},
		Title: "probe result: " + req.Text,
		Year:  2026,
	}}}, nil
}

// Streams answers with one link whose every descriptive field is non-zero,
// because a zero is what a dropped field looks like. The three technical fields
// are the point: a StreamLink crossing this boundary with an empty Container is
// indistinguishable from a source that parsed none, so nothing errors and the
// Platform simply knows less than the module did.
func (probe) Streams(_ context.Context, req v1.StreamRequest) (v1.StreamResponse, error) {
	return v1.StreamResponse{Streams: []v1.StreamLink{{
		Label:     "probe",
		Title:     "probe release " + req.Ref.NativeID,
		Quality:   "2160p",
		SizeBytes: 21_474_836_480,
		Seeders:   99,
		Location: v1.MediaLocation{
			Scheme: v1.RemoteLocation, Provider: "extprobe", Ref: "magnet:?xt=urn:btih:probe",
		},
		Container:  "mkv",
		VideoCodec: "hevc",
		AudioCodec: "eac3",
	}}}, nil
}

// Subtitles echoes the coordinates it was handed back through the response,
// which is the only way to observe the outbound leg from the Platform side:
// nothing the caller can read reports what the child actually received, and a
// request arriving with two zeroes resolves the wrong episode without erroring.
//
// The track's ID carries them because Subtitle has no numeric field and adding
// one to the contract to serve a test would be the tail wagging the dog.
func (probe) Subtitles(_ context.Context, req v1.SubtitlesRequest) (v1.SubtitlesResponse, error) {
	return v1.SubtitlesResponse{Subtitles: []v1.Subtitle{{
		Language: "eng",
		URL:      "https://subs.invalid/probe.srt",
		ID:       "s" + strconv.Itoa(req.Season) + "e" + strconv.Itoa(req.Episode),
	}}}, nil
}

func main() {
	// Controlled death, for the lifecycle tests (platform#39's step 3). A real
	// module has no such switch; this exists only so the Platform's restart,
	// backoff and crash-loop policy can be exercised against a process that
	// actually dies rather than one whose crash is imagined.
	//
	// The exit is armed before Serve and fires from a goroutine, so the
	// handshake completes first and go-plugin reports a live process that then
	// dies — which is the case the monitor must catch. Exiting before Serve
	// would instead fail the launch, a different path the tests cover separately.
	armSelfDestruct()
	host.Serve(probe{})
}

// armSelfDestruct exits the process after a delay when EXTPROBE_EXIT_AFTER_MS is
// set, optionally only on the first launch (when EXTPROBE_CRASH_ONCE names a
// marker file that a surviving second launch finds already present).
func armSelfDestruct() {
	ms := os.Getenv("EXTPROBE_EXIT_AFTER_MS")
	if ms == "" {
		return
	}
	delay, err := strconv.Atoi(ms)
	if err != nil {
		return
	}

	if marker := os.Getenv("EXTPROBE_CRASH_ONCE"); marker != "" {
		if _, err := os.Stat(marker); err == nil {
			return // a previous launch left the marker; this one survives.
		}
		// First launch: leave the marker so the next one survives, then die.
		_ = os.WriteFile(marker, []byte("crashed"), 0o600)
	}

	go func() {
		time.Sleep(time.Duration(delay) * time.Millisecond)
		os.Exit(1)
	}()
}
