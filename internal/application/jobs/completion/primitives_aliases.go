// Package completion — primitives_aliases.go
// (PR-GODOBJ-5-COMPLETION-COLLAPSE, August 2026).
//
// Go-level type aliases exposing the canonical shared primitives
// from internal/application/jobs/completion/internal/. Each alias
// is a Go 1.9+ type-alias (type A = B) — assignment-compatible
// with the underlying type and assignment-incompatible with any
// other type carrying the same structural signature but a different
// identity.
//
// Backward-compatibility surface: existing callers (services,
// mocks, tests, e2e fixtures) reference the canonical types via
// completion.CompleteJobTxRunner / completion.TxContext /
// completion.JobRow / etc. The aliases guarantee that:
//
//   - All existing call sites compile unchanged
//   - All existing mock implementations (mockTxContext at
//     complete_job_service_test.go:152, sqlTxContext at
//     completion_e2e_test.go:170, inMemCache at
//     completion_e2e_test.go:382) keep satisfying the canonical
//     surface
//   - Future drift on the canonical shape surfaces as a build
//     failure (the compile-time pins `var _ TxContext = (*mockTxContext)(nil)`
//     transitively assert via the alias)
//
// godlike/06 SSOT: the type aliases preserve the canonical-owner
// invariant — the underlying types live in internal/, the public
// aliases are pointer-equivalent passthroughs. A future refactor
// that adds a new method must add it to internal/TxContext first
// (the canonical site); the alias updates transitively.
package completion

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion/internal"
)

// ── Tx-runner ────────────────────────────────────────────────────────

// CompleteJobTxRunner is the typed port for the single-TX orchestrator.
// Both CompleteJobService and CompleteWithArtifactsService consume
// this surface. See internal.CompleteJobTxRunner for the canonical
// contract.
type CompleteJobTxRunner = internal.CompleteJobTxRunner

// ── TxContext (7 methods) ────────────────────────────────────────────

// TxContext is the typed in-TX write surface exposed by the runner
// to the service. See internal.TxContext for the canonical contract
// (7-method surface: GetJob + UpdateJobToSucceededCAS +
// InsertResultOnConflict + GetPriorArtifactHashes + PersistArtifactMap
// + InsertOutboxEnvelope + InsertAssetLocations).
type TxContext = internal.TxContext

// ── Row types ────────────────────────────────────────────────────────

// JobRow is the in-TX row-immutable snapshot.
type JobRow = internal.JobRow

// PriorArtifactHash carries one prior row from job_artifacts.
type PriorArtifactHash = internal.PriorArtifactHash

// ArtifactMapEntry is the typed write surface for one job_artifacts row.
type ArtifactMapEntry = internal.ArtifactMapEntry

// OutboxEnvelope is the typed write surface for one outbox_events row.
type OutboxEnvelope = internal.OutboxEnvelope

// AssetLocationEntry is the typed write surface for one
// asset_locations row. Used by CompleteWithArtifactsService;
// CompleteJobService does not call InsertAssetLocations but shares
// the type signature via the TxContext interface (godlike/06 SSOT).
type AssetLocationEntry = internal.AssetLocationEntry

// ── Idempotency cache ───────────────────────────────────────────────

// IdempotencyCachePort is the post-TX replay-cache port consumed by
// both services. See internal.IdempotencyCachePort for the
// canonical contract.
type IdempotencyCachePort = internal.IdempotencyCachePort
