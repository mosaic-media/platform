// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"
)

// PlayerParams is everything the server decides about a playback surface
// (web#4). Every field here is a server decision; what the client owns is
// only the decoding pipeline and the transport controls.
type PlayerParams struct {
	// Src is the Platform-origin ticket URL. It is never the upstream location:
	// that may carry a debrid credential and stays server-side (platform#25).
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
	// ResumeAt is the position in seconds to start from (platform#26). Zero starts
	// at the beginning.
	ResumeAt float64
	// NodeID and PartID name what is playing, so the client can report its
	// position back against them (platform#26).
	//
	// Both are server decisions carried on the node rather than things a client
	// works out, which keeps web#4's limit where it is: the client owns the
	// decoding pipeline and the transport controls, and reports what it sees.
	// It does not decide what it is watching.
	NodeID string
	PartID string
	// Subtitles are authored subtitle scripts the client may draw itself
	// (platform#70), each a URL under the same playback ticket.
	//
	// They do not replace what the playlist already declares. A client that
	// cannot render a script ignores these entirely and uses the HLS subtitle
	// renditions; one that can renders the signs where the author put them. So
	// an empty list is the ordinary case and costs nothing.
	Subtitles []SubtitleTrack
}

// SubtitleTrack is one authored subtitle script offered to the client.
type SubtitleTrack struct {
	// Src is the Platform-origin URL, never the upstream's (platform#25).
	Src string
	// Format names what the client will be parsing, so it can choose a renderer
	// before it fetches. Today always "ass".
	Format string
	// Language is the track's language code, and Label what a menu shows.
	Language string
	Label    string
	// Default marks the track the viewer's preference chose.
	Default bool
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
	if len(p.Subtitles) > 0 {
		els = append(els, ui.Prop("subtitleTracks", subtitleTrackProps(p.Subtitles)))
	}
	return ui.Player(p.Src, els...).Build()
}

// NextEpisodeNode is the "Next episode" control shown alongside a playing
// episode — "what comes next" is a server decision (web#4). It is a plain
// Button node the server pushes into the player region and the host renders
// through the component vocabulary; the client authors no chrome for it. Taking
// it is an ordinary playPart, which the Platform resolves and starts at the
// beginning — a next episode is a new play, not a resume.
//
// label names the episode, partID/nodeID/title are the play it dispatches.
func NextEpisodeNode(label, partID, nodeID, title string) sdui.Node {
	return ui.Button(label, "secondary", ui.OnTap(ui.Invoke(playPartAction, map[string]any{
		paramPartID: partID,
		paramNodeID: nodeID,
		"title":     title,
	}))).Build()
}

// subtitleTrackProps renders the authored subtitle scripts as the props bag the
// `subtitleTracks` prop carries (platform#70).
//
// **Set with ui.Prop rather than a generated builder, and that is deliberate
// rather than the shortcut the rule warns about.** The prop is specced in
// `contracts` in the same change that added this, as a `Player` prop with
// `SubtitleTracks` sugar over it. What is missing is only the *tag*: the
// Platform requires `contracts` at a published version with no replace, and tag
// pushes to the organisation have been refused since before this work began. So
// the builder exists in the spec and not yet in the version this compiles
// against.
//
// That is the narrow case the rule allows — "a type the spec does not cover yet,
// and then add it to the spec" — and it is safe here for the reason the rule
// exists: this prop is not a string nobody renders. `@mosaic-media/sdui-react`
// draws it. **Swap this for `ui.SubtitleTracks` on the contracts bump**; the
// roadmap carries that as an open thread rather than leaving it to be noticed.
func subtitleTrackProps(tracks []SubtitleTrack) []any {
	out := make([]any, 0, len(tracks))
	for _, t := range tracks {
		track := map[string]any{"src": t.Src, "format": t.Format}
		if t.Language != "" {
			track["language"] = t.Language
		}
		if t.Label != "" {
			track["label"] = t.Label
		}
		if t.Default {
			track["default"] = true
		}
		out = append(out, track)
	}
	return out
}
