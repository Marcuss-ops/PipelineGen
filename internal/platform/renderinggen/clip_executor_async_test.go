package renderinggen

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// asyncProbeQueue records how many times the boundary touched the remote job
// status, which is how the async split is asserted: Submit must make ZERO
// status calls (it never waits), Settle must observe the terminal state.
type asyncProbeQueue struct {
	submitted scriptgen.RenderQueueJob
	result    queueclient.Job
	getCalls  int
}

func (f *asyncProbeQueue) Submit(_ context.Context, job scriptgen.RenderQueueJob) error {
	f.submitted = job
	return nil
}

func (f *asyncProbeQueue) Get(context.Context, string) (scriptgen.RenderQueueJob, error) {
	f.getCalls++
	return scriptgen.RenderQueueJob{State: string(f.result.State), Artifact: toScriptArtifact(f.result.Artifact)}, nil
}

func (f *asyncProbeQueue) Retry(context.Context, string) error { return nil }

// TestClipRenderExecutorSubmitDoesNotWait certifies the Wave B boundary: the
// submit half returns after the remote job is ACCEPTED, without ever observing
// the remote render, so the caller can release its worker slot.
func TestClipRenderExecutorSubmitDoesNotWait(t *testing.T) {
	artifactBytes := []byte("submitted-artifact")
	artifactHash := fmt.Sprintf("%x", sha256.Sum256(artifactBytes))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(artifactBytes)
	}))
	defer server.Close()
	q := &asyncProbeQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: artifactHash, ArtifactURL: server.URL + "/out.mp4", SizeBytes: int64(len(artifactBytes)),
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationUS: 1_000_000,
		Backend: "chronon_vulkan", CopyEligible: true,
	}}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetPollInterval(time.Millisecond)
	plan := validClipPlan(t)

	if err := executor.Submit(context.Background(), plan); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// The whole point: submission never waits on the remote render.
	if q.getCalls != 0 {
		t.Fatalf("Submit observed the remote job status %d time(s); the boundary must not wait", q.getCalls)
	}
	if q.submitted.ID != plan.RunID {
		t.Fatalf("submitted job id = %q, want the sealed plan run id %q", q.submitted.ID, plan.RunID)
	}
	if q.submitted.JobType != "render_segment" {
		t.Fatalf("submitted job type = %q, want render_segment", q.submitted.JobType)
	}
	// Submit keeps the overlay-plan.v1 mapping contract.
	var overlay struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(q.submitted.OverlaySpec, &overlay); err != nil {
		t.Fatalf("submitted plan is not valid JSON: %v", err)
	}
	if overlay.SchemaVersion != SemanticSchema {
		t.Fatalf("schema_version = %q, want %q", overlay.SchemaVersion, SemanticSchema)
	}

	// The continuation resumes from the SAME sealed plan — no process-local
	// state — and certifies the artifact.
	outcome, err := executor.Settle(context.Background(), plan)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if q.getCalls == 0 {
		t.Fatal("Settle must observe the remote job state")
	}
	if outcome.SHA256 != artifactHash || outcome.SizeBytes != int64(len(artifactBytes)) {
		t.Fatalf("certified artifact = %s/%d, want %s/%d", outcome.SHA256, outcome.SizeBytes, artifactHash, len(artifactBytes))
	}
	if outcome.OutputPath != plan.OutputPath {
		t.Fatalf("outcome path = %q, want the sealed plan output %q", outcome.OutputPath, plan.OutputPath)
	}
	if outcome.Backend != cliprender.BackendChrononVulkan {
		t.Fatalf("backend = %q, want %q", outcome.Backend, cliprender.BackendChrononVulkan)
	}
}

// TestClipRenderExecutorSettleFailsClosedWithoutArtifact certifies the
// continuation's fail-closed half: a remote job that reaches a terminal state
// without a certified artifact must not project an outcome.
func TestClipRenderExecutorSettleFailsClosedWithoutArtifact(t *testing.T) {
	q := &asyncProbeQueue{result: queueclient.Job{State: queueclient.StateCompleted}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetPollInterval(time.Millisecond)

	if _, err := executor.Settle(context.Background(), validClipPlan(t)); err == nil {
		t.Fatal("Settle must fail closed when the remote job completed without a certified artifact")
	}
}

// TestClipRenderExecutorRenderIsSubmitThenSettle pins the blocking form as a
// pure composition of the two halves, so the historical caller keeps exactly
// one submit and one wait.
func TestClipRenderExecutorRenderIsSubmitThenSettle(t *testing.T) {
	artifactBytes := []byte("blocking-form-artifact")
	artifactHash := fmt.Sprintf("%x", sha256.Sum256(artifactBytes))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(artifactBytes)
	}))
	defer server.Close()
	q := &asyncProbeQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: artifactHash, ArtifactURL: server.URL + "/out.mp4", SizeBytes: int64(len(artifactBytes)),
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationUS: 1_000_000,
		Backend: "chronon_vulkan", CopyEligible: true,
	}}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetPollInterval(time.Millisecond)
	plan := validClipPlan(t)

	outcome, err := executor.Render(context.Background(), plan)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if q.submitted.ID != plan.RunID {
		t.Fatalf("submit did not happen in the blocking form: %+v", q.submitted)
	}
	if q.getCalls == 0 {
		t.Fatal("the blocking form must settle (observe the remote state)")
	}
	if outcome.SHA256 != artifactHash {
		t.Fatalf("outcome digest = %q, want %q", outcome.SHA256, artifactHash)
	}
}
