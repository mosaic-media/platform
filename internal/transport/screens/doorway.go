// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
)

// The doorway (ADR 0101, carrying ADR 0098's two states).
//
// It is a screen like any other — composed from the vocabulary that already
// exists, emitted by the Platform, drawn by a client that bundles nothing. What
// makes it different is only when it is asked for: before a session, through
// AuthService.Bootstrap, which sends the skin and the definitions it needs
// alongside it because a client at this point has neither.
//
// **The server picks which one.** The client is not told which state it is in;
// it is shown one. That is why there is no screen name on the wire and no
// params — a doorway is not a route, and a client that could ask for the setup
// tree could ask for it on a claimed server.
//
// Both states now carry a form. What each form's control emits is an ordinary
// SDUI action, dispatched back through AuthService.Invoke — the pre-session
// counterpart of the session transport's Invoke, which exists because there is
// no session here and therefore no push lane for an outcome to arrive on. The
// client interprets none of it.

// Doorway action names. These are the complete list of what a client with no
// session can invoke, and the pre-session dispatch switch is where that list is
// enforced — the same arrangement the session transport uses, for the same
// reason: a surface a client can name and the server cannot map does not exist.
const (
	ClaimServerAction = "claimServer"
	SignInAction      = "signIn"
)

// The setup wizard's field names. Each is the name an input writes into the
// doorway's one State scope and the key the claim command reads back out, so
// the two spellings cannot drift; they are also the names a field-level
// rejection travels under (ADR 0089), which is the third place the same string
// has to match.
const (
	fieldServerName      = "serverName"
	fieldUsername        = "username"
	fieldDisplayName     = "displayName"
	fieldPassword        = "password"
	fieldConfirmPassword = "confirmPassword"
	fieldStreamSource    = "streamSource"
	fieldStep            = "step"
	// fieldFormError is where a client writes a rejection that belongs to the
	// submission rather than to any one field — a wrong password, a service
	// that was down. Declaring it is what asks to be told; a form binds some
	// node's text to it and thereby decides where the message appears. A door
	// that did not declare it would simply show nothing, which is the right
	// behaviour for a form that did not ask.
	fieldFormError = "formError"
)

// DoorwayModel is everything the doorway is drawn from.
type DoorwayModel struct {
	State app.ServerState
	Setup app.SetupOptions
}

// Doorway builds the pre-session tree for the state this server is in.
func Doorway(model DoorwayModel) sdui.Node {
	if model.State.Degraded {
		return degradedDoorway(model.State)
	}
	if !model.State.Claimed {
		return setupDoorway(model)
	}
	return signInDoorway(model.State)
}

// doorwayTitle is what the door says it is: the household's name for this
// server once it has one, and the product name until then.
//
// A server name is the one thing about the install disclosed here, and it is
// disclosed on purpose — it is what tells a person they are looking at the
// right door on a network with more than one of anything. Nothing else about
// the install is: not how many accounts exist, not what they are called, not
// which version this is.
func doorwayTitle(state app.ServerState) string {
	if state.Name != "" {
		return state.Name
	}
	return "Mosaic"
}

// signInDoorway is what a claimed server shows: the form.
//
// The submit collects the scope and merges it into the action's input
// (ADR 0096), so the username and password the person typed are what arrive —
// under the same names a field-level rejection comes back on.
func signInDoorway(state app.ServerState) sdui.Node {
	return ui.Screen(
		ui.Title(doorwayTitle(state)),
		ui.Subtitle("Sign in to continue."),
		ui.Section("",
			ui.State(
				ui.Vars(sdui.Vars(
					sdui.Var(fieldUsername, sdui.VarString, ""),
					sdui.Var(fieldPassword, sdui.VarString, ""),
					sdui.Var(fieldFormError, sdui.VarString, ""),
				)),
				ui.Form(
					// The refusal shows above the button, bound rather than set:
					// the server cannot know at render time what the answer will
					// be, and the client cannot know where this form wants it.
					ui.BindError(fieldFormError),
					ui.SubmitLabel("Sign in"),
					ui.SubmitAction(ui.Submit(ui.Invoke(SignInAction, nil), "")),
					ui.TextField("Username",
						ui.Name(fieldUsername),
						ui.Placeholder("Your username"),
						ui.Validators(map[string]any{"required": true})),
					ui.TextField("Password",
						ui.Name(fieldPassword),
						ui.InputType("password"),
						ui.Placeholder("Your password"),
						ui.Validators(map[string]any{"required": true})),
				),
			),
		),
	).Build()
}

