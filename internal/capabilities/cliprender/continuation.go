package cliprender

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ── Async submission/completion boundary (Wave B) ──────────────────────
//
// Today clip.render is ONE blocking handler: prepare → seal → submit to
// RenderingGen → WAIT for the render → download → probe → publish. The Master
// worker slot is held for the whole remote render, which is exactly where the
// clip.render throughput collapses: with N Master slots, N slow renders pin
// every slot even though the local process is doing nothing but waiting on a
// remote GPU.
//
// Wave B splits that single call at the point the plan is SEALED:
//
//	clip.render (phase=submit)
//	    prepare + compile        (unchanged, local work)
//	    seal ClipRenderPlanV1    (unchanged, deterministic)
//	    Submit(plan)             (prefetch + enqueue the remote render, NO wait)
//	    persist the submission   (job result + continuation payload)
//	    RETURN                   ← the Master slot is RELEASED here
//
//	clip.render (phase=settle)   ← the continuation
//	    load the resume document (content-addressed, by digest)
//	    Settle(plan)             (wait for terminal, download, certify)
//	    probe + publish          (unchanged)
//	    RETURN
//
// The remote render job id is plan.RunID, which is deterministic, so the
// settle phase needs nothing that the sealed plan does not already carry: a
// restart between the two phases re-derives the same job id and the queue
// treats the resubmission as idempotent (409 → ErrJobExists, already handled
// by the executor).
//
// The phase travels in the job payload under payloadKeyRenderPhase, so the
// continuation is the SAME canonical job type (no new ownership registration:
// architecture/ownership/jobs.yaml still declares exactly one clip.render job
// with one RegisterHandler binding). A payload without the key is the submit
// phase, which keeps every existing caller working unchanged.
//
// ── Why the resume inputs travel as a content-addressed REFERENCE ──────
//
// The settle phase must NOT re-run preparation (that would re-resolve and
// re-upload the clip's assets a second time), so it needs the sealed plan plus
// the preparation results (resolved contract, transcript, subtitle artifact,
// resolved folder). Inlining those would put a large, duplicated, unverifiable
// blob in the job payload — which the Master persists in result_json and the
// broker copies on every retry.
//
// Instead the submit phase writes ONE deterministic document into the
// canonical content-addressed store and the payload carries only its address
// (digest + size). That gives three properties the inline form cannot:
//
//   - the payload stays small and cheap to retry;
//   - the settle phase VERIFIES the resume document by digest, so a truncated
//     or drifted document is detected rather than silently acted on;
//   - a redelivered continuation addresses the same bytes (idempotent).

// payloadKeyRenderPhase is the job-payload key carrying the continuation
// phase. Absent → RenderPhaseSubmit (back-compatible).
const payloadKeyRenderPhase = "render_phase"

// RenderPhase is the continuation phase of a clip.render job.
type RenderPhase string

const (
	// RenderPhaseSubmit is the first boundary: prepare, seal, submit, persist,
	// release the Master slot. It never waits for the remote render.
	RenderPhaseSubmit RenderPhase = "submit"
	// RenderPhaseSettle is the continuation: load the resume document, wait for
	// the remote render, materialize the certified artifact, probe and publish.
	RenderPhaseSettle RenderPhase = "settle"
)

// IsValid reports whether p is one of the canonical phases.
func (p RenderPhase) IsValid() bool {
	return p == RenderPhaseSubmit || p == RenderPhaseSettle
}

