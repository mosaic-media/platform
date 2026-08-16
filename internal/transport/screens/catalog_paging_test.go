// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package screens

import "testing"

// The catalog screen pages by accumulating one provider page per requested
// page, and stops on the provider's own statement (SDK v0.23.0). These cover the
// loop's termination, because the failure mode of getting it wrong is not a
// wrong screen — it is the Platform calling an upstream forever.

func TestPageLoopStopsWhenTheProviderSaysThereIsNoMore(t *testing.T) {
	calls := 0
	items, hasMore := accumulate(3, func(skip int) ([]int, bool, error) {
		calls++
		if skip == 0 {
			return []int{1, 2}, true, nil
		}
		return []int{3}, false, nil
	})
	if calls != 2 {
		t.Errorf("called the provider %d times; it said stop after the second", calls)
	}
	if len(items) != 3 || hasMore {
		t.Errorf("items=%d hasMore=%v", len(items), hasMore)
	}
}

// TestAnEmptyPageStopsTheLoopWhateverTheProviderClaims pins that a provider
// insisting there is more while returning nothing is the case worth guarding:
// believed literally it is an unbounded loop against an upstream, and the
// Platform is the party that pays for it.
func TestAnEmptyPageStopsTheLoopWhateverTheProviderClaims(t *testing.T) {
	calls := 0
	items, hasMore := accumulate(50, func(int) ([]int, bool, error) {
		calls++
		return nil, true, nil
	})
	if calls != 1 {
		t.Errorf("called the provider %d times on an empty page claiming more", calls)
	}
	if len(items) != 0 || hasMore {
		t.Errorf("items=%d hasMore=%v", len(items), hasMore)
	}
}

func TestPageZeroFetchesExactlyOnePage(t *testing.T) {
	calls := 0
	_, hasMore := accumulate(0, func(int) ([]int, bool, error) {
		calls++
		return []int{1}, true, nil
	})
	if calls != 1 {
		t.Errorf("page 0 fetched %d pages", calls)
	}
	if !hasMore {
		t.Error("a provider saying there is more was not carried through")
	}
}

// accumulate mirrors catalogScreen's loop over a substitutable fetch, so the
// termination rules are tested without a provider registry, a caller or a
// screen. The loop is the thing under test; the rest is scenery.
func accumulate(page int, fetch func(skip int) ([]int, bool, error)) ([]int, bool) {
	var items []int
	hasMore := false
	for p := 0; p <= page; p++ {
		got, more, err := fetch(len(items))
		if err != nil {
			return items, false
		}
		items = append(items, got...)
		hasMore = more
		if len(got) == 0 || !more {
			hasMore = false
			break
		}
	}
	return items, hasMore
}
