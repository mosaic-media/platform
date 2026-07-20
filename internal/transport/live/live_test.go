// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package live

import "testing"

// safeName guards a client-supplied mutation name that is interpolated into a
// GraphQL query, so it must reject anything that is not a plain identifier.
func TestSafeName(t *testing.T) {
	valid := []string{"importContent", "a", "a1", "snake_case", "camelCase9"}
	for _, s := range valid {
		if !safeName(s) {
			t.Errorf("safeName(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "1leading", "has space", "paren()", "brace{}", "dash-name", "dot.name", "quote\"", "semi;drop"}
	for _, s := range invalid {
		if safeName(s) {
			t.Errorf("safeName(%q) = true, want false (injection risk)", s)
		}
	}
}