// degradedDoorway is what a server that cannot read its own accounts shows.
//
// It is the third state and it is not one ADR 0098 named, because the answer it
// replaces did not exist to be wrong yet: ServerState falls back to "claimed"
// when the user store will not answer, so before this a database outage drew a
// sign-in form that refused every attempt with "invalid credentials". A door
// that says the lock is broken is worth more than one that says your key is.
func degradedDoorway(state app.ServerState) sdui.Node {
	return ui.Screen(
		ui.Title(doorwayTitle(state)),
		ui.Subtitle("This server is not answering."),
		ui.Section("",
			ui.Banner("Mosaic cannot reach its database, so it cannot sign anyone in. "+
				"Nothing has been lost — try again once the server is back.", ui.ToneDanger),
		),
	).Build()
}

// setupDoorway is what an unclaimed server shows: ADR 0098's four steps.
//
// **The steps are one tree with one scope, not four trees.** Each step is a Box
// that renders only while the scope's `step` variable names it, and Continue is
// a SetValue rather than a round trip. The alternative — the server returning
// the next step each time — would mean the password collected in step two
// travelling back down inside the tree for steps three and four, which is a
// worse property than any amount of client-side branching.
//
// Six steps became four in ADR 0098 and four is what is here. The concept's
// library and playback steps are gone because Mosaic has no filesystem scanner
// and decides playback per stream, and there is no metadata credential to
// collect because official builds carry project credentials (ADR 0105) over a
// Cinemeta floor that needs none. What is left is genuinely everything a
// household must decide.
func setupDoorway(model DoorwayModel) sdui.Node {
	return ui.Screen(
		ui.Title("Set up Mosaic"),
		ui.Subtitle("Nobody has claimed this server yet. Four steps, and you are the first account."),
		ui.Section("",
			ui.State(
				ui.Vars(sdui.Vars(
					// step is a string rather than a number because the predicate
					// vocabulary compares equality against literals and a string
					// reads the same in the tree as it does in the predicate.
					sdui.Var(fieldStep, sdui.VarString, "server"),
					sdui.Var(fieldServerName, sdui.VarString, ""),
					sdui.Var(fieldDisplayName, sdui.VarString, ""),
					sdui.Var(fieldUsername, sdui.VarString, ""),
					sdui.Var(fieldPassword, sdui.VarString, ""),
					sdui.Var(fieldConfirmPassword, sdui.VarString, ""),
					sdui.Var(fieldStreamSource, sdui.VarString, ""),
					sdui.Var(fieldFormError, sdui.VarString, ""),
				)),
				setupRail(),
				setupServerStep(),
				setupAdministratorStep(),
				setupStreamSourceStep(model.Setup),
				setupReviewStep(),
			),
		),
	).Build()
}

// setupSteps is the rail, in order: the key each step's Box tests for and the
// label the rail shows.
var setupSteps = []struct{ key, label string }{
	{"server", "Server"},
	{"administrator", "Administrator"},
	{"source", "Streams"},
	{"review", "Review"},
}

// setupRail draws where you are.
//
// Every step is always drawn, and the current one is a filled badge rather than
// a differently-worded one: the rail's job is to say how much is left, and a
// rail that only showed the step you are on would not do it. It is not
// navigable — a step you have not filled in is not somewhere you can jump to,
// and drawing a control that refuses is the dead end ADR 0036 exists to remove.
func setupRail() ui.El {
	pills := make([]ui.El, 0, len(setupSteps)*2)
	for i, step := range setupSteps {
		// One badge per step per state, each hidden unless the scope agrees. Two
		// nodes rather than one with a bound tone, because tone is a closed set
		// the client maps to colours and a binding into it would be a value the
		// contract cannot check.
		pills = append(pills,
			ui.Box(
				ui.VisibleWhen(map[string]any{"field": fieldStep, "equals": step.key}),
				ui.Badge(stepLabel(i, step.label), ui.ToneInfo)),
			ui.Box(
				ui.VisibleWhen(map[string]any{"not": map[string]any{"field": fieldStep, "equals": step.key}}),
				ui.Badge(stepLabel(i, step.label), ui.ToneNeutral)),
		)
	}
	return ui.Stack("horizontal", 2, pills...)
}

