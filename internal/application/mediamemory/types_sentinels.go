// Package mediamemory — types_sentinels.go is the canonical home
// for the typed-fail-closed sentinel errors raised from every
// mediamemory capability seam. godlike/07 NO-FAKE-AVAILABILITY
// (typed fail-closed boundary):
//
//   - ErrInvalidPhrase                       : malformed input to Normalizer
//   - ErrConceptNotFound                     : concept_id absent from concept repository
//   - ErrBindingNotFound                     : binding_id absent from binding repository
//   - ErrDuplicateBinding                    : (concept_id, asset_id, slot_kind) already present
//   - ErrInvalidSlotKind                     : slot_kind outside the canonical closed set
//   - ErrApprovalRequired                    : ranker refuses to expose unapproved binding
//   - ErrCandidateMaterializationFailed      : materialize worker returned no asset_id
//   - ErrBatchNotFound                       : batch_id unknown to BatchService
//   - ErrBatchNotReconcilable                : batch is in a terminal state (already reconciled)
//   - ErrInvalidFeedbackAction               : unknown FeedbackAction value
//   - ErrInvalidAggregateSince               : malformed `since` input to FeedbackService.AggregateSince
//   - ErrCandidateNotFound                   : candidate_id absent from candidate repository
//   - ErrInvalidBindingInput                 : missing required binding field(s)
//   - ErrBindingMutationDispatcherUnavailable : nil dispatcher at composition time
//   - ErrSemanticNotConfigured               : semantic backend absent
//   - ErrSemanticBackendFailed               : semantic backend operational failure
//   - ErrInvalidBatchMode                    : BatchSpec.Mode outside closed set
//   - ErrBatchSpecDrift                      : idempotent CreateBatch saw a Spec drift
//   - ErrLinkerUnmappableConcept             : linker could not map (concept × slot_kind)
//   - ErrLinkerExtractFailed                 : linker extractor failed
//   - ErrLinkerEmbeddingFailed               : linker multichannel embedding failed
//   - ErrLinkerConceptAssignmentFailed      : linker concept assignment returned zero
//   - ErrLinkerInvariantBroken               : linker internal invariant post-write
//
// Each sentinel is wrapped with %w in the service methods so callers
// probe via errors.Is, not string-match. A duplicate sentinel
// message would let errors.Is mis-route — the canonical distinct
// message + godlike/06 SSOT invariant pin via types_test.go.
//
// File split ownership (godlike/06 SSOT):
//   - types.go               : package doc + SlotKind alias
//   - types_enums.go         : 9 enums + their constants + 9 IsKnown predicates + Provider tag constants + IsKnownProvider
//   - types_entities.go      : MediaConcept + MediaBinding + MediaCandidate + BatchSpec + Batch + BatchChild + UsageEvent
//   - types_resolver.go      : VisualIntent + SceneSpec + Layer + CandidateOption + SceneIntent + SceneBackendCall + SceneResolutionTrace + SceneVisualPlan + ResolvePolicy + OptionalResolvePolicy + ResolveRequest + ResolveResult
//   - types_linker.go        : LinkerRequest + LinkerResult + EncodingChannels + MediaEmbedding + TranscriptSegment + Keyframe
//   - types_sentinels.go     : 19 sentinel errors (14 phase 1.x + 5 ErrLinker*)  ← this file
package mediamemory

import "errors"

// ── Typed error envelope (godlike/07) ──────────────────────────────

