// Package render owns the renderer-facing contract surface of the overlay
// system: the golden workload pins, plan fingerprints, the template registry,
// fonts, durations, dead-air and continuity schedulers, scene intelligence
// candidates and the composite image pools.
//
// It is the terminal tail of the overlays dependency graph: it depends on the
// core planner package (overlays) but the core never depends on this package.
// New public render-surface contracts belong here, not in overlays.
package render
