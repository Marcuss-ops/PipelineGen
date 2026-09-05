package renderinggen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

type fakeClipQueue struct {
	submitted queueclient.Job
	result    queueclient.Job

	// Optional failure injection: when submitErr is set, Submit returns it
	// (e.g. scriptgen.ErrJobExists for a replayed job); when retryErr is set,
	// Retry returns it instead of clearing the failed state.
	submitErr error
	retryErr  error
	retries   int
}

func (f *fakeClipQueue) Submit(_ context.Context, job scriptgen.RenderQueueJob) error {
	f.submitted = queueclient.Job{ID: job.ID, JobType: job.JobType, RenderPlan: job.OverlaySpec}
	return f.submitErr
}

func (f *fakeClipQueue) Get(_ context.Context, _ string) (scriptgen.RenderQueueJob, error) {
	return scriptgen.RenderQueueJob{State: string(f.result.State), Artifact: toScriptArtifact(f.result.Artifact)}, nil
}

func (f *fakeClipQueue) Retry(_ context.Context, _ string) error {
	f.retries++
	if f.retryErr != nil {
		return f.retryErr
	}
	f.result.State = queueclient.StatePending
	return nil
}

func validClipPlan(t *testing.T) cliprender.ClipRenderPlanV1 {
	t.Helper()
	sourcePath := t.TempDir() + "/source.mp4"
	sourceBytes := []byte("test source")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceBytes))
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "clip-1",
		DurationMS: 1000,
		Source:     cliprender.PlanSource{AssetID: "source-asset-001", Path: sourcePath, SHA256: sourceHash},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "VELOX_ASSEMBLY_READY_V1", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: t.TempDir() + "/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// TestClipRenderExecutorSubmitsOverlayPlanV1 verifies the critical contract:
