// Package internal hosts the canonical shared primitives for the
// Sender-side atomic completion services (jobpkg/completion). It is
// the SINGLE canonical owner of the tx-runner, lease-fence,
// idempotency, result-writer, outbox-writer, and asset-location-writer
// surfaces (godlike/06 SSOT one-canonical-owner-per-fact).
//
// Two services consume these primitives side-by-side:
//
//   - CompleteJobService (complete_job_service.go::Service) — the
//     no-artifact path (the agent asserts "CompleteJobService for
//     no-artifact jobs").
//
//   - CompleteWithArtifactsService (complete_with_artifacts_service.go::WithArtifactsService
//     ): the artifact-producing path; adds the 7th TxContext method
//     InsertAssetLocations (the agent asserts "CompleteWithArtifactsService
//     for artifact-producing jobs").
//
// Both services MUST use the SAME primitives per the user spec (June
// 2026, FASE-6 closeout). The external completion package exports
// these primitives via Go-level type aliases at primitives_aliases.go
// so existing callers + tests (mockTxContext + mockCache + mockTxRunner
// in complete_job_service_test.go:155-180, sqlTxContext in
// completion_e2e_test.go:170-200, inMemCache in completion_e2e_test.go:382)
// keep compiling unchanged.
//
// godlike/06 SSOT: this package is package-internal (NOT under pkg/)
// because the typed contracts (CompleteJobTxRunner + TxContext +
// IdempotencyCachePort) are infrastructure-bound (database/sql,
// outbox tables) and the architecture-policy forbids pkg/leakage of
// database/sql types (per AGENTS.md Pattern 4 §"VIETATO import da
// internal/ dentro pkg/"; pkg/ is leaf-only). The package name
// `internal` keeps the imports out of API surface reachability while
// allowing the application's two services to consume the canonical
// types.
//
// godlike/07 typed-error contract: every typed sentinel is owned by
// the canonical domain/remote package (CompleteJob sentinels +
// CompleteWithArtifacts sentinels + ErrRemoteArtifactLocationMismatch).
// This package exposes ONLY primitive types — no error declarations
// — so future drift on sentinel messages is impossible at this layer.
package jobs

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Tx-runner (BEGIN IM ... COMMIT / ROLLBACK boundary) ─────────────

// CompleteJobTxRunner is the typed port for "execute a function inside
// a single SQLite transaction". The runner opens the TX, invokes fn
// with a TxContext, commits if fn returns nil, rolls back otherwise.
// Any panic in fn rolls back + re-panics (mirrors the C6 Adapter.go
// panic-isolation precedent).
//
// Reused by both CompleteJobService and CompleteWithArtifactsService;
// the single canonical surface is the user-spec's "SAME tx-runner"
// requirement.
type CompleteJobTxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxContext) error) error
}

// ── TxContext (the in-TX typed-write surface; 7 methods) ────────────

