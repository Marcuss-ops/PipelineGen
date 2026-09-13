package wiring

// clip_render_async_test.go owns the regression pin for the live 2026-09-13
// defect where the clip.render settle continuation was never created.
//
// Root cause: the submit phase enqueued the settle child with the SAME
// canonical job type (clip.render) AND the parent's correlation id. The parent
// is still RUNNING at that instant, so the broker's (type, correlation_id)
// dedupe — the pre-check in queue.Service.Enqueue plus the conditional UNIQUE
// index idx_jobs_type_correlation (migration 036) — resolved the enqueue back
// to the PARENT. EnqueueContinuation returned the parent's own job id, no
// settle job existed, and the certified GPU artifact was never downloaded,
// probed or published (observed live: parent SUCCEEDED with
// child_job_id == its own id, zero jobs carrying parent_job_id, no media_assets
// written).
//
// Production semantics modelled here (dedupJobService.Enqueue):
//  1. a non-empty ActiveKey that matches a NON-TERMINAL job returns that job;
//  2. a non-empty (type, correlation_id) that matches ANY job returns that job.
//
// The stub is deliberately faithful to the collapse mode: the pre-fix
// correlation-id shape is exercised in
// TestClipRenderContinuationDedupeStubMirrorsProductionContract and collapses,
// while the adapter under test (which now derives a phase-scoped correlation
// id) does not.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// dedupJobService models the two dedupe surfaces the production broker
// exposes on Enqueue. Only Enqueue is implemented; every other job.Service
// method is inherited from the embedded nil interface and panics if a test
// unexpectedly reaches it, which keeps the fake honest about what it covers.
type dedupJobService struct {
	job.Service

	mu     sync.Mutex
	seq    int
	byKey  map[string]string // active_key -> job id
	byCorr map[string]string // type|correlation_id -> job id
	jobs   map[string]*job.Job
}

func newDedupJobService() *dedupJobService {
	return &dedupJobService{
		byKey:  map[string]string{},
		byCorr: map[string]string{},
		jobs:   map[string]*job.Job{},
	}
}

func (s *dedupJobService) corrKey(jobType, correlationID string) string {
	return jobType + "|" + correlationID
}

// seed inserts an already-existing job (e.g. the RUNNING parent) into both
// indexes exactly as the production enqueue would have.
func (s *dedupJobService) seed(j *job.Job) *job.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	if j.ActiveKey != "" {
		s.byKey[j.ActiveKey] = j.ID
	}
	if j.CorrelationID != "" {
		s.byCorr[s.corrKey(j.Type, j.CorrelationID)] = j.ID
	}
	return j
}

