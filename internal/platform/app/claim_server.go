// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"strings"
	"unicode"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Claiming an unclaimed server (platform#54).
//
// **This is the only write in the Platform that no caller authorises**, and the
// only one that can be. Every command able to grant the first authority is
// itself policy-gated, so on a server with no accounts there is nobody who
// could be allowed to make one — which is exactly why the environment-variable
// bootstrap existed, and why it was the only way in.
//
// What stands in for authorisation is emptiness: the claim refuses the moment
// any user exists, and it re-checks inside the transaction so two people
// arriving together produce one owner and one Conflict rather than two owners.
// platform#54 accepts the threat this leaves — on a server reachable before
// somebody sits down to set it up, the first party to find it becomes its
// owner — and names the two mitigations that were deliberately not taken: a
// console claim token, and a claim window that closes after start-up. Neither
// is built here. They are increments, and inventing one of them quietly would
// be worse than the gap, because an operator would then be relying on a
// property nobody wrote down.
//
// The environment-variable bootstrap stays beside this, unchanged. A server
// seeded that way has a user, so it is already claimed and this refuses it —
// which is what makes the two paths safe to have at once.

// minPasswordLength is the shortest password the claim will accept.
//
// Eight rather than something longer, and no composition rules at all. This is
// the credential for a household media server on a home network, and the
// failure mode of a demanding policy here is a password written on a note by
// the television. Length is the only rule with evidence behind it; the rest is
// theatre that pushes people towards worse secrets.
const minPasswordLength = 8

// ClaimServerCommand is the whole of setup, submitted at once (platform#54).
//
// Four steps' worth of fields arrive together because the wizard collects them
// in the client's own form scope and submits once. That is not a shortcut: a
// server-driven step-per-round-trip would have to carry the password back down
// inside the tree for every step after the one that collected it.
type ClaimServerCommand struct {
	// ServerName is what the household calls this machine. It is written outside
	// PostgreSQL (platform#54), so it survives the database being down.
	ServerName string
	// The owner account.
	Username        string
	DisplayName     string
	Email           string
	Password        string
	ConfirmPassword string
	// DeviceID names the device claiming, because claiming signs you in and a
	// session belongs to a device.
	DeviceID domain.DeviceID
	// StreamSourceModuleID names the extension module the household chose as its
	// source of streams, if it chose one. Optional: metadata and catalogs work
	// on a fresh install with no credential at all (module-cinemeta#1), so skipping this
	// leaves a browsable Mosaic that cannot play, which is an honest state and a
	// recoverable one.
	//
	// The repository it comes from is **not** carried beside it. A setup form
	// that sent both would be a form a caller could point at a repository of
	// their own, on the one endpoint that runs unauthenticated; resolving it
	// from the catalogue the Platform already trusts means the only installable
	// things are the ones the Platform was going to offer anyway.
	StreamSourceModuleID string
}

// ClaimServerResult is the owner, their first session, and what became of the
// optional stream source.
type ClaimServerResult struct {
	User    domain.User
	Session domain.Session
	Tokens  domain.TokenPair
	// ServerName is what was recorded, which may be empty if the identity file
	// could not be written — see IdentityProblem.
	ServerName string
	// StreamSource is the module id that was installed, empty if none was asked
	// for or the install did not land.
	StreamSource string
	// StreamSourceProblem and IdentityProblem say what went wrong with the two
	// steps that are allowed to fail without failing the claim. They are
	// returned rather than logged and forgotten, because the person who just set
	// the server up is the only one who can act on either.
	StreamSourceProblem string
	IdentityProblem     string
}

// validateClaimServerCommand rejects per field (contracts#13).
//
// Every message here names the field it belongs to, because this is the first
// screen a person ever sees and the first form Mosaic ever asked anyone to
// fill in. A toast saying "invalid input" over a four-field form does not say
// which field, and the machinery to say so has existed and been unused since
// validation landed.
func validateClaimServerCommand(cmd ClaimServerCommand) error {
	var fields []contracts.FieldRejection
	reject := func(field, message string) {
		fields = append(fields, contracts.FieldRejection{Field: field, Message: message})
	}

	if strings.TrimSpace(cmd.ServerName) == "" {
		reject("serverName", "Give this server a name.")
	}
	switch {
	case strings.TrimSpace(cmd.Username) == "":
		reject("username", "Choose a username for your account.")
	case strings.ContainsFunc(cmd.Username, unicode.IsSpace):
		reject("username", "A username cannot contain spaces.")
	}
	switch {
	case cmd.Password == "":
		reject("password", "Choose a password for your account.")
	case len([]rune(cmd.Password)) < minPasswordLength:
		reject("password", "Use at least 8 characters.")
	}
	// Only checked once the password itself is acceptable. Telling somebody
	// their confirmation does not match a password that was itself refused is
	// two complaints about one mistake.
	if cmd.Password != "" && len([]rune(cmd.Password)) >= minPasswordLength && cmd.ConfirmPassword != cmd.Password {
		reject("confirmPassword", "The two passwords do not match.")
	}
	if cmd.DeviceID == "" {
		// Not a field on any form: the client supplies it. It is a bad request
		// rather than a rejection, because there is nothing for a person to fix.
		return contracts.NewError(contracts.InvalidArgument, "device id is required")
	}
	if len(fields) > 0 {
		// **The summary repeats the individual messages, and that is not
		// redundancy.** The wizard is four steps in one scope, and a rejection
		// about the password marks a field on step two while the person is
		// looking at step four — where the form-level line is the only thing
		// they can see. A generic "that did not work" there would be a refusal
		// with no way to act on it.
		//
		// It is also the case client-side validation stopped covering when the
		// steps became hidden branches rather than hidden fields: a Box that is
		// not rendered unmounts its inputs, and an unmounted input's rules
		// leave the scope, so the check that used to run before anything was
		// sent now runs here instead.
		return contracts.RejectFields(summarise(fields), fields...)
	}
	return nil
}

// summarise joins the field messages into the one line a form shows above its
// button. Each rejection is written to stand alone for exactly this reason —
// "Use at least 8 characters" reads as an instruction wherever it appears,
// where "too short" would need the field beside it to mean anything.
func summarise(fields []contracts.FieldRejection) string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Message)
	}
	return strings.Join(out, " ")
}

