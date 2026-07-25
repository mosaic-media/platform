// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// fanOut runs fn concurrently over items and returns the flattened results in
// input order. It is the read-side's provider scatter-gather: a query fans out
// to every registered provider, and each provider call is an independent remote
// round-trip, so running them concurrently turns a sum of latencies into a max.
//
// fn returns an item's results and an optional fatal error. Returning a nil
// error contributes the results (possibly empty — a provider that is merely
// unreachable is skipped by returning nil, nil). The first fatal error aborts
// the gather: it cancels the derived context so in-flight siblings unwind, and
// is returned with no results. This is the concurrent form of the serial code's
// two error paths — skip a downed provider, but fail the query on a settings
// read that errors.
//
// Ordering is deterministic regardless of completion order: each item writes
// only its own result slot, and the slots are concatenated in input order. The
// Service methods fn calls already serve concurrent HTTP requests, so invoking
// them across goroutines here adds no new safety obligation.
func fanOut[T, R any](ctx context.Context, items []T, fn func(context.Context, T) ([]R, error)) ([]R, error) {
	if len(items) == 0 {
		return nil, nil
	}
	perItem := make([][]R, len(items))
	g, ctx := errgroup.WithContext(ctx)
	for i, item := range items {
		g.Go(func() error {
			res, err := fn(ctx, item)
			if err != nil {
				return err
			}
			perItem[i] = res
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var out []R
	for _, r := range perItem {
		out = append(out, r...)
	}
	return out, nil
}

// fanOutPreferred is fanOut in two tiers: it gathers the ordinary providers
// first and consults the fallback ones only when those produced nothing at all.
//
// It exists for the browse roles, where a union is the wrong operation. Search
// unions honestly — more places looked is more found, and a duplicate is a
// ranking problem. A *catalog* list is a set of named rows on a home screen, and
// unioning two general metadata sources yields two rows of the same films under
// two names, which reads as a bug rather than as breadth. So the browse roles
// pick a source; only the absence of one falls through to the floor.
//
// "Produced nothing" rather than "errored" is deliberate, and it is the case
// that matters: a TMDB with no API key does not fail, it answers emptily. A
// fallback keyed on errors would never have fired on the install that needs it
// most — which is the whole population ADR 0072's guarantee clause is for.
//
// A fatal error in the first tier aborts without consulting the second: the
// query failed, and the floor is for a source that had nothing to say, not for
// one whose settings could not be read.
func fanOutPreferred[T, R any](
	ctx context.Context,
	items []T,
	isFallback func(T) bool,
	fn func(context.Context, T) ([]R, error),
) ([]R, error) {
	preferred := make([]T, 0, len(items))
	fallback := make([]T, 0, len(items))
	for _, it := range items {
		if isFallback(it) {
			fallback = append(fallback, it)
		} else {
			preferred = append(preferred, it)
		}
	}
	out, err := fanOut(ctx, preferred, fn)
	if err != nil || len(out) > 0 || len(fallback) == 0 {
		return out, err
	}
	return fanOut(ctx, fallback, fn)
}
