// This is a deliberately separate Go module. It stands in for a Module built
// by someone who is not the Platform team, and it exists to prove ADR 0016's
// property: the published SDK surface is importable from outside — and, since
// the surface now lives in its own module (sdk), that it can be
// depended on exactly as a third party would depend on it.
//
// The replace points at the sibling SDK working tree. Nothing in the main
// module imports this one; it is compiled only by test/sdkboundary.
module example.com/mosaic-sdk-probe

go 1.25.0

require github.com/mosaic-media/sdk v0.0.0

replace github.com/mosaic-media/sdk => ../../../sdk