var (
	ErrInvalidPhrase = errors.New(
		"mediamemory: invalid phrase (empty or unparsable by the canonical Normalizer)",
	)
	ErrConceptNotFound = errors.New(
		"mediamemory: concept_id absent from concept repository",
	)
	ErrBindingNotFound = errors.New(
		"mediamemory: binding_id absent from binding repository",
	)
	ErrDuplicateBinding = errors.New(
		"mediamemory: duplicate (concept_id, asset_id, slot_kind) — UNIQUE(language, phrase_fingerprint) equivalent",
	)
	ErrInvalidSlotKind = errors.New(
		"mediamemory: slot_kind outside canonical closed set (use media.IsKnownSlotKind)",
	)
	ErrApprovalRequired = errors.New(
		"mediamemory: binding is not approved (resolver refuses to expose unapproved binding)",
	)
	ErrCandidateMaterializationFailed = errors.New(
		"mediamemory: materialize worker returned no asset_id for the candidate (stockpipeline failed)",
	)
	ErrBatchNotFound = errors.New(
		"mediamemory: batch_id unknown to BatchService",
	)
	ErrBatchNotReconcilable = errors.New(
		"mediamemory: batch is in a terminal state (already Completed/Failed) — start a new batch",
	)
	ErrInvalidFeedbackAction = errors.New(
		"mediamemory: unknown FeedbackAction value (closed set: accepted/rejected/replaced/trimmed/used_successfully)",
	)
	// ErrInvalidAggregateSince is the canonical Fase 1.6 sentinel
	// for a malformed `since` input to FeedbackService.AggregateSince.
	// godlike/07 NO-FAKE-AVAILABILITY: an invalid timestamp MUST
	// surface as a typed envelope (NOT a silent zero-value
	// time.Time) so the wire handler can branch via errors.Is and
	// return a 400 to the caller. godlike/06 SSOT: distinct from
	// ErrInvalidPhrase (reserved for Normalizer input corruption).
	ErrInvalidAggregateSince = errors.New(
		"mediamemory: AggregateSince `since` is not a valid RFC3339 timestamp",
	)
	ErrCandidateNotFound = errors.New(
		"mediamemory: candidate_id absent from candidate repository",
	)
	// ErrInvalidBindingInput is the canonical sentinel for binding
	// payloads that miss required fields (concept_id, asset_id,
	// slot_kind) but are otherwise well-typed. godlike/06 SSOT:
	// distinct from ErrInvalidSlotKind (which means "slot kind is
	// outside the canonical closed set"); the wire code branches on
	// these separately.
	ErrInvalidBindingInput = errors.New(
		"mediamemory: binding input missing required field(s) (concept_id / asset_id / slot_kind)",
	)
	// ErrBindingMutationDispatcherUnavailable is the canonical sentinel
	// returned when BindingService detects that the canonical
	// BindingMutationDispatcher was not wired at composition time. A nil
	// dispatcher must never be treated as a silent no-op.
	ErrBindingMutationDispatcherUnavailable = errors.New(
		"mediamemory: BindingMutationDispatcher unavailable",
	)
	// ErrSemanticNotConfigured is the canonical sentinel for a
	// missing/broken semantic backend at the mediamemory
	// capability boundary (godlike/07 NO-FAKE-AVAILABILITY —
	// absent backend MUST NOT silently degrade to zero-candidate
	// reads).
	ErrSemanticNotConfigured = errors.New(
		"mediamemory: semantic lookup backend not configured (Qdrant not reachable at boot, or composition wiring missing)",
	)
	// ErrSemanticBackendFailed is the canonical sentinel for an
	// operational failure of the semantic backend (Qdrant
	// HybridSearch returned an error envelope, embedding call
	// failed with a transient/non-ignored error).
	ErrSemanticBackendFailed = errors.New(
		"mediamemory: semantic lookup backend failed (Qdrant query returned an error envelope, embedding call failed)",
	)
	// ErrInvalidBatchMode is the canonical sentinel for a
	// BatchSpec whose Mode is not in the closed set
	// (catalog_only / materialize_top_k). godlike/06 SSOT:
	// distinct from ErrBatchNotReconcilable (terminal-state
	// refusal) so the wire handler can branch 400 (mode) vs 409
	// (terminal-state) cleanly.
	ErrInvalidBatchMode = errors.New(
		"mediamemory: batch mode outside canonical closed set (use catalog_only or materialize_top_k)",
	)
	// ErrBatchSpecDrift is the canonical sentinel for the
	// idempotent-by-name CreateBatch path: when a caller supplies
	// the same Spec.Name + a different Spec body (e.g. switched
	// Mode from catalog_only to materialize_top_k after the
	// parent was already created), the canonical SSOT rejects
	// the second call. godlike/06 SSOT: Spec is immutable
	// post-CreateBatch so the worker treats the parent shape
	// as fixed for the batch lifetime.
	ErrBatchSpecDrift = errors.New(
		"mediamemory: batch Spec drift on idempotent CreateBatch (same Name + different body)",
	)
)

