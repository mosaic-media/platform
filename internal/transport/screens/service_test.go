// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// fakeQueries stands in for the application query surface, so the screen
// builders are tested without a full Service. The real query surface
// (*app.Service) serves concurrent requests, and homeScreen fans its catalog
// reads out across goroutines, so the fake guards its captured-arg fields with a
// mutex — as any concurrency-safe stand-in must.
type fakeQueries struct {
	playbackState v1.PlaybackState
	playablePart  v1.Part
	partsByNode   map[v1.NodeID][]v1.Part

	currentUser domain.User
	users       []domain.User
	rolesByUser map[domain.UserID][]domain.Role

	results  []v1.SearchResult
	catalogs []app.ModuleCatalog
	items    []v1.CatalogItem
	// The provenance the cache-first browse reads report back (ADR 0052), and
	// whether the render asked for a refresh. Zero values are a live answer,
	// which is what every screen test that does not care about staleness wants.
	catalogAnswer app.BrowseAnswer
	itemAnswer    app.BrowseAnswer
	catalogsErr   error
	gotRefresh    bool
	// compositions is how each viewer arranged their home (ADR 0103), keyed by
	// the session the caller presents.
	compositions map[string]app.HomeComposition
	languages    map[string][]byte
	// sources is the candidate set behind a node (ADR 0116), keyed by node id.
	// An absent entry is the no-candidate state, which is a legitimate answer
	// and not a failure — sourcesErr is how a test asks for the failure.
	sources    map[string][]app.PlaybackSource
	sourcesErr error

	node             v1.Node
	children         []v1.Node
	previewMeta      v1.ContentMetadata
	previewInLibrary bool
	previewNodeID    v1.NodeID
	settingsUI       []byte
	// settingsModules is what ListSettingsModules reports — the modules the
	// settings index offers a way into.
	settingsModules []app.SettingsModule

	// installedExtensions and availableExtensions back the extensions screen;
	// availableErr lets a test drive the repository-unreachable path.
	installedExtensions []app.InstalledExtension
	availableExtensions []app.ExtensionCatalogueEntry
	availableErr        error

	// inProgress is what ListInProgress reports — the continue-watching rail's
	// input. playbackStates is what ListPlaybackStates reports for the watched
	// marks; the fake returns the whole map and lets the caller pick the ids.
	inProgress     []v1.InProgressItem
	playbackStates map[v1.NodeID]v1.PlaybackState
	// watchHistory is what ListWatchHistory reports — the history screen's
	// input (ADR 0103).
	watchHistory []app.WatchedItem

	// The library and its rules (ADR 0104, roadmap M2.1–2.2). libraryWorks is
	// the whole library the fake pages over — not one page — so the screen's
	// paging is under test rather than assumed.
	libraryWorks []v1.Node
	// libraryTotal overrides the total the fake reports, so the one branch a
	// window cannot otherwise reach is testable: a total that is real and a
	// window that came back empty, which is a link into a library that has
	// since shrunk.
	libraryTotal int
	libraryErr   error
	libraryRules []app.LibraryRuleListing
	// The stored detail (ADR 0107): the work's tree, and the document a provider
	// last answered with.
	libraryChildren   []v1.Node
	libraryEpisodes   map[int][]v1.Node
	storedMetadata    v1.ContentMetadata
	hasStoredMetadata bool
	libraryDetailErr  error
	// previewCalls counts provider metadata reads, so "a library detail makes
	// none" is an assertion rather than a claim.
	previewCalls int
	preview      app.PreviewLibraryRuleResult
	previewErr   error
	// childrenByNode, when set for a node id, is what GetContentNode returns as
	// that node's children — so a tree walk (series → seasons → episodes) can be
	// exercised. Absent an entry, GetContentNode falls back to the flat children.
	childrenByNode map[v1.NodeID][]v1.Node

	// The configuration reads (ADR 0011, roadmap M4.4).
	//
	// activeConfigErr defaults to nil and the zero result stands for "a version
	// is active and carries nothing", which is not the state a fresh install is
	// in — a test that wants that sets activeConfigErr to a NotFound, and the
	// panel is expected to render defaults rather than fail.
	activeConfig    app.GetActiveConfigVersionResult
	activeConfigErr error
	pendingConfig   app.GetPendingConfigVersionResult
	issues          []domain.Issue
	issuesErr       error
	// The three effective-settings readers. Zero values would render as "0
	// days", which is never what the Platform applies, so the fake answers the
	// same defaults the real readers fall back to unless a test overrides them.
	retention    *app.TelemetryRetention
	maintenance  *app.LibraryMaintenanceSettings
	availability *app.AvailabilitySettings

	// canReadTelemetry is what CallerCan reports — the fake's way of saying
	// "this caller holds telemetry.read", which is what decides whether the
	// expert-mode affordance is drawn at all.
	canReadTelemetry bool
	// canReadJobs is the same for job.read (ADR 0017). Separate from the one
	// above because the two are separate grants and the nav draws them
	// separately — a fake that answered one bool for every action could not
	// tell the difference between "sees both" and "sees the one it holds".
	canReadJobs bool
	// allow is a per-action override for CallerCan, for tests that need more
	// than one answer at a time.
	allow        map[string]bool
	expertModeOn bool
	logs         []domain.TelemetryLogRecord
	traces       []domain.TelemetryTraceSummary
	spans        []domain.TelemetrySpanRecord

	// userSessions and currentSession back the Devices section (ADR 0102).
	userSessions   []domain.Session
	currentSession domain.SessionID

	// jobs, jobAttempts and jobLogs back the background-work screens.
	jobs        []domain.Job
	jobAttempts []domain.JobAttempt
	jobLogs     []domain.JobLog
	jobErr      error

	mu                  sync.Mutex
	gotText             string
	gotSearchMediaType  v1.MediaType
	gotCatalogID        string
	gotNodeID           v1.NodeID
	gotPreviewRef       v1.ContentRef
	gotSettingsModuleID string
	gotLogFilter        domain.TelemetryLogFilter
	gotTraceFilter      domain.TelemetryTraceFilter
	metrics             []telemetry.MetricSeries
	gotTraceID          string
	gotJobFilter        domain.JobFilter
	gotJobID            domain.JobID
}

// currentUser, users and rolesByUser back the Account and People panels.
func (f *fakeQueries) GetCurrentUser(context.Context, app.GetCurrentUserQuery) (app.GetCurrentUserResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.GetCurrentUserResult{User: f.currentUser}, nil
}

func (f *fakeQueries) ListUsers(context.Context, app.ListUsersQuery) (app.ListUsersResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.ListUsersResult{Users: f.users}, nil
}

func (f *fakeQueries) GetRolesForUser(_ context.Context, q app.GetRolesForUserQuery) (app.GetRolesForUserResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.GetRolesForUserResult{Roles: f.rolesByUser[q.TargetUserID]}, nil
}

func (f *fakeQueries) GetUserByID(_ context.Context, q app.GetUserByIDQuery) (app.GetUserByIDResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.ID == q.UserID {
			return app.GetUserByIDResult{User: u}, nil
		}
	}
	return app.GetUserByIDResult{}, contracts.NewError(contracts.NotFound, "no such user")
}

func (f *fakeQueries) GetEffectivePermissions(_ context.Context, q app.GetEffectivePermissionsQuery) (app.GetEffectivePermissionsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Permission
	for _, r := range f.rolesByUser[q.TargetUserID] {
		out = append(out, r.Permissions...)
	}
	return app.GetEffectivePermissionsResult{Permissions: out}, nil
}

func (f *fakeQueries) GrantablePermissions(_ context.Context, q app.GrantablePermissionsQuery) (app.GrantablePermissionsResult, error) {
	preset, ok := app.Preset(q.Preset)
	if !ok {
		return app.GrantablePermissionsResult{}, contracts.NewError(contracts.InvalidArgument, "no such preset")
	}
	return app.GrantablePermissionsResult{Available: preset, Selected: preset}, nil
}

func (f *fakeQueries) ListWatchHistory(context.Context, app.ListWatchHistoryQuery) (app.ListWatchHistoryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.ListWatchHistoryResult{Items: f.watchHistory}, nil
}

func (f *fakeQueries) ExpertModeEnabled(context.Context, v1.Caller) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.expertModeOn
}

// HomeCompositionFor answers per caller, because the whole point of ADR 0103 is
// that two viewers of one install get two different answers — a fake returning
// one composition for everybody would let a screen that ignored the caller pass.
func (f *fakeQueries) HomeCompositionFor(_ context.Context, caller v1.Caller) app.HomeComposition {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.compositions[caller.Session]
}

// LanguagePreferenceFor answers with whatever a test stored, and nil — the
// unset document — is a valid answer that reads back as the default.
func (f *fakeQueries) LanguagePreferenceFor(_ context.Context, caller v1.Caller) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.languages[caller.Session]
}

func (f *fakeQueries) CallerCan(_ context.Context, _ v1.Caller, action policy.Action, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	// An explicit allow-list wins where a test set one. It exists for the People
	// panels, which ask about five different actions and need to tell them
	// apart — a fake answering one bool for every action could not distinguish
	// "may read the user list" from "may create one", which is exactly the
	// distinction those panels draw.
	if f.allow != nil {
		if v, ok := f.allow[string(action)]; ok {
			return v
		}
	}
	switch action {
	case app.ActionJobRead:
		return f.canReadJobs
	case app.ActionTelemetryRead:
		return f.canReadTelemetry
	default:
		// Every other affordance the nav asks about — user.read, module.read —
		// keeps the old blanket answer, so tests written before the split are
		// unaffected by it.
		return f.canReadTelemetry
	}
}

func (f *fakeQueries) ListSessions(_ context.Context, _ app.ListSessionsQuery) (app.ListSessionsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.ListSessionsResult{Sessions: f.userSessions, Current: f.currentSession}, nil
}

func (f *fakeQueries) GetActiveConfigVersion(_ context.Context, _ app.GetActiveConfigVersionQuery) (app.GetActiveConfigVersionResult, error) {
	return f.activeConfig, f.activeConfigErr
}

func (f *fakeQueries) GetPendingConfigVersion(_ context.Context, _ app.GetPendingConfigVersionQuery) (app.GetPendingConfigVersionResult, error) {
	return f.pendingConfig, nil
}

// The resolution register (ADR 0119). issues is what the Problems panel draws;
// issuesErr is how a test makes the read fail.
func (f *fakeQueries) ListIssues(_ context.Context, _ app.ListIssuesQuery) (app.ListIssuesResult, error) {
	return app.ListIssuesResult{Issues: f.issues}, f.issuesErr
}

func (f *fakeQueries) TelemetryRetention(_ context.Context) app.TelemetryRetention {
	if f.retention != nil {
		return *f.retention
	}
	return app.DefaultTelemetryRetention
}

