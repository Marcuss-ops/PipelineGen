// Package jobs — sibling_dispatcher.go (Step 11B C1/4, July 2026).
//
// SiblingDispatcher routes the canonical "1 parent script job → N
// voiceover sibling jobs + M image sibling jobs" fan-out surfaced by
// the script.generate handler (the parent handler at the API surface
// for /api/script/generate). Per the user spec for Step 11B:
//
//   - N voiceover siblings of type TypeScriptVoiceoverSibling
//   - M image siblings of type TypeScriptImageSibling
//   - Each sibling carries ParentJobID = script.generate.id at the
//     broker level
//   - Siblings are spawned in parallel via pkg/concurrent.Map with a
//     bounded worker pool (NOT first-error-wins — siblings fail
//     independently; per-item errors are collected in an error sink
//     and surfaced as a typed `SiblingDispatchResult` so the parent
//     can decide whether to fail-closed based on AssetRequirements.Required).
//
// Why pkg/concurrent.Map (not WithContext/Group):
//
//	pkg/concurrent.WithContext (the errgroup variant) is CANCEL-on-first-
//	error semantics — when one sibling's enqueue fails, every other
//	in-flight enqueue is cancelled. That is wrong for siblings: a TTS
//	failure for voiceover[0] (e.g. transient) must NOT abort image[0..2]
//	which are independent of voiceover's failure surface.
//
//	pkg/concurrent.Map is a bounded worker pool + per-item error
//	sink that delivers every result regardless of failures on other
//	items. The aggregator (Step 12B) is the canonical place for
//	fail-closed semantics on the parent.
//
// Each sibling carries a typed EnqueueCommand that the broker writes
// in a single transaction (atomic enqueue per sibling per PR-VO-A3).
// The dispatcher does NOT enforce cross-sibling atomicity at the
// SQLite level; partial-enqueue is gracefully handled by the parent
// aggregator (siblings never enqueued will never emit child_terminated,
// and the parent will fail only if AssetRequirements.Required flagged
// them as required).
//
// PR-DISPATCHER-SPLIT (July 2026): decomposed into 2 files per AGENTS.md
// Pattern 5:
//
//	sibling_dispatcher.go           — types + constructor + simple methods
//	                                   (this file)
//	sibling_dispatcher_dispatch.go  — DispatchSiblings + buildSiblingCommand
//	                                   + toSiblingPayload
package jobs