// the submitted JSON carries schema_version="renderinggen.overlay-plan.v1",
// NOT a raw ClipRenderPlanV1 (which would be silently pass-through'd by the
// RenderingGen worker and corrupt the render pipeline).
func TestClipRenderExecutorSubmitsOverlayPlanV1(t *testing.T) {
	artifactBytes := []byte("0123456789")
	artifactHash := fmt.Sprintf("%x", sha256.Sum256(artifactBytes))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(artifactBytes)
	}))
	defer server.Close()
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: artifactHash, ArtifactURL: server.URL + "/out.mp4", SizeBytes: int64(len(artifactBytes)),
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationUS: 1_000_000,
		Backend: "chronon_vulkan", CopyEligible: true,
	}}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	plan := validClipPlan(t)
	outcome, err := executor.Render(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if q.submitted.JobType != "render_segment" || q.submitted.ID != plan.RunID {
		t.Fatalf("submitted job metadata wrong: job_type=%q id=%q", q.submitted.JobType, q.submitted.ID)
	}

	// Decode as the overlay-plan wire type (NOT ClipRenderPlanV1).
	var overlay struct {
		SchemaVersion string `json:"schema_version"`
		PlanID        string `json:"plan_id"`
		VideoID       string `json:"video_id"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		FPSNum        int    `json:"fps_num"`
		FPSDen        int    `json:"fps_den"`
		Source        struct {
			AssetID string `json:"asset_id"`
			SHA256  string `json:"sha256"`
		} `json:"source"`
	}
	if err := json.Unmarshal(q.submitted.RenderPlan, &overlay); err != nil {
		t.Fatalf("submitted plan is not valid JSON: %v", err)
	}
	if overlay.SchemaVersion != SemanticSchema {
		t.Errorf("schema_version = %q, want %q (raw ClipRenderPlanV1 would pass-through to Chronon broken)",
			overlay.SchemaVersion, SemanticSchema)
	}
	if overlay.PlanID != plan.RunID {
		t.Errorf("plan_id = %q, want %q", overlay.PlanID, plan.RunID)
	}
	if overlay.VideoID != plan.Source.AssetID {
		t.Errorf("video_id = %q, want %q", overlay.VideoID, plan.Source.AssetID)
	}
	if overlay.FPSNum != plan.Output.FPSNum || overlay.FPSDen != plan.Output.FPSDen {
		t.Errorf("fps = %d/%d, want %d/%d", overlay.FPSNum, overlay.FPSDen, plan.Output.FPSNum, plan.Output.FPSDen)
	}
	if overlay.Source.SHA256 != plan.Source.SHA256 {
		t.Errorf("source.sha256 = %q, want %q", overlay.Source.SHA256, plan.Source.SHA256)
	}

	// Verify local VPS path is NOT in the submitted JSON.
	if strings.Contains(string(q.submitted.RenderPlan), "/vps/local/source.mp4") {
		t.Error("submitted plan must NOT contain local VPS path — must use hash-addressed object-store key")
	}

	if outcome.Backend != cliprender.BackendChrononVulkan || outcome.SizeBytes != 10 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// TestClipRenderExecutorAssetRefsAreHashAddressed verifies that asset logical
// paths use the content-addressed object-store format, not local VPS paths.
func TestClipRenderExecutorAssetRefsAreHashAddressed(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "clip-asset-test",
		Source:     cliprender.PlanSource{AssetID: "src", Path: "/home/pierone/clips/source.mp4", SHA256: sha},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "C", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: "/tmp/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("no asset refs returned")
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref.LogicalPath, "/") {
			t.Errorf("asset ref LogicalPath %q is a local path — must be hash-addressed (sha256/...)", ref.LogicalPath)
		}
		if !strings.HasPrefix(ref.LogicalPath, "assets/semantic/") {
			t.Errorf("asset ref LogicalPath %q is not a Chronon-mounted semantic path", ref.LogicalPath)
		}
		if !strings.Contains(ref.LogicalPath, "src/source.mp4") {
			t.Errorf("asset ref LogicalPath %q does not contain the source identity", ref.LogicalPath)
		}
	}
}

func TestClipRenderExecutorRejectsMissingCertifiedArtifact(t *testing.T) {
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted}}
	executor, _ := NewClipRenderExecutor(q)
	if _, err := executor.Render(context.Background(), validClipPlan(t)); err == nil {
		t.Fatal("expected missing artifact to fail closed")
	}
}

func TestClipRenderExecutorRejectsNonChrononArtifact(t *testing.T) {
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: "artifact-sha", ArtifactURL: "https://store/out.mp4", SizeBytes: 10,
		Backend: "ffmpeg_fallback",
	}}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Render(context.Background(), validClipPlan(t)); err == nil {
		t.Fatal("expected non-Chronon artifact to fail closed")
	}
}

// storeRecorder is an in-memory object-store double that counts the HTTP
// verbs it sees so tests can prove the prefetch contract: HEAD probes only,
// PUT only for absent objects, and never a full-body GET.
type storeRecorder struct {
	mu        sync.Mutex
	present   map[string][]byte
	putBodies map[string][]byte
	heads     int
	puts      int
	gets      int
	uploaded  int64 // bytes received on PUT bodies
}

func (r *storeRecorder) handler(w http.ResponseWriter, req *http.Request) {
	key := strings.TrimPrefix(req.URL.Path, "/objects/")
	r.mu.Lock()
	defer r.mu.Unlock()
	switch req.Method {
	case http.MethodHead:
		r.heads++
		if _, ok := r.present[key]; ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case http.MethodPut:
		r.puts++
		body, _ := io.ReadAll(req.Body)
		r.putBodies[key] = append([]byte(nil), body...)
		r.uploaded += int64(len(body))
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		r.gets++
		if data, ok := r.present[key]; ok {
			_, _ = w.Write(data)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// TestPrefetchClipAssetsSkipsExistingObject pins the HEAD-before-PUT
// contract: when the content-addressed object is already staged, the source
// file is never re-read or re-uploaded — zero PUT bytes cross the wire.
func TestPrefetchClipAssetsSkipsExistingObject(t *testing.T) {
	plan := validClipPlan(t)
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(plan.Source.Path)
	if err != nil {
		t.Fatal(err)
	}
	rec := &storeRecorder{
		present:   map[string][]byte{plan.Source.SHA256: sourceBytes},
		putBodies: map[string][]byte{},
	}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", srv.URL)

	if err := prefetchClipAssets(context.Background(), plan, refs); err != nil {
		t.Fatalf("prefetch existing asset: %v", err)
	}
	if rec.puts != 0 || rec.uploaded != 0 {
		t.Fatalf("asset already present must not be uploaded: puts=%d uploaded_bytes=%d", rec.puts, rec.uploaded)
	}
	if rec.gets != 0 {
		t.Fatalf("prefetch must probe with HEAD, not GET (gets=%d)", rec.gets)
	}
	if rec.heads == 0 {
		t.Fatal("prefetch must probe the object store before uploading")
	}
}

// TestPrefetchClipAssetsStreamsMissingObject pins the streaming-upload path:
// an absent object is PUT once with exactly the source bytes as the request
// body (constant memory — never a full-file os.ReadFile buffer) and the
// declared Content-Length.
func TestPrefetchClipAssetsStreamsMissingObject(t *testing.T) {
	plan := validClipPlan(t)
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(plan.Source.Path)
	if err != nil {
		t.Fatal(err)
	}
	rec := &storeRecorder{present: map[string][]byte{}, putBodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", srv.URL)

	if err := prefetchClipAssets(context.Background(), plan, refs); err != nil {
		t.Fatalf("prefetch missing asset: %v", err)
	}
	if rec.puts != 1 || rec.uploaded != int64(len(sourceBytes)) {
		t.Fatalf("absent asset must be uploaded exactly once with its full bytes: puts=%d uploaded_bytes=%d want=%d", rec.puts, rec.uploaded, len(sourceBytes))
	}
	if got := rec.putBodies[plan.Source.SHA256]; !bytes.Equal(got, sourceBytes) {
		t.Fatalf("uploaded body does not match the source file: got %d bytes", len(got))
	}
	if rec.gets != 0 {
		t.Fatalf("prefetch must probe with HEAD, not GET (gets=%d)", rec.gets)
	}
}

// TestClipRenderExecutorPropagatesRetryError pins the retry-error contract:
// when a replayed job sits in FAILED and the queue rejects the Retry, the
// executor must surface the real retry failure instead of swallowing it and
// degrading into the generic "completed without certified artifact" timeout.
func TestClipRenderExecutorPropagatesRetryError(t *testing.T) {
	rec := &storeRecorder{present: map[string][]byte{}, putBodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", srv.URL)

	q := &fakeClipQueue{
		submitErr: scriptgen.ErrJobExists,
		result:    queueclient.Job{State: queueclient.StateFailed},
		retryErr:  errors.New("queue retry unavailable"),
	}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Render(context.Background(), validClipPlan(t))
	if err == nil {
		t.Fatal("expected the queue retry failure to propagate")
	}
	if !strings.Contains(err.Error(), "retry failed for clip-1") || !strings.Contains(err.Error(), "queue retry unavailable") {
		t.Fatalf("retry error lost its cause: %v", err)
	}
	if q.retries != 1 {
		t.Fatalf("Retry calls = %d, want 1", q.retries)
	}
}