func (f *fakeQueries) LibraryMaintenance(_ context.Context) app.LibraryMaintenanceSettings {
	if f.maintenance != nil {
		return *f.maintenance
	}
	return app.DefaultLibraryMaintenance
}

func (f *fakeQueries) Availability(_ context.Context) app.AvailabilitySettings {
	if f.availability != nil {
		return *f.availability
	}
	return app.DefaultAvailability
}

func (f *fakeQueries) ListJobs(_ context.Context, q app.ListJobsQuery) (app.ListJobsResult, error) {
	f.mu.Lock()
	f.gotJobFilter = q.Filter
	f.mu.Unlock()
	if f.jobErr != nil {
		return app.ListJobsResult{}, f.jobErr
	}
	if q.Filter.Status == "" {
		return app.ListJobsResult{Jobs: f.jobs}, nil
	}
	out := make([]domain.Job, 0, len(f.jobs))
	for _, j := range f.jobs {
		if j.Status == q.Filter.Status {
			out = append(out, j)
		}
	}
	return app.ListJobsResult{Jobs: out}, nil
}

func (f *fakeQueries) GetJob(_ context.Context, q app.GetJobQuery) (app.GetJobResult, error) {
	f.mu.Lock()
	f.gotJobID = q.JobID
	f.mu.Unlock()
	if f.jobErr != nil {
		return app.GetJobResult{}, f.jobErr
	}
	for _, j := range f.jobs {
		if j.ID == q.JobID {
			return app.GetJobResult{Job: j, Attempts: f.jobAttempts, Logs: f.jobLogs}, nil
		}
	}
	return app.GetJobResult{}, contracts.NewError(contracts.NotFound, "job not found")
}

func (f *fakeQueries) QueryTelemetryLogs(_ context.Context, q app.QueryTelemetryLogsQuery) (app.QueryTelemetryLogsResult, error) {
	f.mu.Lock()
	f.gotLogFilter = q.Filter
	f.mu.Unlock()
	return app.QueryTelemetryLogsResult{Records: f.logs}, nil
}

func (f *fakeQueries) ListMetrics(_ context.Context, _ app.ListMetricsQuery) (app.ListMetricsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.ListMetricsResult{Series: f.metrics}, nil
}

func (f *fakeQueries) ListTraces(_ context.Context, q app.ListTracesQuery) (app.ListTracesResult, error) {
	f.mu.Lock()
	f.gotTraceFilter = q.Filter
	f.mu.Unlock()
	return app.ListTracesResult{Traces: f.traces}, nil
}

func (f *fakeQueries) GetTrace(_ context.Context, q app.GetTraceQuery) (app.GetTraceResult, error) {
	f.mu.Lock()
	f.gotTraceID = q.TraceID
	f.mu.Unlock()
	return app.GetTraceResult{Spans: f.spans, Logs: f.logs}, nil
}

func (f *fakeQueries) SearchAvailableContent(_ context.Context, q app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error) {
	f.mu.Lock()
	f.gotText = q.Text
	f.gotSearchMediaType = q.MediaType
	f.mu.Unlock()
	// The fake filters as the providers would, so a test that asserts a focused
	// page cannot pass on results the query said it did not want.
	if q.MediaType == "" {
		return app.SearchAvailableContentResult{Results: f.results}, nil
	}
	out := make([]v1.SearchResult, 0, len(f.results))
	for _, r := range f.results {
		if r.Ref.MediaType == q.MediaType {
			out = append(out, r)
		}
	}
	return app.SearchAvailableContentResult{Results: out}, nil
}

func (f *fakeQueries) ListModuleCatalogs(_ context.Context, _ app.ListModuleCatalogsQuery) (app.ListModuleCatalogsResult, error) {
	return app.ListModuleCatalogsResult{Catalogs: f.catalogs}, nil
}

// The cache-first browse reads (ADR 0052). The fake answers live by default and
// carries the provenance a test sets, so a home-screen test can drive the stale
// and failed-source paths without a snapshot store behind it.
func (f *fakeQueries) BrowseCatalogs(_ context.Context, q app.BrowseCatalogsQuery) (app.BrowseCatalogsResult, error) {
	f.mu.Lock()
	f.gotRefresh = q.Refresh
	f.mu.Unlock()
	if f.catalogsErr != nil {
		return app.BrowseCatalogsResult{}, f.catalogsErr
	}
	return app.BrowseCatalogsResult{Catalogs: f.catalogs, Answer: f.catalogAnswer}, nil
}

func (f *fakeQueries) BrowseCatalogItems(_ context.Context, q app.BrowseCatalogItemsQuery) (app.BrowseCatalogItemsResult, error) {
	f.mu.Lock()
	f.gotCatalogID = q.CatalogID
	f.mu.Unlock()
	return app.BrowseCatalogItemsResult{Items: f.items, Answer: f.itemAnswer}, nil
}

// ListLibrary pages the seeded works the way the real service does, so a screen
// test exercises the paging arithmetic rather than a fake that hands back
// everything and lets the assertions agree with it.
func (f *fakeQueries) ListLibrary(_ context.Context, q app.ListLibraryQuery) (app.ListLibraryResult, error) {
	if f.libraryErr != nil {
		return app.ListLibraryResult{}, f.libraryErr
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 60
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	works := f.libraryWorks
	if len(q.Genres) > 0 {
		narrowed := make([]v1.Node, 0, len(works))
		for _, w := range works {
			if slices.Contains(w.Genres, q.Genres[0]) {
				narrowed = append(narrowed, w)
			}
		}
		works = narrowed
	}
	if offset >= len(works) {
		works = nil
	} else {
		works = works[offset:]
		if len(works) > limit {
			works = works[:limit]
		}
	}
	total := f.libraryTotal
	if total == 0 {
		total = len(f.libraryWorks)
	}
	// The facet row is built from the seeded works and, like the real store,
	// ignores the query's own genre narrowing so pressing a chip does not empty
	// the row that offered it.
	counts := map[string]int{}
	for _, w := range f.libraryWorks {
		for _, g := range w.Genres {
			counts[g]++
		}
	}
	var facets contracts.Facets
	for value, count := range counts {
		facets.Genres = append(facets.Genres, contracts.FacetValue{Value: value, Count: count})
	}
	sort.SliceStable(facets.Genres, func(i, j int) bool {
		if facets.Genres[i].Count != facets.Genres[j].Count {
			return facets.Genres[i].Count > facets.Genres[j].Count
		}
		return facets.Genres[i].Value < facets.Genres[j].Value
	})

	return app.ListLibraryResult{
		Works: works, Total: total, Offset: offset, Limit: limit, Facets: facets,
	}, nil
}

func (f *fakeQueries) ListLibraryRules(_ context.Context, _ app.ListLibraryRulesQuery) (app.ListLibraryRulesResult, error) {
	return app.ListLibraryRulesResult{Rules: f.libraryRules}, nil
}

// GetLibraryDetail answers from the seeded node, its tree and whatever document
// a test stored (ADR 0107). HasMetadata is driven separately from the document
// so the "never enriched" branch — a node materialised before the store existed
// — is reachable, which is the one a fake returning a zero struct would hide.
func (f *fakeQueries) GetLibraryDetail(_ context.Context, q app.GetLibraryDetailQuery) (app.GetLibraryDetailResult, error) {
	if f.libraryDetailErr != nil {
		return app.GetLibraryDetailResult{}, f.libraryDetailErr
	}
	node := f.node
	if node.ID == "" {
		node = v1.Node{ID: q.NodeID, WorkID: q.NodeID, Kind: v1.NodeWork}
	}
	// The fake pages by season the way the real query does, so a screen test
	// exercises the per-season read rather than a whole-tree one nothing makes.
	season := app.SeasonNumbers(f.libraryChildren)
	selected := q.Season
	if selected == 0 && len(season) > 0 {
		selected = season[0]
		for _, n := range season {
			if n >= 1 {
				selected = n
				break
			}
		}
	}
	return app.GetLibraryDetailResult{
		Node: node, Children: f.libraryChildren,
		Episodes: f.libraryEpisodes[selected], Season: selected,
		Metadata: f.storedMetadata, HasMetadata: f.hasStoredMetadata,
	}, nil
}

func (f *fakeQueries) PreviewLibraryRule(_ context.Context, _ app.PreviewLibraryRuleQuery) (app.PreviewLibraryRuleResult, error) {
	if f.previewErr != nil {
		return app.PreviewLibraryRuleResult{}, f.previewErr
	}
	return f.preview, nil
}

func (f *fakeQueries) ListCatalogItems(_ context.Context, q app.ListCatalogItemsQuery) (app.ListCatalogItemsResult, error) {
	f.mu.Lock()
	f.gotCatalogID = q.CatalogID
	f.mu.Unlock()
	return app.ListCatalogItemsResult{Items: f.items}, nil
}

func (f *fakeQueries) GetContentNode(_ context.Context, q v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	f.mu.Lock()
	f.gotNodeID = q.NodeID
	f.mu.Unlock()
	if kids, ok := f.childrenByNode[q.NodeID]; ok {
		node := f.node
		node.ID = q.NodeID
		return v1.GetContentNodeResult{Node: node, Children: kids}, nil
	}
	return v1.GetContentNodeResult{Node: f.node, Children: f.children}, nil
}

// partsByNode, when set for a node id, is what ListNodeParts reports for it —
// how a test gives one episode of a season a release and leaves the rest bare.
// Absent an entry it falls back to playablePart, so the many tests that only
// care that *something* is playable need not enumerate nodes.
func (f *fakeQueries) ListNodeParts(_ context.Context, q app.ListNodePartsQuery) (app.ListNodePartsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if parts, ok := f.partsByNode[q.NodeID]; ok {
		return app.ListNodePartsResult{Parts: parts}, nil
	}
	if f.playablePart.ID != "" && f.partsByNode == nil {
		return app.ListNodePartsResult{Parts: []v1.Part{f.playablePart}}, nil
	}
	return app.ListNodePartsResult{}, nil
}

// playablePart, when set, is what FirstPlayablePart reports — the fake's way of
// saying "this library item has bytes", which is what gates the Play button.
func (f *fakeQueries) FirstPlayablePart(_ context.Context, _ v1.Caller, _ v1.NodeID) (v1.Part, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.playablePart.ID == "" {
		return v1.Part{}, false, nil
	}
	return f.playablePart, true, nil
}

// playbackState, when set, is what GetPlaybackState reports — the fake's way of
// saying "this viewer already started this", which is what turns Play into
// Resume (ADR 0046).
func (f *fakeQueries) GetPlaybackState(_ context.Context, _ v1.GetPlaybackStateQuery) (v1.GetPlaybackStateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.playbackState.Position == 0 && !f.playbackState.Finished {
		return v1.GetPlaybackStateResult{}, nil
	}
	return v1.GetPlaybackStateResult{State: f.playbackState, Found: true}, nil
}

func (f *fakeQueries) ListInProgress(_ context.Context, _ v1.ListInProgressQuery) (v1.ListInProgressResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return v1.ListInProgressResult{Items: f.inProgress}, nil
}

func (f *fakeQueries) ListPlaybackStates(_ context.Context, _ v1.ListPlaybackStatesQuery) (v1.ListPlaybackStatesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return v1.ListPlaybackStatesResult{States: f.playbackStates}, nil
}

func (f *fakeQueries) PreviewContent(_ context.Context, q app.PreviewContentQuery) (app.PreviewContentResult, error) {
	f.mu.Lock()
	f.previewCalls++
	f.mu.Unlock()
	f.mu.Lock()
	f.gotPreviewRef = q.Ref
	f.mu.Unlock()
	return app.PreviewContentResult{Metadata: f.previewMeta, InLibrary: f.previewInLibrary, NodeID: f.previewNodeID}, nil
}

func (f *fakeQueries) ModuleSettingsUI(_ context.Context, q app.ModuleSettingsUIQuery) (app.ModuleSettingsUIResult, error) {
	f.mu.Lock()
	f.gotSettingsModuleID = q.ModuleID
	f.mu.Unlock()
	return app.ModuleSettingsUIResult{ModuleID: q.ModuleID, UI: f.settingsUI}, nil
}

func (f *fakeQueries) ListSettingsModules(context.Context, app.ListSettingsModulesQuery) (app.ListSettingsModulesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.ListSettingsModulesResult{Modules: f.settingsModules}, nil
}

func (f *fakeQueries) ListInstalledExtensions(context.Context, app.ListInstalledExtensionsQuery) ([]app.InstalledExtension, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installedExtensions, nil
}

func (f *fakeQueries) ListAvailableExtensions(context.Context, app.ListAvailableExtensionsQuery) ([]app.ExtensionCatalogueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.availableExtensions, f.availableErr
}

func render(t *testing.T, svc *Service, name string, params map[string]any) sdui.Node {
	t.Helper()
	node, err := svc.Render(context.Background(), name, v1.CallerFromSession("s-1"), params)
	if err != nil {
		t.Fatalf("Render(%q): %v", name, err)
	}
	return node
}

// find walks a node tree (children and slots) for the first node of the type.
func find(n sdui.Node, typ string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == typ {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := find(c, typ); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := find(c, typ); ok {
				return got, true
			}
		}
	}
	return nil, false
}

