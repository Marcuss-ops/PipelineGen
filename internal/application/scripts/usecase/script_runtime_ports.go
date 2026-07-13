// Package usecase (scripts) — script_runtime_ports.go: the 4 canonical
// per-route use case interfaces that compose the ScriptRuntime bundle
// (Fase 5(a) × (d), July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the 4 use case interfaces consumed by
// ScriptRuntime. The bundle declaration lives at the api-side
// (`internal/api/script/runtime.go::ScriptRuntime`); the per-route
// use case surfaces live here, in the same `usecase` package as the
// canonical use case implementations.
//
// AGENTS.md Phase 5(a) compliance (July 2026):
//
//   - Per-route use case interfaces are declared HERE (alongside use
//     cases), not in the api layer. The api layer imports them
//     via `usecase.X` (the cross-package qualifier is canonical).
//   - Each interface is narrow (1-3 methods) so a route handler
//     only declares the dependency it actually needs.
//   - Pre-Fase-5a callers (handler_deps.go, ScriptFlowDeps,
//     ClipServices) continue compiling unchanged — no caller
//     migration in this push (Push 5.4 migrates handlers).
//
// The 4 interfaces:
//
//  1. SubmissionService          — FASE 2 canonical, aliases operations.Service
//  2. JobQueryService            — NEW; narrowed from kerneljob.Service
//  3. ClipSearchService          — existing canonical (services.go)
//  4. SectionRegenerationService — NEW; alias-of-shape for section_regen.go
//
// The api-side bundle ScriptRuntime lives at
// `internal/api/script/runtime.go::ScriptRuntime` and references
// these 4 interfaces via cross-package import.
package usecase

import (
	"context"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── 1. SubmissionService ─────────────────────────────────────────────
//
// SubmissionService is the FASE 2 canonical submission surface
// (atomically: operations + jobs + outbox_events in one TX). The
// canonical concrete lives at
// `internal/application/operations/generation_submission_service.go::Service`.
//
// Why a Go type alias (not a fresh interface):
//
//   - The test doubles (handler_test_fixtures_test.go::fakeSubmissionService,
//     handler_enqueue_timeout_test.go::slowSubmissionService) already
//     implement the EXACT shape of `*opsapp.Service.Submit`.
//   - A Go type alias preserves byte-stable identity —
//     `usecase.SubmissionService == opsapp.Service` (structurally).
//   - A fresh interface declaration would force callers to redeclare
//     the shape and create drift risk (godlike/07 minimum-blast-
//     radius prefers the alias).
//
// Identity-lock caveat (godlike/06 SSOT alias drift-proofing):
// SubmissionService is a STRUCT alias (`opsapp.Service` is a
// concrete struct, not interface). The canonical interface-style
// lock `var _ SubmissionService = SubmissionService(nil)` does NOT
// compile — Go forbids converting `nil` to a non-pointer non-interface
// type — and a pointer-typed `(*opsapp.Service)(nil)` is structurally
// distinct from the alias type, so it cannot be substituted either.
// For struct aliases the Go type alias declaration itself is the
// drift-detection anchor: programmatic alias-name shadowing (renaming
// `opsapp.Service` to something incompatible) would surface as a
// build error at every callsite that imports the alias name.
//
// Alias identity: `usecase.SubmissionService` and `opsapp.Service` are
// the same type; `var _ usecase.SubmissionService = (*opsapp.Service)(nil)`
// would compile as a TYPE assertion check (BUT it asserts
// `*opsapp.Service` satisfies `SubmissionService`, which it does NOT,
// since `SubscriptionService` is a value-type alias not an interface
// — the assertion fails). Dropping the runtime assertion entirely is
// the correct path here.
type SubmissionService = opsapp.Service

// ── 2. JobQueryService ───────────────────────────────────────────────
//
// JobQueryService is the canonical narrow port for job read queries.
// It narrows the full `jobs.Service` surface (Enqueue + Get + List +
// ListEvents + Retry + Cancel + ...) to JUST the read-only methods a
// route handler needs.
//
// Pre-Fase-5, the canonical read-side surfaced as `jobs.Service`
// (a fat interface). Push 5.4 migrates route handlers from
// `jobs.Service` to `JobQueryService` (decorating the dep bag with
// only the methods the route consumes).
//
// godlike/07 minimum-blast-radius: 3 methods, no I/O primitives,
// no side-effects. The test double
// `handler_test_fixtures_test.go::fakeJobsService.Get / List / ListEvents`
// already implements this shape (the fake returns errors for
// unwired methods; Push 5.4 cleans up the shape).
type JobQueryService interface {
	// Get returns the job row for `id`, or `(nil, nil)` if no row
	// exists. godlike/07 nil-tolerance: callers branch on
	// presence, NOT on a not-found error probe.
	Get(ctx context.Context, id string) (*job.Job, error)

	// List returns the jobs matching `filter`, ordered by
	// created_at DESC. Empty result returns `(nil, nil)`.
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)

	// ListEvents returns all events on a job's timeline in
	// created_at ASC order. Empty result returns `(nil, nil)`.
	ListEvents(ctx context.Context, jobID string) ([]job.Event, error)
}

