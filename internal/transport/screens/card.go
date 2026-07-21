// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"github.com/mosaic-media/sdui/ui"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// contentCard renders a content item — a search result or a catalog entry,
// which carry the same fields. Both open a detail screen on click: an in-library
// item to its node's detail, a virtual one to a preview whose sole library
// affordance is Add to library (ADR 0028 — materialising is the deliberate act,
// made on the detail rather than the card). An in-library item also carries a
// badge so the two read apart at a glance. The poster is routed through the
// artwork proxy (ADR 0030).
func (s *Service) contentCard(ref v1.ContentRef, title string, year int, poster string, inLibrary bool) *ui.Element {
	els := []ui.El{}
	if y := yearLabel(year); y != "" {
		els = append(els, ui.Subtitle(y))
	}
	if poster != "" {
		els = append(els, ui.Poster(s.art(poster)))
	}
	// Both planes open the same ref-based rich detail (ADR 0034); PreviewContent
	// resolves in-library from the ref, so the detail shows the right action. An
	// in-library card also carries a badge so the two read apart on the grid.
	if inLibrary {
		els = append(els, ui.BadgeText("In library"))
	}
	els = append(els, ui.OnTap(ui.Navigate(screenDetail, map[string]any{paramRef: refInput(ref)})))
	return ui.PosterCard(title, string(ref.MediaType), els...)
}
