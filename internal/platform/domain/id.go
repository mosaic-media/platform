package domain

// ID is a Platform-generated identifier. It carries no assumptions about
// the generation strategy (UUID, ULID, sequence, ...); that choice belongs
// to the IDGenerator adapter (MEG-004 §04 — Driven Ports).
type ID string

// UserID identifies a User.
type UserID ID

// SessionID identifies a Session.
type SessionID ID

// RoleID identifies a Role.
type RoleID ID

// ConfigVersionID identifies a ConfigVersion.
type ConfigVersionID ID

// EventID identifies an Event.
type EventID ID
