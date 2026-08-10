// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Cache-first browse reads (platform#30).
//
// Every source-backed screen used to be rendered by calling an aggregator live,
// and that call takes seconds when it works — measured at 9.6s cold and 2.6s
// warm for one search. The screen had no other way to be right, so a slow or
// absent source meant a slow or wrong screen, every time. Worse, the emit-side
// kept only what succeeded, so when every cold call failed after a restart a
// full library rendered "Nothing to show yet — try adding an addon in Settings"
// — a transient failure presented as a configuration mistake.
//
// These reads serve the last good answer and revalidate around it. They are
// **stale-while-revalidate, not optimistic UI**: nothing here predicts anything.
// Optimistic rendering shows a predicted *write* outcome before the server
// confirms it; this serves the last known-good *read*.

// browseFreshFor is how long a stored answer is served without revalidating.
//
// It is not a correctness bound — a snapshot older than this is still served
// immediately, it just triggers a background refresh — so it trades how quickly
// a new title appears against how often a quiet home screen re-renders itself
// under a viewer. A catalog is a ranking that moves hourly at most, and a
// revalidation costs a full provider round trip plus a pushed region, so
// re-running one per navigation would spend the whole cost this exists to avoid.
//
// A constant rather than configuration: nothing has asked to tune it, and a
// config field is a promise to keep a reload class correct forever.
const browseFreshFor = 5 * time.Minute

// AnswerSource says where a browse answer came from. A cached answer and a live
// one must be distinguishable, or nobody looking at telemetry can tell a stale
// screen from a working one.
type AnswerSource string

const (
	// AnswerLive is an answer the source gave during this read.
	AnswerLive AnswerSource = "live"
	// AnswerSnapshot is the last good answer, served from storage.
	AnswerSnapshot AnswerSource = "snapshot"
)

// BrowseAnswer is the provenance of one cache-first read: where the answer came
// from, how old it is, and whether the source is currently answering at all.
//
// It travels back to the emit-side because the screen has to be able to say so.
// A two-day-old home screen beats an empty one, but only if nobody is being told
// it is live.
type BrowseAnswer struct {
	// From is live or snapshot.
	From AnswerSource
	// TakenAt is when the source gave this answer — its own moment for a live
	// read, the stored one for a snapshot.
	TakenAt time.Time
	// Stale reports that the snapshot served is past its freshness window and
	// should be revalidated. False for a live answer and for a snapshot young
	// enough to serve as it is.
	Stale bool
	// Failed names the sources that did not answer this read. Non-empty means
	// what was served is either a snapshot or nothing at all — never a live
	// answer from the named source.
	Failed []string
}

// Age is how old this answer is, at now.
func (a BrowseAnswer) Age(now time.Time) time.Duration {
	if a.TakenAt.IsZero() {
		return 0
	}
	return now.Sub(a.TakenAt)
}

// snapshotCatalogsKey is the question "what catalogs do you have". The empty
// key is deliberate: a module has exactly one catalog list, so there is nothing
// to distinguish, and spelling it "catalogs" would suggest there could be
// another.
const snapshotCatalogsKey = ""

// snapshotItemsKey is the question "what is in this catalog", in the source's
// own spelling. Both halves are needed: a native type and a catalog id are only
// unique together, which is why the provider takes both.
func snapshotItemsKey(nativeType, catalogID string) string {
	return "items:" + nativeType + ":" + catalogID
}

// BrowseCatalogsQuery lists the catalogs the enabled modules expose, cache-first.
type BrowseCatalogsQuery struct {
	Caller v1.Caller
	// Refresh skips the snapshot and asks the sources, writing what they say.
	// It is what a background revalidation sets, and it is the only way a stored
	// answer is ever replaced.
	Refresh bool
}

// BrowseCatalogsResult is the catalog list and where it came from.
type BrowseCatalogsResult struct {
	Catalogs []ModuleCatalog
	Answer   BrowseAnswer
}

// BrowseCatalogs is ListModuleCatalogs with a durable snapshot in front of it.
//
// The catalog *list* is snapshotted as well as its contents, and it has to be:
// an addon's catalogs come out of a manifest fetched over the network, so a cold
// or unreachable addon has no catalogs at all — and a home screen with no
// catalogs renders "add an addon in Settings" to an install that has one.
func (s *Service) BrowseCatalogs(ctx context.Context, q BrowseCatalogsQuery) (BrowseCatalogsResult, error) {
	if q.Caller.Session == "" {
		return BrowseCatalogsResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return BrowseCatalogsResult{}, err
	}
	if s.capabilities == nil {
		return BrowseCatalogsResult{}, nil
	}

	// The same two tiers ListModuleCatalogs reads in, and for the same reason: a
	// catalog list is a set of named rows on a home screen, so unioning two
	// general metadata sources yields two rows of the same films under two
	// names. The floor is consulted only when the preferred sources produced
	// nothing at all — and with a snapshot in front of them, a source that is
	// merely *down* now produces its last answer rather than nothing, which is
	// exactly the case this whole read exists for.
	preferred, fallback := splitFallback(s.capabilities.CatalogProviders())
	out, err := s.browseCatalogTier(ctx, q, preferred)
	if err != nil {
		return BrowseCatalogsResult{}, err
	}
	if len(out.Catalogs) > 0 || len(fallback) == 0 {
		return out, nil
	}
	return s.browseCatalogTier(ctx, q, fallback)
}

