// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"github.com/mosaic-media/sdui/sdui"
	"github.com/mosaic-media/sdui/ui"
)

// PlayerParams is everything the server decides about a playback surface
// (ADR 0047). Every field here is a server decision; what the client owns is
// only the decoding pipeline and the transport controls.
type PlayerParams struct {
	// Src is the Platform-origin ticket URL. It is never the upstream location:
	// that may carry a debrid credential and stays server-side (ADR 0045).
	Src string
	// Title labels the player's own chrome.
	Title string
	// Poster is artwork to show before the first frame decodes.
	Poster string
	// MimeType names what the origin will serve, when the server knows — a
	// remuxed stream is always fragmented MP4. Empty means "discover it from the
	// response", which is correct for a relayed stream whose container is
	// whatever the source had.
	MimeType string
	// ResumeAt is the position in seconds to start from (ADR 0046). Zero starts
	// at the beginning.
	ResumeAt float64
	// NodeID and PartID name what is playing, so the client can report its
	// position back against them (ADR 0046).
	//
	// Both are server decisions carried on the node rather than things a client
	// works out, which keeps ADR 0047's limit where it is: the client owns the
	// decoding pipeline and the transport controls, and reports what it sees.
	// It does not decide what it is watching.
	NodeID string
	PartID string
}

// PlayerNode builds the Player surface pushed into the player region.
//
// It is deliberately a bare node rather than a Screen: a player is not a place
// you navigate to. It sits over the current context, and the screen underneath
// has to still be there when it closes — which is also why it rides its own
// region rather than replacing the content one.
func PlayerNode(p PlayerParams) sdui.Node {
	els := []ui.El{}
	if p.Title != "" {
		els = append(els, ui.Title(p.Title))
	}
	if p.Poster != "" {
		els = append(els, ui.Poster(p.Poster))
	}
	if p.MimeType != "" {
		els = append(els, ui.MimeType(p.MimeType))
	}
	if p.ResumeAt > 0 {
		els = append(els, ui.ResumeAt(p.ResumeAt))
	}
	if p.NodeID != "" {
		els = append(els, ui.Prop("nodeId", p.NodeID))
	}
	if p.PartID != "" {
		els = append(els, ui.Prop("partId", p.PartID))
	}
	return ui.Player(p.Src, els...).Build()
}
