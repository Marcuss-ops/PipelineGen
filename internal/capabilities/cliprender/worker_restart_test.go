package cliprender

// Hermetic half of the restart-mid-render acceptance criterion
// (TICKET-CLIP-RENDER-ASYNC-COMPLETION §5 AC2): a PipelineGen restart between
// submission and completion must resume and never re-submit the running
// render, publishing exactly one artifact.
//
// A hermetic test cannot kill a live RenderingGen — that half stays live-only
// and is NOT claimed here. What it proves on existing contracts: the submit
// half addresses its remote render deterministically (a replayed submit after
// the restart addresses the SAME remote job, never a second render), and the
// settle half resumes purely from the persisted continuation + CAS document
// with a fresh worker that never submitted anything.
import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestWorker_RestartMidRenderResumesWithoutResubmitting(t *testing.T) {
	ctx := context.Background()

	// Worker A submits: preparation runs, the remote job is accepted, the
	// resume document is persisted to the CAS and the settle continuation is
	// enqueued. The Master slot is released here.
	wA, _, _ := newTestWorker(t)
	rendererA := &fakeAsyncRenderExecutor{outcome: &RenderOutcome{
		OutputPath: "/work/rendered-clip.mp4", SizeBytes: 4096, DurationSec: 3,
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Backend: BackendChrononVulkan,
	}}
	store := &fakeContinuationStore{}
	enqueuerA := &fakeContinuationEnqueuer{}
	publisherA := &fakeRenderPublisher{}
	wA.WithRenderExecutor(rendererA).WithContinuationStore(store).WithContinuationEnqueuer(enqueuerA).
		WithRenderPublisher(publisherA)

	submitResult, err := wA.Handle(ctx, &job.Job{ID: "job-restart-parent", Payload: renderJobPayload(t, baseRenderRequest())}, nil)
	if err != nil {
		t.Fatalf("submit Handle() error = %v", err)
	}
	if submitResult["phase"] != "submitted" {
		t.Fatalf("submit phase = %v, want submitted", submitResult["phase"])
	}
	// The remote identity is plan.RunID, derived from the job id — not the
	// plan content digest (local preparation outputs such as temp paths
	// legitimately differ between attempts).
	storeDocA := store.doc
	submissionRenderID := enqueuerA.req.Continuation.Submission.RenderJobID
	if submissionRenderID == "" {
		t.Fatal("submit must persist the deterministic remote-render identity")
	}
	if submissionRenderID != "job-restart-parent" {
		t.Fatalf("remote identity = %q, want the parent job id", submissionRenderID)
	}

	// --- restart: every in-memory handle is dropped. Worker B is constructed
	// from scratch; only the durable state survives: the CAS resume document
	// and the enqueued continuation (whose remote job is terminal by now). ---
	wB, _, _ := newTestWorker(t)
	rendererB := &fakeAsyncRenderExecutor{outcome: rendererA.outcome}
	enqueuerB := &fakeContinuationEnqueuer{}
	publisherB := &fakeRenderPublisher{}
	wB.WithRenderExecutor(rendererB).WithContinuationStore(store).WithContinuationEnqueuer(enqueuerB).
		WithRenderPublisher(publisherB)

	// A duplicated submit delivery after the restart (at-least-once broker)
	// re-runs local preparation — the documented cost — but must address the
	// SAME remote render, never a second one.
	replayResult, err := wB.Handle(ctx, &job.Job{ID: "job-restart-parent", Payload: renderJobPayload(t, baseRenderRequest())}, nil)
	if err != nil {
		t.Fatalf("replayed submit Handle() error = %v", err)
	}
	if replayResult["phase"] != "submitted" {
		t.Fatalf("replayed submit phase = %v, want submitted", replayResult["phase"])
	}
	if got := enqueuerB.req.Continuation.Submission.RenderJobID; got != submissionRenderID {
		t.Fatalf("replayed submit addresses remote %q, want the running render %q", got, submissionRenderID)
	}

	// The settle continuation resumes on the restarted worker: no submission,
	// one settle, one publication.
	//
	// Faithfulness note: this fake store keeps a single slot, while production
	// CAS is content-addressed — the replayed preparation above overwrote the
	// slot with a second resume document (local temp paths legitimately differ
	// between attempts; the REMOTE identity above is what stays stable).
	// Restore the original document, which is what a Get addressed by the
	// original continuation ref returns from the real CAS.
	store.doc = storeDocA
	settlePayload, err := encodeContinuationPayload(enqueuerA.req.Continuation)
	if err != nil {
		t.Fatalf("encode settle payload: %v", err)
	}
	settleResult, err := wB.Handle(ctx, &job.Job{ID: "settle-child-restart", Payload: settlePayload}, nil)
	if err != nil {
		t.Fatalf("settle Handle() error = %v", err)
	}
	if settleResult["phase"] != "rendered" {
		t.Fatalf("settle phase = %v, want rendered", settleResult["phase"])
	}
	if rendererB.submitCalls != 1 {
		t.Fatalf("restarted worker submits = %d, want 1 (the replayed preparation only)", rendererB.submitCalls)
	}
	if rendererB.settleCalls != 1 {
		t.Fatalf("restarted worker settles = %d, want 1", rendererB.settleCalls)
	}
	if publisherB.called != 1 {
		t.Fatalf("publications after restart = %d, want exactly 1", publisherB.called)
	}
}
