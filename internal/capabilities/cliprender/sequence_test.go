package cliprender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

// TestSequenceRequestValidate pins the fail-fast rules: one source per
// position, no transition on the first clip, a certified transition, and the
// encoder bounds. Everything here is decided before a single frame is touched.
func TestSequenceRequestValidate(t *testing.T) {
	valid := func() *SequenceRequest {
		return &SequenceRequest{Clips: []SequenceClip{
			{Path: "/tmp/a.mp4", DurationMS: 4_000},
			{AssetID: "asset-b", TransitionIn: &clipsequence.Transition{Preset: clipsequence.PresetLightLeakHard, DurationMS: 200}},
		}, Output: SequenceOutput{Codec: "libx264", CRF: 20}}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*SequenceRequest)
		want   string
	}{
		{"no clips", func(r *SequenceRequest) { r.Clips = nil }, "clips is required"},
		{"two sources", func(r *SequenceRequest) { r.Clips[0].AssetID = "also-an-asset" }, "exactly one of path"},
		{"no source", func(r *SequenceRequest) { r.Clips[0].Path = "" }, "exactly one of path"},
		{"transition on the first clip", func(r *SequenceRequest) {
			r.Clips[0].TransitionIn = &clipsequence.Transition{Preset: clipsequence.PresetCrossfadeFast}
		}, "clips[0] cannot declare transition_in"},
		{"unknown preset", func(r *SequenceRequest) {
			r.Clips[1].TransitionIn = &clipsequence.Transition{Preset: "warp"}
		}, "not certified"},
		{"too long for the fast lane", func(r *SequenceRequest) {
			r.Clips[1].TransitionIn = &clipsequence.Transition{Preset: clipsequence.PresetCrossfadeFast, DurationMS: 900}
		}, "fast transitions only"},
		{"leak override on an xfade preset", func(r *SequenceRequest) {
			r.Clips[1].TransitionIn = &clipsequence.Transition{Preset: clipsequence.PresetCrossfadeFast, Leak: &clipsequence.LeakOverride{Color: "#FFFFFF"}}
		}, "has no light-leak program"},
		{"crf out of range", func(r *SequenceRequest) { r.Output.CRF = 99 }, "output.crf"},
	}
	for _, tc := range cases {
		request := valid()
		tc.mutate(request)
		err := request.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

// TestBatchSequenceProjectsPositions pins the bridge between the batch items and
// the sequence payload: a cold item travels as a job to wait on, a cache hit as
// an asset id, and the transition lands on the clip that CARRIES it.
func TestBatchSequenceProjectsPositions(t *testing.T) {
	batch := &BatchSequence{
		Transitions: []BatchSequenceTransition{{
			Position: 2, Preset: clipsequence.PresetLightLeakSoft, DurationMS: 280,
			Leak: &clipsequence.LeakOverride{Direction: "left"},
		}},
		Output: SequenceOutput{Dir: "/tmp/out"},
	}
	positions := []int{0, 1, 2}
	assetIDs := []string{"asset-a", "", ""}
	cacheHit := []bool{true, false, false}
	recovery := &SequenceBatchRecovery{BatchID: strings.Repeat("a", 64)}

	request, err := batch.request(assetIDs, cacheHit, positions, recovery, 3)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if request.RenderPhase != RenderPhaseSequence {
		t.Fatalf("render_phase = %q, want %q", request.RenderPhase, RenderPhaseSequence)
	}
	if len(request.Clips) != 3 {
		t.Fatalf("clips = %d, want 3", len(request.Clips))
	}
	if request.Clips[0].AssetID != "asset-a" || request.Clips[0].JobID != "" {
		t.Fatalf("cache hit projected as %+v, want the asset id", request.Clips[0])
	}
	if request.Clips[1].BatchPosition == nil || *request.Clips[1].BatchPosition != 1 || request.Clips[1].TransitionIn != nil {
		t.Fatalf("position 1 projected as %+v, want batch position 1 and a hard cut", request.Clips[1])
	}
	if request.Clips[2].BatchPosition == nil || *request.Clips[2].BatchPosition != 2 || request.Clips[2].TransitionIn == nil {
		t.Fatalf("position 2 projected as %+v, want batch position 2 and the transition", request.Clips[2])
	}
	if request.Recovery != recovery {
		t.Fatalf("recovery manifest was not attached to the sequence request")
	}
	if request.Clips[2].TransitionIn.Leak == nil || request.Clips[2].TransitionIn.Leak.Direction != "left" {
		t.Fatalf("transition leak = %+v, want the per-join override", request.Clips[2].TransitionIn.Leak)
	}

	bad := []*BatchSequence{
		{Transitions: []BatchSequenceTransition{{Position: 0, Preset: clipsequence.PresetFlashWhite}}},
		{Transitions: []BatchSequenceTransition{{Position: 3, Preset: clipsequence.PresetFlashWhite}}},
		{Transitions: []BatchSequenceTransition{
			{Position: 1, Preset: clipsequence.PresetFlashWhite},
			{Position: 1, Preset: clipsequence.PresetWhipLeft},
		}},
		{Transitions: []BatchSequenceTransition{{Position: 1, Preset: "warp"}}},
	}
	for i, candidate := range bad {
		if _, err := candidate.request(assetIDs, cacheHit, positions, recovery, 3); err == nil {
			t.Fatalf("bad projection %d was accepted", i)
		}
	}
}

// TestSequenceHandlerEnqueuesTheSequencePhase pins the standalone transport:
// POST /render/sequence enqueues ONE clip.render job whose payload is the
// sequence phase, and the HTTP answer is the canonical 202 {job_id, QUEUED}.
func TestSequenceHandlerEnqueuesTheSequencePhase(t *testing.T) {
	svc := &stubJobsSvc{returnJob: &job.Job{ID: "job-seq"}}
	router := newTestRouter(svc)

	// HTTP bodies name artifacts by identity (job_id/asset_id): a local `path`
	// and an `output.dir` are job-level inputs and are rejected at this edge
	// (ticket CR-19-04).
	body := `{"clips":[
		{"job_id":"job-a"},
		{"asset_id":"asset-b","transition_in":{"preset":"light_leak_hard","duration_ms":220,
		 "leak":{"color":"#FF8844","peak_alpha":0.9,"band_width":0.12,"direction":"left"}}}
	],"output":{"codec":"libx264","preset":"veryfast","crf":19}}`

	req := httptest.NewRequest(http.MethodPost, "/render/sequence", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.JobID != "job-seq" || response.Status != StatusQueued {
		t.Fatalf("response = %+v, want job-seq QUEUED", response)
	}
	if svc.enqueued == nil || svc.enqueued.Type != TypeClipRender {
		t.Fatalf("enqueued = %+v, want a clip.render job", svc.enqueued)
	}
	payload, ok := svc.enqueued.Payload.(*SequenceRequest)
	if !ok {
		t.Fatalf("payload type = %T, want *SequenceRequest", svc.enqueued.Payload)
	}
	if payload.RenderPhase != RenderPhaseSequence {
		t.Fatalf("payload phase = %q, want sequence", payload.RenderPhase)
	}
	if payload.Clips[1].TransitionIn == nil || payload.Clips[1].TransitionIn.Leak == nil {
		t.Fatalf("payload lost the leak override: %+v", payload.Clips[1])
	}
}

// ── worker assembly ───────────────────────────────────────────────────

type stubSequenceJobs struct {
	jobs map[string]*job.Job
}

func (s *stubSequenceJobs) Get(_ context.Context, id string) (*job.Job, error) {
	return s.jobs[id], nil
}

func (s *stubSequenceJobs) IsTerminal(status job.Status) bool {
	return status == job.StatusSucceeded || status == job.StatusFailed || status == job.StatusCancelled
}

type stubSequenceAssets struct {
	paths map[string]string
	calls int
}

func (s *stubSequenceAssets) MaterializeAsset(_ context.Context, assetID string) (*MaterializedAsset, error) {
	s.calls++
	path, ok := s.paths[assetID]
	if !ok {
		return nil, osErrAssetMissing{assetID}
	}
	return &MaterializedAsset{AssetID: assetID, LocalPath: path, DurationMS: 4_000}, nil
}

type osErrAssetMissing struct{ assetID string }

func (e osErrAssetMissing) Error() string { return "asset " + e.assetID + " is not materializable" }

func requireFfmpegBinaries(t *testing.T) {
	t.Helper()
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not installed", binary)
		}
	}
}

