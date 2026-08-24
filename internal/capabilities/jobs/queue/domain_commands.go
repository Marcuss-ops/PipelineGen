// Package jobs — cross-layer Worker command types. Every command struct here
// is a thin type alias pointing at the canonical definition in
// internal/domain/job (added in ondata-5 stage 10). This preserves package
// name `jobs` (`jobs.RegisterWorkerCommand`) for in-package callers while
// letting downstream consumers (`job.RegisterWorkerCommand` in internal/domain
// or internal/infrastructure) see the same struct.
package queue

import (
	"encoding/json"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// WorkerCapabilities is the worker registration payload stored by the
// authoritative API.
type WorkerCapabilities = job.WorkerCapabilities

type RegisterWorkerCommand = job.RegisterWorkerCommand
type ClaimCommand = job.ClaimCommand
type HeartbeatCommand = job.HeartbeatCommand
type RenewCommand = job.RenewCommand
type ProgressCommand = job.ProgressCommand
type CompleteCommand = job.CompleteCommand
type FailCommand = job.FailCommand

// CompleteWithArtifactsCommand carries the result manifest, published
// artifacts, and outbox events for atomic job finalisation through the
// JobFinalizer spine (Spina Dorsale, Fase 3). Workers that produce
// artifacts MUST use this path instead of CompleteCommand so that
// asset records, versions, locations, and outbox events are written
// in the same transaction as the SUCCEEDED transition.
//
// The command carries enough information for the broker to construct
// a finalization.FinalizationRequest. The broker is responsible for
// constructing the Lease from its own knowledge of the job row.
type CompleteWithArtifactsCommand struct {
	// Worker credentials (mirrors CompleteCommand).
	WorkerID         string `json:"worker_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	JobID            string `json:"job_id"`
	LeaseID          string `json:"lease_id"`
	ExpectedRevision int    `json:"expected_revision"`
	CorrelationID    string `json:"correlation_id,omitempty"`

	// Result manifest data (capability-specific).
	ResultData json.RawMessage `json:"result_data"`

	// StagedArtifacts is the JSON-serialised slice of
	// pre-publish StagedArtifactReference (renamed from
	// PublishedArtifacts in P0-COMPL-5-WIRE-NAMING, July 2026).
	// The canonical Sender-side conversion (StagedArtifactReference
	// -> PublishedArtifact with Drive FileID/link/checksum post-publish)
	// lives on the PublishAndCompleteUseCase surface at
	// internal/application/jobs/completion/publish_and_complete_use_case.go
	// (the EXPAND-phase canonical; handler-wiring to the use case is
	// the BACKFILL phase, forward-pointer P0-COMPL-5-HANDLER-WIRE, deadline 2026-08-15, owner: completion).
	// For now this field continues to carry json.RawMessage on the
	// worker-side pipeline for byte-stability with the legacy wire.
	StagedArtifacts json.RawMessage `json:"staged_artifacts"`

	// OutboxEvents is the JSON-serialised slice of OutboxEvent
	// descriptors. Optional; AssetFinalizerTx also emits its own.
	OutboxEvents json.RawMessage `json:"outbox_events,omitempty"`
}