func findAll(n sdui.Node, typ string, acc *[]sdui.Node) {
	if n == nil {
		return
	}
	if n.GetType() == typ {
		*acc = append(*acc, n)
	}
	for _, c := range n.GetChildren() {
		findAll(c, typ, acc)
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			findAll(c, typ, acc)
		}
	}
}

// prop reads a node's prop from the protobuf Struct (ADR 0044 — props is an open
// Struct, decoded to a Go map for assertions).
// findNavItem finds a settings nav row anywhere in the tree by its label. The
// rows live in the frame's `nav` slot, which is why this walks slots too.
func findNavItem(n sdui.Node, label string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == "SettingsNavItem" && prop(n, "label") == label {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := findNavItem(c, label); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := findNavItem(c, label); ok {
				return got, true
			}
		}
	}
	return nil, false
}

// findIconButton finds an IconButton anywhere in the tree by its label, which on
// an icon-only control is its accessible name rather than visible text.
func findIconButton(n sdui.Node, label string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == "IconButton" && prop(n, "label") == label {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := findIconButton(c, label); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := findIconButton(c, label); ok {
				return got, true
			}
		}
	}
	return nil, false
}

// findButton finds a Button anywhere in the tree by its label.
func findButton(n sdui.Node, label string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == sdui.TypeButton && prop(n, "label") == label {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := findButton(c, label); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := findButton(c, label); ok {
				return got, true
			}
		}
	}
	return nil, false
}

func prop(n sdui.Node, key string) any { return n.GetProps().AsMap()[key] }

// actionOf reads the action riding in a node's open props bag (JSON-in-Struct).
func actionOf(n sdui.Node) map[string]any {
	a, _ := prop(n, "action").(map[string]any)
	return a
}

// slotNodes returns the nodes of a named slot.
func slotNodes(n sdui.Node, name string) []sdui.Node { return n.GetSlots()[name].GetNodes() }

// mapAt reads a nested object out of a decoded props/action map.
func mapAt(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func TestSearchScreenEmptyQueryPromptsWithNoBackendCall(t *testing.T) {
	fake := &fakeQueries{}
	svc := &Service{content: fake}

	node := render(t, svc, "search", nil)
	if node.Type != sdui.TypeScreen {
		t.Fatalf("root type = %q, want Screen", node.Type)
	}
	// The search screen carries its OWN SearchBar (shown on mobile, where search
	// is a tab; desktop hides it and uses the top-bar search). An empty query
	// renders a prompt and hits no backend.
	if _, ok := find(node, sdui.TypeSearchBar); !ok {
		t.Fatal("search screen must carry its own SearchBar (the mobile search field)")
	}
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("an empty query must render an EmptyState prompt")
	}
	if fake.gotText != "" {
		t.Fatal("an empty query must not hit the search backend")
	}
}

func TestHomeScreenRendersHeroAndCatalogRows(t *testing.T) {
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
			{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "series", Name: "Popular Series"}},
		},
		items: []v1.CatalogItem{
			{Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}, Title: "A Movie", Year: 2020},
		},
		previewMeta: v1.ContentMetadata{Title: "A Movie", Backdrop: "http://cdn/bd.jpg", Overview: "Synopsis.", Rating: 8.0},
	}
	node := render(t, &Service{content: fake}, "home", nil)

	// A hero (from the first catalog's first item, enriched via PreviewContent).
	hero, ok := find(node, sdui.TypeHeroBanner)
	if !ok {
		t.Fatal("home screen has no hero")
	}
	if prop(hero, "title") != "A Movie" {
		t.Fatalf("hero title = %v, want the enriched item title", prop(hero, "title"))
	}
	if fake.gotPreviewRef.NativeID != "tt1" {
		t.Fatalf("hero enriched ref = %+v, want the first item", fake.gotPreviewRef)
	}
	// A titled row per catalog, each a carousel of cards.
	var sections, carousels []sdui.Node
	findAll(node, sdui.TypeSection, &sections)
	findAll(node, sdui.TypeCarousel, &carousels)
	if len(sections) != 2 || len(carousels) != 2 {
		t.Fatalf("sections=%d carousels=%d, want 2 each (one per catalog)", len(sections), len(carousels))
	}
}

func TestHomeScreenEmptyWithoutCatalogs(t *testing.T) {
	node := render(t, &Service{content: &fakeQueries{}}, "home", nil)
	empty, ok := find(node, sdui.TypeEmptyState)
	if !ok {
		t.Fatal("home with no catalogs must render an EmptyState")
	}
	// Nothing configured is *advice*: this install has no source and being
	// pointed at Settings is the one thing that fixes it.
	if msg, _ := prop(empty, "title").(string); !strings.Contains(msg, "add an addon in Settings") {
		t.Fatalf("message = %q, want the configure-an-addon prompt", msg)
	}
}

// TestHomeScreenDistinguishesADownSourceFromAnEmptyOne is the defect ADR 0052
// was written about, at the surface it was seen on. A source that did not answer
// must never render as an install with nothing configured: the first is a
// report, the second is advice, and giving the advice sends somebody to fix
// something that is not broken.
func TestHomeScreenDistinguishesADownSourceFromAnEmptyOne(t *testing.T) {
	fake := &fakeQueries{catalogAnswer: app.BrowseAnswer{From: app.AnswerLive, Failed: []string{"tmdb"}}}
	node := render(t, &Service{content: fake}, "home", nil)
	empty, ok := find(node, sdui.TypeEmptyState)
	if !ok {
		t.Fatal("home with unreachable sources must still render an EmptyState")
	}
	msg, _ := prop(empty, "title").(string)
	if strings.Contains(msg, "Settings") {
		t.Fatalf("message = %q, want no configuration advice: nothing here is misconfigured", msg)
	}
	if !strings.Contains(msg, "not answering") {
		t.Fatalf("message = %q, want it to say the sources are not answering", msg)
	}
}

