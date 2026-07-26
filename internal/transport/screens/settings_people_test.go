// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strings"
	"testing"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The People panels (roadmap M1.3). Every command behind them was complete and
// reachable by nobody; these assert the doors, and in particular the two
// properties that would be quietly wrong: the offer is bounded by what the
// caller holds (ADR 0069), and an affordance that would be refused is not drawn
// at all (ADR 0036).

func peopleService(users ...domain.User) *fakeQueries {
	fake := &fakeQueries{
		users:       users,
		rolesByUser: map[domain.UserID][]domain.Role{},
		allow:       map[string]bool{},
	}
	for _, a := range app.AdministratorActions() {
		fake.allow[string(a)] = true
	}
	return fake
}

func admin() domain.User {
	return domain.User{
		ID: "user-admin", Username: "alex", DisplayName: "Alex",
		Status: domain.UserActive, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func viewer() domain.User {
	return domain.User{
		ID: "user-sam", Username: "sam", DisplayName: "Sam",
		Status: domain.UserActive, CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
}

// The list is a way in, not just a list. Before this it drew four names and
// nothing to press.
func TestThePeoplePanelLeadsIntoEachAccount(t *testing.T) {
	fake := peopleService(admin(), viewer())
	fake.currentUser = admin()
	node := render(t, &Service{content: fake}, "settings", map[string]any{"section": "people"})

	var rows []sdui.Node
	findAll(node, "SettingsRow", &rows)
	var opened bool
	for _, r := range rows {
		if prop(r, "label") == "Sam" && r.GetProps().AsMap()["action"] != nil {
			opened = true
		}
	}
	if !opened {
		t.Error("no row opens Sam's account, so the list is still a list of things nobody can press")
	}
	if !strings.Contains(treeStrings(node), "Add a viewer") {
		t.Error("an administrator is offered no way to add anybody")
	}
}

// A caller who cannot create accounts is not invited to try. The failure this
// prevents is specific and memorable: being refused *after* typing somebody
// else's password into a form.
func TestSomebodyWhoCannotCreateAccountsIsNotOfferedTo(t *testing.T) {
	fake := peopleService(admin(), viewer())
	fake.currentUser = admin()
	fake.allow[string(app.ActionUserCreate)] = false
	node := render(t, &Service{content: fake}, "settings", map[string]any{"section": "people"})

	if strings.Contains(treeStrings(node), "Add a viewer") {
		t.Error("a caller who cannot create a user was offered a form that would refuse them")
	}
}

// ADR 0069's offer side: the form says what it will actually grant, computed
// from the grantor's own authority rather than from the preset.
func TestTheNewAccountFormNamesWhatItWillGrant(t *testing.T) {
	fake := peopleService(admin())
	fake.currentUser = admin()
	node := render(t, &Service{content: fake}, "settings", map[string]any{
		"section": "people", "new": app.PresetNameUser,
	})

	text := treeStrings(node)
	for _, want := range []string{"playback.write", "content.read"} {
		if !strings.Contains(text, want) {
			t.Errorf("the form does not say it will grant %q", want)
		}
	}
	// And it does not offer what the preset does not carry. A viewer preset
	// conferring user.create would be the escalation the whole rule exists to
	// prevent, and a form that *said* it would is how nobody notices.
	if strings.Contains(text, "user.create") {
		t.Error("the viewer form claims it will grant user.create")
	}
	if _, ok := find(node, "Form"); !ok {
		t.Error("there is no form to fill in")
	}
}

// A person's panel answers what they hold, in both the forms that differ: the
// roles they were given and the flattened set the policy engine decides with.
func TestAPersonsPanelSaysWhatTheyHold(t *testing.T) {
	fake := peopleService(admin(), viewer())
	fake.currentUser = admin()
	fake.rolesByUser["user-sam"] = []domain.Role{{
		ID: "role-1", Name: "User", Permissions: []domain.Permission{"content.read", "playback.write"},
	}}
	node := render(t, &Service{content: fake}, "settings", map[string]any{
		"section": "people", "userId": "user-sam",
	})

	text := treeStrings(node)
	for _, want := range []string{"sam", "User", "content.read"} {
		if !strings.Contains(text, want) {
			t.Errorf("the panel does not mention %q", want)
		}
	}
	var buttons []sdui.Node
	findAll(node, "Button", &buttons)
	var suspend bool
	for _, b := range buttons {
		if prop(b, "label") == "Suspend" {
			suspend = true
		}
	}
	if !suspend {
		t.Error("an administrator is offered no way to suspend somebody else")
	}
}

// An account holding nothing cannot sign in, which is a state a half-finished
// creation leaves behind. The panel names it and offers the step that was
// missed rather than looking like an ordinary account.
func TestAnAccountWithNoRoleSaysSoAndOffersOne(t *testing.T) {
	fake := peopleService(admin(), viewer())
	fake.currentUser = admin()
	node := render(t, &Service{content: fake}, "settings", map[string]any{
		"section": "people", "userId": "user-sam",
	})

	text := treeStrings(node)
	if !strings.Contains(text, "cannot sign in yet") {
		t.Error("an account with no role reads as an ordinary one")
	}
	if !strings.Contains(text, "Grant User") {
		t.Error("nothing offers to give the account a role")
	}
}

// Suspending yourself is the one outcome with no recovery: on the ordinary
// household install there is one administrator, and they would lock themselves
// out with nothing left to unlock it with.
func TestYouAreNotOfferedAControlThatLocksYouOut(t *testing.T) {
	fake := peopleService(admin())
	fake.currentUser = admin()
	node := render(t, &Service{content: fake}, "settings", map[string]any{
		"section": "people", "userId": "user-admin",
	})

	var buttons []sdui.Node
	findAll(node, "Button", &buttons)
	for _, b := range buttons {
		if prop(b, "label") == "Suspend" {
			t.Error("the panel offers to suspend the account it is being read by")
		}
	}
	// And says why, rather than leaving a control that is simply missing.
	if !strings.Contains(treeStrings(node), "lock you out") {
		t.Error("nothing explains why the control is absent")
	}
}

// A suspended account offers the way back, and says what suspension did and did
// not cost — because "suspended" on its own reads as "deleted" to most people.
func TestASuspendedAccountOffersReactivation(t *testing.T) {
	suspended := viewer()
	suspended.Status = domain.UserSuspended
	fake := peopleService(admin(), suspended)
	fake.currentUser = admin()
	node := render(t, &Service{content: fake}, "settings", map[string]any{
		"section": "people", "userId": "user-sam",
	})

	text := treeStrings(node)
	if !strings.Contains(text, "Reactivate") {
		t.Error("a suspended account cannot be reactivated from its own panel")
	}
	if !strings.Contains(text, "nothing it watched has been lost") {
		t.Error("the panel does not say what suspension left alone")
	}
}

// treeStrings flattens every string a tree carries — props, labels, messages —
// so a test can assert what a screen says without knowing which prop of which
// node it said it in.
func treeStrings(n sdui.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	writeStrings(n.GetProps().AsMap(), &b)
	for _, c := range n.GetChildren() {
		b.WriteString(treeStrings(c))
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			b.WriteString(treeStrings(c))
		}
	}
	return b.String()
}

func writeStrings(v any, b *strings.Builder) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
		b.WriteString(" ")
	case map[string]any:
		for _, sub := range t {
			writeStrings(sub, b)
		}
	case []any:
		for _, sub := range t {
			writeStrings(sub, b)
		}
	}
}
