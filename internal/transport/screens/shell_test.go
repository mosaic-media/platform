// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TestShellWearsTheChromeOfTheScreenItFrames pins that the two sides of Mosaic
// wear different frames, and the shell is the only place that can decide which.
// It used to be emitted once per session and never again, which is why settings
// wore the media side's floating pills: there was no moment at which anything
// could have chosen otherwise.
func TestShellWearsTheChromeOfTheScreenItFrames(t *testing.T) {
	for _, c := range []struct {
		screen     string
		chrome     string
		breadcrumb string
	}{
		{screenHome, chromeMedia, ""},
		{screenSearch, chromeMedia, ""},
		{screenDetail, chromeMedia, ""},
		{screenSettings, chromeAdmin, "/ Settings"},
		{screenExtensions, chromeAdmin, "/ Settings"},
		{screenLogs, chromeAdmin, "/ Settings / Logs"},
		{screenTraces, chromeAdmin, "/ Settings / Traces"},
	} {
		node, err := (&Service{}).shellScreen(context.Background(), v1.Caller{}, c.screen)
		if err != nil {
			t.Fatalf("%s: shellScreen: %v", c.screen, err)
		}
		if got, _ := prop(node, "chrome").(string); got != c.chrome {
			t.Errorf("%s chrome = %q, want %q", c.screen, got, c.chrome)
		}
		if got, _ := prop(node, "breadcrumb").(string); got != c.breadcrumb {
			t.Errorf("%s breadcrumb = %q, want %q", c.screen, got, c.breadcrumb)
		}
		// The administrative side has its own nav in the screen, and a search box
		// over it would search the library from a room the library is not in.
		_, hasSearch := node.GetSlots()["topbar"]
		if c.chrome == chromeAdmin && hasSearch {
			t.Errorf("%s carries the media search bar", c.screen)
		}
		if c.chrome == chromeMedia && !hasSearch {
			t.Errorf("%s lost the media search bar", c.screen)
		}
	}
}
