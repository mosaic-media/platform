// Package builtin defines the registration shape for built-in modules —
// Postgres first — mirroring how a future external Module (MEG-006) would
// declare itself and be discovered, but compiled in, required and trusted.
//
// It owns only the Manifest type, the Module interface and a Registry. It
// imports no concrete module, so modules depend on this package (for the
// Manifest/Module shape) without creating an import cycle; the composition
// root wires concrete modules into a Registry.
package builtin