// ── 3. ClipSearchService ─────────────────────────────────────────────

// ClipSearchService is the canonical narrow port for clip search.
// The interface declaration lives in `services.go` (the existing
// canonical pre-Fase-5 declaration); this file references it via
// same-package name resolution (no import qualifier) for the
// ScriptRuntime bundle.
//
// Rationale for keeping the canonical declaration in services.go:
// services.go is the existing per-capability interface file for
// `ClipServices`. Moving the interface declaration here would force
// a `services.go` rewrite + a `ClipServices.ClipSearch` reassignment
// to `ScriptRuntime.ClipSearchService` — those are Push 5.4
// caller-migration tasks. Until then, the runtime bundle references
// `ClipSearchService` from services.go via same-package name
// resolution. Future services.go renames or removals surface at the
// `var _ ClipSearchService = ClipSearchService(nil)` line below
// rather than at runtime.
//
// IDEAL PUSH 5.4 MIGRATION: move the interface declaration from
// services.go to this file (replacing the same-package reference)
// + reassign `ClipServices.ClipSearch` to `ScriptRuntime.ClipSearchService`.
// For Phase 5(a) define-only, the same-package reference + the
// compile-time identity lock at the bottom of this section is the
// minimum-blast-radius path.

// ── 4. SectionRegenerationService ────────────────────────────────────

// SectionRegenerationService is the canonical use case interface for
// regenerating a single script section. The interface shape mirrors
// the existing canonical concrete
// `SectionRegenerator.Regenerate` declared in section_regen.go.
//
// Pre-Fase-5a, the Regenerate method returned
// `ErrSectionRegenNotImplemented` (Phase 1b stub). Push 5.4 wires
// the canonical CUTOVER-phase implementation; until then, the
// interface contract is stable while the impl is the stub.
//
// godlike/07 typed-error contract: callers branch on errors.Is to
// detect the stub state (3 sentinels: ErrSectionRegenNotImplemented,
// ErrSectionNotFound, ErrScriptNotFound, ErrSectionScriptMismatch,
// ErrEmptyGeneratorOutput — all declared in section_regen.go).
type SectionRegenerationService interface {
	// Regenerate regenerates the script section identified by
	// `req.SectionID`. Returns the regenerated content + the
	// canonical SectionRegenResult projection.
	//
	// Errors: typed sentinels from section_regen.go (see above).
	// Until Push 5.4 wires the canonical impl, returns
	// ErrSectionRegenNotImplemented for ALL non-error paths.
	Regenerate(ctx context.Context, req SectionRegenRequest) (*SectionRegenResult, error)
}

// ── 5. ScriptRuntime (api-side bundle reference) ─────────────────────
//
// NOTE: the ScriptRuntime STRUCT itself lives at
// `internal/api/script/runtime.go::ScriptRuntime` (the api-side
// bundle). This file declares only the 4 INTERFACES; the bundle is
// declared where routes consume it.
//
// Pre-Fase-5 ScriptFlowDeps (handler_deps.go) carries 23 fields.
// Fase 5(a) introduces ScriptRuntime (4 fields) as the canonical
// runtime bundle migration target. The runtime bundle is the
// SOLE forward-pointer for the per-route use case narrowing.

// Compile-time identity lock for the existing canonical
// ClipSearchService declaration in services.go. Future services.go
// renames or removals surface here rather than at first runtime
// call site. This lock is the only compile-time drift anchor in
// this file (the SubmissionService alias is a struct alias and
// doesn't accept the standard interface-style nil cast).
var _ ClipSearchService = ClipSearchService(nil)
