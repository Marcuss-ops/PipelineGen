// Package overlays owns the declarative overlay contract between PipelineGen
// and RenderingGen. The planner is Rust-owned (BrainRust `overlay-compiler`):
// overlay plans arrive as data, and this package is the Go projection/catalog
// layer for them — the contract model, the certified motion/effect/font
// catalogs, map and image/text policy, style projection, the plan cache and
// the GPU gate.
//
// There is no Go plan entry point. `BuildPlan` and the Go planner were retired
// on 2026-11-18 (TICKET-OVERLAY-PLANNER-GO-DEMOLITION) after the single-call
// Rust cutover; reintroducing one is a regression the deletion guard rejects.
//
// Keep this layer projection-only: it may name, validate and decorate items,
// but it must not decide which items a plan contains. Anything that orders
// passes or admits plan content belongs in the Rust planner, not here.
package overlays