import (
	"context"
	"encoding/json"
	"time"

	jobscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// ── Sibling job types (Step 11B) ──────────────────────────────────────
//
// Canonical type strings live in internal/domain/job/job.go
// (TypeScriptVoiceoverSibling, TypeScriptImageSibling — per godlike/02
// capability-specific constants stay in their owning domain package).
// We import the domain surface here and reference qualified
// job.TypeScript* identifiers so a future rename at the domain layer
// is a one-line canonical-surface change (no local-const drift).

// DefaultSiblingConcurrency is the canonical per-worker sibling
// fan-out budget (matches Step 11B user spec; mirrors the registry
// entries registered in internal/application/jobs/registry.go).
const DefaultSiblingConcurrency = 4

// ── AssetRequirements (Step 11B fail-closed surface) ─────────────────
//
// AssetRequirements describes the downstream-artifact contract for a
// single asset the script.generate parent must produce. Required=true
// flags the asset as MUST-PRODUCE — failure surfaces as a parent
// PartialReason="missing_required_downstream" + parent FAILED rank
// (per godlike/07 fail-closed policy).
//
// Re-exported here as the canonical dispatcher-side slice element
// type. The User-spec-step-11 prior scope declared a similar type at
// internal/domain/sceneplan (per audit) — the dispatcher's local
// mirror is a structural alias to avoid the cross-package cycle on
// internal/application/scripts → internal/domain/sceneplan.
// Existing sceneplan.AssetRequirements callers continue to compile
// against their own package; the dispatcher operates against type
// aliases below.

type AssetRequirements struct {
	// AssetID identifies the produced asset (e.g. voiceover file
	// row id, image asset id).
	AssetID string `json:"asset_id"`
	// Kind discriminates voiceover vs image (the dispatcher maps Kind
	// → sibling job type via the canonical lookup table).
	Kind AssetKind `json:"kind"`
	// Required drives the fail-closed semantics: if a sibling for a
	// Required asset FAILED (or was never enqueued), the parent's
	// terminal state is FAILED with PartialReason="missing_required_downstream".
	Required bool `json:"required"`
	// Title is the human-readable name (for logging + audit trail).
	Title string `json:"title,omitempty"`
}

// AssetKind is the canonical discriminated enum for downstream
// artifact type.
type AssetKind string

const (
	AssetKindVoiceover AssetKind = "voiceover"
	AssetKindImage     AssetKind = "image"
)

// ── SiblingCommand (per-sibling enqueue payload) ─────────────────────

// SiblingCommand is the typed enqueue command for a single sibling
// job. Produced by the dispatcher per AssetRequirement and routed to
// the broker via Broker.Enqueue. Each command carries the
// ParentJobID (the script.generate JobID), the canonical job type
// (TypeScriptVoiceoverSibling / TypeScriptImageSibling), the asset
// payload, and the originating AssetRequirements.Required flag.
type SiblingCommand struct {
	ParentJobID string            `json:"parent_job_id"`
	JobType     string            `json:"job_type"` // TypeScriptVoiceoverSibling / TypeScriptImageSibling
	Asset       AssetRequirements `json:"asset"`
	// Payload is the JSON-encoded sibling-specific command body
	// (e.g. {language, voice, filename} for voiceover;
	// {prompt, style, output_format} for image). Opaque to the
	// dispatcher; the per-type handler reads it via the canonical
	// codec for the sibling job type.
	Payload json.RawMessage `json:"payload"`
	// EnqueuedAt is the broker-side enqueue timestamp (mirrors
	// the canonical job.CreatedAt surface).
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// ── SiblingDispatchResult (typed surface for caller) ─────────────────

// SiblingDispatchResult is the typed return envelope from
// DispatchSiblings. Per-sibling outcome + summary counts so the
// caller (the parent handler) can decide whether to fail-closed.
type SiblingDispatchResult struct {
	// SiblingJobIDs are the broker-assigned JobIDs of every
	// successfully enqueued sibling. Length == N + M for a fully-
	// atomic dispatch; zeros for missing siblings must be cross-
	// checked against PlannedCount + SucceededCount.
	SiblingJobIDs []string
	// PlannedCount is the N+M total expected.
	PlannedCount int
	// SucceededCount is how many enqueues returned a non-empty JobID.
	SucceededCount int
	// RequiredMissing is the set of Required=true siblings whose
	// Enqueue returned an error — the caller MUST fail-closed
	// (parent FAILED + PartialReason="missing_required_downstream").
	// Populated ONLY for siblings where Asset.Required=true AND
	// err != nil.
	RequiredMissing []string // AssetIDs of REQUIRED siblings whose enqueue failed
	// Errors is the per-sibling typed error collected; the dispatcher
	// does NOT fail-fast on errors (siblings fail independently).
	Errors []error
}

// HasRequiredMissing returns true if any REQUIRED sibling failed
// to enqueue. The caller MUST propagate this into a parent
// PartialReason="missing_required_downstream" + parent status=FAILED.
func (r *SiblingDispatchResult) HasRequiredMissing() bool {
	return len(r.RequiredMissing) > 0
}

// ── Broker port (Pattern 0) ─────────────────────────────────────────

// SiblingBrokerPort is the narrow broker interface the dispatcher
// needs. The production *appjobs.Service satisfies this implicitly;
// tests inject stubs without instantiation overhead.
type SiblingBrokerPort interface {
	Enqueue(ctx context.Context, cmd EnqueueCommand) (string, error)
}

// EnqueueCommand is the typed enqueue command shape (mirrors the
// canonical broker.Enqueue signature, projected into the dispatcher's
// surface). Kept intentionally narrow — only the fields the dispatcher
// needs are exposed.
type EnqueueCommand struct {
	JobType     string
	ParentJobID string
	Payload     json.RawMessage
	// Required is forwarded to broker.Enqueue's MaxAttempt gate so
	// REQUIRED siblings get the canonical 3-retry default; OPTIONAL
	// siblings may ride default max retries.
	Required bool
}

// Compile-time assertion: if production *appjobs.Service ever drifts,
// the build catches it. We don't have access to *appjobs.Service here
// without an import cycle on the speakers pkg — the assertion runs in
// the composition root (internal/app/sibling_dispatcher_assertions.go,
// out-of-scope for Step 11B).
//
// var _ SiblingBrokerPort = (*appjobs.Service)(nil) // pinned at composition root

// ── SiblingDispatcher ───────────────────────────────────────────────

// SiblingDispatcherDeps groups the dispatcher's single external
// dependency (the broker) through a narrow interface. Bounded
// worker pool is configured at construction; nil-safe logger.
type SiblingDispatcherDeps struct {
	// Broker is the canonical broker. MANDATORY (fail-fast per
	// AGENTS.md WireUp pattern).
	Broker SiblingBrokerPort
	// Concurrency is the per-call worker pool size. Zero or negative
	// defaults to DefaultSiblingConcurrency (= 4, matching the
	// user-spec Step 11B).
	Concurrency int
	// Logger is OPTIONAL (nil-safe via zap.NewNop()).
	Logger *zap.Logger
}

// SiblingDispatcher is the entry-point for parallel sibling fan-out
// from a parent script.generate job. One instance per process.
type SiblingDispatcher struct {
	deps SiblingDispatcherDeps
}

// NewSiblingDispatcher constructs the dispatcher with the canonical
// defaults applied (Concurrency=4 if zero/negative; nil-safe logger).
// Fail-fast on nil Broker (WireUp pattern).
func NewSiblingDispatcher(deps SiblingDispatcherDeps) *SiblingDispatcher {
	if deps.Broker == nil {
		panic("jobs.NewSiblingDispatcher: Broker is required (SiblingDispatcherDeps.Broker)")
	}
	if deps.Concurrency <= 0 {
		deps.Concurrency = DefaultSiblingConcurrency
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &SiblingDispatcher{deps: deps}
}

// ── Compile-time role assertion (canonical SiblingDispatcher identity) ─
//
// SiblingDispatcher is registered as a typed singleton in the
// composition root. To catch future drift at build time, the canonical
// namer interface is pinned here. The user-code import site is
// internal/app/sibling_dispatcher_assertions.go (out-of-scope for
// this commit; declared via int-application interface below).

// SiblingDispatcherInterface is the user-facing canonical surface.
// internal/app's NewSiblingDispatcherAdapter returns *SiblingDispatcherAdapter
// which satisfies this interface.
type SiblingDispatcherInterface interface {
	DispatchSiblings(ctx context.Context, parentJobID string, requirements []AssetRequirements) *SiblingDispatchResult
}

// Compile-time assertion: *SiblingDispatcher satisfies
// SiblingDispatcherInterface (canonical surface check, per AGENTS.md
// Pattern 0 port contract).
var _ SiblingDispatcherInterface = (*SiblingDispatcher)(nil)

// ── Back-compat type alias for domain/job.Type* constants ────────────
//
// The user-spec Step 11B references `internal/domain/job/registry.go`
// adding the type constants. The canonical surface for those
// constants is `internal/domain/job/job.go` (Phase A.2 layered
// structure: kernel/job for status, domain/job for capability-specific
// type strings + aliases). The dispatcher references the locally-
// declared TypeScriptVoiceoverSibling / TypeScriptImageSibling here;
// the domain/job/job.go additions are mirrored in Commit 2 of Step 11B.

// CanonicalTypeStringForKind maps an AssetKind → the canonical
// domain/job.* Type constant for the sibling. Used by tests +
// composition-root assertions to detect drift.
func CanonicalTypeStringForKind(k AssetKind) (string, bool) {
	switch k {
	case AssetKindVoiceover:
		return jobscript.TypeVoiceoverSibling, true
	case AssetKindImage:
		return jobscript.TypeImageSibling, true
	default:
		return "", false
	}
}