// ClaimServer creates the first account and signs it in.
//
// The command order is the usual one with steps 2 and 3 replaced rather than
// skipped: *nobody exists* is what authorises this, and it is checked twice —
// once before any work, and again inside the transaction, because the check and
// the write must not be separable by a second claimant.
func (s *Service) ClaimServer(ctx context.Context, cmd ClaimServerCommand) (ClaimServerResult, error) {
	// 1. validate command shape.
	if err := validateClaimServerCommand(cmd); err != nil {
		return ClaimServerResult{}, err
	}

	// 2-3. emptiness stands in for authentication and authorisation. This first
	// check is a courtesy — it turns the common case (somebody opening a claimed
	// server's setup page from a stale tab) into a clean refusal without opening
	// a transaction. The one that actually holds is inside it.
	if !s.ServerState(ctx).Claimable() {
		return ClaimServerResult{}, contracts.NewError(contracts.Conflict, "this server has already been claimed")
	}

	// **Resolved before the claim, not after.** The catalogue read is gated on
	// the server still being claimable — deliberately, so a claimed server never
	// reaches a remote repository for an unauthenticated caller — which means
	// asking for it after the commit asks a server that has just stopped being
	// claimable and gets nothing. It answered "Mosaic no longer offers
	// aiostreams" on the very claim that chose it, and no test saw it: every
	// one of them either had no extension manager or never got as far as the
	// install. It took claiming a server in a browser.
	streamRepository, streamOffered := "", cmd.StreamSourceModuleID == ""
	if cmd.StreamSourceModuleID != "" {
		streamRepository, streamOffered = s.streamSourceRepository(ctx, cmd.StreamSourceModuleID)
	}

	var result ClaimServerResult
	username := strings.TrimSpace(cmd.Username)
	displayName := strings.TrimSpace(cmd.DisplayName)
	if displayName == "" {
		displayName = username
	}

	// 4. open a UnitOfWork. The account, its credential, its role, the grant and
	// its first session all commit together: a claim that left a user with no
	// authority would be a server nobody could administer and nobody could claim
	// again.
	err := s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. the domain rule, re-read inside the transaction. Two people
		// claiming at once both pass the check above; only one passes this one.
		existing, err := tx.Users().List(ctx)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return contracts.NewError(contracts.Conflict, "this server has already been claimed")
		}

		now := s.clock.Now()
		owner := domain.User{
			ID:          domain.UserID(s.ids.NewID()),
			Username:    username,
			Email:       strings.TrimSpace(cmd.Email),
			DisplayName: displayName,
			Status:      domain.UserActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		created, err := tx.Users().Create(ctx, owner)
		if err != nil {
			return err
		}

		hash, err := s.passwordVerifier.Hash(cmd.Password)
		if err != nil {
			return contracts.WrapError(contracts.Internal, "hash the owner's password", err)
		}
		if err := tx.Credentials().SavePassword(ctx, domain.PasswordCredential{
			UserID: created.ID, Hash: hash, UpdatedAt: now,
		}); err != nil {
			return err
		}

		// The owner holds everything, because it is the root of every other
		// grant: an authority withheld here could never be given to anyone
		// (platform#44). This is the one role the boot-time reconciliation keeps
		// current as the Platform gains actions; every role created after it is
		// a snapshot and stays one.
		perms := permissionsOf(SuperuserActions())
		role, err := tx.Permissions().CreateRole(ctx, domain.Role{
			ID: domain.RoleID(s.ids.NewID()), Name: PresetNameSuperuser, Permissions: perms,
		})
		if err != nil {
			return err
		}
		if err := tx.Permissions().GrantRole(ctx, domain.Grant{UserID: created.ID, RoleID: role.ID}); err != nil {
			return err
		}

		// Claiming signs you in: the person who set the server up should not
		// then be asked to prove who they are to the account they just made.
		session, pair, err := s.sessionManager.Issue(
			ctx, tx.Sessions(), tx.Tokens(), created.ID, cmd.DeviceID, domain.AuthStrengthPassword, perms)
		if err != nil {
			return err
		}

		// 7. state and the outbox event in the same transaction. The actor is the
		// account that was just made — there was nobody else it could be, and an
		// empty actor would read as a system action.
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "server.claimed", []byte(created.Username), string(created.ID)),
		}); err != nil {
			return err
		}

		result = ClaimServerResult{User: created, Session: session, Tokens: pair}
		return nil
	})
	if err != nil {
		return ClaimServerResult{}, err
	}

	// Everything below is after the commit and none of it may fail the claim.
	// The account and its authority are the part that had to be atomic; a name
	// that did not get written and a module that did not install are both
	// things the owner can fix from Settings, and refusing to let them in over
	// either would be the worse answer.
	result.ServerName, result.IdentityProblem = s.recordIdentity(ctx, strings.TrimSpace(cmd.ServerName))
	result.StreamSource, result.StreamSourceProblem = s.installStreamSource(
		ctx, result.Tokens, cmd.StreamSourceModuleID, streamRepository, streamOffered)

	telemetry.From(ctx).For("auth").Info("server claimed",
		telemetry.Identifier("username", result.User.Username),
		telemetry.Bool("named", result.ServerName != ""),
		telemetry.String("stream_source", result.StreamSource))
	return result, nil
}