func (s *dedupJobService) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	if req == nil {
		return nil, fmt.Errorf("enqueue: nil request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. ActiveKey dedupe (only for a non-terminal match, mirroring
	//    queue.Service.Enqueue's `!existing.IsTerminal()` guard).
	if req.ActiveKey != "" {
		if id, ok := s.byKey[req.ActiveKey]; ok {
			if existing := s.jobs[id]; existing != nil && !existing.IsTerminal() {
				return existing, nil
			}
		}
	}
	// 2. (type, correlation_id) dedupe — returns the job regardless of
	//    status, exactly like FindByTypeAndCorrelation.
	if req.CorrelationID != "" {
		if id, ok := s.byCorr[s.corrKey(req.Type, req.CorrelationID)]; ok {
			return s.jobs[id], nil
		}
	}

	s.seq++
	created := &job.Job{
		ID:            fmt.Sprintf("job_child_%d", s.seq),
		Type:          req.Type,
		Status:        job.StatusQueued,
		ActiveKey:     req.ActiveKey,
		CorrelationID: req.CorrelationID,
		Payload:       rawPayload(req.Payload),
	}
	if link := job.ParentLinkFromPayload(created.Payload); link.ParentJobID != "" {
		created.ParentJobID = link.ParentJobID
	}
	s.jobs[created.ID] = created
	if created.ActiveKey != "" {
		s.byKey[created.ActiveKey] = created.ID
	}
	if created.CorrelationID != "" {
		s.byCorr[s.corrKey(created.Type, created.CorrelationID)] = created.ID
	}
	return created, nil
}

func rawPayload(payload any) json.RawMessage {
	switch v := payload.(type) {
	case nil:
		return json.RawMessage("{}")
	case json.RawMessage:
		return v
	case []byte:
		return v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

// settleContinuationFixture builds a valid continuation for the adapter.
func settleContinuationFixture(parentCorrelation string) cliprender.Continuation {
	return cliprender.Continuation{
		Submission: cliprender.Submission{
			RenderJobID:   "render-remote-1",
			PlanSHA256:    strings.Repeat("a", 64),
			CorrelationID: parentCorrelation,
			State:         cliprender.RemoteRenderSubmitted,
			Attempt:       1,
		},
		Resume: cliprender.ContinuationRef{SHA256: strings.Repeat("b", 64), SizeBytes: 4096},
	}
}

const parentJobID = "job_parent_running"

// TestClipRenderContinuationDoesNotCollapseOntoRunningParent is the regression
// pin: the settle child returned by the composition adapter must be a DISTINCT
// job, and its payload must carry the canonical parent link so the aggregator
// can recover the parent/child relationship.
func TestClipRenderContinuationDoesNotCollapseOntoRunningParent(t *testing.T) {
	const parentCorrelation = "req-live-20260913"

	svc := newDedupJobService()
	svc.seed(&job.Job{
		ID:            parentJobID,
		Type:          cliprender.TypeClipRender,
		Status:        job.StatusRunning, // the parent is mid-submit, NOT terminal
		ActiveKey:     "clip.render.api:" + parentCorrelation,
		CorrelationID: parentCorrelation,
	})

	adapter := &clipRenderContinuationEnqueuer{jobs: svc}
	continuation := settleContinuationFixture(parentCorrelation)

	childID, err := adapter.EnqueueContinuation(context.Background(), cliprender.ContinuationRequest{
		ParentJobID:  parentJobID,
		ParentRunID:  parentJobID,
		ActiveKey:    cliprender.ActiveKeyFor(continuation.Submission.RenderJobID, 1),
		Continuation: continuation,
	})
	if err != nil {
		t.Fatalf("EnqueueContinuation: %v", err)
	}
	if childID == parentJobID {
		t.Fatalf("settle child id == parent id (%q): the continuation collapsed onto the RUNNING parent "+
			"under the (type=clip.render, correlation_id) dedupe, so no settle job exists and the "+
			"rendered artifact can never be published", childID)
	}
	child := svc.jobs[childID]
	if child == nil {
		t.Fatalf("returned child %q is not a stored job", childID)
	}
	if child.Type != cliprender.TypeClipRender {
		t.Fatalf("child type = %q, want %q (the settle phase is the same canonical job type)", child.Type, cliprender.TypeClipRender)
	}
	if child.CorrelationID == parentCorrelation {
		t.Fatalf("child correlation id %q must not equal the parent's — that is the collapse key", child.CorrelationID)
	}
	if want := cliprender.SettleCorrelationID(parentCorrelation, "render-remote-1", 1); child.CorrelationID != want {
		t.Fatalf("child correlation id = %q, want the phase-scoped %q", child.CorrelationID, want)
	}
	if child.ParentJobID != parentJobID {
		t.Fatalf("child parent_job_id = %q, want %q (the aggregator recovers the parent from the payload link)", child.ParentJobID, parentJobID)
	}

	// The payload must still be the settle-phase envelope with the
	// continuation address, so the child can resume without re-preparing.
	var envelope struct {
		RenderPhase  string                   `json:"render_phase"`
		ParentJobID  string                   `json:"parent_job_id"`
		Continuation *cliprender.Continuation `json:"continuation"`
	}
	if err := json.Unmarshal(child.Payload, &envelope); err != nil {
		t.Fatalf("decode child payload: %v", err)
	}
	if envelope.RenderPhase != string(cliprender.RenderPhaseSettle) {
		t.Fatalf("child render_phase = %q, want %q", envelope.RenderPhase, cliprender.RenderPhaseSettle)
	}
	if envelope.ParentJobID != parentJobID {
		t.Fatalf("payload parent_job_id = %q, want %q", envelope.ParentJobID, parentJobID)
	}
	if envelope.Continuation == nil || envelope.Continuation.Resume.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("payload continuation = %+v, want the resume address preserved", envelope.Continuation)
	}
}

// TestClipRenderContinuationRetryIsIdempotent pins the other half of the
// enqueue contract: a redelivered submit must address the SAME settle child
// (via the deterministic ActiveKey), never fan out a second one.
func TestClipRenderContinuationRetryIsIdempotent(t *testing.T) {
	const parentCorrelation = "req-retry"

	svc := newDedupJobService()
	svc.seed(&job.Job{
		ID:            parentJobID,
		Type:          cliprender.TypeClipRender,
		Status:        job.StatusRunning,
		ActiveKey:     "clip.render.api:" + parentCorrelation,
		CorrelationID: parentCorrelation,
	})
	adapter := &clipRenderContinuationEnqueuer{jobs: svc}
	continuation := settleContinuationFixture(parentCorrelation)
	req := cliprender.ContinuationRequest{
		ParentJobID:  parentJobID,
		ParentRunID:  parentJobID,
		ActiveKey:    cliprender.ActiveKeyFor(continuation.Submission.RenderJobID, 1),
		Continuation: continuation,
	}

	first, err := adapter.EnqueueContinuation(context.Background(), req)
	if err != nil {
		t.Fatalf("first EnqueueContinuation: %v", err)
	}
	second, err := adapter.EnqueueContinuation(context.Background(), req)
	if err != nil {
		t.Fatalf("retried EnqueueContinuation: %v", err)
	}
	if first != second {
		t.Fatalf("retried submit created a second settle child: %q vs %q", first, second)
	}
	if first == parentJobID {
		t.Fatalf("settle child collapsed onto the parent: %q", first)
	}
}

// TestClipRenderContinuationDedupeStubMirrorsProductionContract documents WHY
// the fix is a correlation-id derivation and not a queue-service change: the
// stub enforces the exact production (type, correlation_id) semantics, so the
// pre-fix shape (child inheriting the parent's correlation id) visibly
// collapses while the post-fix shape (phase-scoped correlation id) does not.
func TestClipRenderContinuationDedupeStubMirrorsProductionContract(t *testing.T) {
	const parentCorrelation = "req-shape"
	svc := newDedupJobService()
	parent := svc.seed(&job.Job{
		ID:            parentJobID,
		Type:          cliprender.TypeClipRender,
		Status:        job.StatusRunning,
		ActiveKey:     "clip.render.api:" + parentCorrelation,
		CorrelationID: parentCorrelation,
	})

	// Pre-fix shape: same type + same correlation id → collapses onto parent.
	preFix, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:          cliprender.TypeClipRender,
		CorrelationID: parentCorrelation,
		ActiveKey:     cliprender.ActiveKeyFor("render-remote-1", 1),
	})
	if err != nil {
		t.Fatalf("pre-fix enqueue: %v", err)
	}
	if preFix.ID != parent.ID {
		t.Fatalf("stub no longer models the collapse: pre-fix enqueue returned %q, want the parent %q", preFix.ID, parent.ID)
	}

	// Post-fix shape: distinct phase-scoped correlation id → distinct child.
	postFix, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:          cliprender.TypeClipRender,
		CorrelationID: cliprender.SettleCorrelationID(parentCorrelation, "render-remote-1", 1),
		ActiveKey:     cliprender.ActiveKeyFor("render-remote-1", 1),
	})
	if err != nil {
		t.Fatalf("post-fix enqueue: %v", err)
	}
	if postFix.ID == parent.ID {
		t.Fatalf("post-fix enqueue still collapsed onto the parent %q", parent.ID)
	}
}
