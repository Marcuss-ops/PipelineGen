// Package overlays owns the declarative contract between PipelineGen and
// RenderingGen. PipelineGen decides what should appear and when; the
// renderer only materializes that decision into an artifact.
//
// Nearby-animation layout (duplicate-motion rotation, chain fusion,
// image/date collision placement and the entrance cascade) has exactly one
// owner: animation_layout.go. BuildPlan and the script-generation compile
// boundary call its finalizers; do not re-order those passes in a caller.
package overlays