// Linker sentinels (godlike/06 SSOT: same envelope family as
// discovery-worker sentinels; godlike/07 NO-FAKE-AVAILABILITY:
// every non-trivial linker failure surfaces a typed envelope
// so the BatchService.EnrichLinker orchestrator can branch
// hard-fail (Failed+continue) vs resumable (leave Searched +
// return).

// ErrLinkerUnmappableConcept is the HARD-fail sentinel: the
// linker could not produce any (concept × slot_kind) tuple
// for the candidate (e.g. zero detectable entities, zero
// mappable visual actions). The candidate's DiscoveryStatus
// is set to DiscoveryFailed at the batch orchestrator and the
// batch continues with the next candidate. Operator-visible
// in the dashboard per-candidate Failures[] column.
var ErrLinkerUnmappableConcept = errors.New(
	"mediamemory: linker could not map candidate to any (concept × slot_kind) tuple (no detectable entities / no mappable visual actions)",
)

// ErrLinkerExtractFailed is the FAIL-CLOSED envelope for any
// failure in the extraction phase (TranscriptExtractor /
// KeyframeExtractor / VisualDescriptionGenerator). Wrapped
// with %w so the BatchService orchestrator's errors.Is branch
// routes it through its per-candidate failure record. The
// candidate's DiscoveryStatus is NOT mutated on this envelope
// so a subsequent EnrichLinker retry re-runs the full
// extraction pipeline naturally (idempotent Resume contract).
var ErrLinkerExtractFailed = errors.New(
	"mediamemory: linker extractor failed (transcript / keyframe / visual description generator)",
)

// ErrLinkerEmbeddingFailed is the FAIL-CLOSED envelope for the
// multichannel embedding encoder path. Same Resume-on-retry
// semantics as ErrLinkerExtractFailed — the candidate stays
// at DiscoverySearched until the encoder returns a valid
// vector. godlike/07 NO-FAKE-AVAILABILITY: a zero-length
// vector is NOT silently accepted; canonical embedding call
// sites check len(MediaEmbedding.Vector) > 0 before stamping
// the Qdrant payload and surface ErrLinkerEmbeddingFailed
// otherwise.
var ErrLinkerEmbeddingFailed = errors.New(
	"mediamemory: linker multichannel embedding encoder failed",
)

// ErrLinkerConceptAssignmentFailed is the FAIL-CLOSED envelope
// for the concept-assignment phase: the EntityDetector /
// ConceptAssigner generated zero canonical concepts.
// godlike/06 SSOT distinct from ErrLinkerUnmappableConcept
// (assignment-failure vs unmappable: a zero-concept assignment
// result is operational; an unmappable result is semantic).
// Both end up at DiscoveryFailed on the candidate.
var ErrLinkerConceptAssignmentFailed = errors.New(
	"mediamemory: linker concept assignment layer returned zero concepts",
)

// ErrLinkerInvariantBroken is the PANIC-equivalent sentinel:
// the linker reached an internal post-write state that the
// canonical invariants forbid (e.g. binding persisted without
// a concept row, embedding stamped to Qdrant without a
// concept_id Match). godlike/07 NO-FAKE-AVAILABILITY: this is
// NEVER recoverable from Resume — it surfaces a 500-level
// typed envelope and the candidate goes to DiscoveryFailed.
var ErrLinkerInvariantBroken = errors.New(
	"mediamemory: linker internal invariant broken (binding-without-concept or embedding-without-binding detected post-write)",
)
