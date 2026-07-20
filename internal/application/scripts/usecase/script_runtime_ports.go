// Package usecase (scripts) — script_runtime_ports.go: the canonical
// per-route use case interfaces for the script flow (Fase 5(a),
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the per-route use case interfaces consumed
// by the script api layer. The api layer imports them via
// `usecase.X` (the cross-package qualifier is canonical).
//
// AGENTS.md Phase 5(a) compliance (July 2026):
//
//   - Per-route use case interfaces are declared HERE (alongside use
//     cases), not in the api layer. The api layer imports them via
//     `usecase.X` (the cross-package qualifier is canonical).
//   - Each interface is narrow (1-3 methods) so a route handler only
//     declares the dependency it actually needs.
//
// The interfaces:
//
//  1. SubmissionService — FASE 2 canonical, aliases operations.Service
//  2. JobQueryService   — narrowed from job.Service
//  3. ClipSearchService — existing canonical (declared in services.go)
//
// The legacy `SectionRegenerationService` interface (and its matching
// `SectionRegenerator` use-case file) was RETIRED in this push. The
// route `POST /api/script/:id/sections/:section_id/regenerate` is gone;
// clients must use the canonical `POST /api/script/generate` with
// `source.type` resolvers instead. See architecture/current.yaml for
// the deprecation ticket.
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
// the same type.
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
// already implements this shape.
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
//
// ClipSearchService is the canonical narrow port for clip search.
// The interface declaration lives in `services.go` (the existing
// canonical pre-Fase-5 declaration); this file references it via
// same-package name resolution (no import qualifier) for the
// per-route handler ports.
//
// Compile-time identity lock for the existing canonical
// ClipSearchService declaration in services.go. Future services.go
// renames or removals surface here rather than at first runtime
// call site. This lock is the only compile-time drift anchor in
// this file (the SubmissionService alias is a struct alias and
// doesn't accept the standard interface-style nil cast).
var _ ClipSearchService = ClipSearchService(nil)
