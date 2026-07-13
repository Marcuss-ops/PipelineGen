// Package job — handler.go alias layer (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
//
// The canonical Handler type + JobExecutionTools struct + Result alias
// now live in internal/kernel/job/handler.go (the kernel subzone is
// the SOLE owner of cross-cutting contracts per godlike/06 SSOT).
//
// This file is a back-compat alias layer preserving the 31 in-tree
// pre-P1-#13 reference sites that declared `type H = domainjob.Handler`
// or read `tools.Progress` / `tools.Event` via the domain-job import
// path. Go type aliases are transparent at the package boundary:
// `domainjob.Handler` and `kerneljob.Handler` are the same type as far
// as the compiler and runtime are concerned.
//
// Future code SHOULD import internal/kernel/job directly. The aliases
// here are scheduled for cutover in the CONTRACT phase (deadline
// 2026-10-01) per the migration schedule in the previous version of
// this file (preserved for cross-referencing under the
// GODLIKE-07-EXPAND-BACKFILL-CUTOVER-CONTRACT discipline).
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2 → commit 9) ──────────

type (
	// JobExecutionTools is the canonical typed tools envelope.
	// Handlers invoke tools.Progress + tools.Event callbacks to
	// report progress + emit typed events on the job timeline.
	JobExecutionTools = kerneljob.JobExecutionTools

	// Handler is the canonical job-handler signature consumed by
	// BOTH internal/application/jobs.Dispatcher.Register AND
	// internal/application/jobs/worker.Registry.Register.
	Handler = kerneljob.Handler
)

// Result stays in domain/job as a typed-string-alias re-export:
// the kernel declares `type Result = map[string]any` which is
// identical to this; both `domainjob.Result` and `kerneljob.Result`
// resolve to the same `map[string]any` shape. Kept as a top-level
// type alias in BOTH packages so the canonical map-equivalent
// shape has a 107-import-site back-compat surface.
type Result = kerneljob.Result