func synthSequenceClip(t *testing.T, dir, name, color string, seconds float64) string {
	t.Helper()
	path := filepath.Join(dir, name+".mp4")
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=" + color + ":s=320x180:r=24:d=" + strconv.FormatFloat(seconds, 'f', 3, 64),
		"-f", "lavfi", "-i", "sine=frequency=440:duration=" + strconv.FormatFloat(seconds, 'f', 3, 64),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path,
	}
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("synthesize %s: %v: %s", name, err, output)
	}
	return path
}

// TestSequenceWorkerAssemblesPublishedClips is the end-to-end proof: two
// finished clip.render jobs, a light leak between them, and one merged artifact
// with the transition overlap actually removed from the timeline.
func TestSequenceWorkerAssemblesPublishedClips(t *testing.T) {
	requireFfmpegBinaries(t)
	dir := t.TempDir()
	first := synthSequenceClip(t, dir, "first", "black", 4)
	second := synthSequenceClip(t, dir, "second", "white", 4)

	worker := &Worker{log: zap.NewNop(), workspaceDir: dir}
	worker.WithSequence(SequenceAssembly{
		Jobs: &stubSequenceJobs{jobs: map[string]*job.Job{
			"job-a": {ID: "job-a", Status: job.StatusSucceeded, Result: json.RawMessage(`{"asset":{"asset_id":"asset-a"},"render":{"duration_sec":4.0}}`)},
			"job-b": {ID: "job-b", Status: job.StatusSucceeded, Result: json.RawMessage(`{"asset":{"asset_id":"asset-b"},"render":{"duration_sec":4.0}}`)},
		}},
		Assets:  &stubSequenceAssets{paths: map[string]string{"asset-a": first, "asset-b": second}},
		Encoder: media.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 20},
		Root:    filepath.Join(dir, "sequences"),
	})

	payload, err := json.Marshal(&SequenceRequest{
		RenderPhase: RenderPhaseSequence,
		Clips: []SequenceClip{
			{JobID: "job-a"},
			{JobID: "job-b", TransitionIn: &clipsequence.Transition{Preset: clipsequence.PresetLightLeakHard, DurationMS: 250}},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	result, err := worker.Handle(context.Background(), &job.Job{ID: "job-seq", Payload: payload}, nil)
	if err != nil {
		t.Fatalf("sequence: %v", err)
	}
	paths, _ := result["paths"].([]string)
	if len(paths) != 1 {
		t.Fatalf("paths = %v, want one merged artifact", result["paths"])
	}
	if duration, _ := result["duration_ms"].(int64); duration != 7_750 {
		t.Fatalf("duration_ms = %v, want 7750 (8s minus the 250ms join)", result["duration_ms"])
	}
	if result["local_only"] != true || result["delivery_status"] != "not_required" {
		t.Fatalf("assembly delivery contract = local_only:%v delivery_status:%v", result["local_only"], result["delivery_status"])
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", paths[0]).Output()
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(probe)), 64)
	if err != nil {
		t.Fatalf("parse duration: %v", err)
	}
	if seconds < 7.6 || seconds > 7.9 {
		t.Fatalf("merged duration = %.3fs, want ~7.75s", seconds)
	}
}

// TestSequenceWorkerDefersWhileASiblingRenders pins the wait: a clip that is
// still rendering defers the attempt (no retry budget spent, no failure) and
// never touches the assembler.
func TestSequenceWorkerDoesNotMaterializeBeforeAllSiblingsSettle(t *testing.T) {
	assets := &stubSequenceAssets{paths: map[string]string{"asset-a": "/tmp/a.mp4"}}
	worker := &Worker{log: zap.NewNop()}
	worker.WithSequence(SequenceAssembly{
		Jobs: &stubSequenceJobs{jobs: map[string]*job.Job{
			"job-a": {ID: "job-a", Status: job.StatusSucceeded, Result: json.RawMessage(`{"asset":{"asset_id":"asset-a"}}`)},
			"job-b": {ID: "job-b", Status: job.StatusRunning},
		}},
		Assets:  assets,
		Encoder: media.EncoderPolicy{Codec: "libx264"},
	})
	payload, _ := json.Marshal(&SequenceRequest{RenderPhase: RenderPhaseSequence, Clips: []SequenceClip{{JobID: "job-a"}, {JobID: "job-b"}}})
	if _, err := worker.Handle(context.Background(), &job.Job{ID: "job-seq", Payload: payload}, nil); err == nil {
		t.Fatal("a running sibling must defer")
	}
	if assets.calls != 0 {
		t.Fatalf("materialize calls = %d, want 0 before every sibling is terminal", assets.calls)
	}
}

func TestSequenceWorkerFailsAfterMaxWait(t *testing.T) {
	worker := &Worker{log: zap.NewNop()}
	worker.WithSequence(SequenceAssembly{
		Jobs: &stubSequenceJobs{jobs: map[string]*job.Job{
			"job-b": {ID: "job-b", Status: job.StatusRunning},
		}},
		Assets:  &stubSequenceAssets{},
		Encoder: media.EncoderPolicy{Codec: "libx264"},
	})
	payload, _ := json.Marshal(&SequenceRequest{
		RenderPhase: RenderPhaseSequence, MaxWaitMS: 1,
		Clips: []SequenceClip{{JobID: "job-b"}},
	})
	_, err := worker.Handle(context.Background(), &job.Job{ID: "job-seq", CreatedAt: time.Now().Add(-time.Minute), Payload: payload}, nil)
	if !errors.Is(err, ErrSequenceTimeout) || !strings.Contains(err.Error(), "job-b") {
		t.Fatalf("error = %v, want ErrSequenceTimeout naming job-b", err)
	}
}

func TestSequenceWorkerDefersWhileASiblingRenders(t *testing.T) {
	worker := &Worker{log: zap.NewNop()}
	worker.WithSequence(SequenceAssembly{
		Jobs: &stubSequenceJobs{jobs: map[string]*job.Job{
			"job-a": {ID: "job-a", Status: job.StatusSucceeded, Result: json.RawMessage(`{"asset":{"asset_id":"asset-a"}}`)},
			"job-b": {ID: "job-b", Status: job.StatusRunning},
		}},
		Assets:  &stubSequenceAssets{paths: map[string]string{"asset-a": "/tmp/a.mp4"}},
		Encoder: media.EncoderPolicy{Codec: "libx264"},
	})
	payload, _ := json.Marshal(&SequenceRequest{RenderPhase: RenderPhaseSequence, Clips: []SequenceClip{{JobID: "job-a"}, {JobID: "job-b"}}})
	_, err := worker.Handle(context.Background(), &job.Job{ID: "job-seq", Payload: payload}, nil)
	deferral, ok := job.AsDeferral(err)
	if !ok {
		t.Fatalf("error = %v, want a deferral while job-b renders", err)
	}
	if deferral.Delay <= 0 {
		t.Fatalf("deferral delay = %v, want a positive wait", deferral.Delay)
	}

	// A failed sibling is NOT a wait: it is a terminal error naming the job.
	worker.sequence.in.Jobs = &stubSequenceJobs{jobs: map[string]*job.Job{
		"job-a": {ID: "job-a", Status: job.StatusSucceeded, Result: json.RawMessage(`{"asset":{"asset_id":"asset-a"}}`)},
		"job-b": {ID: "job-b", Status: job.StatusFailed, Error: "render exploded"},
	}}
	if _, err := worker.Handle(context.Background(), &job.Job{ID: "job-seq", Payload: payload}, nil); err == nil || !strings.Contains(err.Error(), "render exploded") {
		t.Fatalf("error = %v, want the failed sibling reported", err)
	}
}

// TestSequenceHandlerIsMounted keeps the route discoverable from the router that
// serves /render and /render/batch.
func TestSequenceHandlerIsMounted(t *testing.T) {
	router := newTestRouter(&stubJobsSvc{})
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	if !routes["POST /render/sequence"] {
		t.Fatalf("routes = %v, want POST /render/sequence", routes)
	}
}

// recordingJobsSvc remembers every enqueue in order, so a batch test can prove
// the ONE request produced N renders AND the assembly.
type recordingJobsSvc struct {
	requests []*job.EnqueueRequest
}

func (s *recordingJobsSvc) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.requests = append(s.requests, req)
	return &job.Job{ID: "job-" + strconv.Itoa(len(s.requests))}, nil
}
func (s *recordingJobsSvc) Get(_ context.Context, _ string) (*job.Job, error) { return nil, nil }
func (s *recordingJobsSvc) Cancel(_ context.Context, _ string) error          { return nil }
func (s *recordingJobsSvc) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *recordingJobsSvc) IsTerminal(status job.Status) bool     { return status.IsTerminal() }
func (s *recordingJobsSvc) RegisterHandler(_ string, _ any) error { return nil }
func (s *recordingJobsSvc) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (s *recordingJobsSvc) Retry(_ context.Context, _ string) (*job.Job, error) { return nil, nil }

