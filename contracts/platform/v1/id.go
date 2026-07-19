package v1

// Content-model identifiers. They are UUIDv7 in the database (ADR 0013), but
// across this contract surface they are opaque strings a capability passes
// back exactly as it received them — the generation strategy is the
// Platform's, not the module's.
//
// These are independent string types rather than a shared base: the identity
// identifiers keep their own base under internal/, and the two id families do
// not mix.

// NodeID identifies a Node.
type NodeID string

// PartID identifies a Part.
type PartID string

// RelationID identifies a Relation.
type RelationID string

// SourceBindingID identifies a SourceBinding.
type SourceBindingID string