// TxContext is the typed in-transaction port surface exposed to the
// services. Implementations pass an *sql.Tx through a type-masked
// handle (godlike/06 SSOT: NO database/sql leaks above the
// infrastructure layer — the test mocks implement this interface
// with in-memory state).
//
// godlike/06 SSOT for shadow-out-of-TX calls: every method on
// TxContext is wired to the in-flight TX via the infra layer; no
// shadow connection pool is consulted (because that would split
// the atomic guarantee the C7 spec requires).
//
// godlike/07 typed-error contract: the 7 methods collectively
// encode the canonical 7-step single-TX atomic chain that both
// services share. A future refactor that drops OR renames a method
// MUST fail to compile because the test mockTxContext + sqlTxContext
// implement this interface directly (compile-time pin).
//
// The 7-method surface (C7 + Azione 6):
//
//  1. GetJob — read current job row (lease fence)
//  2. UpdateJobToSucceededCAS — atomic CAS for status → SUCCEEDED
//  3. InsertResultOnConflict — UNIQUE (job_id, attempt, result_hash)
//     dedup for job_results
//  4. GetPriorArtifactHashes — read prior (sha256, remote_asset_id)
//     tuples for round-trip check
//  5. PersistArtifactMap — write job_artifacts mapping rows
//  6. InsertOutboxEnvelope — enqueue outbox events in same TX
//  7. InsertAssetLocations — write asset_locations rows (P1 wave
//     Azione 6, July 2026; the artifact-aware variant uses this;
//     the no-artifact variant skips the call)
type TxContext interface {
	// GetJob fetches the current job row scoped to the TX.
	// Returns (nil, ErrConcurrentLeaseRefutation) if not found —
	// the lease-stolen + job-deleted cases share the same typed
	// surface because both indicate the lease is no longer valid.
	GetJob(ctx context.Context, jobID string) (*JobRow, error)

	// UpdateJobToSucceededCAS flips jobs.status to SUCCEEDED with
	// the canonical guard (id, lease_id, attempt, status NOT IN
	// terminal sinks). Returns rowsAffected;
	// ErrConcurrentLeaseRefutation if 0 (guard rejected by SQLite).
	UpdateJobToSucceededCAS(ctx context.Context, jobID, leaseID string, attempt int) (int64, error)

	// InsertResultOnConflict is the UNIQUE (job_id, attempt,
	// result_hash) dedup surface for job_results. The infra-layer
	// implementation uses INSERT ... ON CONFLICT (job_id, attempt,
	// result_hash) DO NOTHING RETURNING id; replayed=true when the
	// existing row was preserved (no work re-done).
	InsertResultOnConflict(ctx context.Context, jobID string, attempt int, codecID string, payload []byte, resultHash string) (rowID int64, replayed bool, err error)

	// GetPriorArtifactHashes returns the (sha256, remote_asset_id)
	// tuple for every artifact previously persisted to
	// job_artifacts for the given jobID. Used for the hash round-
	// trip check (godlike/07 drift detection).
	GetPriorArtifactHashes(ctx context.Context, jobID string) (map[string]PriorArtifactHash, error)

	// PersistArtifactMap inserts one row per manifest entry to
	// job_artifacts(job_id, artifact_id, sha256, remote_asset_id,
	// status, attempt). ON CONFLICT (job_id, artifact_id, attempt)
	// DO UPDATE SET sha256=excluded.sha256 (allow round-trip
	// updates without breaking the uniqueness invariant).
	PersistArtifactMap(ctx context.Context, jobID string, attempt int, entries []ArtifactMapEntry) error

	// InsertOutboxEnvelope enqueues one outbox event in the same
	// TX (atomic with the complete-job state flips). The
	// (envelope.idempotencyKey, envelope.event_kind) tuple is
	// unique; a replayed replay yields ErrOutboxEnvelopeDuplicate
	// (typed sentinel at infra layer).
	InsertOutboxEnvelope(ctx context.Context, envelope OutboxEnvelope) error

	// InsertAssetLocations (P1 wave Azione 6, July 2026) persists
	// asset_locations rows in the same atomic TX as the job flip +
	// result insert + artifact map + outbox envelopes. The
	// (asset_id, location_kind) UNIQUE lets ON CONFLICT DO UPDATE
	// safely round-trip without breaking the uniqueness invariant.
	// Used by CompleteWithArtifactsService; CompleteJobService does
	// NOT call this method but shares the type signature via the
	// interface (godlike/06 SSOT: same TxContext signature for both
	// services is a forward-prevention invariant).
	InsertAssetLocations(ctx context.Context, entries []AssetLocationEntry) error
}

// ── Domain row types (mirror the canonical DB columns) ───────────────
//
// These types are intentionally distinct from the canonical
// domain/job.Job: the canonical Job is the API-level envelope
// (carries codec_id correlation + lifecycle metadata), while the
// service-level JobRow is the in-TX row-immutable snapshot used
// for CAS operations. The TxContext.GetJob returns JobRow (not
// domain/job.Job) so the service can reason about CAS ordering
// without dragging the full envelope through the test surface.

// JobRow is the in-TX row-immutable snapshot consumed by the service
// for CAS operations.
type JobRow struct {
	JobID   string
	LeaseID string
	Attempt int
	Status  job.Status
}

// PriorArtifactHash carries the (sha256, remote_asset_id, status)
// tuple for one prior row in job_artifacts.
type PriorArtifactHash struct {
	SHA256        string
	RemoteAssetID string
	Status        string
}

// ArtifactMapEntry is the typed write to job_artifacts for one
// artifact in the request's RemoteArtifactManifest.
type ArtifactMapEntry struct {
	ArtifactID    string
	SHA256        string
	RemoteAssetID string
	Status        string
}