// ParseRenderPhase reads the phase from a job payload. A missing key is the
// submit phase (the historical, blocking form's entry point); an unknown value
// is a typed error rather than a silent fallback, so a payload typo can never
// silently re-run submission (which would re-prepare and re-upload assets).
func ParseRenderPhase(payload map[string]any) (RenderPhase, error) {
	if payload == nil {
		return RenderPhaseSubmit, nil
	}
	raw, ok := payload[payloadKeyRenderPhase]
	if !ok {
		return RenderPhaseSubmit, nil
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", raw))
	if s == "" {
		return RenderPhaseSubmit, nil
	}
	phase := RenderPhase(s)
	if !phase.IsValid() {
		return "", fmt.Errorf("%w: unknown %s=%q (want %q or %q)",
			ErrInvalidJobPayload, payloadKeyRenderPhase, s, RenderPhaseSubmit, RenderPhaseSettle)
	}
	return phase, nil
}

// RemoteRenderState is the explicit state of a clip whose render is owned by
// RenderingGen. It replaces the implicit "the handler is blocked, therefore a
// render is in flight" state, so an operator (or a restart) can always tell
// where a submission is:
//
//	PREPARING        local preparation/sealing is running (no remote job yet)
//	SUBMITTED        the remote job was accepted; the local slot is released
//	REMOTE_RENDERING the settle phase is waiting on RenderingGen
//	ARTIFACT_READY   the certified artifact is on local disk
//	PUBLISHING       the artifact is being certified/published
//	COMPLETED        terminal success
//	FAILED           terminal failure (retryable via the broker)
type RemoteRenderState string

const (
	RemoteRenderPreparing     RemoteRenderState = "PREPARING"
	RemoteRenderSubmitted     RemoteRenderState = "SUBMITTED"
	RemoteRenderRendering     RemoteRenderState = "REMOTE_RENDERING"
	RemoteRenderArtifactReady RemoteRenderState = "ARTIFACT_READY"
	RemoteRenderPublishing    RemoteRenderState = "PUBLISHING"
	RemoteRenderCompleted     RemoteRenderState = "COMPLETED"
	RemoteRenderFailed        RemoteRenderState = "FAILED"
)

// IsValid reports whether s is one of the canonical states.
func (s RemoteRenderState) IsValid() bool {
	switch s {
	case RemoteRenderPreparing, RemoteRenderSubmitted, RemoteRenderRendering,
		RemoteRenderArtifactReady, RemoteRenderPublishing, RemoteRenderCompleted,
		RemoteRenderFailed:
		return true
	}
	return false
}

// IsTerminal reports whether no further local action follows.
func (s RemoteRenderState) IsTerminal() bool {
	return s == RemoteRenderCompleted || s == RemoteRenderFailed
}

// ParentStateWaitingChildren is the application-level parent_state the submit
// phase writes to its job result. It is the SAME wire value the scripts and
// voiceover fan-out handlers use, so the existing
// jobs.Service.ListAwaitingAggregation / FinalizeAggregateParent machinery can
// find and re-finalise a clip.render parent without a new mechanism and
// without a migration.
const ParentStateWaitingChildren = "waiting_children"

// Submission is the durable record the submit phase persists and the settle
// phase resumes from:
//
//   - RenderJobID is the deterministic RenderingGen job id (= plan.RunID), so
//     the continuation does not depend on any process-local state.
//   - PlanSHA256 is the sealed plan digest: the settle phase verifies that the
//     resume document it loads describes the plan that was actually submitted.
//   - CorrelationID ties the remote render to the originating request.
//   - State is the explicit remote-render state (never inferred).
//   - Attempt guards against an unbounded submit→settle loop.
type Submission struct {
	RenderJobID   string            `json:"render_job_id"`
	PlanSHA256    string            `json:"plan_sha256"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	State         RemoteRenderState `json:"remote_state"`
	Attempt       int               `json:"attempt"`
}

// Validate fails closed on an incomplete or drifted submission record.
func (s Submission) Validate() error {
	if strings.TrimSpace(s.RenderJobID) == "" {
		return fmt.Errorf("%w: submission.render_job_id is required", ErrInvalidJobPayload)
	}
	if !isSHA256Hex(s.PlanSHA256) {
		return fmt.Errorf("%w: submission.plan_sha256=%q is not a SHA-256 hex digest", ErrInvalidJobPayload, s.PlanSHA256)
	}
	if !s.State.IsValid() {
		return fmt.Errorf("%w: submission.remote_state=%q is not a canonical state", ErrInvalidJobPayload, s.State)
	}
	if s.Attempt < 1 {
		return fmt.Errorf("%w: submission.attempt=%d must be >= 1", ErrInvalidJobPayload, s.Attempt)
	}
	return nil
}

// ContinuationRef is the content address of a ResumeDocument in the canonical
// CAS. SizeBytes is retained so the settle phase can reject an implausible
// object before allocating for it, and so an operator can see how large the
// resume document was without opening the store.
type ContinuationRef struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Validate fails closed on a malformed address.
func (r ContinuationRef) Validate() error {
	if !isSHA256Hex(r.SHA256) {
		return fmt.Errorf("%w: continuation.resume.sha256=%q is not a SHA-256 hex digest", ErrInvalidJobPayload, r.SHA256)
	}
	if r.SizeBytes <= 0 {
		return fmt.Errorf("%w: continuation.resume.size_bytes=%d must be > 0", ErrInvalidJobPayload, r.SizeBytes)
	}
	return nil
}

// Continuation is the small payload the submit phase hands to the settle
// phase: the submission record plus the ADDRESS of the resume document. The
// document itself lives in the CAS (see ContinuationStore).
type Continuation struct {
	Submission Submission      `json:"submission"`
	Resume     ContinuationRef `json:"resume"`
}

// Validate fails closed before a continuation is enqueued or resumed.
func (c Continuation) Validate() error {
	if err := c.Submission.Validate(); err != nil {
		return err
	}
	return c.Resume.Validate()
}

// Encode marshals the continuation for the job payload.
func (c Continuation) Encode() (json.RawMessage, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("clip.render: encode continuation: %w", err)
	}
	return raw, nil
}

// DecodeContinuation reads a continuation out of a settle-phase payload.
func DecodeContinuation(raw json.RawMessage) (Continuation, error) {
	var c Continuation
	if len(raw) == 0 {
		return c, fmt.Errorf("%w: settle phase requires a continuation payload", ErrInvalidJobPayload)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%w: decode continuation: %v", ErrInvalidJobPayload, err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// ResumeDocument is the content-addressed document the settle phase loads. It
// carries exactly the preparation results the post-submit half needs, so the
// continuation is a pure resume: no preparation, no asset re-resolution, no
// second upload.
//
// Every field is a value the submit phase already had in memory; the document
// is deterministic for a given sealed plan, which is what makes its digest a
// meaningful identity.
type ResumeDocument struct {
	Plan            ClipRenderPlanV1  `json:"plan"`
	Request         RenderRequest     `json:"request"`
	PublishFolderID string            `json:"publish_folder_id,omitempty"`
	Contract        *ResolvedContract `json:"contract,omitempty"`
	Transcript      *TranscriptResult `json:"transcript,omitempty"`
	Subtitles       *SubtitleArtifact `json:"subtitles,omitempty"`
}

// Validate fails closed on a document that cannot drive the post-submit half.
func (d ResumeDocument) Validate() error {
	if err := d.Plan.Validate(); err != nil {
		return fmt.Errorf("%w: resume document plan: %v", ErrInvalidJobPayload, err)
	}
	if strings.TrimSpace(d.Request.SourceAssetID) == "" {
		return fmt.Errorf("%w: resume document request.source_asset_id is required", ErrInvalidJobPayload)
	}
	if d.Subtitles != nil && strings.TrimSpace(d.Subtitles.LocalPath) == "" {
		return fmt.Errorf("%w: resume document subtitles.local_path is required when subtitles are present", ErrInvalidJobPayload)
	}
	if err := d.Attributes(d.Plan.RunID, d.Plan.PlanSHA256); err != nil {
		return err
	}
	return nil
}

// Attributes verifies the document describes the submission it is resuming:
// the same remote render (run id) over the same sealed plan (digest). A
// document that drifted would publish an artifact nobody can attribute to the
// submitted plan, so this is fail-closed.
func (d ResumeDocument) Attributes(renderJobID, planSHA256 string) error {
	if d.Plan.RunID != renderJobID {
		return fmt.Errorf("%w: resume document run_id=%q does not match the submitted render %q",
			ErrInvalidJobPayload, d.Plan.RunID, renderJobID)
	}
	if d.Plan.PlanSHA256 != planSHA256 {
		return fmt.Errorf("%w: resume document plan_sha256=%q does not match the submitted plan %q",
			ErrInvalidJobPayload, d.Plan.PlanSHA256, planSHA256)
	}
	return nil
}

// ContinuationStore persists and loads the resume document in the canonical
// content-addressed store. It is a capability-owned port so the worker never
// imports the platform store.
//
// Fail-closed contract: the submit phase MUST NOT report success unless the
// document is durable. A submission whose resume document was lost is a render
// nobody can collect, so the submit phase returns the store error and the
// broker retries the whole job (submission is idempotent — a retried submit
// re-derives the same remote job id and the same document digest, and the
// queue answers ErrJobExists).
type ContinuationStore interface {
	// PutResumeDocument stores the document and returns its content address.
	PutResumeDocument(ctx context.Context, doc ResumeDocument) (ContinuationRef, error)
	// GetResumeDocument loads the document at ref, verifying its digest.
	GetResumeDocument(ctx context.Context, ref ContinuationRef) (ResumeDocument, error)
}

// ActiveKeyFor is the canonical idempotency key for a submission's settle job.
func ActiveKeyFor(renderJobID string, attempt int) string {
	return fmt.Sprintf("clip.render.settle:%s:%d", renderJobID, attempt)
}

// ContinuationRequest is the typed enqueue instruction for the settle phase.
//
// ParentJobID/ParentRunID are the ParentLink the broker persists on the child
// payload (kernel/job.ParentLink), which is what lets the aggregator recover
// the parent/child relationship without process-local memory. ActiveKey makes
// the enqueue idempotent per (parent, attempt): a retried submit cannot fan
// out two settle jobs for the same render.
type ContinuationRequest struct {
	ParentJobID  string
	ParentRunID  string
	ActiveKey    string
	Continuation Continuation
}

// ContinuationEnqueuer enqueues the settle-phase continuation for a submitted
// clip.render. It is a capability-owned port so the worker never imports the
// jobs service (the composition root satisfies it with job.Service.Enqueue +
// ParentLink injection).
type ContinuationEnqueuer interface {
	EnqueueContinuation(ctx context.Context, req ContinuationRequest) (childJobID string, err error)
}