// TestSequenceWorkerRecoversMissingBatchItems certifies the CR-04 recovery
// half: the sequence job is durable before the items, resolves each batch
// position by its deterministic correlation, and enqueues only the missing
// render. Duplicate positions share the canonical job, and a second attempt is
// a no-op.
func TestSequenceWorkerRecoversMissingBatchItems(t *testing.T) {
	batchID := strings.Repeat("b", 64)
	position0, position1 := 0, 1
	req := &SequenceRequest{
		Recovery: &SequenceBatchRecovery{BatchID: batchID, Items: []BatchRecoveryItem{
			{Position: 0, Request: RenderRequest{SourceAssetID: "asset-a"}},
			{Position: 1, Request: RenderRequest{SourceAssetID: "asset-b"}},
		}},
		Clips: []SequenceClip{
			{BatchPosition: &position0},
			{BatchPosition: &position1},
			{BatchPosition: &position0}, // duplicate item shares position 0
		},
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Fatalf("recovery request rejected: %v", err)
	}

	var enqueued []*job.EnqueueRequest
	byCorrelation := make(map[string]job.Job)
	assembly := SequenceAssembly{Batch: BatchRecoveryPorts{
		List: func(_ context.Context, filter job.Filter) ([]job.Job, error) {
			if filter.CorrelationPrefix != batchID+":" {
				t.Fatalf("list prefix = %q, want %q", filter.CorrelationPrefix, batchID+":")
			}
			jobs := make([]job.Job, 0, len(byCorrelation))
			for _, j := range byCorrelation {
				jobs = append(jobs, j)
			}
			return jobs, nil
		},
		Enqueue: func(_ context.Context, in *job.EnqueueRequest) (*job.Job, error) {
			enqueued = append(enqueued, in)
			j := job.Job{ID: "job-" + strconv.Itoa(len(enqueued)), CorrelationID: in.CorrelationID, Status: job.StatusQueued}
			byCorrelation[in.CorrelationID] = j
			return &j, nil
		},
	}}
	worker := &Worker{log: zap.NewNop()}
	resolved, err := worker.resolveBatchRecovery(context.Background(), *req, assembly)
	if err != nil {
		t.Fatalf("first recovery: %v", err)
	}
	if len(enqueued) != 2 {
		t.Fatalf("enqueued = %d, want one job per unique cold position", len(enqueued))
	}
	// The recovery contract is read-only on the input: the caller owns the
	// returned slice and the request keeps its original pending positions.
	if req.Clips[0].JobID != "" || req.Clips[0].BatchPosition == nil || *req.Clips[0].BatchPosition != 0 {
		t.Fatalf("input clip 0 mutated: %+v", req.Clips[0])
	}
	if resolved[0].JobID == "" || resolved[0].BatchPosition != nil {
		t.Fatalf("clip 0 = %+v, want a resolved job id", resolved[0])
	}
	if resolved[2].JobID != resolved[0].JobID {
		t.Fatalf("duplicate clip job = %q, want the canonical %q", resolved[2].JobID, resolved[0].JobID)
	}
	if resolved[1].JobID == "" || resolved[1].JobID == resolved[0].JobID {
		t.Fatalf("clip 1 = %+v, want its own job id", resolved[1])
	}

	// The caller commits the resolved slice before retrying; the retry then
	// sees the durable rows and must not enqueue the renders again.
	req.Clips = resolved
	if _, err := worker.resolveBatchRecovery(context.Background(), *req, assembly); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
	if len(enqueued) != 2 {
		t.Fatalf("retry enqueued %d jobs, want the original 2", len(enqueued))
	}
}

