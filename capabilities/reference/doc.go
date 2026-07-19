// Package reference is the reference capability: the thesis test of Mosaic's
// extension model (ADR 0012). It does what a capability actually does —
// source external metadata, search existing content, create nodes and
// relations, and cause events — using only the published contract surface
// (contracts/platform/v1) and owning no schema.
//
// It imports contracts/platform/v1 and the standard library, and nothing
// else. That constraint is the whole point and is enforced by a boundary
// test: if this package needed a private Platform import, the published
// contracts would not be ready (the stop point in the roadmap). It lives
// outside internal/ so it is shaped exactly like a Module a third party would
// build.
package reference