// TestHomeScreenSaysWhenItIsShowingAStoredAnswer covers the other half of
// staleness: rows drawn from a snapshot while the source is down are shown, and
// shown with their age. A two-day-old home beats an empty one, but only if
// nobody is being told it is live.
func TestHomeScreenSaysWhenItIsShowingAStoredAnswer(t *testing.T) {
	taken := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
		},
		items: []v1.CatalogItem{
			{Ref: v1.ContentRef{Provider: "tmdb", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}, Title: "A Movie"},
		},
		itemAnswer: app.BrowseAnswer{From: app.AnswerSnapshot, TakenAt: taken, Failed: []string{"tmdb"}},
	}
	svc := &Service{content: fake, clock: func() time.Time { return taken.Add(90 * time.Minute) }}
	ctx, report := WithReport(context.Background())
	node, err := svc.Render(ctx, "home", v1.CallerFromSession("s-1"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if _, ok := find(node, sdui.TypeEmptyState); ok {
		t.Fatal("home served from a snapshot must render its rows, not an empty state")
	}
	banner, ok := find(node, sdui.TypeBanner)
	if !ok {
		t.Fatal("a screen served from a snapshot because its source failed must say so")
	}
	msg, _ := prop(banner, "message").(string)
	if !strings.Contains(msg, "1 hour ago") {
		t.Fatalf("banner = %q, want the age of what is being shown", msg)
	}
	if !report.FromSnapshot() || len(report.Failed()) != 1 {
		t.Fatalf("report = snapshot:%v failed:%v, want the transport told which source is down",
			report.FromSnapshot(), report.Failed())
	}
}

// TestHomeScreenSchedulesRevalidationWhenStale proves the report carries what
// the transport acts on: a stale snapshot asks to be revalidated, and a fresh
// one does not — otherwise every navigation to home would cost a full provider
// fan-out and a pushed region.
func TestHomeScreenSchedulesRevalidationWhenStale(t *testing.T) {
	// Built per case rather than copied: fakeQueries holds a mutex, and copying
	// one is the shape `go vet` refuses.
	newFake := func(answer app.BrowseAnswer) *fakeQueries {
		return &fakeQueries{
			catalogs: []app.ModuleCatalog{
				{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
			},
			items: []v1.CatalogItem{
				{Ref: v1.ContentRef{Provider: "tmdb", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}, Title: "A Movie"},
			},
			itemAnswer: answer,
		}
	}
	for _, tc := range []struct {
		name      string
		answer    app.BrowseAnswer
		wantStale bool
	}{
		{"fresh snapshot", app.BrowseAnswer{From: app.AnswerSnapshot}, false},
		{"stale snapshot", app.BrowseAnswer{From: app.AnswerSnapshot, Stale: true}, true},
		{"live", app.BrowseAnswer{From: app.AnswerLive}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, report := WithReport(context.Background())
			if _, err := (&Service{content: newFake(tc.answer)}).Render(ctx, "home", v1.CallerFromSession("s-1"), nil); err != nil {
				t.Fatalf("Render: %v", err)
			}
			if report.Stale() != tc.wantStale {
				t.Fatalf("report.Stale() = %v, want %v", report.Stale(), tc.wantStale)
			}
		})
	}
}

// TestTwoViewersGetTwoDifferentHomes is ADR 0103's exit, at the surface. One
// shared library, and everything about how a person experiences it is theirs
// alone: the same install, the same catalogs, two arrangements.
func TestTwoViewersGetTwoDifferentHomes(t *testing.T) {
	popular := app.ModuleCatalog{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "popular", NativeType: "movie", Name: "Popular Movies"}}
	trending := app.ModuleCatalog{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "trending", NativeType: "movie", Name: "Trending Films"}}
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{popular, trending},
		items: []v1.CatalogItem{
			{Ref: v1.ContentRef{Provider: "tmdb", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}, Title: "A Movie"},
		},
		compositions: map[string]app.HomeComposition{
			// One viewer hid Trending Films; the other put it first.
			"hider":    {Hidden: []string{"catalog:tmdb:movie:trending"}},
			"arranger": {Order: []string{"catalog:tmdb:movie:trending", "catalog:tmdb:movie:popular"}},
		},
	}
	svc := &Service{content: fake}

	headings := func(session string) []string {
		node, err := svc.Render(context.Background(), "home", v1.CallerFromSession(session), nil)
		if err != nil {
			t.Fatalf("Render(%s): %v", session, err)
		}
		var sections []sdui.Node
		findAll(node, sdui.TypeSection, &sections)
		var out []string
		for _, sec := range sections {
			if title, _ := prop(sec, "title").(string); title != "" {
				out = append(out, title)
			}
		}
		return out
	}

	hider := headings("hider")
	if strings.Contains(strings.Join(hider, "|"), "Trending Films") {
		t.Fatalf("hider's home = %v, want the row they hid absent", hider)
	}
	if !strings.Contains(strings.Join(hider, "|"), "Popular Movies") {
		t.Fatalf("hider's home = %v, want the rows they kept", hider)
	}

	arranger := headings("arranger")
	joined := strings.Join(arranger, "|")
	if strings.Index(joined, "Trending Films") > strings.Index(joined, "Popular Movies") {
		t.Fatalf("arranger's home = %v, want the row they moved up first", arranger)
	}

	// And a viewer who has decided nothing takes the server's order.
	if got := headings("undecided"); strings.Index(strings.Join(got, "|"), "Popular Movies") >
		strings.Index(strings.Join(got, "|"), "Trending Films") {
		t.Fatalf("undecided home = %v, want the server's own order", got)
	}
}

// TestAHiddenRowCostsNoRoundTrip is the reason the arrangement is applied before
// the items are fetched rather than while the tree is assembled: a row this
// viewer turned off must not send the Platform to a provider to draw nothing
// with.
func TestAHiddenRowCostsNoRoundTrip(t *testing.T) {
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "popular", NativeType: "movie", Name: "Popular Movies"}},
		},
		compositions: map[string]app.HomeComposition{
			"hider": {Hidden: []string{"catalog:tmdb:movie:popular"}},
		},
	}
	if _, err := (&Service{content: fake}).Render(context.Background(), "home",
		v1.CallerFromSession("hider"), nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if fake.gotCatalogID != "" {
		t.Fatalf("asked catalog %q, want no items read for a row nobody is going to see", fake.gotCatalogID)
	}
}

// TestHomeScreenRefreshReachesTheReads proves a revalidation is actually a
// revalidation: the refresh marker on the context has to reach every
// source-backed read, or the background pass re-serves the same snapshot and
// nothing ever gets fresher.
func TestHomeScreenRefreshReachesTheReads(t *testing.T) {
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
		},
	}
	if _, err := (&Service{content: fake}).Render(WithRefresh(context.Background()),
		"home", v1.CallerFromSession("s-1"), nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !fake.gotRefresh {
		t.Fatal("a refresh render must ask the sources rather than re-reading the snapshot")
	}
}

func TestHomeScreenRendersContinueWatchingRail(t *testing.T) {
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "series", Name: "Popular Series"}},
		},
		items: []v1.CatalogItem{
			{Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt1", NativeType: "series", MediaType: v1.MediaTVSeries}, Title: "A Series"},
		},
		previewMeta: v1.ContentMetadata{Title: "A Series", Backdrop: "http://cdn/bd.jpg"},
		// The Work the in-progress episode belongs to, with stored art (ADR 0071).
		node: v1.Node{
			ID: "work-1", WorkID: "work-1", Kind: v1.NodeWork,
			MediaType: v1.MediaTVSeries, Title: "The Series",
			Artwork: v1.Artwork{Poster: "http://cdn/poster.jpg"},
		},
		inProgress: []v1.InProgressItem{{
			Node: v1.Node{
				ID: "ep-3", WorkID: "work-1", ItemType: v1.ItemEpisode,
				MediaType: v1.MediaTVSeries, Title: "The Third Episode",
			},
			State: v1.PlaybackState{NodeID: "ep-3", PartID: "part-9", Position: 30 * time.Minute, Duration: 60 * time.Minute},
		}},
	}
	node := render(t, &Service{content: fake}, "home", nil)

	var sections []sdui.Node
	findAll(node, sdui.TypeSection, &sections)
	var rail sdui.Node
	for _, s := range sections {
		if prop(s, "title") == "Continue watching" {
			rail = s
		}
	}
	if rail == nil {
		t.Fatal("home with an in-progress item has no Continue watching rail")
	}

	// A landscape tile, not a poster: the rail carries a progress bar and a
	// resume affordance over the artwork, which a 2:3 frame has no room for.
	card, ok := find(rail, sdui.TypeMediaTile)
	if !ok {
		t.Fatal("continue-watching rail has no card")
	}
	// The series poster and title come from the Work; the episode is named
	// beneath it.
	if got, _ := prop(card, "title").(string); got != "The Series" {
		t.Fatalf("card title = %q, want the work title", got)
	}
	if got, _ := prop(card, "subtitle").(string); got != "The Third Episode" {
		t.Fatalf("card subtitle = %q, want the episode title", got)
	}
	// A resume-progress fraction (30 of 60 minutes).
	if got, _ := prop(card, "progress").(float64); got != 0.5 {
		t.Fatalf("card progress = %v, want 0.5", prop(card, "progress"))
	}
	// …and how much of it is left, for the veil the tile shows on approach.
	if got, _ := prop(card, "progressLabel").(string); got != "30 min left" {
		t.Fatalf("card progressLabel = %q, want %q", got, "30 min left")
	}
	// The tap resumes rather than navigating — a node cannot open a rich detail
	// (ADR 0071), so the card carries a play action, not a navigation.
	if actionOf(card) == nil {
		t.Fatal("continue-watching card has no action to resume")
	}
}

func TestSeriesDetailMarksWatchedEpisodes(t *testing.T) {
	fake := &fakeQueries{
		previewInLibrary: true,
		previewNodeID:    "series-1",
		previewMeta: v1.ContentMetadata{
			Title: "The Series",
			Episodes: []v1.EpisodePreview{
				{Season: 1, Episode: 1, Title: "Pilot"},
				{Season: 1, Episode: 2, Title: "Second"},
				{Season: 1, Episode: 3, Title: "Third"},
			},
		},
		// The materialised tree: a season container, then episode items, each
		// carrying its number as NaturalOrder — the bridge from the live preview's
		// (season, episode) back to the nodes playback state is keyed under.
		childrenByNode: map[v1.NodeID][]v1.Node{
			"series-1": {{ID: "s1", Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason, NaturalOrder: 1}},
			"s1": {
				{ID: "e1", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 1},
				{ID: "e2", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 2},
				{ID: "e3", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 3},
			},
		},
		// e1 is finished (watched); e2 is started but not finished; e3 unseen.
		playbackStates: map[v1.NodeID]v1.PlaybackState{
			"e1": {NodeID: "e1", Finished: true},
			"e2": {NodeID: "e2", Position: 5 * time.Minute},
		},
	}
	ref := map[string]any{"provider": "stremio", "nativeId": "tt1", "nativeType": "series", "mediaType": string(v1.MediaTVSeries)}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": ref})

	var rows []sdui.Node
	findAll(node, sdui.TypeEpisodeRow, &rows)
	if len(rows) != 3 {
		t.Fatalf("got %d episode rows, want 3", len(rows))
	}
	watched := map[string]bool{}
	for _, r := range rows {
		title, _ := prop(r, "title").(string)
		watched[title] = prop(r, "watched") == true
	}
	if !watched["Pilot"] {
		t.Error("a finished episode should be marked watched")
	}
	if watched["Second"] {
		t.Error("a started-but-unfinished episode must not be marked watched")
	}
	if watched["Third"] {
		t.Error("an unseen episode must not be marked watched")
	}
}

func TestSearchScreenRendersResultsWithVirtualAndInLibraryActions(t *testing.T) {
	fake := &fakeQueries{results: []v1.SearchResult{
		{ // virtual — must carry a materialise (importContent) action
			Ref:   v1.ContentRef{Provider: "stremio", NativeID: "tt1254207", NativeType: "movie", MediaType: v1.MediaMovie, ExternalScheme: "imdb", ExternalID: "tt1254207"},
			Title: "Blade Runner 2049", Year: 2017,
		},
		{ // in-library — must carry a badge and a navigate action
			Ref:   v1.ContentRef{Provider: "stremio", NativeID: "tt0903747", NativeType: "series", MediaType: v1.MediaTVSeries},
			Title: "Breaking Bad", InLibrary: true, NodeID: "n-1",
		},
	}}
	svc := &Service{content: fake}

	node := render(t, svc, "search", map[string]any{"text": "blade"})
	if fake.gotText != "blade" {
		t.Fatalf("backend saw text %q, want the query forwarded", fake.gotText)
	}

	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(cards))
	}

	// The virtual card opens a detail preview, carrying its ref (materialising
	// happens on that screen, not the card).
	virtual := cards[0]
	act := actionOf(virtual)
	if act["kind"] != sdui.KindNavigate || act["screen"] != "detail" {
		t.Fatalf("virtual card action = %+v, want Navigate detail", act)
	}
	ref := mapAt(mapAt(act, "params"), "ref")
	if ref["externalId"] != "tt1254207" || ref["provider"] != "stremio" {
		t.Fatalf("detail ref = %+v, want the result's ref", ref)
	}

	// The in-library card navigates to detail and carries a badge.
	inLib := cards[1]
	if prop(inLib, "badge") != "In library" {
		t.Fatalf("in-library card badge = %v, want \"In library\"", prop(inLib, "badge"))
	}
	libAct := actionOf(inLib)
	if libAct["kind"] != sdui.KindNavigate || libAct["screen"] != "detail" {
		t.Fatalf("in-library card action = %+v, want Navigate detail", libAct)
	}

	// The whole tree serializes to JSON (the wire form the client renders).
	if _, err := protojson.Marshal(node); err != nil {
		t.Fatalf("screen does not serialize: %v", err)
	}
}