// TestRenderBatchEnqueuesTheAssemblyInOneRequest is the point of the batch
// hook: one POST produces the clips AND the job that joins them, so no caller
// runs the assembler by hand.
// lastSequenceCorrelation returns the correlation of the most recent sequence
// enqueue. The handler now enqueues the sequence before the items, so the last
// request is an item and cannot be used to compare assembly identities.
func lastSequenceCorrelation(t *testing.T, requests []*job.EnqueueRequest) string {
	t.Helper()
	for i := len(requests) - 1; i >= 0; i-- {
		if _, ok := requests[i].Payload.(*SequenceRequest); ok {
			return requests[i].CorrelationID
		}
	}
	t.Fatal("no sequence request was enqueued")
	return ""
}

func TestRenderBatchEnqueuesTheAssemblyInOneRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &recordingJobsSvc{}
	handler := NewHandler(svc, zap.NewNop())
	router := gin.New()
	handler.RegisterRoutes(&router.RouterGroup)

	body := `{
		"items":[{"source_asset_id":"asset-1"},{"source_asset_id":"asset-2"}],
		"sequence":{
			"transitions":[{"position":1,"preset":"light_leak_hard","duration_ms":260,
				"leak":{"color":"#FFAA55","peak_alpha":0.7,"band_width":0.2}}],
			"output":{"codec":"libx264"}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/render/batch", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(svc.requests) != 3 {
		t.Fatalf("enqueues = %d, want 3 (1 assembly + 2 renders)", len(svc.requests))
	}
	assembly := svc.requests[0]
	payload, ok := assembly.Payload.(*SequenceRequest)
	if !ok {
		t.Fatalf("assembly payload type = %T, want *SequenceRequest", assembly.Payload)
	}
	if len(payload.Clips) != 2 || payload.Clips[0].BatchPosition == nil || *payload.Clips[0].BatchPosition != 0 || payload.Clips[1].BatchPosition == nil || *payload.Clips[1].BatchPosition != 1 {
		t.Fatalf("assembly clips = %+v, want the two batch positions in order", payload.Clips)
	}
	if payload.Recovery == nil || len(payload.Recovery.Items) != 2 || payload.Recovery.Items[0].Position != 0 || payload.Recovery.Items[1].Position != 1 {
		t.Fatalf("assembly recovery = %+v, want the two cold items", payload.Recovery)
	}
	if payload.Recovery.Items[0].Request.SourceAssetID != "asset-1" || payload.Recovery.Items[1].Request.SourceAssetID != "asset-2" {
		t.Fatalf("assembly recovery lost an item request: %+v", payload.Recovery.Items)
	}
	if payload.Clips[1].TransitionIn == nil || payload.Clips[1].TransitionIn.Leak == nil {
		t.Fatalf("assembly lost the transition: %+v", payload.Clips[1])
	}
	if payload.Output.Dir != "" || payload.Output.Codec != "libx264" {
		t.Fatalf("assembly output = %+v, want the batch settings and a server-owned output dir", payload.Output)
	}
	if item, ok := svc.requests[1].Payload.(*RenderRequest); !ok || item.SourceAssetID != "asset-1" {
		t.Fatalf("first item payload = %T %+v, want asset-1", svc.requests[1].Payload, svc.requests[1].Payload)
	}
	var response batchRenderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SequenceJobID != "job-1" {
		t.Fatalf("sequence_job_id = %q, want job-1 (the manifest enqueued before the items)", response.SequenceJobID)
	}
	if response.Jobs[0].JobID != "job-2" || response.Jobs[1].JobID != "job-3" {
		t.Fatalf("item jobs = %+v, want the two renders after the manifest", response.Jobs)
	}

	// A transition that points at the first clip (or at nothing) fails the WHOLE
	// batch before any job is enqueued.
	bad := &recordingJobsSvc{}
	badRouter := gin.New()
	NewHandler(bad, zap.NewNop()).RegisterRoutes(&badRouter.RouterGroup)
	badBody := `{"items":[{"source_asset_id":"a"},{"source_asset_id":"b"}],"sequence":{"transitions":[{"position":0,"preset":"flash_white"}]}}`
	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPost, "/render/batch", bytes.NewReader([]byte(badBody)))
	badReq.Header.Set("Content-Type", "application/json")
	badRouter.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad sequence status = %d, want 400: %s", badRec.Code, badRec.Body.String())
	}
	if len(bad.requests) != 0 {
		t.Fatalf("bad sequence enqueued %d jobs; a rejected batch must create nothing", len(bad.requests))
	}
}
