// Package scripts — ClipSearchPort is the narrow port for semantic
// clip discovery consumed by MediaCurator.
//
// PJ-CURATE-1 (June 2026): productionizes the audit-recommended
// "clip search optional" path. The previous MediaCurator only
// consumed req.HintClipIDs (caller-seeded) and silently fell back
// to text-only if none were supplied. This port lets the worker
// invoke Qdrant on demand (opt-in via Search=true in CurateRequest)
// application/scripts package to Qdrant-specific payload types.
//
// Port shape is intentionally narrow (assetID + score + name) so
// the application layer never sees the full SearchResult.
//
// Production adapter: *qdrant.ClipSearchAdapter (implements via
// Searcher.SearchByText + Searcher.Search with filter must-clauses).
//
// nil-safe in MediaCurator — Qdrant-disabled deployments keep
// working with HintClipIDs-only as before; the port call is
// skipped when SetClipSearchPort has never been invoked.
package ports

import (
	"context"
	"errors"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ClipSearchPort is the canonical port for semantic clip discovery.
//
// Method topology (godlike/06 SSOT — one canonical owner per fact):
//
//   - SearchAssets (embedded via AssetSearchPort): the unified
//     Qdrant search surface; takes AssetSearchQuery + emits
//     AssetSearchHit. Curate and stock paths share the same method
//     but branch on KindAsset internally.
//
//   - SearchClips: the LEGACY single-query route. SourceSearch uses
//     it directly (its own narrower SemanticSearchPort routes
//     internally). SourceCurate also falls back to it for the
//     HintClipIDs-only / single-query path when the planner has not
//     yet produced a SlotPrePlan. Kept verbatim per the user's
//     "mantieni la firma singola come legacy route solo per
//     SourceSearch" spec — no signature drift during the 7-day soak.
//
//   - SearchSlots (new): the per-slot MULTI-query route. The plan
//     (ClipPrePlan.Slots[i].SearchQuery) guides N targeted queries,
//     one per slot, with per-slot timeout + candidate limit. Used by
//     the curate flow when the planner has emitted a
//     deterministic SlotPrePlan. Distinct from SearchClips: the
//     legacy route issues one query and relies on the curator to
//     deduplicate; the multi-slot route issues N queries (one per
//     slot) and produces an aggregate map keyed by slot.Ref so the
//     shared ClipSampler keeps its single Select call per slot.
//
// There is NO parallel registry: methods coexist on the same
// port; the resolver decides which to invoke (godlike/07
// composition-root discipline, not duplicated decision logic).
type ClipSearchPort interface {
	AssetSearchPort

	// SearchClips embeds the query text, performs an ANN search
	// over the configured vector store, and returns up to `limit`
	// clip hits ranked by similarity (highest first).
	//
	// Optional fields (Source, Category, MediaType) are applied as
	// Qdrant filter must-clauses by the adapter. Empty fields =
	// no filter on that axis.
	//
	// minScore defaults to 0.5 if zero (matches the legacy
	// MediaCurator.Curate threshold).
	//
	// An empty Query returns an empty hit slice with nil error.
	// Adapter errors propagate to the worker, which surfaces them
	// via the standard job-failure contract.
	//
	// Source-search call site: SearchByText → SemanticSearchPort
	// (SourceSearch doesn't reach this method directly; it has its
	// own narrower port). Kept verbatim for backwards compatibility
	// during the 7-day soak — the user's spec pins the legacy
	// single-signature as the SourceSearch fast path.
	SearchClips(ctx context.Context, q ClipSearchQuery) ([]ClipSearchHit, error)

	// SearchSlots is the per-slot multi-query route. The plan
	// (scriptpkg.ClipPrePlan.Slots[i].SearchQuery) guides N
	// targeted queries — one per slot — instead of the legacy
	// single-query pattern. Per-slot options (PerSlotTimeout,
	// PerSlotCandidateLimit, MinScore, SourceFilter/Category/
	// MediaType, WorkspaceID, IsSystem) bound each query.
	//
	// godlike/07 NO-FAKE-AVAILABILITY:
	//   - nil plan / empty Slots → typed ErrSlotSearchInvalidPlan.
	//   - ctx canceled mid-batch → slots remaining are marked in
	//     ErroredRefs; the partial ByRef is still populated.
	//   - per-slot failures are preserved in ErroredRefs[key] so
	//     the audit trail can replay partial-batch behavior.
	//
	// godlike/06 SSOT: the adapter implements SearchSlots by
	// looping N times over SearchAssets (single shared underlying
	// retriever). No duplicate retriever logic exists in the
	// caller — the planner-emitted per-slot SearchQuery is the
	// sole input.
	//
	// Cross-kind runtime guard: a stock-flavored adapter returns
	// a typed error (curate-only); the compile-time pin
	// var _ ports.ClipSearchPort = (*semanticAssetSearchAdapter)(nil)
	// surfaces signature drift at build time.
	SearchSlots(ctx context.Context, plan *scriptpkg.ClipPrePlan, opts SlotsSearchOptions) (*SlotsSearchResult, error)
}

// ClipSearchQuery is the input contract for ClipSearchPort.SearchClips.
type ClipSearchQuery struct {
	// Query is the search text (will be embedded by the adapter).
	Query string
	// Source restricts to a single source family (artlist|youtube|stock).
	// Empty = any.
	Source string
	// Category restricts to a category. Empty = any.
	Category string
	// MediaType restricts to image|video. Empty = any.
	MediaType string
	// WorkspaceID is REQUIRED for user-facing traffic. The adapter
	// applies it as a server-side workspace isolation clause (PR 5,
	// June 2026, fix/qdrant-tenant-scope). An empty workspace means
	// "background/system path" — the adapter then REJECTS the call
	// unless IsSystem=true is set explicitly. The matching rule
	// mirrors mediasearch.Service::Search's ErrMissingWorkspace
	// rejection: a workspace derived from the auth middleware is the
	// contract, an empty workspace is a programming error.
	WorkspaceID string
	// IsSystem opts out of the workspace isolation clause. Only
	// admin / reconcile / DR paths set this. Callers MUST NOT infer
	// it from the request body — the handler should pass it through
	// from an authenticated "is_admin" principal check.
	IsSystem bool
	// Limit is the max number of hits. Zero = 20 (legacy default).
	Limit int
	// MinScore is the cosine-similarity threshold. Zero = 0.5.
	MinScore float64
}

// ClipSearchHit is the result item. AssetID is the canonical
// media_assets.id reference (the only field MediaCurator consumes);
// Score is cosine similarity; Name is for log/debug only.
type ClipSearchHit struct {
	AssetID string
	Name    string
	Score   float64
	Source  string
}

// ── Per-slot multi-query route (FASE-NEXT — curate-only path) ────────

// SlotsPerSlotDefaultTimeout is the per-slot context.WithTimeout
// budget when SlotsSearchOptions.PerSlotTimeout is zero. 3s aligns
// with the legacy single-query path's empirical Qdrant round-trip +
// embed cost on the project's reference hardware.
//
// godlike/06 SSOT: threshold lives in ONE place. Operators tune
// by editing this constant; FASE may promote to per-call opts if
// needed for very large plans.
const SlotsPerSlotDefaultTimeout = 3 * time.Second

// SlotsPerSlotDefaultCandidateLimit is the per-slot hit cap when
// SlotsSearchOptions.PerSlotCandidateLimit is zero or negative.
// 20 matches the legacy SearchAssets default for clip-path
// queries (the canonical curate path uses clip kind, so this
// aligns with the curate-path default surface).
const SlotsPerSlotDefaultCandidateLimit = 20

// SlotsSearchOptions is the per-call envelope for the per-slot
// multi-query route. Defaults are applied once per call (NOT per
// slot), so a caller can override PerSlotTimeout /
// PerSlotCandidateLimit per-call without re-asserting for every
// slot. Field naming mirrors AssetSearchQuery where possible so
// the adapter code stays symmetric.
type SlotsSearchOptions struct {
	// PerSlotTimeout bounds each per-slot context.WithTimeout; the
	// adapter respects ctx.Done() and surfaces a typed ctx-canceled
	// error on each incompleted slot via ErroredRefs[key].
	// Zero = SlotsPerSlotDefaultTimeout (3s). Negative durations
	// are clamped to 0 (= adapter default).
	PerSlotTimeout time.Duration

	// PerSlotCandidateLimit caps the hits returned per slot. The
	// adapter returns at most this many hits per slot even if the
	// underlying searcher returned more.
	// Zero or negative = SlotsPerSlotDefaultCandidateLimit (20).
	PerSlotCandidateLimit int

	// MinScore is the cosine-similarity floor per slot. Candidates
	// with score < MinScore are dropped. Zero = adapter default
	// (clip=0.5, mirroring SearchAssets per-kind defaults).
	MinScore float64

	// SourceFilter / Category / MediaType are the standard Qdrant
	// filter must-clauses (mirror AssetSearchQuery). Empty fields
	// = no filter on that axis.
	SourceFilter string
	Category     string
	MediaType    string

	// WorkspaceID + IsSystem mirror the AssetSearchQuery tenant
	// isolation contract (PR 5 June 2026, fix/qdrant-tenant-scope).
	// The fail-closed workspace check runs ONCE before the per-slot
	// loop (not per slot, to avoid repeated validation cost on
	// large plans).
	WorkspaceID string
	IsSystem    bool
}

// SlotsSearchResult is the per-slot aggregate outcome of the
// multi-query route.
//
// godlike/07 NO-FAKE-AVAILABILITY: a missing slot (e.g. ctx
// canceled mid-batch, per-slot error, toolbar-typed failure) is
// represented as an empty slice in ByRef AND an entry in
// ErroredRefs. Callers branch on the ErroredRefs map to decide
// whether to surface a typed error envelope.
//
// godlike/06 SSOT: the result shape mirrors the canonical
// plan-to-candidate mapping (SlotRef → []ClipCandidate). The
// Sampler downstream receives this map and adapts it to
// ClipSamplerRequest.PreviousSelections across iterations; the
// adapter does NOT pre-build sampler-shaped types — that would
// premature-inline the Sampler's dedup logic.
type SlotsSearchResult struct {
	// ByRef is the canonical map of slot.Ref -> per-slot candidate
	// slice. Empty slice on a slot that errored; the slot is also
	// present in ErroredRefs with the underlying error. Empty
	// slice on a slot with empty SearchQuery (no expensive embed).
	ByRef map[string][]scriptpkg.ClipCandidate

	// TruncatedRefs lists slots whose underlying searcher hits
	// exceeded PerSlotCandidateLimit and were trimmed. Reserved
	// for FASE observability; current Qdrant adapter does not
	// emit (the Searcher caps at Limit + the adapter's
	// per-slot-loop cannot tell truncation happened).
	// Kept non-nil (= empty slice) so the JSON shape is stable.
	TruncatedRefs []string

	// ErroredRefs holds per-slot errors so partial-batch failure
	// is auditable (godlike/07: never represent an unavailable
	// backend as a successful no-op; the slot-level error is
	// preserved per ref).
	ErroredRefs map[string]error

	// Duration is the total wall-clock for the entire SearchSlots
	// call, useful for budget observability + operator dashboards.
	Duration time.Duration
}

// ── Typed errors (godlike/07 fail-closed boundary) ──────────────────

// ErrSlotSearchInvalidPlan: plan nil, plan.Version != 1, or
// plan.Slots empty. Surfaced BEFORE the per-slot loop so a
// malformed plan does not consume embed + search resources.
var ErrSlotSearchInvalidPlan = errors.New("clip_search_port: SearchSlots: plan is nil, plan.Version != 1, or plan.Slots is empty")

// ErrSlotSearchContextCanceled: ctx canceled mid-batch. The
// partial ByRef entries that completed before cancellation are
// still populated; ErroredRefs carries this error on each ref
// that didn't complete. Caller can branch on Either (a)
// TopLevelErr == ErrSlotSearchContextCanceled when ALL slots
// were canceled, or (b) ErroredRefs entry per incomplete slot.
var ErrSlotSearchContextCanceled = errors.New("clip_search_port: SearchSlots: ctx canceled mid-batch")