func TestSearchScreenNoResultsShowsEmptyState(t *testing.T) {
	svc := &Service{content: &fakeQueries{results: nil}}
	node := render(t, svc, "search", map[string]any{"text": "zzz"})
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("no results must render an EmptyState")
	}
}

// A route naming no screen renders the 404 rather than failing the render. It
// used to return NotFound, which the session transport turned into a raw error
// node — so a stale bookmark put "no screen named nope" in the content region.
// A wrong route is an ordinary thing for a user to do, and it needs a way out
// rather than a diagnosis.
func TestUnknownScreenRendersTheNotFoundScreen(t *testing.T) {
	svc := &Service{content: &fakeQueries{}}
	node, err := svc.Render(context.Background(), "nope", v1.CallerFromSession("s-1"), nil)
	if err != nil {
		t.Fatalf("an unknown screen must render, not fail: %v", err)
	}
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("the 404 must render an EmptyState")
	}
	// Both ways out are present, and both are navigations the client can
	// dispatch — a 404 whose buttons went nowhere would be the dead end this
	// replaced, wearing better clothes.
	var buttons []sdui.Node
	findAll(node, sdui.TypeButton, &buttons)
	if len(buttons) != 2 {
		t.Fatalf("the 404 has %d buttons, want 2 (home and search)", len(buttons))
	}
	for _, b := range buttons {
		if actionOf(b) == nil {
			t.Fatalf("404 button %q has no action", prop(b, "label"))
		}
	}
}

func TestCollectionsScreenListsCatalogsAsNavigableRows(t *testing.T) {
	fake := &fakeQueries{catalogs: []app.ModuleCatalog{
		{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
	}}
	node := render(t, &Service{content: fake}, "collections", nil)

	var buttons []sdui.Node
	findAll(node, sdui.TypeButton, &buttons)
	if len(buttons) != 1 {
		t.Fatalf("buttons = %d, want 1 per catalog", len(buttons))
	}
	act := actionOf(buttons[0])
	if act["kind"] != sdui.KindNavigate || act["screen"] != "catalog" {
		t.Fatalf("catalog row action = %+v, want Navigate catalog", act)
	}
	if mapAt(act, "params")["catalogId"] != "top" || mapAt(act, "params")["moduleId"] != "stremio" {
		t.Fatalf("navigate params = %+v, want the catalog's module and id", act)
	}
}

func TestCollectionsScreenEmpty(t *testing.T) {
	node := render(t, &Service{content: &fakeQueries{}}, "collections", nil)
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("no catalogs must render an EmptyState")
	}
}

func TestCatalogScreenRendersItemsAsDetailLinks(t *testing.T) {
	fake := &fakeQueries{items: []v1.CatalogItem{
		{Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt1254207", NativeType: "movie", MediaType: v1.MediaMovie, ExternalScheme: "imdb", ExternalID: "tt1254207"}, Title: "Blade Runner 2049", Year: 2017},
	}}
	node := render(t, &Service{content: fake}, "catalog", map[string]any{"moduleId": "stremio", "catalogId": "top", "nativeType": "movie"})
	if fake.gotCatalogID != "top" {
		t.Fatalf("backend saw catalog %q, want the param forwarded", fake.gotCatalogID)
	}
	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	act := actionOf(cards[0])
	if act["kind"] != sdui.KindNavigate || act["screen"] != "detail" {
		t.Fatalf("catalog item action = %+v, want Navigate detail", act)
	}
}

func TestVirtualDetailOffersPlayAndAdd(t *testing.T) {
	fake := &fakeQueries{
		previewMeta: v1.ContentMetadata{
			Title: "Blade Runner 2049", Year: 2017, Overview: "A blade runner uncovers a secret.",
			Backdrop: "http://cdn/bd.jpg", Logo: "http://cdn/logo.png", Rating: 8.0, Runtime: "164 min",
			Cast: []v1.Person{{Name: "Ryan Gosling"}, {Name: "Harrison Ford"}}, Genres: []string{"Sci-Fi"},
		},
		// Curating the library is an administrator's authority (ADR 0069), so
		// the control is drawn only for a caller who holds it.
		allow: map[string]bool{string(app.ActionContentImport): true},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": map[string]any{
		"provider": "stremio", "nativeId": "tt1254207", "nativeType": "movie",
		"mediaType": "movie", "externalScheme": "imdb", "externalId": "tt1254207",
	}})
	if fake.gotPreviewRef.NativeID != "tt1254207" {
		t.Fatalf("preview saw ref %+v, want the card's ref", fake.gotPreviewRef)
	}
	// The rich detail is a DetailHero carrying the title, logo and the primary
	// action; a glass info panel docks in its aside slot (ADR 0034).
	hero, ok := find(node, "DetailHero")
	if !ok || prop(hero, "title") != "Blade Runner 2049" {
		t.Fatalf("hero = %+v, want the previewed title", hero.Props)
	}
	if prop(hero, "logo") == nil || prop(hero, "logo") == "" {
		t.Fatalf("hero has no logo prop; want the clearlogo bound")
	}
	// Two library affordances, and Play comes first (ADR 0118): a viewer wants
	// to watch the thing, and adding it is what the Platform does to let them.
	// Both carry the previewed ref, and both are drawn only for a caller who may
	// import — because pressing Play here adds it.
	actions := slotNodes(hero, "actions")
	if len(actions) != 2 {
		t.Fatalf("hero actions = %+v, want Play and Add to library", actions)
	}
	if prop(actions[0], "label") != "Play" || prop(actions[1], "label") != "Add to library" {
		t.Fatalf("hero actions = %+v, want Play first and Add to library second", actions)
	}

	play := actionOf(actions[0])
	if play["kind"] != sdui.KindInvoke || play["mutation"] != "playPart" {
		t.Fatalf("play action = %+v, want Invoke playPart", play)
	}
	// It names no part, because there is no Part yet — the ref is what
	// materialises one.
	playInput := mapAt(play, "input")
	if playInput["partId"] != nil {
		t.Errorf("play names a part id (%v) for something not in the library", playInput["partId"])
	}
	if ref := mapAt(playInput, "ref"); ref["nativeId"] != "tt1254207" {
		t.Fatalf("play ref = %+v, want the previewed ref", ref)
	}

	act := actionOf(actions[1])
	if act["kind"] != sdui.KindInvoke || act["mutation"] != "importContent" {
		t.Fatalf("add action = %+v, want Invoke importContent", act)
	}
	ref := mapAt(mapAt(act, "input"), "ref")
	if ref["nativeId"] != "tt1254207" {
		t.Fatalf("add ref = %+v, want the previewed ref", ref)
	}
	// Top cast renders as PersonChips.
	var chips []sdui.Node
	findAll(node, sdui.TypePersonChip, &chips)
	if len(chips) != 2 {
		t.Fatalf("cast chips = %d, want 2", len(chips))
	}
	// The whole tree serializes to the wire form the client renders.
	if _, err := protojson.Marshal(node); err != nil {
		t.Fatalf("screen does not serialize: %v", err)
	}
}

func TestInLibraryDetailShowsInLibraryMarker(t *testing.T) {
	// An in-library ref renders the same rich detail from live metadata (ADR
	// 0034), differing only in the primary action — an In library marker, not
	// Add to library — and does not fall back to a structural node read.
	fake := &fakeQueries{
		previewInLibrary: true, previewNodeID: "n-9",
		previewMeta: v1.ContentMetadata{Title: "Already Here", Year: 2020},
		allow:       map[string]bool{string(app.ActionContentImport): true},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": map[string]any{
		"provider": "stremio", "nativeId": "tt1", "nativeType": "movie", "mediaType": "movie",
	}})
	if fake.gotNodeID != "" {
		t.Fatalf("in-library detail should render from metadata, not read node %q", fake.gotNodeID)
	}
	hero, ok := find(node, "DetailHero")
	if !ok || prop(hero, "title") != "Already Here" {
		t.Fatalf("hero = %+v, want the metadata title", hero.Props)
	}
	// An in-library item offers no Add to library — adding what is already
	// there is the affordance this screen must never offer.
	if _, ok := findButton(node, "Add to library"); ok {
		t.Error("an in-library item must not offer Add to library")
	}
	// There is no "In library" badge any more. The primary action says which
	// plane this is — Resume or Play rather than Add — and a badge repeating it
	// was a label for a state the control beside it already showed.
	if _, ok := find(node, sdui.TypeBadge); ok {
		t.Error("the In library badge is gone; the primary action carries the plane")
	}
	// It still offers Refresh sources: a candidate set goes stale as releases
	// appear and disappear, and re-importing is how a user asks for the current
	// answer. It is an icon control now rather than a labelled pill, so its
	// label is the accessible name. Play is absent because this fake has no
	// playable part, which TestDetailPlayAffordanceIsGatedOnAPartExisting covers.
	if _, ok := findIconButton(node, "Refresh sources"); !ok {
		t.Error("an in-library item must offer Refresh sources")
	}
	if _, ok := findButton(node, "Play"); ok {
		t.Error("Play must not appear when nothing in the tree has bytes")
	}
}

