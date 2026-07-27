// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package screens is the Platform's SDUI emit-side (ADR 0029). It builds UINode
// trees from the application query services using the mosaic-sdui Go producer
// binding, and serves them through a name-keyed registry. It is a projection
// surface, exactly like a transport handler: every builder calls application
// query services and nothing else — no store, no transaction.
//
// Screens are Platform-emitted. A module contributes content through its
// provider roles (ADR 0027); the Platform owns how that content is shown. A
// screen's Action names an action the session transport dispatches (ADR 0061),
// so the emitted tree and the data its actions drive share one transport.
//
// The package is split one screen to a file — home.go, search.go, catalog.go,
// detail.go, shell.go, settings.go — over the shared card builder (card.go) and
// the small DOM/param helpers (build.go). This file holds the Service, the
// name→builder dispatch, and the vocabulary the builders and their Navigate
// actions agree on.
package screens

import (
	"context"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Screen names. A screen is reached both through Render (the dispatch below) and
// through a Navigate action another screen emits; naming them once keeps the two
// sides from drifting.
const (
	screenShell  = "shell"
	screenHome   = "home"
	screenSearch = "search"
	// The expert-mode diagnostics screens (ADR 0058). Reaching any of them
	// requires telemetry.read; the affordance that leads here is hidden from
	// anyone without it.
	screenLogs   = "logs"
	screenTraces = "traces"
	screenTrace  = "trace"
	// The background-work screens (ADR 0017). Same expert-mode group as the
	// telemetry ones and a different permission: `job.read` is what the
	// install did to itself, `telemetry.read` is what its users did.
	screenJobs        = "jobs"
	screenJob         = "job"
	screenCollections = "collections"
	screenCatalog     = "catalog"
	screenDetail      = "detail"
	screenSettings    = "settings"
	// screenLibrary is what the install owns (roadmap M2.1) — the first screen
	// over the object graph rather than over a provider. It sits beside
	// `collections` in the nav and is deliberately not the same screen: one is
	// this household's shelf, the other is a source's catalogue.
	screenLibrary = "library"
	// screenHistory is what this viewer has watched (ADR 0103). It is its own
	// screen rather than a settings panel because it is content — a grid of
	// things you can open — and the settings side of the app is a working
	// surface with no artwork on it.
	screenHistory = "history"
	// screenExtensions is the browse-and-install surface for extension modules
	// (ADR 0081). It is its own screen rather than a settings section because
	// listing what is available reaches the trusted repository over the network,
	// which should happen when a user opens it, not on every settings render.
	screenExtensions = "extensions"
	// setPreferenceMutation is the Invoke action the expert-mode toggle emits.
	setPreferenceMutation = "setPreference"
	// installExtensionAction and uninstallExtensionAction are the Invoke actions
	// the extensions screen emits (ADR 0081). They match the dispatch cases in the
	// session transport.
	installExtensionAction   = "installExtension"
	uninstallExtensionAction = "uninstallExtension"
	// revokeSessionAction ends one device's session (ADR 0102). It is the
	// tenth action a client can invoke, and the first that is about the
	// account rather than about content.
	revokeSessionAction = "revokeSession"
	// signOutAction ends the session it arrives on. It is distinct from
	// revokeSession, which names a target: this one cannot name one, so it
	// cannot be pointed at somebody else's device, and the affordance that
	// emits it needs to know nothing but that you are signed in.
	signOutAction = "signOut"
)

// Screen param keys. Each is written into a Navigate action's params by the
// screen that links onward and read back by stringParam in the screen it opens;
// a shared constant keeps the write and the read spelling the same key.
const (
	paramModuleID = "moduleId"
	// paramSection names which section of the settings screen is open — the
	// Platform's own sections, as paramModuleID names a module's.
	paramSection   = "section"
	paramCatalogID = "catalogId"
	// paramTitle carries a catalog's display name through the navigation that
	// opens it. A catalog is addressed by id, and only the provider's list knows
	// what it is called, so a screen that had to name one from its id alone could
	// not.
	paramTitle      = "title"
	paramNativeType = "nativeType"
	paramRef        = "ref"
	paramNodeID     = "nodeId"
	paramSeason     = "season"
	paramText       = "text"
	// paramMediaType focuses a search on one kind of thing. Distinct from
	// paramNativeType, which is a *provider's* own word for a type; this is the
	// Platform's canonical one (ADR 0015).
	paramMediaType = "mediaType"
	paramPage      = "page"
	paramLevel     = "level"
	paramComponent = "component"
	paramTrace     = "trace"
	// paramScreen names the screen the shell is being rendered around, so the
	// frame can wear the chrome that screen belongs to.
	paramScreen = "screen"
	paramOrder  = "order"
	paramFailed = "failed"
	// The jobs screens' params. paramStatus is a job status rather than a
	// generic one — nothing else on any screen filters by "status", and giving
	// it a shared name is how a key comes to mean two things.
	paramJobID     = "jobId"
	paramSessionID = "sessionId"
	paramStatus    = "status"
	paramKind      = "kind"
)

// Empty-state illustration keys the client maps to an icon.
//
// **These must be names in the client's glyph set**, and "collections" was not
// one — so every "no collections yet" state Mosaic has ever drawn rendered a
// blank circle where its illustration should be. Nothing reported it: the Icon
// primitive resolves an unknown name to nothing rather than failing, which is
// the right behaviour for an open vocabulary and the reason a typo here is
// invisible. The set is `IconName` in sdui-react's shared.tsx.
const (
	emptyIconCollections = "grid"
	emptyIconSearch      = "search"
	// emptyIconNotFound illustrates a route that resolves to nothing. "error"
	// rather than a not-found glyph of its own: growing the client's icon set is
	// a client release, and a wrong link is well enough served by the glyph that
	// already means "this did not work".
	emptyIconNotFound = "error"
)

// importContentMutation is the Platform mutation a detail's Add-to-library action
// invokes to materialise a virtual ref (ADR 0028).
const importContentMutation = "importContent"

// playPartAction is the action a detail's Play button emits (ADR 0047). It
// resolves server-side to a playback ticket and a Player surface rather than to
// a screen, which is why it is an action name rather than a route.
const playPartAction = "playPart"

// paramPartID is the key the play action carries its Part under.
const paramPartID = "partId"

// setWatchedAction marks an item watched or unwatched by explicit request
// (ADR 0046). The dispatch case has existed since playback state landed; until
// the detail screen's tick emitted it, the only way to finish something was to
// watch it to the end.
const setWatchedAction = "setWatched"

// contentQueries is the slice of the application query surface the screen
// builders read. Narrowing to an interface keeps the emit-side a projection of
// the services (like any transport handler) and makes the builders testable without
// standing up a full Service. *app.Service satisfies it.
type contentQueries interface {
	SearchAvailableContent(context.Context, app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error)
	// ListLibrary is one page of what the install owns, with the real total
	// (roadmap M2.1). It is the first read on this surface that goes to the
	// object graph rather than through a provider.
	ListLibrary(context.Context, app.ListLibraryQuery) (app.ListLibraryResult, error)
	// The library rules and their maintenance (ADR 0104). Reading and previewing
	// are what the settings panel is built from; the pass itself is triggered by
	// an action, not by rendering.
	ListLibraryRules(context.Context, app.ListLibraryRulesQuery) (app.ListLibraryRulesResult, error)
	PreviewLibraryRule(context.Context, app.PreviewLibraryRuleQuery) (app.PreviewLibraryRuleResult, error)
	ListModuleCatalogs(context.Context, app.ListModuleCatalogsQuery) (app.ListModuleCatalogsResult, error)
	ListCatalogItems(context.Context, app.ListCatalogItemsQuery) (app.ListCatalogItemsResult, error)
	GetContentNode(context.Context, v1.GetContentNodeQuery) (v1.GetContentNodeResult, error)
	PreviewContent(context.Context, app.PreviewContentQuery) (app.PreviewContentResult, error)
	ModuleSettingsUI(context.Context, app.ModuleSettingsUIQuery) (app.ModuleSettingsUIResult, error)
	// ListSettingsModules backs the settings index (ADR 0038). Without it the
	// host has to name one module by constant, which leaves every module that
	// contributes a screen after the first with no way in.
	ListSettingsModules(context.Context, app.ListSettingsModulesQuery) (app.ListSettingsModulesResult, error)
	// ListInstalledExtensions and ListAvailableExtensions back the extensions
	// screen (ADR 0081): the durable installed set, and what the trusted
	// repository offers to install. Available reaches the repository over the
	// network, which is why the screen that reads it is opened on demand rather
	// than folded into every settings render.
	ListInstalledExtensions(context.Context, app.ListInstalledExtensionsQuery) ([]app.InstalledExtension, error)
	ListAvailableExtensions(context.Context, app.ListAvailableExtensionsQuery) ([]app.ExtensionCatalogueEntry, error)
	// FirstPlayablePart backs the detail screen's Play affordance: a Work has no
	// bytes of its own, so the emit-side has to look one level down for an item
	// that does before it can offer Play at all (ADR 0036 — an affordance with
	// nothing behind it is the dead end this whole thread exists to remove).
	FirstPlayablePart(context.Context, v1.Caller, v1.NodeID) (v1.Part, bool, error)
	// GetCurrentUser answers "who am I" for the account panel. Every other user
	// read takes the id of the user to read, which is useless to a screen that
	// holds a session and wants to say "you".
	GetCurrentUser(context.Context, app.GetCurrentUserQuery) (app.GetCurrentUserResult, error)
	// The People panels (ADR 0069, roadmap M1.3). Every one of these was a
	// complete application service whose only callers were tests; naming them
	// here is the first half of the door, and the dispatch cases beside them are
	// the second.
	ListUsers(context.Context, app.ListUsersQuery) (app.ListUsersResult, error)
	GetUserByID(context.Context, app.GetUserByIDQuery) (app.GetUserByIDResult, error)
	GetRolesForUser(context.Context, app.GetRolesForUserQuery) (app.GetRolesForUserResult, error)
	GetEffectivePermissions(context.Context, app.GetEffectivePermissionsQuery) (app.GetEffectivePermissionsResult, error)
	// GrantablePermissions is what a grantor may confer, narrowed to what they
	// hold (ADR 0069). It is the offer side of the delegation rule and the
	// reason a create-account form can be honest about what it will grant.
	GrantablePermissions(context.Context, app.GrantablePermissionsQuery) (app.GrantablePermissionsResult, error)
	// ListNodeParts reads one item's releases. FirstPlayablePart answers the
	// same question about a *work* and deliberately will not walk into a
	// series' seasons to pick an episode; once the screen has picked one itself
	// this is how it asks what that episode actually holds.
	ListNodeParts(context.Context, app.ListNodePartsQuery) (app.ListNodePartsResult, error)
	// GetPlaybackState backs Resume (ADR 0046): a detail screen has to know
	// whether this viewer already started this item before it can decide
	// whether its primary action says Play or Resume.
	GetPlaybackState(context.Context, v1.GetPlaybackStateQuery) (v1.GetPlaybackStateResult, error)
	// ListInProgress backs the home's continue-watching rail (ADR 0046): the
	// items this viewer started and did not finish, most recently touched first.
	ListInProgress(context.Context, v1.ListInProgressQuery) (v1.ListInProgressResult, error)
	// ListPlaybackStates backs the watched marks on a season's episode rows — one
	// batched read over the season's nodes rather than a query per row.
	ListPlaybackStates(context.Context, v1.ListPlaybackStatesQuery) (v1.ListPlaybackStatesResult, error)
	// ListWatchHistory backs the history screen (ADR 0103): everything this
	// viewer has watched, finished or not. Deliberately not on the SDK's
	// ContentService — no module needs to read a person's viewing back, and the
	// one list ADR 0103 is most emphatic is private should not be on the surface
	// every installed extension holds.
	ListWatchHistory(context.Context, app.ListWatchHistoryQuery) (app.ListWatchHistoryResult, error)

	// The expert-mode reads (ADR 0058). Each authorises telemetry.read for
	// itself, so a screen calling one cannot be reached without the grant even
	// if the affordance leading to it were ever drawn by mistake.
	QueryTelemetryLogs(context.Context, app.QueryTelemetryLogsQuery) (app.QueryTelemetryLogsResult, error)
	ListTraces(context.Context, app.ListTracesQuery) (app.ListTracesResult, error)
	GetTrace(context.Context, app.GetTraceQuery) (app.GetTraceResult, error)
	// The background-work reads (ADR 0017). Each authorises job.read for
	// itself, so the screens cannot be reached without the grant however the
	// affordance was drawn.
	ListJobs(context.Context, app.ListJobsQuery) (app.ListJobsResult, error)
	GetJob(context.Context, app.GetJobQuery) (app.GetJobResult, error)
	// ListSessions backs the Devices section: where this account is signed in,
	// so a person can end a device they no longer have (ADR 0102).
	ListSessions(context.Context, app.ListSessionsQuery) (app.ListSessionsResult, error)
	// CallerCan decides whether an affordance is drawn at all. It is the only
	// method here that answers about authority rather than returning data, and
	// it never substitutes for the checks above.
	CallerCan(context.Context, v1.Caller, policy.Action, string) bool
	// ExpertModeEnabled reads the caller's own preference. Separate from
	// CallerCan because they answer different questions: one is authority, the
	// other is taste, and collapsing them is how a toggle becomes an access
	// control (ADR 0058).
	ExpertModeEnabled(context.Context, v1.Caller) bool
}

// Service renders named screens. It holds the query surface the builders read
// from, and an artwork rewriter that routes remote poster/backdrop URLs through
// the Platform's artwork proxy (ADR 0030); it opens nothing of its own.
type Service struct {
	content contentQueries
	artwork func(string) string
	// clock is what the screens read when a render depends on the time — how
	// long ago something was watched, so far. Injected rather than time.Now so a
	// test can assert "Yesterday" without waiting a day; nil means time.Now.
	clock func() time.Time
}

// NewService wires the emit-side to the application services. artwork rewrites a
// remote image URL to a Platform-proxied one; a nil rewriter passes URLs
// through unchanged (a Service built without the proxy).
func NewService(a *app.Service, artwork func(string) string) *Service {
	if artwork == nil {
		artwork = func(u string) string { return u }
	}
	return &Service{content: a, artwork: artwork}
}

// art proxies a non-empty image URL through the artwork rewriter (ADR 0030),
// passing an empty URL and a Service built without a rewriter through unchanged.
func (s *Service) art(u string) string {
	if u == "" || s.artwork == nil {
		return u
	}
	return s.artwork(u)
}

// Render builds the named screen for the caller, with the given params. An
// unknown name is NotFound. The returned Node is the root the client renders.
func (s *Service) Render(ctx context.Context, name string, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	switch name {
	case screenShell:
		// The shell wears the chrome of the screen it is framing, which the
		// caller names in params — the transport knows the route, this does not.
		return s.shellScreen(ctx, caller, stringParam(params, paramScreen))
	case screenHome:
		return s.homeScreen(ctx, caller)
	case screenSearch:
		return s.searchScreen(ctx, caller, params)
	case screenLibrary:
		return s.libraryScreen(ctx, caller, params)
	case screenCollections:
		return s.collectionsScreen(ctx, caller)
	case screenCatalog:
		return s.catalogScreen(ctx, caller, params)
	case screenDetail:
		return s.detailScreen(ctx, caller, params)
	case screenSettings:
		return s.settingsScreen(ctx, caller, params)
	case screenHistory:
		return s.historyScreen(ctx, caller)
	case screenExtensions:
		return s.extensionsScreen(ctx, caller)
	case screenLogs:
		return s.logsScreen(ctx, caller, params)
	case screenTraces:
		return s.tracesScreen(ctx, caller, params)
	case screenTrace:
		return s.traceScreen(ctx, caller, params)
	case screenJobs:
		return s.jobsScreen(ctx, caller, params)
	case screenJob:
		return s.jobScreen(ctx, caller, params)
	default:
		// A route that names no screen is a 404, not a transport error. Returning
		// NotFound here put the raw string "no screen named colletions" into the
		// content region, which is a diagnosis where a user needs a way out — and
		// a stale bookmark or a mistyped URL is an ordinary thing to do.
		return notFoundScreen(), nil
	}
}
