// Package coreengine defines the reusable automation/rendering orchestration core.
//
// Goal:
//   - keep domain logic reusable outside the VeloxEditing-branded app
//   - isolate product branding, HTTP surface, and deployment concerns
//   - let future apps reuse jobs, workers, orchestration, queue, and persistence
//
// Rules of the boundary:
//   1. coreengine must not depend on app branding
//   2. coreengine should expose contracts, policies, and orchestration primitives
//   3. VeloxEditing app code should wire HTTP handlers, branding, and product defaults
package coreengine
