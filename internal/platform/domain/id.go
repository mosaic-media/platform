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

// DeviceID identifies the registered device or browser a Session was
// issued to (MEG-015 §07 — Session Model).
type DeviceID ID

// The content-model identifiers live in the published contract surface
// (contracts/platform/v1), not here: they cross the module boundary, while
// these infrastructure identifiers stay internal (ADR 0016). The content ids
// are UUIDv7 and these remain random UUIDv4 (ADR 0013).
