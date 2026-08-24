// Package mediamemory — types_sentinels.go: typed fail-closed sentinel errors.
package mediamemory

import "errors"

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
	ErrInvalidAggregateSince = errors.New(
		"mediamemory: AggregateSince `since` is not a valid RFC3339 timestamp",
	)
	ErrCandidateNotFound = errors.New(
		"mediamemory: candidate_id absent from candidate repository",
	)
	ErrInvalidBindingInput = errors.New(
		"mediamemory: binding input missing required field(s) (concept_id / asset_id / slot_kind)",
	)
	ErrBindingMutationDispatcherUnavailable = errors.New(
		"mediamemory: BindingMutationDispatcher unavailable",
	)
	ErrSemanticNotConfigured = errors.New(
		"mediamemory: semantic lookup backend not configured (Qdrant not reachable at boot, or composition wiring missing)",
	)
	ErrSemanticBackendFailed = errors.New(
		"mediamemory: semantic lookup backend failed (Qdrant query returned an error envelope, embedding call failed)",
	)
	ErrInvalidBatchMode = errors.New(
		"mediamemory: batch mode outside canonical closed set (use catalog_only or materialize_top_k)",
	)
	ErrBatchSpecDrift = errors.New(
		"mediamemory: batch Spec drift on idempotent CreateBatch (same Name + different body)",
	)
)

// ── Linker sentinels ──────────────────────────────────────────────

var ErrLinkerUnmappableConcept = errors.New(
	"mediamemory: linker could not map candidate to any (concept × slot_kind) tuple (no detectable entities / no mappable visual actions)",
)

var ErrLinkerExtractFailed = errors.New(
	"mediamemory: linker extractor failed (transcript / keyframe / visual description generator)",
)

var ErrLinkerEmbeddingFailed = errors.New(
	"mediamemory: linker multichannel embedding encoder failed",
)

var ErrLinkerConceptAssignmentFailed = errors.New(
	"mediamemory: linker concept assignment layer returned zero concepts",
)

var ErrLinkerInvariantBroken = errors.New(
	"mediamemory: linker internal invariant broken (binding-without-concept or embedding-without-binding detected post-write)",
)