// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"
)

// The setup tree is what an unclaimed server serves (ADR 0098). It asks for an
// owner and carries the one action such a tree may: claimServer.
func TestSetupScreenAsksForAnOwnerAndNothingElse(t *testing.T) {
	node := (&Service{}).SetupScreen("")

	if node.GetType() != "SetupFrame" {
		t.Fatalf("root = %q, want SetupFrame", node.GetType())
	}
	form, ok := find(node, "Form")
	if !ok {
		t.Fatal("no form on the setup screen")
	}
	act, _ := prop(form, "submitAction").(map[string]any)
	if act["mutation"] != "claimServer" {
		t.Fatalf("submit = %v, want the claimServer invoke", act)
	}

	// The four fields, and no more: naming the server, choosing folders,
	// connecting services and setting playback defaults are the design's other
	// five steps, and each needs a capability that does not exist.
	var tf []string
	var fieldNodes []sdui.Node
	findAll(node, "TextField", &fieldNodes)
	for _, f := range fieldNodes {
		n, _ := prop(f, "name").(string)
		tf = append(tf, n)
	}
	want := []string{"displayName", "username", "email", "password"}
	if len(tf) != len(want) {
		t.Fatalf("fields = %v, want exactly %v", tf, want)
	}
	for i, w := range want {
		if tf[i] != w {
			t.Errorf("field %d = %q, want %q", i, tf[i], w)
		}
	}
}

// A refused claim is stated by the screen that asked, like a refused sign-in.
func TestSetupScreenCarriesTheRefusal(t *testing.T) {
	node := (&Service{}).SetupScreen("this server has already been set up")
	form, ok := find(node, "Form")
	if !ok {
		t.Fatal("no form")
	}
	if got, _ := prop(form, "error").(string); got != "this server has already been set up" {
		t.Fatalf("error = %q, want the refusal", got)
	}
}
