// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The boundary conformance suite.
//
// Every exported Service method that accepts a caller must authenticate it and
// put it through policy before doing anything else. Nothing in Go enforces that:
// a handler that omits the preamble compiles, passes its own tests, and serves
// reads to anyone holding a made-up session id.
//
// The suite has two halves and needs both:
//
//   - boundaryCases asserts the behaviour. Each caller-bearing method is called
//     twice — once with a session that does not exist, once with a real session
//     holding no grants — and must answer Unauthenticated and then
//     PermissionDenied. A handler that forgot either gate fails here.
//   - TestBoundaryTableCoversEveryCallerBearingMethod asserts the table is
//     complete, by reflecting over *app.Service and demanding that every method
//     carrying a v1.Caller or a domain.SessionID appears above. A new handler
//     cannot be added without a row.
//
// It deliberately does not check that each handler names the right action. Only
// a declared action-per-handler table could, and that table would itself be the
// thing to keep in sync. Omission is the failure this catches.

// caller builds the opaque published caller for a session id (platform#13).
func caller(session domain.SessionID) v1.Caller {
	return v1.Caller{Session: string(session)}
}

// discard drops a handler's result. The conformance property is about refusal,
// so only the error is under test, and every handler shares that shape once the
// result is dropped.
func discard[R any](_ R, err error) error { return err }

// boundaryCase is one exported Service method, invoked as the given session.
// The argument is shaped to pass the handler's own validation, so that a
// rejection can only have come from the boundary rather than from step 1.
type boundaryCase struct {
	method string
	call   func(context.Context, *app.Service, domain.SessionID) error
}

// boundaryExempt lists caller-bearing methods that are not entry points, with
// the reason each is safe to leave out. It exists so that "not in the table"
// is always a stated decision rather than an oversight.
var boundaryExempt = map[string]string{
	// CallerCan answers whether an affordance should be drawn, not whether an
	// operation may proceed (platform#36). It returns a bool and no authorized,
	// so nothing downstream can mistake its answer for the proof platform#41
	// requires; the screens and services behind every affordance run the real
	// boundary themselves.
	//
	// An entry point must deny, and this one must answer: returning "no" for an
	// unauthenticated caller is its correct behaviour, not a missing gate.
	"CallerCan": "affordance visibility hint; returns a bool, grants nothing, and the surfaces behind it each enter the boundary",
	// ExpertModeEnabled reads the caller's own display preference. It
	// authenticates but deliberately does not authorize: a preference is taste,
	// not authority, and requiring a permission to read your own setting would
	// make the plain interface fail for anyone who lacks it.
	//
	// It returns a bool and no authorized, and it can reveal nothing on its
	// own — the affordance it feeds is separately gated on telemetry.read, and
	// a test asserts a stored preference cannot surface it without the grant.
	"ExpertModeEnabled": "display preference; authenticates but does not authorize, returns a bool, and reveals nothing the permission has not already allowed",
	// HomeCompositionFor reads how the caller arranged their own home screen
	// (platform#59). Exempt on the terms above: it is a preference, not a scope
	// (platform#42). It returns which rows one person chose to hide, discloses no
	// content, and every row whose visibility it decides is separately reachable
	// by search and by link. A hidden row is not evidence of a permission.
	"HomeCompositionFor": "display preference; authenticates but does not authorize, returns no content, and hides nothing that is not reachable by other means",
	// LanguagePreferenceFor reads what the caller wants to hear and read
	// (platform#83). Exempt on the same terms as the two rows above: it
	// authenticates, deliberately does not authorize, and returns the caller's
	// own stored document and nothing else — no content, no other viewer's
	// setting, nothing a permission was protecting.
	//
	// It is also read on the path that starts a playback, and an entry point
	// must deny: denying would turn "your language preference could not be
	// read" into "you cannot watch this". It returns nil on every failure, and
	// the transport reads nil as the default.
	"LanguagePreferenceFor": "playback preference; authenticates but does not authorize, returns only the caller's own stored document, and degrades to the default rather than refusing a play",
	// SessionForCaller answers "which session is this credential" (platform#58).
	// It authenticates — an unknown credential is Unauthenticated, like
	// everywhere else — and deliberately does not authorize, because there is
	// no action to gate: it is a fact about the credential already presented
	// rather than a new thing to be permitted. It reveals nothing a caller
	// holding that credential could not already reach.
	"SessionForCaller": "resolves a credential to its own session; authenticates, has no action to authorize, and discloses nothing the credential does not already grant",
	// GetCurrentUser answers "who am I". It authenticates and deliberately does
	// not authorize, for the same reason as the row above: the record is the
	// caller's own and the session already proves it, so a permission to be told
	// what you have just proved is not an access control.
	//
	// Do not gate it on user.read: that is administrator authority, so every
	// ordinary viewer would be refused their own name and the account cluster on
	// every screen would draw a question mark for them.
	"GetCurrentUser": "the caller's own record; authenticates, has no action to authorize, and discloses nothing the session does not already prove",
}

