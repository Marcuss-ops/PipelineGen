// Package qdrant — ReindexAllV2 (PR 8, June 2026).
//
// The previous ReindexAll (index_writer.go) loaded every media_assets.id
// into memory and held the entire batched set up before upserting —
// 100% OOM crash on any fleet > ~50k rows. Worse: a crash mid-run
// dropped every committed upsert; resume-after-crash was undefined.
//
// PR 8 ships ReindexAllV2 alongside the v1 entry point (NOT a
// replacement; the v1 surface stays for the cmd/admin/reindex_qdrant.go
// JSON-shape contract and any 3rd-party consumers). The V2 entry point
// is documented as the canonical path; PR 9 will swap the call sites
// over and retire v1.
//
// Scope shipped in this PR:
//   - SQLite keyset pagination via qdrantprojection.BatchReader
//     (memory-bounded by `batchSize`, never holds the whole fleet).
//   - Per-batch checkpoint via qdrantprojection.CheckpointStore
//     (resume after crash is exactly-at-least-once because Qdrant's
//     upsert-by-id is itself idempotent).
//   - DLQ per validation failure via
//     qdrantprojection.CheckpointStore.DLQ.
//   - Retry with jitter via pkg/retry — only for idempotent ops
//     (UpsertPoints). Skips non-idempotent ops.
//
// Scope deferred to follow-ups (sections #2, #6, #7 of PR 8 verdict):
//   - Bounded fan-out validation workers (`pkg/concurrent.WithContext`
//     across assets per batch) — see batch_validation.go, deferred.
//   - Pre-promotion hash sampling (N% of points compared to
//     SQLite.content_hash) — see pr_blue_green_sampling.go, deferred.
//   - Token-bucket rate-limiter (`max_batches_per_second` config) —
//     see rate_limit.go, deferred.
//   - Prometheus metrics (qdrant_reindex_documents_total{status}) —
//     see metrics_v2.go, deferred.

package qdrant

// ReindexAllV2Options configures the v2 reindex entry point.
//
// Method:
//
//   - "single"   — process each batch synchronously inside Run.
//   - "parallel" — fan out per-asset validation via pkg/concurrent
//     (NOT yet implemented in PR 8; deferred to follow-up).
//
// JobID: caller-supplied UUID-string. Distinct jobs MUST use distinct
// JobIDs; the checkpoint row's PK enforces uniqueness.
type ReindexAllV2Options struct {
	JobID            string
	TargetCollection string
	BatchSize        int
	// Limit caps the total number of rows processed. 0 = no cap (full
	// fleet index). Used by the admin CLI to test the pipeline
	// against smaller-than-fleet samples.
	Limit int

	// Method selects the validation pipeline processing model. PR 8
	// supports only "single"; "parallel" is the documented extension
	// point but not yet wired (the parallel path lives in the
	// deferred batch_validation.go).
	Method string
}

// ReindexAllV2 is the v2 entry point. The shape of *ReindexResult is
// unchanged from v1 so operators reading the JSON output don't need
// to distinguish versions — only the underlying pipeline differs.
//
// Behavioural differences vs ReindexAll (v1):
//   - Memory bounded by BatchSize; never holds the whole set up.
//   - Resume-safe: a crash between batches leaves the checkpoint at
//     the LAST committed cursor; the next invocation reads from
//     there.
//   - DLQ-visible: validation failures do NOT abort the run; they
//     land in qdrantprojection_dlq so operators can triage.
//
// Failure semantics (PR 10-style fail-closed vs PR 8's soft-fail
// on DLQ-eligible validation errors):
//   - HARD failures (pagination query failures, checkpoint write
//     failures, Qdrant upsert-side errors) abort the run; the caller
//     receives (nil, err) so they can investigate.
//   - SOFT failures (per-doc validation failures) DO NOT abort; they
//     land in the DLQ and the run continues with the next batch.
//     Operators observe `error_count` growing in metrics; the
//     runbook directs them to inspect the DLQ at completion.
