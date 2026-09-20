package cliprender

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ── Async submission/completion boundary (Wave B) ──────────────────────
//
// LIVE mode (2026-09-13 onward): clip.render is TWO phases of one job type.
// The submit phase prepares, seals and hands the render to RenderingGen, then
// releases its Master slot; the settle phase is a continuation job that waits
// for the remote render, then probes, publishes and commits. The runtime mode
// is decided in exactly one place — Worker.Handle dispatches on the payload
// phase — and the composition root enables it by attaching BOTH durable
// continuation ports (WithContinuationStore + WithContinuationEnqueuer).
//
// Historically (pre-Wave-B) clip.render was ONE blocking handler: prepare →
// seal → submit to RenderingGen → WAIT for the render → download → probe →
// publish. The Master worker slot was held for the whole remote render, which
// is where the clip.render throughput collapsed: with N Master slots, N slow
// renders pinned every slot even though the local process was doing nothing
// but waiting on a remote GPU. Wave B split that single call at the point the
// plan is SEALED, which is what the phases below describe:
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
// The phase travels in the job payload under PayloadKeyRenderPhase, so the
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

// PayloadKeyRenderPhase is the job-payload key carrying the continuation
// phase. Absent → RenderPhaseSubmit (back-compatible).
//
// It is EXPORTED because the composition root needs it to build the worker-pool
// scope (kernel/job.PayloadMatch / PayloadNotMatch) that routes settle
// continuations to their dedicated pool. The key is a wire fact owned HERE, so
// the routing decision derives from this constant rather than re-declaring the
// literal: a second declaration would silently stop matching — voiding the
// dedicated-pool guardrail with no error and no log — the moment the key is
// renamed in this file.
const PayloadKeyRenderPhase = "render_phase"

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
	raw, ok := payload[PayloadKeyRenderPhase]
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
			ErrInvalidJobPayload, PayloadKeyRenderPhase, s, RenderPhaseSubmit, RenderPhaseSettle)
	}
	return phase, nil
}

// RemoteRenderState is the explicit state a submission records when the remote
// render is handed to RenderingGen.
//
// There is exactly ONE state, and that is a fact about the boundary rather than
// an unfinished state machine: the submit phase is the only writer (SUBMITTED —
// the remote job was accepted and the local worker slot is released), and the
// settle continuation resumes from the immutable, content-addressed
// ResumeDocument instead of from a mutable state row.
//
// The former PREPARING / REMOTE_RENDERING / ARTIFACT_READY / PUBLISHING /
// COMPLETED / FAILED vocabulary was never written by any code path and never
// read by any caller. It was removed rather than kept as documentation of
// behavior that does not exist: no durable sink for intermediate states exists
// by design (the resume document is addressed by its digest, and the broker
// writes the job result once, at the end), so a state machine nothing can
// advance is a lie about the runtime an operator would have to unlearn. When a
// real need for resumable intermediate state appears, it comes with the store
// that can hold it — not with constants.
//
// The type and its validation stay because they ARE load-bearing: the value
// travels on the continuation payload and the job result, and Validate rejects
// anything that is not a canonical state, so a drifted or hand-edited payload
// fails closed instead of being acted on.
type RemoteRenderState string

const (
	// RemoteRenderSubmitted is the state the submit phase records: RenderingGen
	// accepted the remote job and the local worker slot is released.
	RemoteRenderSubmitted RemoteRenderState = "SUBMITTED"
)