// RefreshSession is not in either list, and cannot be: it carries no caller at
// all, so the reflection pass never sees it (platform#58).
//
// Step 2 of its boundary is the refresh token itself, exactly as the password
// credential plays that role in AuthenticateLocalUser — there is no caller
// session to look up, because the point of the call is that the caller's access
// token has expired. There is deliberately no step 3: refreshing continues an
// authority already granted, so a policy action gating it would be an action
// nobody could hold before they had refreshed. Its own tests cover what it must
// refuse.

func boundaryCases() []boundaryCase {
	return []boundaryCase{
		// --- content, published surface ---
		{"AddContentWork", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.AddContentWork(ctx, v1.AddContentWorkCommand{
				Caller: caller(sid), Title: "A Work", MediaType: v1.MediaMovie,
			}))
		}},
		{"AddContentChild", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.AddContentChild(ctx, v1.AddContentChildCommand{
				Caller: caller(sid), ParentID: "node-1", Title: "A Child",
				Kind: v1.NodeItem, ItemType: v1.ItemEpisode,
			}))
		}},
		{"AttachContentPart", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.AttachContentPart(ctx, v1.AttachContentPartCommand{
				Caller: caller(sid), NodeID: "node-1", Role: v1.PartEdition,
				Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/media/a.mkv"},
			}))
		}},
		{"SetContentArtwork", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SetContentArtwork(ctx, v1.SetContentArtworkCommand{
				Caller: caller(sid), NodeID: "node-1",
				Artwork: v1.Artwork{Poster: "https://cdn/p.jpg"},
			}))
		}},
		{"RelateContent", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.RelateContent(ctx, v1.RelateContentCommand{
				Caller: caller(sid), FromNodeID: "node-1", ToNodeID: "node-2",
				Type: "adaptation", Confidence: 1, Origin: v1.OriginUserConfirmed,
			}))
		}},
		{"BindContentSource", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.BindContentSource(ctx, v1.BindContentSourceCommand{
				Caller: caller(sid), NodeID: "node-1", SourceProvider: "stremio", SourceRef: "tt1",
				MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact, Status: v1.BindingConfirmed,
			}))
		}},
		{"ResolveContentBinding", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ResolveContentBinding(ctx, v1.ResolveContentBindingCommand{
				Caller: caller(sid), BindingID: "binding-1", Resolution: v1.ResolveConfirm,
			}))
		}},
		{"SearchContent", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SearchContent(ctx, v1.SearchContentQuery{
				Caller: caller(sid), Title: "anything",
			}))
		}},
		{"FindContentByExternalID", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
				Caller: caller(sid), Scheme: "imdb", Value: "tt1",
			}))
		}},
		{"GetContentNode", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetContentNode(ctx, v1.GetContentNodeQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		{"ListContentParts", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListContentParts(ctx, v1.ListContentPartsQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		{"ListNodeParts", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListNodeParts(ctx, app.ListNodePartsQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		// Three results rather than two, so discard does not fit: it reports
		// playability as well as an error, because the screens transport asks a
		// yes/no question (platform#24) and must still be able to tell "no bytes
		// here" from "your session expired".
		{"FirstPlayablePart", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			_, _, err := s.FirstPlayablePart(ctx, caller(sid), "node-1")
			return err
		}},

		// --- discovery and modules ---
		{"SearchAvailableContent", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{
				Caller: caller(sid), Text: "anything",
			}))
		}},
		{"PreviewContent", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PreviewContent(ctx, app.PreviewContentQuery{
				Caller: caller(sid),
				Ref:    v1.ContentRef{Provider: "stremio", NativeID: "tt1", NativeType: "movie"},
			}))
		}},
		{"ImportContent", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ImportContent(ctx, app.ImportContentCommand{
				Caller: caller(sid),
				Ref:    v1.ContentRef{Provider: "stremio", NativeID: "tt1", NativeType: "movie"},
			}))
		}},
		{"RefreshAvailability", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.RefreshAvailability(ctx, app.RefreshAvailabilityCommand{Caller: caller(sid)}))
		}},
		{"ListModuleCatalogs", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller(sid)}))
		}},
		{"ListCatalogItems", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
				Caller: caller(sid), ModuleID: "stremio", CatalogID: "top",
			}))
		}},
		// The cache-first browse reads (platform#30). They can answer from storage
		// without asking a source at all, so the boundary is the only thing
		// standing between a snapshot and anybody who asks for it.
		{"BrowseCatalogs", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.BrowseCatalogs(ctx, app.BrowseCatalogsQuery{Caller: caller(sid)}))
		}},
		{"BrowseCatalogItems", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
				Caller: caller(sid), ModuleID: "stremio", CatalogID: "top",
			}))
		}},
		{"ConfigureModule", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ConfigureModule(ctx, app.ConfigureModuleCommand{
				Caller: caller(sid), ModuleID: "stremio", Settings: []byte(`{}`),
			}))
		}},
		{"GetModuleSettings", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetModuleSettings(ctx, app.GetModuleSettingsQuery{
				Caller: caller(sid), ModuleID: "stremio",
			}))
		}},
		{"ModuleSettingsUI", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ModuleSettingsUI(ctx, app.ModuleSettingsUIQuery{
				Caller: caller(sid), ModuleID: "stremio",
			}))
		}},
		// The settings index authorises the same read as opening one of the
		// screens it lists: which modules are installed is not public just because
		// the list itself invokes nothing.
		{"ListSettingsModules", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListSettingsModules(ctx, app.ListSettingsModulesQuery{Caller: caller(sid)}))
		}},
		// Installing and uninstalling an extension changes which third-party code
		// runs with the Platform's authority (platform#51), so both refuse an
		// unknown session and an ungranted caller. The rejection must land at the
		// boundary, before the injected manager is reached: the Service in this
		// suite has none.
		{"InstallExtension", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.InstallExtension(ctx, app.InstallExtensionCommand{
				Caller: caller(sid), Repository: "mosaic-official", ModuleID: "stremio",
			}))
		}},
		{"UninstallExtension", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return s.UninstallExtension(ctx, app.UninstallExtensionCommand{
				Caller: caller(sid), ModuleID: "stremio",
			})
		}},
		{"ListInstalledExtensions", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListInstalledExtensions(ctx, app.ListInstalledExtensionsQuery{Caller: caller(sid)}))
		}},
		{"ListAvailableExtensions", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListAvailableExtensions(ctx, app.ListAvailableExtensionsQuery{Caller: caller(sid)}))
		}},
		{"ResolvePlayback", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ResolvePlayback(ctx, app.ResolvePlaybackQuery{
				Caller: caller(sid), PartID: "part-1",
			}))
		}},
		// The origin's correction for a dead cached link (platform#28). Called
		// from a transport rather than a client, which is why it earns a row
		// instead of an exemption: a ticket proves its own provenance and
		// nothing else, so the session it names must clear the boundary here.
		{"ReresolvePlayback", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ReresolvePlayback(ctx, caller(sid), "part-1", "browser"))
		}},
		{"PlaybackSources", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PlaybackSources(ctx, app.PlaybackSourcesQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		{"PlaybackSubtitles", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PlaybackSubtitles(ctx, app.PlaybackSubtitlesQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		{"PlayableAfterImport", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PlayableAfterImport(ctx, app.PlayableAfterImportQuery{
				Caller: caller(sid), WorkID: "work-1",
			}))
		}},
		// A write on a read path: recording a probe is triggered by playing, but
		// it mutates the content graph and must refuse an ungranted caller like
		// any other mutation.
		{"RecordPartProbe", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.RecordPartProbe(ctx, app.RecordPartProbeCommand{
				Caller: caller(sid), PartID: "part-1", Probe: []byte(`{"v":1}`),
			}))
		}},

		// --- playback state (platform#26) ---
		//
		// These methods resolve whose state they touch from the caller's own
		// session, so a session that does not authenticate must never reach a
		// store. There is no user parameter to get wrong, and these rows are
		// what keep it that way.
		{"RecordPlaybackProgress", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
				Caller: caller(sid), NodeID: "node-1", Position: time.Minute,
			}))
		}},
		{"SetPlaybackFinished", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SetPlaybackFinished(ctx, v1.SetPlaybackFinishedCommand{
				Caller: caller(sid), NodeID: "node-1", Finished: true,
			}))
		}},
		{"GetPlaybackState", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		{"ListPlaybackStates", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListPlaybackStates(ctx, v1.ListPlaybackStatesQuery{
				Caller: caller(sid), NodeIDs: []v1.NodeID{"node-1"},
			}))
		}},
		{"ListInProgress", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListInProgress(ctx, v1.ListInProgressQuery{Caller: caller(sid)}))
		}},
		{"ListWatchHistory", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListWatchHistory(ctx, app.ListWatchHistoryQuery{Caller: caller(sid)}))
		}},

		// --- user preferences and telemetry reads ---
		{"SetUserPreference", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SetUserPreference(ctx, app.SetUserPreferenceCommand{
				Caller: caller(sid), Key: "expert_mode", Value: []byte(`true`),
			}))
		}},
		{"GetUserPreferences", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetUserPreferences(ctx, app.GetUserPreferencesQuery{Caller: caller(sid)}))
		}},
		{"GetRoleByName", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetRoleByName(ctx, app.GetRoleByNameQuery{Caller: caller(sid), Name: "User"}))
		}},
		{"GrantablePermissions", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
				Caller: caller(sid), Preset: app.PresetNameUser,
			}))
		}},
		{"QueryTelemetryLogs", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{Caller: caller(sid)}))
		}},
		{"ListTraces", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListTraces(ctx, app.ListTracesQuery{Caller: caller(sid)}))
		}},
		// Reads the process rather than the store (sdk#9), and takes the same
		// gate for the same reason: an instrument's dimensions describe what a
		// module was asked to do.
		{"ListMetrics", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListMetrics(ctx, app.ListMetricsQuery{Caller: caller(sid)}))
		}},
		{"GetTrace", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetTrace(ctx, app.GetTraceQuery{Caller: caller(sid), TraceID: "trace-1"}))
		}},
		{"PurgeTelemetry", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PurgeTelemetry(ctx, app.PurgeTelemetryCommand{Caller: caller(sid)}))
		}},

		// --- background work (platform#13) ---
		//
		// These carry an ordinary caller like every row above, which is the
		// point: the system principal is a caller, not a bypass, so the same
		// handlers must still refuse an unknown session and an ungranted user.
		// A separate test asserts the system principal gets through the gate.
		{"ListJobs", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListJobs(ctx, app.ListJobsQuery{Caller: caller(sid)}))
		}},
		{"GetJob", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetJob(ctx, app.GetJobQuery{Caller: caller(sid), JobID: "job-1"}))
		}},

		// --- the session's own devices (platform#58) ---
		{"ListSessions", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListSessions(ctx, app.ListSessionsQuery{Caller: caller(sid)}))
		}},
		{"PurgeSessionTokens", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PurgeSessionTokens(ctx, app.PurgeSessionTokensCommand{Caller: caller(sid)}))
		}},

		// --- users, roles, sessions ---
		{"CreateLocalUser", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.CreateLocalUser(ctx, app.CreateLocalUserCommand{
				CallerSessionID: sid, Username: "someone", Email: "someone@example.com", Password: "irrelevant",
			}))
		}},
		{"ListUsers", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListUsers(ctx, app.ListUsersQuery{CallerSessionID: sid}))
		}},
		{"GetUserByID", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetUserByID(ctx, app.GetUserByIDQuery{CallerSessionID: sid, UserID: "user-1"}))
		}},
		{"SetUserStatus", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SetUserStatus(ctx, app.SetUserStatusCommand{
				CallerSessionID: sid, TargetUserID: "user-1", Status: domain.UserSuspended,
			}))
		}},
		{"CreateRole", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.CreateRole(ctx, app.CreateRoleCommand{
				CallerSessionID: sid, Name: "Editor", Permissions: []string{string(app.ActionContentRead)},
			}))
		}},
		{"GrantRole", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GrantRole(ctx, app.GrantRoleCommand{
				CallerSessionID: sid, UserID: "user-1", RoleID: "role-1",
			}))
		}},
		{"GetRolesForUser", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetRolesForUser(ctx, app.GetRolesForUserQuery{CallerSessionID: sid, TargetUserID: "user-1"}))
		}},
		{"GetGrantsForUser", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetGrantsForUser(ctx, app.GetGrantsForUserQuery{CallerSessionID: sid, TargetUserID: "user-1"}))
		}},
		{"GetEffectivePermissions", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetEffectivePermissions(ctx, app.GetEffectivePermissionsQuery{
				CallerSessionID: sid, TargetUserID: "user-1",
			}))
		}},
		{"RevokeSession", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			// Somebody else's session, which is the case that needs a grant.
			// Ending your own is signing out and is deliberately ungated, so a
			// case naming the caller's own session would assert the opposite of
			// what this suite is for. The harness seeds this one to another user.
			return discard(s.RevokeSession(ctx, app.RevokeSessionCommand{
				CallerSessionID: sid, TargetSessionID: boundaryOtherSession,
			}))
		}},

		// --- configuration ---
		{"DraftConfigVersion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
				CallerSessionID: sid, Payload: []byte(`{}`),
			}))
		}},
		{"ValidateConfigVersion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{
				CallerSessionID: sid, ConfigVersionID: "config-1",
			}))
		}},
		{"ActivateConfigVersion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{
				CallerSessionID: sid, ConfigVersionID: "config-1",
			}))
		}},
		{"GetConfigVersion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetConfigVersion(ctx, app.GetConfigVersionQuery{
				CallerSessionID: sid, ConfigVersionID: "config-1",
			}))
		}},
		{"GetActiveConfigVersion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetActiveConfigVersion(ctx, app.GetActiveConfigVersionQuery{CallerSessionID: sid}))
		}},
		{"GetPendingConfigVersion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetPendingConfigVersion(ctx, app.GetPendingConfigVersionQuery{CallerSessionID: sid}))
		}},

		// --- the resolution register (platform#74) ---
		//
		// Reading and resolving are separate rows because they are separate
		// powers: seeing that an extension will not start is diagnostic, and
		// uninstalling it changes what this install is. Raising a finding has
		// no row and must not: it takes no caller by design, because the code
		// that detects a problem is a boot path or a spool rather than
		// somebody's request.
		{"ListIssues", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListIssues(ctx, app.ListIssuesQuery{CallerSessionID: sid}))
		}},
		{"ApplySuggestion", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ApplySuggestion(ctx, app.ApplySuggestionCommand{
				CallerSessionID: sid, IssueID: "issue-1", Suggestion: "dismiss",
			}))
		}},

		// --- the library and its rules (platform#60) ---
		//
		// RunLibraryMaintenance acts as the system principal whoever triggers
		// it, and that must not weaken the gate on triggering it: an unknown
		// session and an ungranted caller are refused before anything acts as
		// anybody. A pass that authorised itself because it was going to act as
		// the install would run unbounded authority from an unauthenticated
		// request.
		{"ListLibrary", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListLibrary(ctx, app.ListLibraryQuery{Caller: caller(sid)}))
		}},
		// Reading a title out of the graph rather than from its provider
		// (platform#62). A content read like any other: the library is shared, so
		// everybody who may see it may see all of it, and nobody who may not
		// gets a description of it either.
		{"GetLibraryDetail", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{
				Caller: caller(sid), NodeID: "node-1",
			}))
		}},
		{"ListLibraryRules", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListLibraryRules(ctx, app.ListLibraryRulesQuery{Caller: caller(sid)}))
		}},
		{"CreateLibraryRule", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.CreateLibraryRule(ctx, app.CreateLibraryRuleCommand{
				Caller: caller(sid), Name: "Trending", Kind: domain.LibraryRuleCollection,
				ModuleID: "tmdb", CatalogID: "trending", NativeType: "movie",
			}))
		}},
		{"SetLibraryRuleEnabled", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SetLibraryRuleEnabled(ctx, app.SetLibraryRuleEnabledCommand{
				Caller: caller(sid), RuleID: "rule-1", Enabled: false,
			}))
		}},
		{"DeleteLibraryRule", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return s.DeleteLibraryRule(ctx, app.DeleteLibraryRuleCommand{
				Caller: caller(sid), RuleID: "rule-1",
			})
		}},
		{"PreviewLibraryRule", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.PreviewLibraryRule(ctx, app.PreviewLibraryRuleQuery{
				Caller: caller(sid), Name: "Trending", Kind: domain.LibraryRuleCollection,
				ModuleID: "tmdb", CatalogID: "trending", NativeType: "movie",
			}))
		}},
		{"RunLibraryMaintenance", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller(sid)}))
		}},
	}
}

