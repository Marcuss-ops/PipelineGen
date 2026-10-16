// Package model defines the domain types shared by the queue, its HTTP server
// and every storage backend (in-memory and PostgreSQL).
package model

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// State is the lifecycle state of a job.
//
// It is an ALIAS of the public wire contract type (queue/client), not a second
// definition: the queue service, the worker and every producer share exactly
// one State type, so the vocabulary cannot drift across the wire. The internal
// names below stay for readability at the storage layer.
//
// StateCancelled marks a job a producer cancelled before it reached a
// terminal state. Cancelled is terminal: the job is never claimable again and
// lease expiry never requeues it, so a cancelled job can never trigger a new
// Chronon render across lease/claim cycles. An in-flight render that was
// already claimed when the cancel landed finishes its GPU invocation (it
// cannot be preempted), but its complete/fail reports are rejected and no
// further invocation ever starts.
//
// StateRendered marks a job whose render is finished and durably stored in the
// artifact store, but whose external publication (e.g. Google Drive) failed. A
// worker re-claiming it must skip rendering and only retry the publication
// step.
type State = client.State

const (
	StatePending    = client.StatePending
	StateRunning    = client.StateRunning
	StateCompleted  = client.StateCompleted
	StateFailed     = client.StateFailed
	StateRendered   = client.StateRendered
	StateCancelled  = client.StateCancelled
	StateFinalizing = client.StateFinalizing
)

// States returns the canonical job-state vocabulary. It is the list the
// render_jobs_state_check SQL constraint must match.
func States() []State { return client.AllStates() }

// AttemptStatus is the lifecycle status of one render attempt
// (render_attempts.status). Its vocabulary mirrors the job states plus
// lease_expired, which has no job-state counterpart: an attempt that ended
// because its lease elapsed is recorded as lease_expired, while the job row
// returns to pending (or fails permanently).
type AttemptStatus string

const (
	AttemptRunning      AttemptStatus = "running"
	AttemptCompleted    AttemptStatus = "completed"
	AttemptFailed       AttemptStatus = "failed"
	AttemptLeaseExpired AttemptStatus = "lease_expired"
	AttemptRendered     AttemptStatus = "rendered"
	AttemptCancelled    AttemptStatus = "cancelled"
)

// AttemptStatuses returns the canonical attempt-status vocabulary. It is the
// list the render_attempts_status_check SQL constraint must match.
func AttemptStatuses() []AttemptStatus {
	return []AttemptStatus{
		AttemptRunning, AttemptCompleted, AttemptFailed,
		AttemptLeaseExpired, AttemptRendered, AttemptCancelled,
	}
}

const (
	PublicationObjectStoreOnly     = client.PublicationObjectStoreOnly
	PublicationObjectStoreAndDrive = client.PublicationObjectStoreAndDrive

	JobTypeRenderSegment  = client.JobTypeRenderSegment
	JobTypeOverlayPrepare = client.JobTypeOverlayPrepare
	JobTypeOverlayRender  = client.JobTypeOverlayRender
	JobTypeOverlayImport  = client.JobTypeOverlayImport
)

// JobSchemaV1 identifies the renderinggen.job.v1 envelope. It is an alias of
// the public wire contract so the value exists once, in queue/client.
const (
	JobSchemaV1        = client.JobSchemaV1
	JobSchemaVersionV1 = client.JobSchemaVersionV1
)

// FrameRange is the half-open [Start, End) chunk contract. It is an ALIAS of
// the public wire type: the queue service, the worker and every producer share
// one declaration, so a chunk range can never mean two different things on the
// two sides of the wire (the same rule that already applies to State and
// Artifact).
type FrameRange = client.FrameRange

// ValidateChunk validates chunk metadata for a job. It is the single authority
// for frame_range/chunk_index invariants (U6): both memory and postgres
// repositories must call this instead of duplicating inline checks.
func ValidateChunk(job Job) error {
	if err := client.ValidateJobMetadata(job); err != nil {
		return err
	}
	if err := client.ValidateJobID(job.ID); err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	if job.ParentJobID != "" {
		if err := client.ValidateJobID(job.ParentJobID); err != nil {
			return fmt.Errorf("invalid parent job id: %w", err)
		}
	}
	if job.FrameRange != nil && (job.FrameRange.Start < 0 || job.FrameRange.End <= job.FrameRange.Start) {
		return fmt.Errorf("invalid frame_range for job %s", job.ID)
	}
	if job.ChunkIndex < 0 {
		return fmt.Errorf("invalid chunk_index for job %s", job.ID)
	}
	return nil
}

// AssetRef points at an asset in the central artifact store by content hash
// and the logical path it must be materialized at in the job workspace. It is
// an ALIAS of the public wire type, not a second declaration.
type AssetRef = client.AssetRef

// Job is a unit of work in the queue: one render SEGMENT. The renderable
// content carried in RenderPlan is the semantic OverlaySpec
// (renderinggen.overlay-plan.v1, accepted by the worker's
// overlay.CompileSemantic) for prepared jobs. The worker writes plan.json and
// Chronon3d composes every layer of the segment in a single pass.
//
// It is an ALIAS of the public wire contract type (queue/client), exactly like
// State, Artifact, Progress, Stats, Worker, AssetRef and FrameRange: there is
// ONE job declaration for the submit body, the claim response and the GET
// projection, so a field added to the wire can never be dropped (or invented)
// by a second struct that happens to agree today. The historical internal
// re-declaration — kept in step only by hand-maintained field copies in the
// server and the worker adapter — is gone.
type Job = client.Job

// Progress is the per-job render progress reported by the worker that owns
// the job's lease. FramesDone is the last frame position the renderer
// reported (absolute, already offset for chunked execution). TotalFrames is
// the segment length when known (0 = unknown). LastFrameAt is the wall-clock
// time of the last frame report and doubles as a render liveness signal. It is
// an ALIAS of the public wire type — one declaration for the report payload
// and the GET projection.
type Progress = client.Progress

// Stats is a snapshot of the queue, used for autoscaling and monitoring.
// Ok reports whether the snapshot came from a successful store query: a
// failed snapshot is all-zeros with Ok=false, so consumers can distinguish
// "queue is empty" from "store unavailable". It is an ALIAS of the public wire
// type (the client used to drop Ok, which made an outage look like an idle
// queue).
type Stats = client.Stats
