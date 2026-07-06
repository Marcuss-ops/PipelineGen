// Package completion — complete_job_ports.go (split July 2026).
//
// This file owns the canonical port interfaces and domain row types
// for the completion service. Extracted from complete_job_service.go
// per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: each port interface is the single canonical owner
// of its contract. TxContext is the single in-transaction port surface.
package completion

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
)

// ── Port interfaces (Pattern 0 — keep service out of infra) ───────────

// CompleteJobTxRunner is the typed port for "execute a function
// inside a single SQLite transaction". The runner opens the TX,
// invokes fn with a TxContext, commits if fn returns nil, rolls
// back otherwise. Any panic in fn rolls back + re-panics (mirrors
// the C6 Adapter.go panic-isolation precedent).
type CompleteJobTxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxContext) error) error
}

// TxContext is the typed in-transaction port surface exposed to the
// service. The implementations pass an *sql.Tx through a
// type-masked handle (godlike/06 SSOT: NO database/sql leaks above
// the infrastructure layer — the test mocks implement this
// interface with in-memory state).
//
// godlike/06 SSOT for shadow-out-of-TX calls: every method on
// TxContext is wired to the in-flight TX via the infra layer; no
// shadow connection pool is consulted (because that would split
// the atomic guarantee the C7 spec requires).
type TxContext interface {
	// GetJob fetches the current job row scoped to the TX.
	// Returns (nil, ErrConcurrentLeaseRefutation) if not found —
	// the lease-stolen + job-deleted cases share the same typed
	// surface because both indicate the lease is no longer valid.
	GetJob(ctx context.Context, jobID string) (*JobRow, error)

	// UpdateJobToSucceededCAS flips jobs.status to SUCCEEDED with
	// the canonical guard (id, lease_id, attempt, status NOT IN
	// terminal sinks). Returns rowsAffected; ErrConcurrentLeaseRefutation
	// if 0 (guard rejected by SQLite).
	UpdateJobToSucceededCAS(ctx context.Context, jobID, leaseID string, attempt int) (int64, error)

	// InsertResultOnConflict is the UNIQUE (job_id, attempt,
	// result_hash) dedup surface for job_results. The infra-layer
	// implementation uses INSERT ... ON CONFLICT (job_id, attempt,
	// result_hash) DO NOTHING RETURNING id; replayed=true when
	// the existing row was preserved (no work re-done).
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
	// safely round-trip without breaking the uniqueness invariant
	// (forward-pointer infra-layer implementation; today the
	// canonical adapter is the test mock that satisfies the
	// contract for the service-layer migration to CUTOVER).
	InsertAssetLocations(ctx context.Context, entries []AssetLocationEntry) error
}

// ── IdempotencyCachePort (post-TX replay short-circuit) ──────────────

// IdempotencyCachePort is the typed port for the post-TX replay
// cache. The canonical implementation is an in-memory LRU + a
// SQLite tenant table; the test mock implements it with a
// thread-safe map. On a hit, the service short-circuits at step 2
// (pre-TX replay probe) without opening a SQLite TX.
type IdempotencyCachePort interface {
	// LookupReplay returns a cached canonical response for the
	// given (jobID, attempt, resultHash) triple, or (nil, false,
	// nil) when the triple is not cached.
	LookupReplay(ctx context.Context, jobID string, attempt int, resultHash string) (*remote.CompleteJobResponse, bool, error)

	// StoreCanonical persists the canonical response so future
	// replays of the same triple can short-circuit at step 2.
	StoreCanonical(ctx context.Context, jobID string, attempt int, resultHash string, resp *remote.CompleteJobResponse) error
}

// ── JobTypeRegistry (Pattern 0 port for SSOT policy lookup) ───────────

// CompleteWithArtifactsSender is the Pattern 0 port (godlike/06 SSOT)
// for the Sender-side atomic terminal surface consumed by
// PublishAndCompleteUseCase. The single method mirrors
// WithArtifactsService.CompleteWithArtifacts — the canonical
// post-P0-COMPL-4 dedup-closure surface.
type CompleteWithArtifactsSender interface {
	CompleteWithArtifacts(ctx context.Context, req *remote.CompleteWithArtifactsRequest, published []*finalization.PublishedArtifact) (*remote.CompleteWithArtifactsResponse, error)
}

// Compile-time pin (AGENTS.md Pattern 0): drift in the concrete
// WithArtifactsService.CompleteWithArtifacts signature is a build
// failure (the interface anchor catches signature drift at compile).
var _ CompleteWithArtifactsSender = (*WithArtifactsService)(nil)

// JobTypeRegistry is the typed port for "does this job type produce
// artifacts". godlike/06 SSOT: the application-layer JobRegistry
// (`internal/application/jobs/registry.go::Registry`) is the SINGLE
// canonical owner of this fact — NOT the request envelope, NOT the
// SQL column. The in-TX typed-error gate in completeInTx uses this
// port to look up the policy by jobRow.JobType (fetched in-TX) and
// reject legacy-shape requests for artifact-producing jobs with
// remote.ErrCompleteJobPathViolation.
//
// EXPAND-phase semantics (godlike/07 fail-closed at composition): the
// port is OPTIONAL on the Service struct. When registry == nil the
// in-TX gate is silently skipped (backward-compat for callers that
// haven't yet wired the JobTypeRegistry). The BACKFILL phase wires
// the registry via Service.WithJobTypeRegistry(impl) at the composition
// root. The CONTRACT phase promotes registry to a non-optional
// constructor argument (fail-closed at boot if nil). Today this PR
// ships the surface live + 3 TDD tests + the canonical sentinel
// declaration at internal/domain/remote/complete_job.go.
type JobTypeRegistry interface {
	// ProducesArtifacts returns true if jobs of the given type MUST
	// route through CompleteWithArtifacts (and may NOT route through
	// the legacy Complete path). The registry is the SSOT; the SQL
	// map at SQLiteStore.producesArtifacts is a propagation copy
	// seeded via Registry.ProducesArtifactsMap() at boot.
	ProducesArtifacts(jobType string) bool
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

type JobRow struct {
	JobID string
	// JobType is the canonical job_type from jobs.type column (mirrored from
	// the SQLite row). Required for the in-TX typed-error gate in
	// completeInTx that looks up the JobTypeRegistry.ProducesArtifacts
	// policy per job type (godlike/06 SSOT: the registry is the canonical
	// owner of "does this job type produce artifacts"; the request envelope
	// is NOT trusted for policy fields).
	JobType string
	LeaseID string
	Attempt int
	Status  job.Status
}

// PriorArtifactHash carries the (sha256, remote_asset_id) tuple
// for one prior row in job_artifacts.
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
	ArtifactID  string
	AssetID     string
	Kind        asset.LocationKind
	Provider    string
	ExternalID  string
	AccessURL   string
	DownloadURL string
	MIMEType    string
	SizeBytes   int64
	FileHash    string
	IsPrimary   bool
}
