// This is a deliberately separate Go module. It stands in for a Module built
// by someone who is not the Platform team, and it exists to prove platform#12's
// property: the published SDK surface is importable from outside — and, since
// the surface now lives in its own module (sdk), that it can be
// depended on exactly as a third party would depend on it.
//
// The replace points at the sibling SDK working tree. Nothing in the main
// module imports this one; it is compiled only by test/sdkboundary.
//
// **The five indirect requires below are the point, not clutter.** They are what
// a third party inherits in their own go.mod for importing the SDK and nothing
// else, and they are the visible cost sdk#10 decides to remove. When that lands,
// `go mod tidy` here deletes them, and the empty block is the proof it worked.
// They are committed rather than resolved with -mod=mod because that flag writes
// this file during the test run, and a gate that dirties the tree it is checking
// is not a gate.
//
// They go stale when the SDK's own requirements move, and the boundary test then
// fails with "missing go.sum entry". That is a real failure and the fix is to
// re-run `go mod tidy` here — in the container, since the resolution needs the
// proxy. A third party would hit exactly the same thing, which is the argument
// for leaving it visible rather than papering over it.
module example.com/mosaic-sdk-probe

go 1.25.0

require github.com/mosaic-media/sdk v0.0.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/log v0.21.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
)

replace github.com/mosaic-media/sdk => ../../../sdk
