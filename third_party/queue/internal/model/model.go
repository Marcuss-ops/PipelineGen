// Package model defines the domain types shared by the queue, its HTTP server
// and every storage backend (in-memory and PostgreSQL).
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

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
	JobTypeRenderPrepare  = client.JobTypeRenderPrepare
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

// PreparedRenderPackageRef is the shared immutable family preparation
// reference. It aliases the one public queue wire declaration.
type PreparedRenderPackageRef = client.PreparedRenderPackageRef

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
	if job.PreparedRenderPackage != nil {
		if err := job.PreparedRenderPackage.Validate(); err != nil {
			return fmt.Errorf("job %s prepared render package: %w", job.ID, err)
		}
	}
	return nil
}

// ValidateJobBatch applies the batch-level ingress rules shared by every
// backend: a non-empty batch, a required unique id per job, a chunk whose
// frame range is well-formed, and at most ONE job per non-empty idempotency
// key. The key rule lives here — not in one repository — because PostgreSQL
// enforces it with a unique index while the in-memory store has to enforce it
// explicitly; without one shared check the same batch would be accepted by one
// backend and rejected by the other. Validation is complete before any
// mutation, so a bad child never leaves a partial family behind.
func ValidateJobBatch(jobs []Job) error {
	if len(jobs) == 0 {
		return fmt.Errorf("job batch is empty")
	}
	seen := make(map[string]struct{}, len(jobs))
	keyOwner := make(map[string]string, len(jobs))
	var familyPackage *client.PreparedRenderPackageRef
	for _, job := range jobs {
		if job.ID == "" {
			return fmt.Errorf("job id is required")
		}
		if _, ok := seen[job.ID]; ok {
			return fmt.Errorf("duplicate job id %s", job.ID)
		}
		seen[job.ID] = struct{}{}
		if job.IdempotencyKey != "" {
			if other, ok := keyOwner[job.IdempotencyKey]; ok {
				return fmt.Errorf("duplicate idempotency key %q for jobs %s and %s", job.IdempotencyKey, other, job.ID)
			}
			keyOwner[job.IdempotencyKey] = job.ID
		}
		if err := ValidateChunk(job); err != nil {
			return err
		}
		if job.PreparedRenderPackage != nil {
			if err := job.PreparedRenderPackage.Validate(); err != nil {
				return fmt.Errorf("job %s prepared render package: %w", job.ID, err)
			}
		}
		if job.ParentJobID == "" && job.PreparedRenderPackage != nil {
			return fmt.Errorf("assembly anchor %s must not reference a child render package", job.ID)
		}
		if job.ParentJobID != "" && job.PreparedRenderPackage != nil {
			if familyPackage == nil {
				ref := *job.PreparedRenderPackage
				familyPackage = &ref
			} else if *familyPackage != *job.PreparedRenderPackage {
				return fmt.Errorf("job batch members reference different prepared render packages")
			}
		}
	}
	if familyPackage != nil {
		// A prepared package certifies one ordered set of child ranges. Treat
		// chunk_index and frame_range as the durable family manifest: every
		// child must belong to the same anchor, indices must be dense from zero,
		// and their half-open frame ranges must cover one contiguous timeline.
		var anchorID string
		children := make([]*Job, 0, len(jobs)-1)
		for i := range jobs {
			job := &jobs[i]
			if job.ParentJobID == "" {
				if anchorID != "" {
					return fmt.Errorf("prepared render family has multiple assembly anchors")
				}
				anchorID = job.ID
				continue
			}
			children = append(children, job)
		}
		if anchorID == "" || len(children) == 0 {
			return fmt.Errorf("prepared render family requires one assembly anchor and at least one chunk")
		}
		ordered := make([]*Job, len(children))
		for _, child := range children {
			if child.ParentJobID != anchorID {
				return fmt.Errorf("prepared render chunk %s belongs to anchor %s, want %s", child.ID, child.ParentJobID, anchorID)
			}
			if child.ChunkIndex < 0 || child.ChunkIndex >= len(children) || ordered[child.ChunkIndex] != nil {
				return fmt.Errorf("prepared render family has invalid or duplicate chunk index %d", child.ChunkIndex)
			}
			if child.FrameRange == nil {
				return fmt.Errorf("prepared render chunk %s has no frame range", child.ID)
			}
			ordered[child.ChunkIndex] = child
		}
		for i, child := range ordered {
			if child == nil {
				return fmt.Errorf("prepared render family is missing chunk index %d", i)
			}
			if i == 0 && child.FrameRange.Start != 0 {
				return fmt.Errorf("prepared render family starts at frame %d, want 0", child.FrameRange.Start)
			}
			if i > 0 && child.FrameRange.Start != ordered[i-1].FrameRange.End {
				return fmt.Errorf("prepared render chunk %d range starts at %d, previous chunk ends at %d", i, child.FrameRange.Start, ordered[i-1].FrameRange.End)
			}
			if child.FrameRange.End > familyPackage.TotalFrames {
				return fmt.Errorf("prepared render chunk %d ends at frame %d past package total %d", i, child.FrameRange.End, familyPackage.TotalFrames)
			}
			if i == len(ordered)-1 && child.FrameRange.End != familyPackage.TotalFrames {
				return fmt.Errorf("prepared render family ends at frame %d, want package total %d", child.FrameRange.End, familyPackage.TotalFrames)
			}
		}
		for _, job := range jobs {
			if job.ParentJobID != "" && job.PreparedRenderPackage == nil {
				return fmt.Errorf("chunk %s is missing the family prepared render package", job.ID)
			}
		}
	}
	return nil
}