// OutboxEnvelope is the typed envelope consumed by the
// outbox-events infrastructure. The exact event_kind constants
// live on the infra-layer side; the service just threads unique
// idempotency keys so (event_kind, idempotency_key) collisions
// surface as the typed duplicate sentinel.
type OutboxEnvelope struct {
	IdempotencyKey string // canonical SHA-256 of (jobID, attempt, event_kind)
	EventKind      string // e.g. "artifact.uploaded", "job.completed"
	Payload        []byte // canonical JSON wire-payload for the event
}

// AssetLocationEntry is the typed in-TX write surface for the
// asset_locations table (P1 wave Azione 6, July 2026). Mirrors
// the canonical SQL columns minus db-managed fields (id, created_at,
// updated_at) — those are filled by the infra-layer adapter at
// UpsertLocation time. The (asset_id, location_kind) UNIQUE
// permits ON CONFLICT DO UPDATE round-trips without breaking
// the uniqueness invariant.
//
// godlike/06 SSOT: this struct lives in the completion package
// (NOT in domain/asset) because the completion service is the
// single canonical owner of "what asset_locations rows are tied
// to which artifact_id" — making the typed write surface local
// prevents other code paths from accidentally writing their own
// shape to the same table.
type AssetLocationEntry struct {
	// ArtifactID is the content-hash artifact_id (from
	// PublishedArtifact.ArtifactID) preserved for the audit join
	// that connects the canonical (job_artifacts) row to the
	// (asset_locations) row.
	ArtifactID string

	// AssetID is the catalog asset_id resolved from the request's
	// AssetMappings[ArtifactID]. The SQL row is keyed on this.
	AssetID string

	// Kind is the typed enum for the location_kind SQL column.
	// Mirrors asset.LocationKind (Local / Drive / ObjectStorage).
	Kind asset.LocationKind

	// Provider is the storage backend label (e.g. "drive", "s3").
	// Distinct from Kind (the type-system enum) — Provider is the
	// free-form label for telemetry + UI rendering.
	Provider string

	// ExternalID is the provider-specific file identifier (Drive
	// file ID, S3 object key, etc.). Mapped to asset_locations.
	// external_id. Distinct from AccessURL (the human-readable
	// web view) and DownloadURL (the direct-download URL).
	ExternalID string

	// AccessURL is the web view link copied from
	// finalization.AssetLocation.WebViewLink. Mapped to
	// web_view_link SQL column.
	AccessURL string

	// DownloadURL is the direct-download URL copied from
	// finalization.AssetLocation.DownloadLink. Mapped to
	// download_url SQL column.
	DownloadURL string

	// MIMEType is the IANA media type (from PublishedArtifact.
	// MIMEType). Mapped to mime_type SQL column.
	MIMEType string

	// SizeBytes is the artifact size (from PublishedArtifact.
	// SizeBytes). Mapped to file_size_bytes SQL column.
	SizeBytes int64

	// LegacyFileMD5 is the SHA-256 hex digest (from PublishedArtifact.
	// SHA256). Distinct from any provider-returned checksum —
	// file_hash stores the canonical content hash so the
	// round-trip gate can verify byte-stability independent of
	// the publication backend.
	LegacyFileMD5 string

	// IsPrimary marks the row as the primary location for the
	// (asset_id, kind) UNIQUE. Defaults to true for the first
	// location written; secondary locations (e.g. a backup copy)
	// flip to false.
	IsPrimary bool
}

// ── IdempotencyCachePort (post-TX replay short-circuit) ──────────────

// IdempotencyCachePort is the typed port for the post-TX replay
// cache. The canonical implementation is an in-memory LRU + a
// SQLite tenant table; the test mock implements it with a
// thread-safe map. On a hit, the service short-circuits at step 2
// (pre-TX replay probe) without opening a SQLite TX.
//
// Reused by both CompleteJobService and CompleteWithArtifactsService;
// the single canonical surface is the user-spec's "SAME idempotency"
// requirement.
type IdempotencyCachePort interface {
	// LookupReplay returns a cached canonical response for the
	// given (jobID, attempt, resultHash) triple, or (nil, false,
	// nil) when the triple is not cached.
	LookupReplay(ctx context.Context, jobID string, attempt int, resultHash string) (*remote.CompleteJobResponse, bool, error)

	// StoreCanonical persists the canonical response so future
	// replays of the same triple can short-circuit at step 2.
	StoreCanonical(ctx context.Context, jobID string, attempt int, resultHash string, resp *remote.CompleteJobResponse) error
}