// TestEveryEntryPointRejectsAnUnknownSession is gate 2 of the boundary order,
// asserted once per entry point rather than once per handler someone remembered
// to write a test for. A session id that was never issued must never reach
// state, whatever the rest of the argument says.
func TestEveryEntryPointRejectsAnUnknownSession(t *testing.T) {
	for _, tc := range boundaryCases() {
		t.Run(tc.method, func(t *testing.T) {
			db := newFakeDB()
			tr := &trace{}
			svc := newTestService(db, tr, testNow)

			err := tc.call(context.Background(), svc, "no-such-session")
			if err == nil {
				t.Fatalf("%s accepted a session that was never issued", tc.method)
			}
			if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
				t.Fatalf("%s: CategoryOf(err) = %s, want %s (err = %v)", tc.method, got, contracts.Unauthenticated, err)
			}
		})
	}
}

// boundaryOtherSession is a session the caller does not own, seeded for
// RevokeSession — the one entry point whose grant is only required when the
// target is not yours.
const boundaryOtherSession = domain.SessionID("session-somebody-else")

// TestEveryEntryPointRejectsACallerWithoutGrants is gate 3. The caller here is
// real — the session resolves — and holds no role at all, so the default-deny
// engine must refuse. A handler that authenticates and then forgets policy
// passes the test above and fails this one.
func TestEveryEntryPointRejectsACallerWithoutGrants(t *testing.T) {
	const session = domain.SessionID("session-nobody")

	for _, tc := range boundaryCases() {
		t.Run(tc.method, func(t *testing.T) {
			db := newFakeDB()
			tr := &trace{}
			db.seedSession(session, "user-nobody", testNow)
			// A session belonging to somebody else, for the one case whose
			// authorisation depends on whose it is.
			db.seedSession(boundaryOtherSession, "user-somebody-else", testNow)
			svc := newTestService(db, tr, testNow)

			err := tc.call(context.Background(), svc, session)
			if err == nil {
				t.Fatalf("%s served a caller holding no grants", tc.method)
			}
			if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
				t.Fatalf("%s: CategoryOf(err) = %s, want %s (err = %v)", tc.method, got, contracts.PermissionDenied, err)
			}
		})
	}
}