// splitFallback separates the ordinary catalog providers from the floor.
func splitFallback(entries []CatalogProviderEntry) (preferred, fallback []CatalogProviderEntry) {
	for _, e := range entries {
		if e.Fallback {
			fallback = append(fallback, e)
		} else {
			preferred = append(preferred, e)
		}
	}
	return preferred, fallback
}

// browseCatalogTier reads one tier of providers concurrently, cache-first, and
// folds their answers together.
//
// Concurrently because each provider is an independent remote round trip and a
// serial gather is a sum of latencies rather than a max — the same reason the
// fan-out beside it is concurrent. Per provider rather than through fanOut
// because this needs each source's *provenance* as well as its rows, and a
// gather that concatenates cannot say which source was the stale one.
func (s *Service) browseCatalogTier(ctx context.Context, q BrowseCatalogsQuery, entries []CatalogProviderEntry) (BrowseCatalogsResult, error) {
	type tierResult struct {
		catalogs []ModuleCatalog
		answer   BrowseAnswer
		fatal    error
	}
	results := make([]tierResult, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The settings read is separated from the provider call because the
			// two failures are not alike: a provider that will not answer is
			// skipped, and a settings read that fails means storage is broken and
			// must fail the query rather than quietly emptying a screen.
			settings, err := s.readModuleSettings(ctx, e.ModuleID)
			if err != nil {
				results[i].fatal = err
				return
			}
			cats, answer := cacheFirst(ctx, s, e.ModuleID, snapshotCatalogsKey, q.Refresh,
				func(ctx context.Context) ([]v1.Catalog, error) {
					resp, err := e.Provider.Catalogs(ctx, v1.CatalogsRequest{Caller: q.Caller, Settings: settings})
					if err != nil {
						return nil, err
					}
					return resp.Catalogs, nil
				},
				func() []v1.Catalog { return nil })
			out := make([]ModuleCatalog, 0, len(cats))
			for _, c := range cats {
				out = append(out, ModuleCatalog{ModuleID: e.ModuleID, Catalog: c})
			}
			results[i] = tierResult{catalogs: out, answer: answer}
		}()
	}
	wg.Wait()

	out := BrowseCatalogsResult{Answer: BrowseAnswer{From: AnswerLive}}
	for _, r := range results {
		if r.fatal != nil {
			return BrowseCatalogsResult{}, r.fatal
		}
		out.Catalogs = append(out.Catalogs, r.catalogs...)
		out.Answer = mergeAnswer(out.Answer, r.answer)
	}
	return out, nil
}

// BrowseCatalogItemsQuery reads one catalog's first page, cache-first.
//
// Deliberately no Skip and no Filters. A snapshot exists to make the screen a
// session lands on survive a failing source, and that screen shows the top of an
// unfiltered catalog; snapshotting every page of every narrowing would be
// storage and staleness for pages nobody is waiting on. A drill-down asks live
// and says so when it fails.
type BrowseCatalogItemsQuery struct {
	Caller     v1.Caller
	ModuleID   string
	CatalogID  string
	NativeType string
	Refresh    bool
}

// BrowseCatalogItemsResult is one page of items and where it came from.
type BrowseCatalogItemsResult struct {
	Items  []v1.CatalogItem
	Answer BrowseAnswer
}

