// Package stock — planner (Stock Cutover Commit 1, July 2026).
//
// ClipPlanner is the deterministic replacement for the pre-cutover
// rng-based clip-sampling logic that lived inside processSingleVideo
// (used the usedOffsets map + ran up to 20 attempts + picked random
// start offsets via rng.Float64()). The new planner:
//
//  1. Takes (source, budgetSec, clipDur, policyVersion) — no rng.
//  2. Produces N non-overlapping ClipPlan entries where N =
//     budgetSec / clipDur, with each entry carrying its source ID,
//     start/end, deterministic OutputLogicalID, and the policy
//     version tag it was locked against.
//  3. SHA-256 hash of (URL + index) mod sliceSec gives a stable
//     permutation that is identical across runs (replacing rng
//     which gave a different permutation every time the binary
//     started — the rng seed was time.Now().UnixNano()).
//
// Same input → same output. That is the only invariant the
// orchestrator and downstream consumers (cut, retry, render) lock
// against: the plan is persisted as data and consumed verbatim
// rather than re-computed.
//
// Files:
//   - planner_types.go: constants + type alias references
//   - planner_source.go: inferSourceProvider, inferSourceVideoID
//   - planner_deterministic.go: deterministicPlanner + Plan() + helpers
//   - planner_explicit.go: explicitPlanner + Plan() + expand helpers
package stockpipeline
