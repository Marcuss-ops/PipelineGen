package videocreate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// settleChainChildren is a minimal ChildJobs fake whose jobs are seeded
// literally, so the tests drive awaitRenderedSegment against the real
// submit-envelope → settle-continuation wire shape.
type settleChainChildren struct{ byID map[string]*job.Job }

func (f settleChainChildren) HasHandler(string) bool { return true }

func (f settleChainChildren) EnqueueChild(context.Context, ChildJobRequest) (string, error) {
	return "", fmt.Errorf("settleChainChildren: enqueue is not used in this test")
}

func (f settleChainChildren) WaitTerminal(_ context.Context, id string) (*job.Job, error) {
	j, ok := f.byID[id]
	if !ok {
		return nil, fmt.Errorf("settleChainChildren: unknown child %s", id)
	}
	copied := *j
	return &copied, nil
}

// submitEnvelope builds the clip.render submit-phase result exactly as
// cliprender/worker.go does: phase="submitted", parent_state and the
// top-level child_job_id of the settle continuation that owns
// materialize/probe/publish.
func submitEnvelope(t *testing.T, settleChildID string) json.RawMessage {
	t.Helper()
	submission := cliprender.Submission{
		RenderJobID: "render_job_1",
		PlanSHA256:  strings.Repeat("a", 64),
		State:       cliprender.RemoteRenderSubmitted,
		Attempt:     1,
	}
	raw, err := json.Marshal(map[string]any{
		"job_id":            "job_submit_1",
		"source_asset_id":   "yt_src",
		"phase":             "submitted",
		"parent_state":      "waiting_children",
		"child_job_id":      settleChildID,
		"render_submission": submission,
	})
	if err != nil {
		t.Fatalf("marshal submit envelope: %v", err)
	}
	return raw
}

// renderedEnvelope builds the settle-phase renderedResult shape (the
// materialized segment lives HERE, never in the submit envelope).
func renderedEnvelope(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"job_id":          "job_settle_1",
		"source_asset_id": "yt_src",
		"contract_id":     kernelmedia.AssemblyMediaContractID,
		"phase":           "rendered",
		"render": map[string]any{
			"output_path":  "/tmp/seg.mp4",
			"size_bytes":   12345,
			"duration_sec": 16.0,
			"backend":      "chronon-vulkan",
		},
		"asset": map[string]any{
			"asset_id":           "asset_seg_1",
			"publication_status": "published",
		},
	})
	if err != nil {
		t.Fatalf("marshal rendered envelope: %v", err)
	}
	return raw
}

// TestRenderSettleChildIDReadsSubmitEnvelope pins the async-boundary
// projection: a submit-phase result surfaces its settle continuation id
// and a rendered-phase result surfaces nothing.
func TestRenderSettleChildIDReadsSubmitEnvelope(t *testing.T) {
	if got := renderSettleChildID(submitEnvelope(t, "job_settle_1")); got != "job_settle_1" {
		t.Fatalf("submit envelope settle child: got %q, want %q", got, "job_settle_1")
	}
	if got := renderSettleChildID(renderedEnvelope(t)); got != "" {
		t.Fatalf("rendered result must not report a settle child, got %q", got)
	}
	if got := renderSettleChildID(json.RawMessage(`{`)); got != "" {
		t.Fatalf("unparsable result must not report a settle child, got %q", got)
	}
}

// TestAwaitRenderedSegmentFollowsSettleContinuation pins the canonical
// consumption of the clip.render async boundary: the segment is
// projected from the SETTLE child's result after following the submit
// envelope's continuation — decoding the submit-phase result directly
// would fail (it has no materialization).
func TestAwaitRenderedSegmentFollowsSettleContinuation(t *testing.T) {
	children := settleChainChildren{byID: map[string]*job.Job{
		"job_submit_1": {ID: "job_submit_1", Type: job.TypeClipRender, Status: job.StatusSucceeded, Result: submitEnvelope(t, "job_settle_1")},
		"job_settle_1": {ID: "job_settle_1", Type: job.TypeClipRender, Status: job.StatusSucceeded, Result: renderedEnvelope(t)},
	}}
	run := &Run{Deps: Deps{Children: children}}
	res, err := awaitRenderedSegment(context.Background(), run, StepSpec{Stage: "07_render"}, "job_submit_1")
	if err != nil {
		t.Fatalf("awaitRenderedSegment: %v", err)
	}
	if res.AssetID != "asset_seg_1" || res.LocalPath != "/tmp/seg.mp4" {
		t.Fatalf("segment projected from the wrong result: %+v", res)
	}
	if res.DurationMS != 16000 {
		t.Fatalf("duration: got %d ms, want 16000", res.DurationMS)
	}
}

// TestAwaitRenderedSegmentSettleFailureIsTransient pins the retry
// classification: a settle (remote render execution) failure carries
// renderSettleError — the one class the render stage re-attempts — while
// a submit-phase child failure does not.
func TestAwaitRenderedSegmentSettleFailureIsTransient(t *testing.T) {
	children := settleChainChildren{byID: map[string]*job.Job{
		"job_submit_1": {ID: "job_submit_1", Type: job.TypeClipRender, Status: job.StatusSucceeded, Result: submitEnvelope(t, "job_settle_1")},
		"job_settle_1": {ID: "job_settle_1", Type: job.TypeClipRender, Status: job.StatusFailed, Error: "clip.render: render plan: render job r1 failed: EncoderFailed"},
	}}
	run := &Run{Deps: Deps{Children: children}}
	_, err := awaitRenderedSegment(context.Background(), run, StepSpec{Stage: "07_render"}, "job_submit_1")
	var settleErr renderSettleError
	if !errors.As(err, &settleErr) {
		t.Fatalf("settle failure must carry renderSettleError, got %v", err)
	}

	submitFailed := settleChainChildren{byID: map[string]*job.Job{
		"job_submit_1": {ID: "job_submit_1", Type: job.TypeClipRender, Status: job.StatusFailed, Error: "clip.render: invalid payload"},
	}}
	run = &Run{Deps: Deps{Children: submitFailed}}
	_, err = awaitRenderedSegment(context.Background(), run, StepSpec{Stage: "07_render"}, "job_submit_1")
	if errors.As(err, &settleErr) {
		t.Fatalf("a submit-phase failure must not be classified as a settle failure: %v", err)
	}
}

// TestRenderAttemptChildKeyIsPerAttempt pins the retry identity rule
// (the cliprender.ActiveKeyFor rule at the workflow seam): a new attempt
// must never collapse onto the previous child.
func TestRenderAttemptChildKeyIsPerAttempt(t *testing.T) {
	first := SceneChildKey("root", "render", 1)
	if got := renderAttemptChildKey("root", 1, 2); got == first || got != first+":attempt:2" {
		t.Fatalf("attempt-2 key: got %q, want %q", got, first+":attempt:2")
	}
	if renderAttemptChildKey("root", 1, 2) == renderAttemptChildKey("root", 1, 3) {
		t.Fatal("a new attempt must get a new child key")
	}
}