func TestSeriesDetailRendersEpisodesWithSeasonSelector(t *testing.T) {
	fake := &fakeQueries{previewMeta: v1.ContentMetadata{
		Title: "Avatar: The Last Airbender",
		Episodes: []v1.EpisodePreview{
			{Season: 1, Episode: 1, Title: "The Boy in the Iceberg", Overview: "Katara and Sokka find Aang."},
			{Season: 1, Episode: 2, Title: "The Avatar Returns", Overview: "Zuko attacks."},
			{Season: 2, Episode: 1, Title: "The Avatar State", Overview: "Aang trains."},
		},
	}}
	refParam := map[string]any{"provider": "stremio", "nativeId": "tt0417299", "nativeType": "series", "mediaType": "tv-series"}

	// Default (no season param) shows season 1's two episodes.
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": refParam})
	selector, ok := find(node, sdui.TypeSeasonSelector)
	if !ok {
		t.Fatal("series detail has no SeasonSelector")
	}
	seasons, _ := prop(selector, "seasons").([]any)
	if len(seasons) != 2 {
		t.Fatalf("season entries = %d, want 2", len(seasons))
	}
	if prop(selector, "selected") != "1" {
		t.Fatalf("default selected season = %v, want \"1\"", prop(selector, "selected"))
	}
	var rows []sdui.Node
	findAll(node, sdui.TypeEpisodeRow, &rows)
	if len(rows) != 2 {
		t.Fatalf("season 1 episode rows = %d, want 2", len(rows))
	}
	if prop(rows[0], "title") != "The Boy in the Iceberg" || prop(rows[0], "overview") == nil {
		t.Fatalf("episode row = %+v, want the title and synopsis", rows[0].Props)
	}

	// The season param switches to season 2's single episode.
	node2 := render(t, &Service{content: fake}, "detail", map[string]any{"ref": refParam, "season": "2"})
	var rows2 []sdui.Node
	findAll(node2, sdui.TypeEpisodeRow, &rows2)
	if len(rows2) != 1 || prop(rows2[0], "title") != "The Avatar State" {
		t.Fatalf("season 2 rows = %+v, want the single S2 episode", rows2)
	}
	sel2, _ := find(node2, sdui.TypeSeasonSelector)
	if prop(sel2, "selected") != "2" {
		t.Fatalf("selected season = %v, want \"2\"", prop(sel2, "selected"))
	}
}

func TestCatalogScreenRequiresParams(t *testing.T) {
	_, err := (&Service{content: &fakeQueries{}}).Render(context.Background(), "catalog", v1.CallerFromSession("s-1"), map[string]any{"moduleId": "stremio"})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}

// A node with no stored document renders what the graph holds — its own title,
// artwork and contents (ADR 0107's floor). It is what a title materialised
// before the store existed, or by a provider that has never answered since,
// falls back to.
func TestDetailScreenRendersTheGraphWhenNothingWasStored(t *testing.T) {
	fake := &fakeQueries{
		node: v1.Node{
			ID: "n-1", WorkID: "n-1", Kind: v1.NodeWork, MediaType: v1.MediaTVSeries,
			Title: "Breaking Bad", Artwork: v1.Artwork{Poster: "https://cdn/bb.jpg"},
		},
		libraryChildren: []v1.Node{
			{ID: "n-2", ParentID: nodeRef("n-1"), Kind: v1.NodeContainer,
				ContainerType: v1.ContainerSeason, MediaType: v1.MediaTVSeries, Title: "Season 1",
				NaturalOrder: 1, Artwork: v1.Artwork{Poster: "https://cdn/s1.jpg"}},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"nodeId": "n-1"})

	hero, ok := find(node, "DetailHero")
	if !ok || prop(hero, "title") != "Breaking Bad" {
		t.Fatalf("detail hero = %+v, want the node title", hero)
	}
	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 1 {
		t.Fatalf("child cards = %d, want 1 per child", len(cards))
	}
	// The child's poster, which the version this replaces did not draw — a grid
	// of blank cards was the visible half of the same omission.
	if prop(cards[0], "poster") == nil {
		t.Error("a child card carries no poster, so the contents grid is blank")
	}
	act := actionOf(cards[0])
	if act["kind"] != sdui.KindNavigate || mapAt(act, "params")["nodeId"] != "n-2" {
		t.Fatalf("child card action = %+v, want Navigate to the child's detail", act)
	}
}

// The whole point of ADR 0107: a library detail renders the rich tree from the
// stored document and the materialised tree, with **no provider call**.
func TestDetailScreenRendersStoredMetadataWithNoProviderCall(t *testing.T) {
	fake := &fakeQueries{
		node: v1.Node{
			ID: "n-1", WorkID: "n-1", Kind: v1.NodeWork, MediaType: v1.MediaTVSeries,
			Title: "Breaking Bad", Artwork: v1.Artwork{Poster: "https://cdn/bb.jpg"},
		},
		hasStoredMetadata: true,
		storedMetadata: v1.ContentMetadata{
			Ref:      v1.ContentRef{Provider: "tmdb", NativeID: "1396", NativeType: "tv", MediaType: v1.MediaTVSeries},
			Title:    "Breaking Bad",
			Overview: "A chemistry teacher diagnosed with cancer turns to making meth.",
			Year:     2008, Rating: 8.9, Genres: []string{"Drama"},
			Cast: []v1.Person{{Name: "Bryan Cranston", Role: "Walter White"}},
		},
		libraryChildren: []v1.Node{
			{ID: "s-1", ParentID: nodeRef("n-1"), Kind: v1.NodeContainer,
				ContainerType: v1.ContainerSeason, MediaType: v1.MediaTVSeries,
				Title: "Season 1", NaturalOrder: 1},
		},
		libraryEpisodes: map[int][]v1.Node{
			1: {{ID: "e-1", ParentID: nodeRef("s-1"), Kind: v1.NodeItem,
				ItemType: v1.ItemEpisode, MediaType: v1.MediaTVSeries,
				Title: "Pilot", NaturalOrder: 1}},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"nodeId": "n-1"})
	text := treeStrings(node)

	if fake.previewCalls != 0 {
		t.Errorf("a library detail made %d provider calls, want none", fake.previewCalls)
	}
	if !strings.Contains(text, "chemistry teacher") {
		t.Errorf("the stored overview is not on the screen: %s", text)
	}
	if !strings.Contains(text, "Bryan Cranston") {
		t.Errorf("the stored cast is not on the screen: %s", text)
	}
	// The episode came from the tree, not from the document — the document
	// deliberately carries none.
	if !strings.Contains(text, "Pilot") {
		t.Errorf("the tree's episode is not on the screen: %s", text)
	}
	// And the kicker counts the seasons the tree holds.
	hero, _ := find(node, "DetailHero")
	if got, _ := prop(hero, "kicker").(string); !strings.Contains(got, "1 season") {
		t.Errorf("kicker = %q, want the season count from the tree", got)
	}
}

// nodeRef is a pointer to a node id, for the parent links a tree fixture needs.
func nodeRef(id v1.NodeID) *v1.NodeID { return &id }

func TestSettingsScreenHostsModuleUI(t *testing.T) {
	// The Platform hosts the module's contributed settings UINode verbatim (ADR
	// 0038): the settings screen renders whatever the module returned, in the
	// panel of a frame of the Platform's own.
	moduleUI := `{"type":"Screen","props":{"title":"AIOStreams"},"children":[{"type":"Section","props":{"title":"Instance"}}]}`
	fake := &fakeQueries{
		settingsUI:      []byte(moduleUI),
		settingsModules: []app.SettingsModule{{ModuleID: "aiostreams", Name: "AIOStreams"}},
		allow:           map[string]bool{string(app.ActionModuleRead): true},
	}

	node := render(t, &Service{content: fake}, "settings", map[string]any{"moduleId": "aiostreams"})
	if fake.gotSettingsModuleID != "aiostreams" {
		t.Fatalf("settings screen resolved module %q, want the requested one", fake.gotSettingsModuleID)
	}
	if node.GetType() != "SettingsFrame" {
		t.Fatalf("root type = %q, want the Platform's SettingsFrame", node.GetType())
	}
	// The module's own Screen container is replaced by the Platform's panel —
	// its padding is a whole page's and would apply twice inside the frame — and
	// its title becomes the panel heading. Everything the module put IN that
	// Screen is hosted verbatim, as a child of the frame rather than the nav
	// slot: the module fills the panel and cannot draw the chrome around it.
	if prop(node, "heading") != "AIOStreams" {
		t.Fatalf("panel heading = %v, want the module's screen title", prop(node, "heading"))
	}
	if _, ok := find(node, sdui.TypeScreen); ok {
		t.Fatalf("the module's Screen container survived into the panel: %+v", node)
	}
	section, ok := find(node, sdui.TypeSection)
	if !ok || prop(section, "title") != "Instance" {
		t.Fatal("settings screen did not render the module's section")
	}
	// The way back out is the Platform's, not the module's: the nav is on the
	// screen the whole time, with the open module's row marked active.
	row, ok := findNavItem(node, "AIOStreams")
	if !ok {
		t.Fatal("the hosted module has no row in the Platform's settings nav")
	}
	if prop(row, "active") != true {
		t.Fatalf("open module's nav row active = %v, want true", prop(row, "active"))
	}
}

// TestSettingsNavReachesEveryModuleWithAScreen is the client path a module's
// settings screen is owed. The host used to name one module by constant, so a
// second module's screen existed and nothing could open it.
func TestSettingsNavReachesEveryModuleWithAScreen(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: []byte(`{"type":"Screen","props":{"title":"AIOStreams"}}`),
		settingsModules: []app.SettingsModule{
			{ModuleID: "aiostreams", Name: "AIOStreams"},
			{ModuleID: "stremio", Name: "Stremio addon source"},
			{ModuleID: "tmdb", Name: "TMDB"},
		},
		// The nav asks before it reads: a caller who may not list modules gets
		// no module rows rather than a settings screen that will not open.
		allow: map[string]bool{string(app.ActionModuleRead): true},
	}

	node := render(t, &Service{content: fake}, "settings", nil)
	for _, m := range fake.settingsModules {
		row, ok := findNavItem(node, m.Name)
		if !ok {
			t.Fatalf("settings nav has no way into %q", m.ModuleID)
		}
		act := actionOf(row)
		if act["kind"] != sdui.KindNavigate || mapAt(act, "params")["moduleId"] != m.ModuleID {
			t.Fatalf("%q nav row action = %+v, want a Navigate carrying its moduleId", m.ModuleID, act)
		}
	}
	// Opened with no params the panel lands on Account, which is the design's
	// first section and the one every caller has. It does not invoke a module:
	// asking a module to render is work, and a settings screen opened from the
	// app nav must not do it on the way to somebody's own name.
	if fake.gotSettingsModuleID != "" {
		t.Fatalf("no-param settings invoked module %q, want Account and no module call", fake.gotSettingsModuleID)
	}
}

// TestSettingsAlwaysOffersAccount replaces a test that pinned the opposite. A
// build with no settings-UI module, read by a caller with no install-level
// permission, used to have nothing to configure and said so. It has Account now:
// everyone signed in has a name, and the screen that shows it is not gated on
// anything, so "nothing here" is no longer a state settings can be in.
func TestSettingsAlwaysOffersAccount(t *testing.T) {
	fake := &fakeQueries{currentUser: domain.User{
		Username: "alex", DisplayName: "Alex Rivera", Email: "alex@home.local", Status: domain.UserActive,
	}}
	node := render(t, &Service{content: fake}, "settings", nil)
	if _, ok := findNavItem(node, "Account"); !ok {
		t.Fatal("every caller must be offered Account")
	}
	if prop(node, "heading") != "Account" {
		t.Fatalf("heading = %v, want Account on a no-param render", prop(node, "heading"))
	}
	if fake.gotSettingsModuleID != "" {
		t.Fatalf("rendering Account invoked module %q", fake.gotSettingsModuleID)
	}
	// The profile is stated, not offered for editing: nothing in the application
	// layer updates a display name, so a field and a Save button would be a
	// control over a mutation that does not exist.
	if _, ok := find(node, "TextField"); ok {
		t.Error("Account must not draw editable fields — there is no command behind them")
	}
}

