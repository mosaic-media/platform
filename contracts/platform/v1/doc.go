// Package v1 is the Platform's published contract surface — the packages a
// Module compiles against, and the only Platform code it may import
// (ADR 0008, ADR 0016).
//
// It holds the content models (Node, Part, Relation, SourceBinding and their
// vocabularies), the command, query and result types of the content
// application services, the service interface those methods satisfy, and the
// opaque Caller a capability forwards from its invocation context (ADR 0017).
//
// It deliberately does not hold the store contracts (NodeStore, Tx,
// StorageAdapter) or the identity and configuration models: those are the
// Platform's plumbing and stay under internal/. A capability calls
// application services, never stores.
//
// While the Platform and its SDK share this repository the package is staged
// here; the extraction slice moves it to the standalone SDK repository
// (ADR 0008) unchanged.
package v1