// BrowseCatalogItems is ListCatalogItems with a durable snapshot in front of it.
func (s *Service) BrowseCatalogItems(ctx context.Context, q BrowseCatalogItemsQuery) (BrowseCatalogItemsResult, error) {
	if q.Caller.Session == "" {
		return BrowseCatalogItemsResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.ModuleID == "" || q.CatalogID == "" {
		return BrowseCatalogItemsResult{}, contracts.NewError(contracts.InvalidArgument, "module id and catalog id are required")
	}
	az, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"})
	if err != nil {
		return BrowseCatalogItemsResult{}, err
	}
	provider, ok := s.capabilityCatalogProvider(q.ModuleID)
	if !ok {
		return BrowseCatalogItemsResult{}, contracts.NewError(contracts.NotFound, "no catalog provider registered under id "+q.ModuleID)
	}

	read := func(ctx context.Context) ([]v1.CatalogItem, error) {
		settings, err := s.readModuleSettings(ctx, q.ModuleID)
		if err != nil {
			return nil, err
		}
		resp, err := provider.CatalogItems(ctx, v1.CatalogItemsRequest{
			Caller: q.Caller, Settings: settings, CatalogID: q.CatalogID, NativeType: q.NativeType,
		})
		if err != nil {
			return nil, err
		}
		return resp.Items, nil
	}

	items, answer := cacheFirst(ctx, s, q.ModuleID, snapshotItemsKey(q.NativeType, q.CatalogID), q.Refresh,
		read, func() []v1.CatalogItem { return nil })

	// **In-library is re-derived, never restored.** It is a fact about this
	// install's own graph rather than about the source's answer, so a snapshot
	// taken before a title was added would otherwise draw it as absent — and the
	// read is local, so it costs nothing to be right about.
	for i := range items {
		items[i].InLibrary, items[i].NodeID = s.resolveInLibrary(ctx, az, items[i].Ref)
	}
	return BrowseCatalogItemsResult{Items: items, Answer: answer}, nil
}

// cacheFirst is the shape both browse reads share: serve the stored answer when
// there is one and it is not being deliberately refreshed, ask the source when
// there is not, and fall back to the stored answer when the source refuses.
//
// The generic parameter is the answer's element type, because the two questions
// return different things and the *policy* is identical. It stays in this
// package rather than becoming a general utility: the policy is specific to
// browsing a source, not to caching.
func cacheFirst[T any](
	ctx context.Context,
	s *Service,
	source, key string,
	refresh bool,
	read func(context.Context) ([]T, error),
	empty func() []T,
) ([]T, BrowseAnswer) {
	now := s.clock.Now()
	lg := telemetry.From(ctx).For("browse").With(
		telemetry.String("source", source), telemetry.String("key", key))

	// The stored answer, read first so it is in hand for both the serve path and
	// the fallback. A store-less Service (a test, a build with no snapshots)
	// simply always goes live, which is what every build did before this.
	var stored []T
	var storedAt time.Time
	if s.snapshots != nil {
		if snap, err := s.snapshots.Get(ctx, source, key); err == nil {
			if err := json.Unmarshal(snap.Document, &stored); err == nil {
				storedAt = snap.TakenAt
			} else {
				// A document this build can no longer read is a cache degrading,
				// not data lost: the live read below replaces it.
				lg.Warn("stored snapshot could not be decoded; asking the source",
					telemetry.Err(err))
				stored = nil
			}
		}
	}

	if !refresh && !storedAt.IsZero() {
		age := now.Sub(storedAt)
		stale := age >= browseFreshFor
		lg.Info("browse answered from a snapshot",
			telemetry.String("answer", string(AnswerSnapshot)),
			telemetry.Duration("age", age.Round(time.Second)),
			telemetry.Bool("stale", stale),
			telemetry.Int("items", len(stored)))
		return stored, BrowseAnswer{From: AnswerSnapshot, TakenAt: storedAt, Stale: stale}
	}

	started := s.clock.Now()
	items, err := read(ctx)
	if err != nil {
		// **The failure is counted and named, never discarded.** Dropping it is
		// what made a downed source indistinguishable from an empty library:
		// "no results" and "the source failed" are different states and only one
		// of them is the user's to act on.
		lg.Warn("source did not answer a browse read",
			telemetry.Err(err), telemetry.Bool("served_snapshot", !storedAt.IsZero()))
		if !storedAt.IsZero() {
			return stored, BrowseAnswer{
				From: AnswerSnapshot, TakenAt: storedAt, Failed: []string{source},
			}
		}
		return empty(), BrowseAnswer{From: AnswerLive, TakenAt: now, Failed: []string{source}}
	}

	lg.Info("browse answered live",
		telemetry.String("answer", string(AnswerLive)),
		telemetry.Duration("elapsed", s.clock.Now().Sub(started).Round(time.Millisecond)),
		telemetry.Int("items", len(items)))

	// Store what the source said, before anything renders from it. An empty
	// answer is stored like any other — a catalog that has genuinely emptied
	// must be able to say so, and skipping it would freeze the last non-empty
	// answer forever.
	if s.snapshots != nil {
		if document, err := json.Marshal(items); err != nil {
			lg.Warn("browse answer could not be encoded for the snapshot", telemetry.Err(err))
		} else if err := s.snapshots.Put(ctx, contracts.SourceSnapshot{
			Source: source, Key: key, Document: document, TakenAt: now,
		}); err != nil {
			// A snapshot that could not be written costs the *next* render, not
			// this one, so it is reported and not returned.
			lg.Warn("browse answer could not be stored", telemetry.Err(err))
		}
	}
	return items, BrowseAnswer{From: AnswerLive, TakenAt: now}
}

// mergeAnswer folds one source's provenance into the answer for a screen built
// from several.
//
// A screen is as stale as its stalest part and as failed as its worst part: one
// source served from a snapshot makes the whole screen a snapshot, because the
// viewer cannot see which row came from where and being told half of it is live
// is the same lie as being told all of it is.
func mergeAnswer(into, next BrowseAnswer) BrowseAnswer {
	if next.From == AnswerSnapshot {
		into.From = AnswerSnapshot
		if into.TakenAt.IsZero() || (!next.TakenAt.IsZero() && next.TakenAt.Before(into.TakenAt)) {
			into.TakenAt = next.TakenAt
		}
	}
	into.Stale = into.Stale || next.Stale
	for _, f := range next.Failed {
		if !containsString(into.Failed, f) {
			into.Failed = append(into.Failed, f)
		}
	}
	return into
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