// AssetRef points at an asset in the central artifact store by content hash
// and the logical path it must be materialized at in the job workspace. It is
// an ALIAS of the public wire type, not a second declaration.
type AssetRef = client.AssetRef

// Ownership identifies one claimed attempt of a job. Generation is the
// monotonically increasing lease_generation returned by Claim or
// ClaimFinalization. Every transition requires a positive generation that
// matches the row exactly; zero is rejected at the service boundary.
type Ownership struct {
	JobID      string
	WorkerID   string
	Generation int64
}

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

// SameSubmitRequest reports whether two envelopes describe the same immutable
// request identity. The operational queue ID and lifecycle fields (state,
// lease, timestamps, artifact and progress) are deliberately excluded, so a
// retry can resolve to the canonical ID and remains valid after work has
// started or completed. The queue uses this while resolving its unique key;
// an idempotency key alone is never proof of equivalence.
func SameSubmitRequest(a, b Job) bool {
	if a.Schema == "" {
		a.Schema = JobSchemaV1
	}
	if b.Schema == "" {
		b.Schema = JobSchemaV1
	}
	if a.Version == 0 {
		a.Version = JobSchemaVersionV1
	}
	if b.Version == 0 {
		b.Version = JobSchemaVersionV1
	}
	if a.JobType == "" {
		a.JobType = JobTypeRenderSegment
	}
	if b.JobType == "" {
		b.JobType = JobTypeRenderSegment
	}
	if a.Schema != b.Schema || a.Version != b.Version ||
		a.IdempotencyKey != b.IdempotencyKey || a.JobType != b.JobType ||
		a.PublicationPolicy != b.PublicationPolicy || a.DaemonAdmissionWaitMS != b.DaemonAdmissionWaitMS ||
		a.ParentJobID != b.ParentJobID || a.ChunkIndex != b.ChunkIndex ||
		!reflect.DeepEqual(a.FrameRange, b.FrameRange) ||
		!reflect.DeepEqual(a.PreparedRenderPackage, b.PreparedRenderPackage) ||
		!sameNotBefore(a.NotBefore, b.NotBefore) ||
		!sameSubmitAssets(a.Assets, b.Assets) || !sameJSON(a.RenderPlan, b.RenderPlan) {
		return false
	}
	return true
}

// sameSubmitAssets compares the semantic asset manifest used by producer
// request identities. Asset order and SourceURL are transport details: the
// content hash and workspace path determine render inputs, while signed URLs
// may legitimately rotate between an original submit and its retry.
func sameSubmitAssets(a, b []AssetRef) bool {
	if len(a) != len(b) {
		return false
	}
	canonical := func(assets []AssetRef) []AssetRef {
		out := make([]AssetRef, len(assets))
		for i, asset := range assets {
			out[i] = AssetRef{
				Hash:        strings.ToLower(strings.TrimSpace(asset.Hash)),
				LogicalPath: strings.TrimSpace(asset.LogicalPath),
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Hash != out[j].Hash {
				return out[i].Hash < out[j].Hash
			}
			return out[i].LogicalPath < out[j].LogicalPath
		})
		return out
	}
	return reflect.DeepEqual(canonical(a), canonical(b))
}

func sameNotBefore(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func sameJSON(a, b json.RawMessage) bool {
	var left, right any
	ld := json.NewDecoder(bytes.NewReader(a))
	ld.UseNumber()
	rd := json.NewDecoder(bytes.NewReader(b))
	rd.UseNumber()
	if ld.Decode(&left) != nil || rd.Decode(&right) != nil {
		return false
	}
	if ld.Decode(new(any)) != io.EOF || rd.Decode(new(any)) != io.EOF {
		return false
	}
	return reflect.DeepEqual(left, right)
}

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
