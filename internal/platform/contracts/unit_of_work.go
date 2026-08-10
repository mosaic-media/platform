// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import "context"

// UnitOfWork is the transaction boundary application services use to
// coordinate writes across multiple stores.
type UnitOfWork interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Tx provides transaction-scoped access to Platform stores. Every store
// reached through a single Tx participates in the same underlying
// transaction, so state and outbox events commit atomically.
// The set of stores is Platform-owned and closed, and this interface
// enumerates it. Capabilities do not own stores (platform#8), so there is no
// registration mechanism and nothing to resolve at runtime; growing this
// set is deliberate Platform evolution, which is why it looks like an edit
// to a Platform interface rather than a plugin point.
//
// One transaction spans one bounded context's stores plus the outbox
// (platform#10). Work that crosses contexts is two transactions joined by an
// event, not one transaction touching both.
type Tx interface {
	Users() UserStore
	Sessions() SessionStore
	Permissions() PermissionStore
	Config() ConfigStore
	Outbox() EventOutbox
	Credentials() CredentialStore

	// Tokens is the session's bearer pair (platform#58). It joins the set because
	// issuing a pair and creating the session it belongs to have to commit
	// together: a session with no tokens is a row nobody can use, and tokens
	// with no session are a credential pointing at nothing.
	Tokens() TokenStore

	// The content model (platform#9) — the first stores added to this set
	// since it was closed.
	Nodes() NodeStore
	Parts() PartStore
	Relations() RelationStore
	SourceBindings() SourceBindingStore

	// PlaybackStates persists where each viewer got to (platform#26). It is the
	// fifth content store and the first per-user one — everything above it is
	// install-global. It joins the set for the same reason the others did: a
	// position change emits an outbox event, and the two commit together or
	// neither does.
	PlaybackStates() PlaybackStateStore

	// ModuleSettings persists an optional module's user-managed settings
	// document (platform#17). It joins the set so a settings change and its
	// outbox event commit in one transaction, like every other write.
	ModuleSettings() ModuleSettingsStore

	// UserPreferences persists what a user chose for themselves — expert mode
	// (platform#36) first, and more to come. It joins the set for the same reason
	// ModuleSettings did: a preference change emits an outbox event, and the
	// two must commit together or neither.
	UserPreferences() UserPreferenceStore

	// InstalledExtensions persists the durable set of extension modules a user
	// has installed (platform#51). It joins the set so an install or uninstall and
	// its outbox event commit in one transaction, like every other write; the
	// Platform reads it at boot to re-adopt what the user last installed.
	InstalledExtensions() InstalledExtensionStore

	// LibraryRules persists what the library should contain (platform#60). It
	// joins the set for the same reason the others did: writing a rule emits an
	// outbox event, and a rule that existed without its event — or an event
	// about a rule that did not — is a statement half-made.
	LibraryRules() LibraryRuleStore

	// NodeMetadata persists what a provider said about a materialised title
	// (platform#62). It joins the set so a document and the node it describes can
	// be written together — an enrichment that stored a description of a node
	// the same transaction then rolled back would be a cache entry for content
	// that does not exist.
	NodeMetadata() NodeMetadataStore

	// Issues is the resolution register (platform#74). It joins the set so a
	// finding and the act that produced it commit together — an extension
	// uninstalled to resolve an Issue must not leave the Issue behind if the
	// uninstall rolls back, and an Issue cleared by a repair that then failed
	// would be a register saying the problem is gone when it is not.
	Issues() IssueStore
}
