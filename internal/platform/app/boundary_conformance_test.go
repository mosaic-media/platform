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
// put it through policy before doing anything else. Nothing in Go enforces
// that: there are no annotations and no runtime proxies, so a handler that
// simply omits the preamble compiles, passes its own tests, and serves reads
// to anyone holding a made-up session id. The rule has been documentation and
// developer discipline, which is exactly the arrangement that let a helper
// re-run the gates ten times in a loop without anyone noticing either.
//
// This suite is the enforcement. It has two halves and needs both:
//
//   - boundaryCases asserts the *behaviour*. Each caller-bearing method is
//     called twice — once with a session that does not exist, once with a real
//     session holding no grants — and must answer Unauthenticated and then
//     PermissionDenied. A handler that forgot either gate fails here.
//   - TestBoundaryTableCoversEveryCallerBearingMethod asserts the table is
//     *complete*, by reflecting over *app.Service and demanding that every
//     method carrying a v1.Caller or a domain.SessionID appears above. A new
//     handler cannot be added without a row, which is the property an
//     annotation would have given for free and a hand-written table would not.
//
// What it deliberately does not check is that each handler names the *right*
// action. Only a declared action-per-handler table could, and that table would
// itself be the thing to keep in sync. Omission is the failure that happens;
// this catches omission.

// caller builds the opaque published caller for a session id (ADR 0017).
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
	// CallerCan answers whether an affordance should be *drawn*, not whether
	// an operation may proceed (ADR 0058). It returns a bool and no
	// authorized, so nothing downstream can mistake its answer for the proof
	// ADR 0066 requires — the screens and services behind every affordance run
	// the real boundary themselves, and a test asserts telemetry.read is
	// enforced there.
	//
	// Holding it to the entry-point contract would be actively wrong: an entry
	// point must deny, and this one must *answer* — returning "no" for an
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
}

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
		// Three results rather than two, so discard does not fit. It is the one
		// entry point that reports playability as well as an error, because the
		// screens transport asks it a yes/no question (ADR 0036) and must still
		// be able to tell "no bytes here" from "your session expired".
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
		{"ListModuleCatalogs", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller(sid)}))
		}},
		{"ListCatalogItems", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
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
		// runs with the Platform's authority (ADR 0081), so both refuse an unknown
		// session and an ungranted caller like any other administrator action. The
		// rejection lands at the boundary, before the injected manager is reached —
		// the Service in this suite has none, which is exactly why the rejection
		// must come first.
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
		// A write on a read path, which is why it is worth having a row here
		// rather than an exemption: recording a probe is triggered by playing,
		// but it mutates the content graph and must refuse an ungranted caller
		// like any other mutation.
		{"RecordPartProbe", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.RecordPartProbe(ctx, app.RecordPartProbeCommand{
				Caller: caller(sid), PartID: "part-1", Probe: []byte(`{"v":1}`),
			}))
		}},

		// --- playback state (ADR 0046) ---
		//
		// The first per-user rows in the content domain, and the boundary matters
		// more here than anywhere above it: these methods resolve *whose* state
		// they touch from the caller's own session, so a session that does not
		// authenticate must never reach a store. There is no user parameter to
		// get wrong, and this is what keeps it that way.
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

		// --- user preferences and telemetry reads ---
		{"SetUserPreference", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.SetUserPreference(ctx, app.SetUserPreferenceCommand{
				Caller: caller(sid), Key: "expert_mode", Value: []byte(`true`),
			}))
		}},
		{"GetUserPreferences", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetUserPreferences(ctx, app.GetUserPreferencesQuery{Caller: caller(sid)}))
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
		{"GetTrace", func(ctx context.Context, s *app.Service, sid domain.SessionID) error {
			return discard(s.GetTrace(ctx, app.GetTraceQuery{Caller: caller(sid), TraceID: "trace-1"}))
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
			return discard(s.RevokeSession(ctx, app.RevokeSessionCommand{
				CallerSessionID: sid, TargetSessionID: "session-target",
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

// TestBoundaryTableCoversEveryCallerBearingMethod is the half that makes the
// two tests above a guarantee rather than a sample. Reflection finds every
// exported method that takes a caller in any form; the table and the exemption
// list must between them account for all of them. Add a handler, forget a row,
// and this fails on the same commit.
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
	// exists is stale, and a stale row is how a table quietly stops covering
	// what it claims to.
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
// take. That is the structural definition of an entry point: a method holding a
// caller is one that has to establish who it is before acting. Methods that take
// an already-resolved domain.UserID are inside the boundary by construction and
// are not listed.
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
