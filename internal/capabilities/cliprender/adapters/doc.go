// Package adapters wires the concrete clip.render adapters (asset
// resolver/materializer, subtitle compiler, render executor, publisher,
// overlay compositor, output prober, transcript resolver) to the
// cliprender capability ports.
//
// Import discipline:
//   - MAY import the cliprender capability contracts.
//   - MAY import external infrastructure (drive, rustexec, overlays)
//     under godlike/06 §"Single capability ownership" strictness.
package adapters