// IsValid reports whether s is the canonical submission state.
//
// The narrowness is the point: it is what makes a payload claiming an
// intermediate state that nothing produces fail closed instead of validating.
func (s RemoteRenderState) IsValid() bool {
	return s == RemoteRenderSubmitted
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
	Plan ClipRenderPlanV1 `json:"plan"`
	// RemoteRenderID is normally empty and then Plan.RunID is the queue id. A
	// chunked family uses its content-addressed assembly anchor instead; keeping
	// that address in the resume document makes settle restart-safe.
	RemoteRenderID  string            `json:"remote_render_id,omitempty"`
	Request         RenderRequest     `json:"request"`
	PublishFolderID string            `json:"publish_folder_id,omitempty"`
	SourceTitle     string            `json:"source_title,omitempty"`
	SourceSizeBytes int64             `json:"source_size_bytes,omitempty"`
	Contract        *ResolvedContract `json:"contract,omitempty"`
	Transcript      *TranscriptResult `json:"transcript,omitempty"`
	Subtitles       *SubtitleArtifact `json:"subtitles,omitempty"`

	// PreparationTimings is what the SUBMIT half measured while staging the
	// clip's assets (materialize_source/watermark/background and the other
	// phase walls). The settle continuation deliberately does NOT re-run
	// preparation, so without carrying this the settle report would answer
	// `asset_materialize_ms = NOT_INSTRUMENTED` for work that WAS measured and
	// the job wall could not be reconciled against its own phases. An empty
	// slice stays NOT_INSTRUMENTED — never a fabricated zero.
	PreparationTimings PreparationTimings `json:"preparation_timings,omitempty"`
	// SubtitleCompileMS is the ASS compile wall measured before Submit. It is a
	// pointer so "subtitles were disabled / not measured" (absent) is
	// distinguishable from "compiled in 0 ms" (present and zero).
	SubtitleCompileMS *int64 `json:"subtitle_compile_ms,omitempty"`
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
	renderID := d.Plan.RunID
	if strings.TrimSpace(d.RemoteRenderID) != "" {
		renderID = d.RemoteRenderID
	}
	if err := d.Attributes(renderID, d.Plan.PlanSHA256); err != nil {
		return err
	}
	return nil
}

// Attributes verifies the document describes the submission it is resuming:
// the same remote render (run id) over the same sealed plan (digest). A
// document that drifted would publish an artifact nobody can attribute to the
// submitted plan, so this is fail-closed.
func (d ResumeDocument) Attributes(renderJobID, planSHA256 string) error {
	documentRenderID := d.Plan.RunID
	if strings.TrimSpace(d.RemoteRenderID) != "" {
		documentRenderID = d.RemoteRenderID
	}
	if documentRenderID != renderJobID {
		return fmt.Errorf("%w: resume document render_id=%q does not match the submitted render %q",
			ErrInvalidJobPayload, documentRenderID, renderJobID)
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

// settleCorrelationScope is the phase marker that scopes the settle child's
// correlation id away from its parent's. Mirrors the voiceover fan-out's
// "<parent>:item:<idx>" shape (see voiceover/service/jobs/fanout.go).
const settleCorrelationScope = "settle"

// SettleCorrelationID derives the settle child's correlation id from the
// parent's.
//
// The continuation is the SAME canonical job type as the job it resumes
// (clip.render), and the broker dedupes on (type, correlation_id):
// queue.Service.Enqueue pre-checks with repo.FindByTypeAndCorrelation and the
// persistence layer enforces the conditional UNIQUE index
// idx_jobs_type_correlation (migration 036). At submit time the parent is
// still RUNNING, so a child that inherits the parent's correlation id is
// resolved to the PARENT — EnqueueContinuation returns the parent's job id,
// the settle job is never created, and the clip is never collected: the GPU
// render finishes and nothing ever downloads, probes or publishes it.
//
// Scoping the correlation id to the DETERMINISTIC remote render id (the
// sealed plan's RunID) plus the attempt makes the (type, correlation_id) key
// distinct from the parent while keeping parent and child greppable together,
// and makes a redelivered submit address the identical child.
func SettleCorrelationID(parentCorrelationID, renderJobID string, attempt int) string {
	base := strings.TrimSpace(parentCorrelationID)
	if base == "" {
		base = string(TypeClipRender)
	}
	return fmt.Sprintf("%s:%s:%s:%d", base, settleCorrelationScope, renderJobID, attempt)
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