// stepLabel numbers a step in the rail. The number is in the label rather than
// beside it because a badge draws one run of text, and "2 · Administrator" is
// one phrase a screen reader announces once.
func stepLabel(index int, label string) string {
	return string(rune('1'+index)) + " · " + label
}

// step wraps a wizard step's contents in the Box that decides whether it is on
// screen, with the Back and Continue controls beneath it.
//
// back is empty on the first step, and next is empty on the last — the last
// step's control is the submit, which belongs to the form rather than to the
// navigation.
func step(key string, back, next string, body ...ui.El) ui.El {
	controls := []ui.El{}
	if back != "" {
		controls = append(controls, ui.Button("Back", "ghost",
			ui.OnTap(ui.SetValue(fieldStep, back))))
	}
	if next != "" {
		controls = append(controls, ui.Button("Continue", "primary",
			ui.OnTap(ui.SetValue(fieldStep, next))))
	}
	children := append([]ui.El{}, body...)
	if len(controls) > 0 {
		children = append(children, ui.Stack("horizontal", 3, controls...))
	}
	return ui.Box(
		ui.VisibleWhen(map[string]any{"field": fieldStep, "equals": key}),
		ui.Stack("vertical", 5, children...))
}

// setupServerStep names the machine.
func setupServerStep() ui.El {
	return step("server", "", "administrator",
		ui.Banner("This is the name the household will see on every device. "+
			"It is kept in a file beside Mosaic rather than in its database, so it still answers "+
			"when the database does not.", ui.ToneInfo),
		ui.TextField("Server name",
			ui.Name(fieldServerName),
			ui.Placeholder("The living room box"),
			ui.Help("You can change this later in Settings."),
			ui.Validators(map[string]any{"required": true})),
	)
}

// setupAdministratorStep creates the owner.
//
// It says what the account is for rather than only what to type. This is the
// one account that can create every other one, and somebody setting up a
// household server for the first time has no way to know that from four labelled
// boxes.
func setupAdministratorStep() ui.El {
	return step("administrator", "server", "source",
		ui.Banner("The first account owns the server: it can create everyone else and change "+
			"anything. Each person in the household gets their own account afterwards, with their "+
			"own progress and history.", ui.ToneInfo),
		ui.TextField("Your name",
			ui.Name(fieldDisplayName),
			ui.Placeholder("Alex")),
		ui.TextField("Username",
			ui.Name(fieldUsername),
			ui.Placeholder("alex"),
			ui.Help("What you sign in with. No spaces."),
			ui.Validators(map[string]any{"required": true})),
		ui.TextField("Password",
			ui.Name(fieldPassword),
			ui.InputType("password"),
			ui.Help("At least 8 characters."),
			ui.Validators(map[string]any{"required": true, "minLength": 8})),
		ui.TextField("Confirm password",
			ui.Name(fieldConfirmPassword),
			ui.InputType("password"),
			ui.Validators(map[string]any{"required": true, "matches": fieldPassword})),
	)
}