// TestBoundaryTableCoversEveryCallerBearingMethod makes the two tests above a
// guarantee rather than a sample. Reflection finds every exported method that
// takes a caller in any form; the table and the exemption list must between them
// account for all of them, so adding a handler without a row fails the build.
func TestBoundaryTableCoversEveryCallerBearingMethod(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range boundaryCases() {
		if covered[tc.method] {
			t.Errorf("%s appears twice in the boundary table", tc.method)
		}
		covered[tc.method] = true
	}

	var missing []string
	for _, name := range callerBearingMethods() {
		if covered[name] {
			continue
		}
		if _, exempt := boundaryExempt[name]; exempt {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("these exported Service methods take a caller but are not in the boundary table: %v\n"+
			"Add a row to boundaryCases, or an entry to boundaryExempt saying why the boundary does not apply.", missing)
	}

	// The reverse direction: an exemption or a row for a method that no longer
	// exists is how a table quietly stops covering what it claims to.
	known := map[string]bool{}
	for _, name := range callerBearingMethods() {
		known[name] = true
	}
	for name := range covered {
		if !known[name] {
			t.Errorf("boundaryCases has a row for %s, which is not a caller-bearing method on *app.Service", name)
		}
	}
	for name := range boundaryExempt {
		if !known[name] {
			t.Errorf("boundaryExempt names %s, which is not a caller-bearing method on *app.Service", name)
		}
	}
}

// callerBearingMethods reports the exported methods of *app.Service that accept
// a caller — either directly, or as a field of the command or query struct they
// take. That is the structural definition of an entry point. Methods taking an
// already-resolved domain.UserID are inside the boundary by construction and are
// not listed. It inspects only the struct's own fields, not embedded or nested
// ones, so a command that carries its caller indirectly would not be found.
func callerBearingMethods() []string {
	var (
		callerType  = reflect.TypeOf(v1.Caller{})
		sessionType = reflect.TypeOf(domain.SessionID(""))
	)
	carries := func(t reflect.Type) bool {
		if t == callerType || t == sessionType {
			return true
		}
		if t.Kind() != reflect.Struct {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			if ft := t.Field(i).Type; ft == callerType || ft == sessionType {
				return true
			}
		}
		return false
	}

	serviceType := reflect.TypeOf((*app.Service)(nil))
	var out []string
	for i := 0; i < serviceType.NumMethod(); i++ {
		m := serviceType.Method(i)
		// Index 0 is the receiver.
		for p := 1; p < m.Type.NumIn(); p++ {
			if carries(m.Type.In(p)) {
				out = append(out, m.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