// TestSettingsSaysWhetherASectionWasAskedFor is what makes one payload serve a
// phone and a desktop. The frame renders as a list you drill into on a phone and
// as two panes on a desktop, and the difference it needs is not "is there a
// panel" — a no-param render resolves a default section so a desktop is not left
// with an empty pane. It is "did the caller ask for this section".
func TestSettingsSaysWhetherASectionWasAskedFor(t *testing.T) {
	newFake := func() *fakeQueries {
		return &fakeQueries{
			settingsUI:      []byte(`{"type":"Screen","props":{"title":"AIOStreams"}}`),
			settingsModules: []app.SettingsModule{{ModuleID: "aiostreams", Name: "AIOStreams"}},
		}
	}

	// Opened from the app nav: a default section renders, but nobody asked for it.
	defaulted := render(t, &Service{content: newFake()}, "settings", nil)
	if prop(defaulted, "selected") != false {
		t.Fatalf("selected = %v on a no-param render, want false — a phone must land on the list", prop(defaulted, "selected"))
	}

	// Tapped from the nav.
	asked := render(t, &Service{content: newFake()}, "settings", map[string]any{"moduleId": "aiostreams"})
	if prop(asked, "selected") != true {
		t.Fatalf("selected = %v after navigating to a module, want true", prop(asked, "selected"))
	}

	// Its own screen, always reached by asking for it.
	ext := render(t, &Service{content: newFake()}, "extensions", nil)
	if prop(ext, "selected") != true {
		t.Fatalf("selected = %v on the extensions screen, want true", prop(ext, "selected"))
	}
}

// TestSettingsNavIsGatedPerCaller pins ADR 0058's visibility rule onto the nav: a
// caller without the grant is not shown the affordance at all, rather than shown
// one that fails when they use it.
func TestSettingsNavIsGatedPerCaller(t *testing.T) {
	withPermission := render(t, &Service{content: &fakeQueries{canReadTelemetry: true}}, "settings", nil)
	if _, ok := findNavItem(withPermission, "Extension store"); !ok {
		t.Fatal("a caller holding module.read must be offered the Extension store section")
	}
	if _, ok := find(withPermission, "Toggle"); !ok {
		t.Fatal("a caller holding telemetry.read must be offered the expert-mode switch")
	}

	without := render(t, &Service{content: &fakeQueries{}}, "settings", nil)
	if _, ok := findNavItem(without, "Extension store"); ok {
		t.Fatal("Extensions is drawn for a caller who cannot read the module catalogue")
	}
	if _, ok := find(without, "Toggle"); ok {
		t.Fatal("the expert-mode switch is drawn for a caller who cannot read telemetry")
	}
}