// setupStreamSourceStep asks where streams come from.
//
// It is the only step that reads anything, and the only one that can offer
// nothing. Metadata and catalogs work on a fresh install with no credential at
// all (ADR 0072), so a household that skips this gets a Mosaic it can browse
// and cannot play — which is a real state, reached deliberately, and recoverable
// from Settings. Saying that is better than making the step mandatory over a
// repository that might not be reachable during setup.
func setupStreamSourceStep(options app.SetupOptions) ui.El {
	body := []ui.El{
		ui.Banner("Mosaic finds titles and artwork on its own. Playing them needs a source, "+
			"which is a module you install — and which will ask for its own account details once "+
			"you are inside.", ui.ToneInfo),
	}
	switch {
	case options.Problem != "":
		body = append(body, ui.Banner(options.Problem, ui.ToneWarning))
	case len(options.StreamSources) == 0:
		body = append(body, ui.Banner("This repository offers no stream sources. "+
			"You can add one from Settings later.", ui.ToneWarning))
	default:
		choices := make([]any, 0, len(options.StreamSources)+1)
		choices = append(choices, map[string]any{"value": "", "label": "Skip for now"})
		for _, s := range options.StreamSources {
			choices = append(choices, map[string]any{
				"value": s.ModuleID,
				"label": streamSourceLabel(s),
			})
		}
		body = append(body,
			ui.Select("Stream source", ui.Name(fieldStreamSource), ui.Options(choices)),
			ui.Text(
				ui.Prop("text", "Mosaic will download and verify the module you pick, and install it "+
					"as your account once setup finishes."),
				ui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted"})))
	}
	return step("source", "administrator", "review", body...)
}

// streamSourceLabel names a catalogued module for the picker: what it is called
// and, where it has one, its own sentence about itself from its signed
// manifest. The version is deliberately absent — it is not a choice anybody
// makes at setup, and an install pins whatever the repository offered anyway.
func streamSourceLabel(e app.ExtensionCatalogueEntry) string {
	name := e.Name
	if name == "" {
		name = e.ModuleID
	}
	if e.Description == "" {
		return name
	}
	return name + " — " + e.Description
}

// setupReviewStep shows what is about to happen and submits.
//
// The values are read back out of the scope through bindings, so the review is
// the same data the submit will send rather than a second copy assembled
// server-side — which could not exist anyway, since the server has not been
// told any of it yet. The password is not among them: a review screen that
// prints the password back is a review screen somebody photographs.
func setupReviewStep() ui.El {
	return step("review", "source", "",
		ui.InfoPanel(ui.Rows([]any{
			map[string]any{"label": "Server name", "value": sdui.Bind(fieldServerName)},
			map[string]any{"label": "Administrator", "value": sdui.Bind(fieldUsername)},
		})),
		// The third step's answer, in the two states it has. Two boxes rather
		// than one bound row, because "none" is a sentence and the module id is a
		// value, and a row that rendered an empty string for the first would look
		// like a step that had not been asked.
		ui.Box(
			ui.VisibleWhen(map[string]any{"field": fieldStreamSource, "notEmpty": true}),
			ui.InfoPanel(ui.Rows([]any{
				map[string]any{"label": "Stream source", "value": sdui.Bind(fieldStreamSource)},
			}))),
		ui.Box(
			ui.VisibleWhen(map[string]any{"not": map[string]any{"field": fieldStreamSource, "notEmpty": true}}),
			ui.InfoPanel(ui.Rows([]any{
				map[string]any{"label": "Stream source", "value": "None — add one later in Settings"},
			}))),
		// Either the claim, or what is stopping it. **Not both, and never
		// neither.**
		//
		// A submit whose scope fails validation is refused by the client before
		// anything is sent, and the fields it marks are on steps two and three —
		// which are not on screen from here. So a review step that always drew
		// the button had a state where pressing it did nothing at all and said
		// nothing at all, which is the worst thing a final step can do. The
		// predicate is over the same scope the submit collects, so the two
		// cannot disagree about whether the form is ready.
		ui.Box(
			ui.VisibleWhen(map[string]any{"all": []any{
				map[string]any{"field": fieldServerName, "notEmpty": true},
				map[string]any{"field": fieldUsername, "notEmpty": true},
				map[string]any{"field": fieldPassword, "notEmpty": true},
			}}),
			ui.Stack("vertical", 5,
				ui.Banner("Claiming this server creates that account, gives it every permission, and "+
					"signs you in on this device. Nobody else can claim it afterwards.", ui.ToneInfo),
				ui.Form(
					ui.BindError(fieldFormError),
					ui.SubmitLabel("Claim this server"),
					ui.SubmitAction(ui.Submit(ui.Invoke(ClaimServerAction, nil), "")),
				))),
		ui.Box(
			ui.VisibleWhen(map[string]any{"any": []any{
				map[string]any{"not": map[string]any{"field": fieldServerName, "notEmpty": true}},
				map[string]any{"not": map[string]any{"field": fieldUsername, "notEmpty": true}},
				map[string]any{"not": map[string]any{"field": fieldPassword, "notEmpty": true}},
			}}),
			ui.Banner("Something above is still blank. Go back and fill in the server's name, and "+
				"the username and password for your account.", ui.ToneWarning)),
	)
}
