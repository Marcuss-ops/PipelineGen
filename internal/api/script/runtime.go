// Package script (api/script) — runtime.go: the ScriptRuntime bundle
// (Fase 5(a) × (d), July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the ScriptRuntime struct. The 4 use case
// INTERFACES (SubmissionService, JobQueryService, ClipSearchService,
// SectionRegenerationService) live in
// `internal/application/scripts/usecase/script_runtime_ports.go`;
// this struct references them via cross-package import.
//
// AGENTS.md Phase 5(a) compliance (July 2026):
//
//   - "internal/api owns transport only".
//   - "ScriptRuntime only": this struct is the SOLE use case
//     surface the api layer declares. The api MUST NOT import
//     internal/infrastructure/database/sqlite/*,
//     internal/infrastructure/drive/*, or any FFmpeg provider
//     orchestrator directly.
//   - "ogni route riceve solo il proprio use case": each handler
//     receives exactly ONE field from ScriptRuntime; godlike/07
//     minimum-blast-radius per-route narrowing.
//
// # Phase 5(a) — declare only
//
// Push 5.4 migrates the canonical ScriptFlowHandler constructor
// surface from `ScriptFlowDeps` (23-field legacy) to
// `ScriptRuntime` (4-field canonical). Until Push 5.4:
//   - The legacy `ScriptFlowDeps` in handler_deps.go stays in place.
//   - `ScriptRuntime` is a parallel declaration that handlers
//     can opt into incrementally.
//   - Build still constructs `ScriptFlowHandler(ScriptFlowDeps{...})`
//     (no production-side caller change).
//
// # Phase 5(d) cutover (NOT this push)
//
//   - handler_deps.go's `ScriptFlowDeps` is reduced to a meta-bundle
//     containing one `Runtime ScriptRuntime` field.
//   - Each route handler declares a single use case field
//     (`runtime.SubmissionService` for POST /generate,
//     `runtime.JobQueryService` for GET /jobs/:id, etc.).
//   - handler_deps.go and the composers in `internal/app/wire_script*.go`
//     migrate to construct ScriptRuntime + populate the 4 use cases.
//
// No new routing, source policy, sampling, or resolution logic enters
// ScriptRuntime (AGENTS.md "shared resolver rule"). The 4 use case
// fields are pure namespace declarations.
package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
)

// ScriptRuntime is the canonical api-side bundle for the script flow.
// It packages the 4 per-route use case surfaces:
//
//   - SubmissionService          — POST /api/script/generate
//   - JobQueryService            — GET /api/script/jobs/:id (List / Get / Events)
//   - ClipSearchService          — GET /api/script/clips/search
//   - SectionRegenerationService — POST /api/script/regenerate-section (Phase 9 stub)
//
// godlike/06 SSOT: the 4 field types reference `usecase.X` from
// `internal/application/scripts/usecase/script_runtime_ports.go`.
// Field-by-field locality: each route handler receives ONLY the field
// it consumes; unused fields are not nil-checked ("per-route narrowing" per
// AGENTS.md Phase 5(d)).
//
// godlike/07 minimum-blast-radius: ScriptRuntime is intentionally
// flat (no nested bundles). A future per-capability split (e.g.
// `ScriptRuntime.Submission *SubmissionSurface` with nested fields)
// would over-design the bundle for the 4-route script surface.
//
// Composition: built once at startup by the composition root (Build
// in internal/app/wire_script_runtime.go — Push 5.4) and passed
// verbatim to ScriptFlowHandler.NewScriptFlowHandler.
type ScriptRuntime struct {
	// SubmissionService is the FASE 2 canonical submission surface.
	// Consumed by POST /api/script/generate (HandlerGenerate).
	SubmissionService usecase.SubmissionService

	// JobQueryService is the read-only job query surface.
	// Consumed by GET /api/script/jobs/:id (JobsHandler), and the
	// canonical events sub-route GET /api/script/jobs/:id/events.
	JobQueryService usecase.JobQueryService

	// ClipSearchService is the clip-name search surface.
	// Consumed by GET /api/script/clips/search?q= (search handler).
	ClipSearchService usecase.ClipSearchService

	// SectionRegenerationService is the section-regen surface.
	// Consumed by POST /api/script/regenerate-section (Phase 9 stub
	// handler; canonical impl lands in Push 5.4).
	SectionRegenerationService usecase.SectionRegenerationService
}