// TestExtensionsScreenKeepsTheSettingsNav is the ADR 0081 screen inside the ADR
// 0038 frame: it stays its own screen (the catalogue is a network read), and
// opening it does not cost the nav that leads back to everything else.
func TestExtensionsScreenKeepsTheSettingsNav(t *testing.T) {
	fake := &fakeQueries{
		canReadTelemetry: true,
		settingsModules:  []app.SettingsModule{{ModuleID: "aiostreams", Name: "AIOStreams"}},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	if node.GetType() != "SettingsFrame" {
		t.Fatalf("root type = %q, want the extensions surface inside the settings frame", node.GetType())
	}
	row, ok := findNavItem(node, "Extension store")
	if !ok {
		t.Fatal("the extensions screen dropped the settings nav")
	}
	if prop(row, "active") != true {
		t.Fatalf("Extension store nav row active = %v, want true on its own screen", prop(row, "active"))
	}
	if _, ok := findNavItem(node, "AIOStreams"); !ok {
		t.Fatal("the extensions screen must keep the way back to the other sections")
	}
	// Listing the catalogue must not drag every module's settings UI in with it.
	if fake.gotSettingsModuleID != "" {
		t.Fatalf("the extensions screen rendered module %q's settings UI", fake.gotSettingsModuleID)
	}
}

// TestExtensionsScreenInstallAndUninstall is the browse-and-install surface
// (ADR 0081): an installed extension offers Uninstall, an available one that is
// not installed offers Install, and an available one that IS installed is not
// offered for install again.
func TestExtensionsScreenInstallAndUninstall(t *testing.T) {
	fake := &fakeQueries{
		installedExtensions: []app.InstalledExtension{{ModuleID: "stremio", Version: "v0.24.0"}},
		availableExtensions: []app.ExtensionCatalogueEntry{
			{Repository: "mosaic-official", ModuleID: "stremio", Name: "Stremio addon source", Version: "v0.24.0"},
			{Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0"},
		},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	un, ok := findButton(node, "Uninstall")
	if !ok {
		t.Fatal("installed extension has no Uninstall control")
	}
	act := actionOf(un)
	if act["kind"] != sdui.KindInvoke || act["mutation"] != "uninstallExtension" || mapAt(act, "input")["moduleId"] != "stremio" {
		t.Fatalf("Uninstall action = %+v, want an Invoke of uninstallExtension carrying stremio", act)
	}

	// aiostreams is available and not installed → offered; stremio is installed,
	// so it is not offered for install again. Each is a card, and the card's
	// control NAVIGATES: installing runs somebody else's binary, so it happens on
	// the far side of a screen that says what the thing does (see below).
	var cards []sdui.Node
	findAll(node, "ExtensionCard", &cards)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want one for the installed module and one for the offered one", len(cards))
	}
	in, ok := findButton(node, "Install…")
	if !ok {
		t.Fatal("an available, not-installed extension has no Install control")
	}
	act = actionOf(in)
	if act["kind"] != sdui.KindOpenOverlay || act["surface"] != sdui.SurfaceModal {
		t.Fatalf("Install control = %+v, want an overlay over the catalogue", act)
	}
	// No RENDERED control installs: the only install action in the surface is
	// inside the confirmation the overlay carries. That is the whole point of it.
	if walkFindInvoke(node, "installExtension") {
		t.Fatal("the catalogue list emits installExtension directly; it must confirm first")
	}
}

// TestExtensionCardsDescribeWhatTheModuleCanDo — a card that names a module and
// nothing else asks a user to decide about a word. The capabilities come from
// the module's signed manifest, phrased in the Platform's vocabulary, so what is
// shown is what the module can actually do rather than what it says about itself.
func TestExtensionCardsDescribeWhatTheModuleCanDo(t *testing.T) {
	fake := &fakeQueries{
		availableExtensions: []app.ExtensionCatalogueEntry{{
			Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0",
			Provides: []string{"stream", "subtitles", "settings_ui"},
		}},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	card, ok := find(node, "ExtensionCard")
	if !ok {
		t.Fatal("no card for the offered extension")
	}
	// Two chips, not three: settings_ui is plumbing — every configurable module
	// declares it, and it says nothing about what this one is FOR.
	caps, _ := prop(card, "capabilities").([]any)
	if len(caps) != 2 {
		t.Fatalf("capability chips = %d, want one per declared role except settings_ui", len(caps))
	}
	summary, _ := prop(card, "summary").(string)
	for _, want := range []string{"streams", "subtitles"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not mention %q", summary, want)
		}
	}
	if strings.Contains(summary, "settings screen") {
		t.Fatalf("summary %q offers a settings screen as a reason to install something", summary)
	}
	if origin, _ := prop(card, "origin").(string); !strings.Contains(origin, "v0.3.0") || !strings.Contains(origin, "mosaic-official") {
		t.Fatalf("card origin = %q, want the version and the repository it comes from", origin)
	}
}

// TestExtensionCardPrefersTheModulesOwnWords — the capabilities say what a
// module can DO and the Platform derives them; only the author can say what it
// IS. So a description from the signed manifest wins, and a module that
// publishes none is still described rather than left blank.
func TestExtensionCardPrefersTheModulesOwnWords(t *testing.T) {
	fake := &fakeQueries{availableExtensions: []app.ExtensionCatalogueEntry{
		{
			Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0",
			Provides:    []string{"stream", "subtitles"},
			Description: "An independent aggregator that searches many sources at once.",
		},
		{
			Repository: "mosaic-official", ModuleID: "quiet", Name: "Quiet module", Version: "v1",
			Provides: []string{"artwork"},
		},
	}}
	node := render(t, &Service{content: fake}, "extensions", nil)

	var cards []sdui.Node
	findAll(node, "ExtensionCard", &cards)
	byName := map[string]sdui.Node{}
	for _, c := range cards {
		byName[prop(c, "name").(string)] = c
	}

	if got, _ := prop(byName["AIOStreams"], "summary").(string); got != "An independent aggregator that searches many sources at once." {
		t.Fatalf("card summary = %q, want the module's own sentence", got)
	}
	// No description: the capabilities still have to say something useful.
	got, _ := prop(byName["Quiet module"], "summary").(string)
	if !strings.Contains(got, "artwork") {
		t.Fatalf("undescribed module's summary = %q, want its capabilities", got)
	}
}

// TestInstallConfirmationCarriesTheInstall is where consent lives: the overlay
// the card opens names the capabilities and the provenance, and it is the only
// place the install action exists.
//
// An overlay rather than a screen because the decision is about the catalogue
// you are looking at — a screen would take it away and need a route back.
func TestInstallConfirmationCarriesTheInstall(t *testing.T) {
	fake := &fakeQueries{
		availableExtensions: []app.ExtensionCatalogueEntry{{
			Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0",
			Provides: []string{"stream", "subtitles", "settings_ui"},
		}},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	btn, ok := findButton(node, "Install…")
	if !ok {
		t.Fatalf("no Install control on the card: %s", nodeText(node))
	}
	act := actionOf(btn)
	if act["kind"] != sdui.KindOpenOverlay {
		t.Fatalf("Install action = %+v, want an overlay", act)
	}
	overlay, _ := act["node"].(map[string]any)
	if overlay == nil {
		t.Fatal("the overlay action carries no node — nothing would be presented")
	}
	text := fmt.Sprint(overlay)
	for _, want := range []string{"Streams", "Finds playable sources", "Subtitles", "mosaic-official", "v0.3.0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the confirmation does not state %q: %s", want, text)
		}
	}
	// settings_ui is plumbing and is not a capability a user weighs.
	if strings.Contains(text, "Adds its own section to Settings") {
		t.Fatalf("the confirmation lists the settings-screen role: %s", text)
	}
	if !mapHasInvoke(overlay, "installExtension", "aiostreams") {
		t.Fatalf("the confirmation carries no install for aiostreams: %s", text)
	}
	if !strings.Contains(text, "closeOverlay") {
		t.Fatalf("a confirmation with no way out is not a confirmation: %s", text)
	}
}

// mapHasInvoke reports whether a decoded node tree carries an Invoke of the
// mutation against the module id — the action rides inside the overlay's node,
// which is a decoded map rather than a UINode, so this walks maps.
func mapHasInvoke(v any, mutation, moduleID string) bool {
	switch x := v.(type) {
	case map[string]any:
		if x["kind"] == sdui.KindInvoke && x["mutation"] == mutation {
			if in, ok := x["input"].(map[string]any); ok && in["moduleId"] == moduleID {
				return true
			}
		}
		for _, val := range x {
			if mapHasInvoke(val, mutation, moduleID) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if mapHasInvoke(val, mutation, moduleID) {
				return true
			}
		}
	}
	return false
}

// walkFindInvoke reports whether any node in the tree carries an Invoke of the
// named mutation.
func walkFindInvoke(n sdui.Node, mutation string) bool {
	if n == nil {
		return false
	}
	if act := actionOf(n); act["kind"] == sdui.KindInvoke && act["mutation"] == mutation {
		return true
	}
	for _, c := range n.GetChildren() {
		if walkFindInvoke(c, mutation) {
			return true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if walkFindInvoke(c, mutation) {
				return true
			}
		}
	}
	return false
}

// TestExtensionsScreenSurvivesAnUnreachableRepository pins the resilience rule: a
// repository that cannot be reached must not fail the whole screen, so a user can
// still uninstall when the repo is down.
func TestExtensionsScreenSurvivesAnUnreachableRepository(t *testing.T) {
	fake := &fakeQueries{
		installedExtensions: []app.InstalledExtension{{ModuleID: "stremio", Version: "v0.24.0"}},
		availableErr:        contracts.NewError(contracts.Unavailable, "repository down"),
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	if _, ok := findButton(node, "Uninstall"); !ok {
		t.Fatal("Uninstall must remain available when the repository is unreachable")
	}
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("an unreachable repository should render an empty state for the available list")
	}
}

func TestDetailScreenRequiresNodeId(t *testing.T) {
	_, err := (&Service{content: &fakeQueries{}}).Render(context.Background(), "detail", v1.CallerFromSession("s-1"), nil)
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}

// TestDetailPlayAffordanceIsGatedOnAPartExisting is ADR 0036's rule made
// executable on the emit-side: an affordance must never appear with nothing
// behind it. Being in the library is not enough — a Work has no bytes of its
// own, and a metadata-only import has none anywhere in its tree, so Play is
// offered on the presence of a Part and on nothing else.
func TestDetailPlayAffordanceIsGatedOnAPartExisting(t *testing.T) {
	inLibrary := func(part v1.Part) *fakeQueries {
		return &fakeQueries{
			previewMeta:      v1.ContentMetadata{Title: "Avatar", Year: 2009},
			previewInLibrary: true,
			previewNodeID:    v1.NodeID("work-1"),
			playablePart:     part,
		}
	}

	// No Part anywhere in the tree: In library, and no Play.
	node := render(t, &Service{content: inLibrary(v1.Part{})}, "detail",
		map[string]any{"ref": map[string]any{"provider": "stremio", "nativeId": "tt0499549", "nativeType": "movie"}})
	if btn, ok := findButton(node, "Play"); ok {
		t.Fatalf("Play offered with no playable part: %+v", btn)
	}

	// A Part exists: Play appears, and carries that part's id.
	withPart := v1.Part{ID: v1.PartID("part-7"), Role: v1.PartEdition}
	node = render(t, &Service{content: inLibrary(withPart)}, "detail",
		map[string]any{"ref": map[string]any{"provider": "stremio", "nativeId": "tt0499549", "nativeType": "movie"}})
	btn, ok := findButton(node, "Play")
	if !ok {
		t.Fatal("a library item with a part must offer Play")
	}
	act := actionOf(btn)
	if act["kind"] != sdui.KindInvoke || act["mutation"] != "playPart" {
		t.Fatalf("Play action = %+v, want an Invoke of playPart", act)
	}
	input, _ := act["input"].(map[string]any)
	if input["partId"] != string(withPart.ID) {
		t.Fatalf("Play carried partId %v, want %q", input["partId"], withPart.ID)
	}
}

// TMDB fills Similar, Collection, Certification and Trailers on every detail
// read, and the screen rendered none of them — so the cost of resolving them
// was paid on every view and thrown away. These assert the screen spends what
// it fetches.
func TestRichDetailSurfacesTheMetadataItWasDiscarding(t *testing.T) {
	ref := func(id string) v1.ContentRef {
		return v1.ContentRef{Provider: "tmdb", NativeID: id, NativeType: "movie", MediaType: v1.MediaMovie}
	}
	self := ref("tt0133093")
	fake := &fakeQueries{
		previewInLibrary: false,
		previewMeta: v1.ContentMetadata{
			Title:         "The Matrix",
			Certification: "15",
			Trailers: []v1.Trailer{
				{Name: "Fan cut", Site: "YouTube", Key: "fan123"},
				{Name: "Official Trailer", Site: "YouTube", Key: "vKQi3bBA1y8", Official: true},
			},
			Collection: &v1.Collection{
				Name: "The Matrix Collection",
				// Includes the work being described, as the SDK says it does.
				Items: []v1.RelatedItem{
					{Ref: self, Title: "The Matrix"},
					{Ref: ref("tt0234215"), Title: "The Matrix Reloaded"},
				},
			},
			Similar: []v1.RelatedItem{{Ref: ref("tt0137523"), Title: "Fight Club"}},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(self)})

	// The age rating rides the hero's meta line.
	hero, ok := find(node, "DetailHero")
	if !ok {
		t.Fatal("no DetailHero")
	}
	meta, _ := prop(hero, "meta").([]any)
	var found bool
	for _, m := range meta {
		if m == "15" {
			found = true
		}
	}
	if !found {
		t.Fatalf("meta = %v, want the certification among the pills", meta)
	}

	// The trailer opens the OFFICIAL video, not the first one in the list.
	var buttons []sdui.Node
	findAll(node, sdui.TypeButton, &buttons)
	var trailerURL string
	for _, b := range buttons {
		if prop(b, "label") == "Trailer" {
			if a := actionOf(b); a != nil {
				trailerURL, _ = a["url"].(string)
			}
		}
	}
	if !strings.Contains(trailerURL, "vKQi3bBA1y8") {
		t.Fatalf("trailer url = %q, want the official video", trailerURL)
	}

	// Both rails render, and the franchise one has dropped the film itself.
	var sections []sdui.Node
	findAll(node, sdui.TypeSection, &sections)
	titles := map[string]sdui.Node{}
	for _, sec := range sections {
		if s, _ := prop(sec, "title").(string); s != "" {
			titles[s] = sec
		}
	}
	franchise, ok := titles["The Matrix Collection"]
	if !ok {
		t.Fatalf("no franchise rail; sections = %v", titles)
	}
	var cards []sdui.Node
	findAll(franchise, sdui.TypePosterCard, &cards)
	if len(cards) != 1 {
		t.Fatalf("franchise rail has %d cards, want 1 — the work itself must be filtered out", len(cards))
	}
	if _, ok := titles["More like this"]; !ok {
		t.Fatalf("no similar rail; sections = %v", titles)
	}
}

// With a release behind the play button the panel describes that release —
// the mockups' "This playback". The probe data (ADR 0050) was already being
// resolved for the play action and discarded.
func TestDetailPanelDescribesTheReleaseBehindThePlayButton(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}
	fake := &fakeQueries{
		previewInLibrary: true,
		previewNodeID:    "n-1",
		previewMeta:      v1.ContentMetadata{Title: "A Film", Rating: 8},
		playablePart: v1.Part{
			ID: "p-1", NodeID: "n-1", Height: 2160, HDRFormat: "Dolby Vision",
			VideoCodec: "hevc", AudioCodec: "eac3", Container: "mkv", SizeBytes: 21474836480,
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})

	panel, ok := find(node, "InfoPanel")
	if !ok {
		t.Fatal("no InfoPanel")
	}
	if got, _ := prop(panel, "heading").(string); got != "This playback" {
		t.Fatalf("heading = %q, want %q", got, "This playback")
	}
	rows, _ := prop(panel, "rows").([]any)
	got := map[string]string{}
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			l, _ := m["label"].(string)
			v, _ := m["value"].(string)
			got[l] = v
		}
	}
	// The panel answers what a viewer is about to *get*, in the design's terms:
	// the quality as it is written on a box and the audio as it is spoken about.
	// The codec, the container and the byte count moved to the facts grid, which
	// is where a question about the file rather than the viewing belongs.
	for label, want := range map[string]string{
		"Quality": "4K HDR", "Audio": "EAC3",
	} {
		if got[label] != want {
			t.Fatalf("row %q = %q, want %q (rows: %v)", label, got[label], want, got)
		}
	}
	// And it does not answer what it cannot. There is no device registry, so
	// there is no "Playing on" row rather than one naming the server.
	if _, present := got["Playing on"]; present {
		t.Errorf("panel claims a playback device; Mosaic has no device registry (rows: %v)", got)
	}
}

// An unprobed release reports no dimensions and no size (ADR 0050 relays
// unprobed rather than failing). Those rows must be absent, not "0p" and "0 B".
func TestDetailPanelOmitsUnprobedFacts(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "tt2", NativeType: "movie", MediaType: v1.MediaMovie}
	fake := &fakeQueries{
		previewInLibrary: true,
		previewNodeID:    "n-2",
		previewMeta:      v1.ContentMetadata{Title: "Unprobed", Year: 2020},
		playablePart:     v1.Part{ID: "p-2", NodeID: "n-2", Container: "mkv"},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})
	panel, ok := find(node, "InfoPanel")
	if !ok {
		t.Fatal("no InfoPanel")
	}
	rows, _ := prop(panel, "rows").([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		l, _ := m["label"].(string)
		if l == "Quality" || l == "Size" {
			t.Fatalf("unprobed release still reported %q = %v", l, m["value"])
		}
	}
}

// PlaybackSources answers with whatever a test staged, and an empty set
// otherwise — which is the no-candidate state rather than a failure.
func (f *fakeQueries) PlaybackSources(_ context.Context, q app.PlaybackSourcesQuery) (app.PlaybackSourcesResult, error) {
	if f.sourcesErr != nil {
		return app.PlaybackSourcesResult{}, f.sourcesErr
	}
	src := f.sources[string(q.NodeID)]
	return app.PlaybackSourcesResult{Sources: src, Total: len(src)}, nil
}