// recordIdentity writes the server's name to the durable file (platform#54),
// returning what was recorded and, if it failed, why.
//
// A Platform built without an identity store records nothing and says nothing:
// that is a deployment choice, not a failure the person setting up can act on.
func (s *Service) recordIdentity(ctx context.Context, name string) (string, string) {
	if s.instance == nil {
		return name, ""
	}
	identity := domain.InstanceIdentity{
		ID:        string(s.ids.NewID()),
		Name:      name,
		ClaimedAt: s.clock.Now(),
	}
	if err := s.instance.Write(ctx, identity); err != nil {
		telemetry.From(ctx).For("auth").Error(
			"the server was claimed but its name could not be recorded", telemetry.Err(err))
		return "", "This server was set up, but its name could not be saved to disk. Set it again in Settings."
	}
	return name, ""
}

// installStreamSource installs the module the household picked, as the owner
// who just claimed the server.
//
// **As the owner, not as the system principal.** Installing an extension is
// downloading and running third-party code, and it is an act somebody chose;
// attributing it to the install itself would put the one decision a person made
// during setup outside the record of what people did. The session was minted a
// moment ago and carries `extension.manage`, so the ordinary authorised path
// works unchanged — this is not a special case, it is the normal command with a
// caller that is one second old.
func (s *Service) installStreamSource(
	ctx context.Context, tokens domain.TokenPair, moduleID, repository string, offered bool,
) (string, string) {
	if moduleID == "" {
		return "", ""
	}
	if s.extensions == nil {
		return "", "This build cannot install modules, so no stream source was added."
	}
	if !offered {
		return "", "Mosaic no longer offers " + moduleID + ". You can add a stream source from Settings."
	}
	_, err := s.InstallExtension(ctx, InstallExtensionCommand{
		Caller:     v1.CallerFromSession(tokens.AccessToken),
		Repository: repository,
		ModuleID:   moduleID,
	})
	if err != nil {
		telemetry.From(ctx).For("auth").Warn("the chosen stream source did not install",
			telemetry.String("module", moduleID), telemetry.Err(err))
		return "", "Mosaic could not install " + moduleID + ": " + err.Error() +
			" You can add a stream source from Settings."
	}
	return moduleID, ""
}

// streamSourceRepository resolves which trusted repository offers a module,
// among the ones the setup step was allowed to offer.
//
// It re-reads rather than trusting a value the claim carried, and it filters on
// the stream role for the same reason the step does: the question is "may this
// be installed during setup", and answering it from the catalogue means the
// answer cannot be widened by the request.
func (s *Service) streamSourceRepository(ctx context.Context, moduleID string) (string, bool) {
	for _, e := range s.SetupOptions(ctx).StreamSources {
		if e.ModuleID == moduleID {
			return e.Repository, true
		}
	}
	return "", false
}

// permissionsOf converts a preset's actions into the permissions a role row
// carries. The two are the same strings in different vocabularies — one is what
// the policy engine evaluates, the other is what a role stores — and the
// conversion is here rather than at each call site so that stays true.
func permissionsOf(actions []policy.Action) []domain.Permission {
	out := make([]domain.Permission, 0, len(actions))
	for _, a := range actions {
		out = append(out, domain.Permission(a))
	}
	return out
}
