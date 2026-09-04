package cliprender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

func newTestWorker(t *testing.T) (*Worker, *fakeMaterializer, *fakeTranscriptResolver) {
	t.Helper()
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
	})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		existing: &TranscriptResult{
			AssetID:  "asset-source",
			Language: "en",
			Text:     "existing",
			Cues:     []Cue{{StartMs: 0, EndMs: 1000, Text: "existing"}},
			Reused:   true,
		},
		existingOK: true,
	}
	preparer := newTestPreparer(resolver, mat, tr)
	w, err := NewWorker(preparer, t.TempDir(), zap.NewNop())
	if err != nil {
		panic(err)
	}
	return w, mat, tr
}

func renderJobPayload(t *testing.T, req *RenderRequest) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

type fakeRenderExecutor struct {
	called  int
	plan    ClipRenderPlanV1
	outcome *RenderOutcome
}

func (f *fakeRenderExecutor) Render(_ context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error) {
	f.called++
	f.plan = plan
	return f.outcome, nil
}

// fakeOverlayResolver returns a canned segment for the declared render_job_id.
type fakeOverlayResolver struct {
	segment *OverlaySegment
	err     error
	got     OverlayResolveInput
}

func (f *fakeOverlayResolver) Resolve(_ context.Context, in OverlayResolveInput) (*OverlaySegment, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.segment, nil
}

// fakeOverlayCompositor records the composite input and returns a canned
// composited output, mirroring the real pass contract (a new hashed file).
type fakeOverlayCompositor struct {
	composite *OverlayCompositeResult
	err       error
	got       OverlayCompositeInput
}

func (f *fakeOverlayCompositor) Composite(_ context.Context, in OverlayCompositeInput) (*OverlayCompositeResult, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.composite, nil
}

// fakeOutputProber returns a canned probe for post-render certification.
type fakeOutputProber struct {
	probe *OutputProbe
	err   error
}

func (f *fakeOutputProber) ProbeOutput(_ context.Context, _ string) (*OutputProbe, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.probe, nil
}

func TestRenderedResult_LegacyFieldsAreReadOnlyProjections(t *testing.T) {
	outcome := &RenderOutcome{OutputPath: "/work/out.mp4", SizeBytes: 1, DurationSec: 1, FFmpegMS: 1234, SubtitleRasterCPU: boolPtr(true), GPUCopyBytes: uint64Ptr(99), Metrics: NewRenderMetricsV2()}
	outcome.Metrics.CompositeMS = 1234
	outcome.Metrics.GPUCopyBytes = 99
	outcome.Metrics.SubtitleRasterCPU = true
	result := renderedResult(&job.Job{ID: "job-projection"}, &RenderRequest{SourceAssetID: "asset-source", Transcript: &TranscriptSpec{Mode: "reuse_or_generate"}}, &Prepared{Contract: &ResolvedContract{}, Source: &MaterializedAsset{}, Transcript: &TranscriptResult{}}, ClipRenderPlanV1{}, nil, outcome, nil, nil)
	render, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("render result = %v", result["render"])
	}
	metrics, ok := render["metrics_v2"].(*RenderMetricsV2)
	if !ok || metrics == nil {
		t.Fatalf("metrics_v2 = %v", render["metrics_v2"])
	}
	if render["ffmpeg_ms"] != outcome.FFmpegMS || metrics.CompositeMS != outcome.Metrics.CompositeMS {
		t.Fatalf("legacy/canonical projection mismatch: render=%v metrics=%+v", render, metrics)
	}
	if render["gpu_copy_bytes"] != outcome.GPUCopyBytes || metrics.GPUCopyBytes != outcome.Metrics.GPUCopyBytes {
		t.Fatalf("gpu projection mismatch: render=%v metrics=%+v", render, metrics)
	}
	if render["subtitle_raster_cpu"] != outcome.SubtitleRasterCPU || metrics.SubtitleRasterCPU != *outcome.SubtitleRasterCPU {
		t.Fatalf("subtitle projection mismatch: render=%v metrics=%+v", render, metrics)
	}
}

func boolPtr(v bool) *bool       { return &v }
func uint64Ptr(v uint64) *uint64 { return &v }

func TestWorker_ExecutesSealedPlanThroughRenderExecutor(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
		FFmpegMS:    1234,
	}}
	w.WithRenderExecutor(renderer)

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-render", Payload: renderJobPayload(t, baseRenderRequest())}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if renderer.called != 1 || renderer.plan.PlanSHA256 == "" {
		t.Fatalf("renderer calls/plan = %d/%+v", renderer.called, renderer.plan)
	}
	if result["phase"] != "rendered" {
		t.Fatalf("phase = %v, want rendered", result["phase"])
	}
	render, ok := result["render"].(map[string]any)
	if !ok || render["ffmpeg_ms"] != int64(1234) {
		t.Fatalf("render result = %v", result["render"])
	}
	// V2 report envelope: the worker folds the job-level total into the
	// adapter report and exposes it in the job result. Phases without real
	// instrumentation (subtitles disabled here) stay NOT_INSTRUMENTED.
	metrics, ok := render["metrics_v2"].(*RenderMetricsV2)
	if !ok || metrics == nil {
		t.Fatalf("metrics_v2 = %v, want *RenderMetricsV2 in the render block", render["metrics_v2"])
	}
	// A fast fake can legitimately measure 0 ms of wall time — the worker must
	// still have MEASURED the job total (never the NOT_INSTRUMENTED sentinel).
	if int64(metrics.TotalMS) == NotInstrumented {
		t.Fatalf("total_ms = %d, want a measured job-level wall time", int64(metrics.TotalMS))
	}
	if metrics.Frames != 72 {
		t.Fatalf("frames = %d, want 72 (3s × 24fps)", metrics.Frames)
	}
	if int64(metrics.SubtitleCompileMS) != NotInstrumented {
		t.Fatalf("subtitle_compile_ms = %d, want NOT_INSTRUMENTED (subtitles disabled)", int64(metrics.SubtitleCompileMS))
	}
	// asset_materialize_ms folds the preparer's materialize phase walls into
	// the report (the real preparer records materialize_source even through
	// the fake materializer), so the benchmark can attribute the "bring the
	// assets to disk" cost instead of leaving it in the unaccounted gap.
	if int64(metrics.AssetMaterializeMS) == NotInstrumented {
		t.Fatalf("asset_materialize_ms = %d, want the measured materialize phase wall", int64(metrics.AssetMaterializeMS))
	}
}

// TestMaterializeWallMS verifies the phase-fold helper: materialize_* walls
// sum into asset_materialize_ms; a preparation without materialize phases
// (all cached, nothing recorded) stays NOT_INSTRUMENTED (-1).
func TestMaterializeWallMS(t *testing.T) {
	summed := materializeWallMS(PreparationTimings{Phases: []PhaseTiming{
		{Phase: "resolve_source", WallMS: 12},
		{Phase: "materialize_source", WallMS: 340},
		{Phase: "materialize_watermark", WallMS: 60},
		{Phase: "transcript_resolve", WallMS: 5},
	}})
	if summed != 400 {
		t.Fatalf("materializeWallMS = %d, want 400 (340+60)", summed)
	}
	if got := materializeWallMS(PreparationTimings{Phases: []PhaseTiming{
		{Phase: "resolve_source", WallMS: 12},
	}}); got != -1 {
		t.Fatalf("materializeWallMS without materialize phases = %d, want -1 (NOT_INSTRUMENTED)", got)
	}
	if got := materializeWallMS(PreparationTimings{}); got != -1 {
		t.Fatalf("materializeWallMS(empty) = %d, want -1", got)
	}
}

// TestWorker_ValidPayload_PreparesAndFailsClosed verifies the full worker
// path: decode → validate → prepare → result envelope emitted + typed
// terminal error (render phase not implemented — fail-closed, never a
// silent success).
func TestWorker_ValidPayload_PreparesAndFailsClosed(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	req := baseRenderRequest()
	payload := renderJobPayload(t, req)

	tools := &job.JobExecutionTools{
		Progress: func(int, string) {},
		Event:    func(string, string, map[string]any) {},
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-1", Payload: payload}, tools)

	if !errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
	if result == nil {
		t.Fatal("expected result envelope with prepared artifacts")
	}
	if result["phase"] != "plan_sealed" {
		t.Errorf("phase: got %v, want plan_sealed", result["phase"])
	}
	plan, ok := result["plan"].(map[string]any)
	if !ok || plan["plan_sha256"] == "" {
		t.Errorf("plan envelope: got %v", result["plan"])
	}
	if got := result["contract_id"]; got != OutputContractVeloxAssemblyReadyV1 {
		t.Errorf("contract_id: got %v", got)
	}
	if len(mat.calls) != 1 || mat.calls[0] != "asset-source" {
		t.Errorf("expected source materialization only, got %v", mat.calls)
	}
}

// TestWorker_InvalidPayload_Terminal verifies an undecodable payload fails
// with the typed terminal sentinel before any preparation runs.
func TestWorker_InvalidPayload_Terminal(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-2", Payload: json.RawMessage(`{not json`)}, nil)
	if !errors.Is(err, ErrInvalidJobPayload) {
		t.Fatalf("expected ErrInvalidJobPayload, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on invalid payload, got %v", result)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run on invalid payload, got %v", mat.calls)
	}
}

// TestWorker_ValidationFailure_Terminal verifies an invalid (non-normalized)
// request fails with the typed terminal sentinel.
func TestWorker_ValidationFailure_Terminal(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	// Missing source_asset_id — fails Validate after Normalize.
	raw, _ := json.Marshal(&RenderRequest{})
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-3", Payload: raw}, nil)
	if !errors.Is(err, ErrInvalidJobPayload) {
		t.Fatalf("expected ErrInvalidJobPayload, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on validation failure, got %v", result)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run on invalid request, got %v", mat.calls)
	}
}

// fakeSubtitleCompiler records the compile input and returns a deterministic
// artifact (path + content hash).
type fakeSubtitleCompiler struct {
	inputs []SubtitleCompileInput
}

func (f *fakeSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleArtifact, error) {
	f.inputs = append(f.inputs, in)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", in.StyleID, in.Language, len(in.Cues))))
	return &SubtitleArtifact{
		LocalPath: filepath.Join(in.OutputDir, "subtitles.ass"),
		SHA256:    hex.EncodeToString(sum[:]),
		Mode:      in.Mode,
		StyleID:   in.StyleID,
	}, nil
}

// TestWorker_SubtitlesEnabled_CompilesFromTranscriptAndSeals verifies the
// ASS artifact is compiled from the canonical transcript cues (never a
// re-transcription) and referenced by the sealed plan with path + sha256.
func TestWorker_SubtitlesEnabled_CompilesFromTranscriptAndSeals(t *testing.T) {
	w, _, _ := newTestWorker(t)
	compiler := &fakeSubtitleCompiler{}
	w.WithSubtitleCompiler(compiler)

	req := baseRenderRequest()
	req.Subtitles = &SubtitlesSpec{Enabled: true, Mode: SubtitlesModeBurn, StyleID: "shorts-v1"}
	payload := renderJobPayload(t, req)

	var emitted []string
	tools := &job.JobExecutionTools{
		Progress: func(int, string) {},
		Event: func(eventType, _ string, _ map[string]any) {
			emitted = append(emitted, eventType)
		},
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-sub", Payload: payload}, tools)
	if !errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
	if len(compiler.inputs) != 1 {
		t.Fatalf("compile calls = %d, want 1", len(compiler.inputs))
	}
	in := compiler.inputs[0]
	if in.AssetID != "asset-source" || in.Mode != SubtitlesModeBurn || in.StyleID != "shorts-v1" {
		t.Errorf("compile input: got %+v", in)
	}
	// Cues come from the prepared transcript — the compiler never sees a
	// "generate" request (speech recognition is never re-run for subtitles).
	if len(in.Cues) != 1 || in.Cues[0].Text != "existing" {
		t.Errorf("compile input must carry the canonical transcript cues, got %+v", in.Cues)
	}
	if !contains(emitted, "clip.render.subtitles.compiled") || !contains(emitted, "clip.render.plan.sealed") {
		t.Errorf("expected compile+seal events, got %v", emitted)
	}
	plan := result["plan"].(map[string]any)
	if plan["plan_sha256"] == "" {
		t.Errorf("plan must be sealed, got %v", plan)
	}
	sub := result["subtitles"].(map[string]any)
	if sub["mode"] != SubtitlesModeBurn || sub["sha256"] == "" {
		t.Errorf("result subtitles block: got %v", sub)
	}
	// A fake compiler does not own cache instrumentation, so cache fields
	// must be absent rather than fabricated as false.
	if _, ok := sub["content_cache_hit"]; ok {
		t.Errorf("content_cache_hit = %v, must be absent when unmeasured", sub["content_cache_hit"])
	}
	if _, ok := sub["artifact_cache_hit"]; ok {
		t.Errorf("artifact_cache_hit = %v, must be absent when unmeasured", sub["artifact_cache_hit"])
	}
}

// TestWorker_SubtitlesEnabled_NoCompilerFailsClosed verifies subtitles
// enabled without a wired compiler is a typed failure — never a plan sealed
// without its ASS artifact.
func TestWorker_SubtitlesEnabled_NoCompilerFailsClosed(t *testing.T) {
	w, _, _ := newTestWorker(t) // no WithSubtitleCompiler

	req := baseRenderRequest()
	req.Subtitles = &SubtitlesSpec{Enabled: true, Mode: SubtitlesModeSidecar}
	payload := renderJobPayload(t, req)

	_, err := w.Handle(context.Background(), &job.Job{ID: "job-sub-noc", Payload: payload}, nil)
	if !errors.Is(err, ErrSubtitleCompileUnavailable) {
		t.Fatalf("expected ErrSubtitleCompileUnavailable, got %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestWorker_RecordsRunReportStages pins the clip.render observability
// contract: the worker records its serial chain (clip.prepare → clip.render
// → clip.publish) as stages on the kernel RunReport bound to ctx, plus the
// rust.render_clip operation for the render work, so the RunReport critical
// path and the benchmark can separate wall / work / critical path per phase
// instead of treating the run as uninstrumented.
func TestWorker_RecordsRunReportStages(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-obs-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
	}})
	w.WithRenderPublisher(&fakeRenderPublisher{})

	if _, err := w.Handle(ctx, &job.Job{ID: "job-obs-1", Payload: renderJobPayload(t, baseRenderRequest())}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	run.Finish()
	report := run.Report()

	// Every serial phase must be recorded as a stage (wall) on the run.
	found := map[string]bool{}
	for _, st := range report.Stages {
		found[st.Name] = true
	}
	for _, name := range []string{string(StageClipPrepare), string(StageClipRender), string(StageClipPublish)} {
		if !found[name] {
			t.Errorf("stage %s must be recorded, got %+v", name, report.Stages)
		}
	}

	// The render boundary must also be recorded as an operation (work),
	// attributed to the clip.render stage with the Chronon component.
	var renderOp bool
	for _, op := range report.Operations {
		if op.Stage == string(StageClipRender) && op.Component == string(kernobs.ComponentName("chronon")) && op.Operation == string(kernobs.OperationName("render_clip")) {
			renderOp = true
		}
	}
	if !renderOp {
		t.Errorf("chronon.render_clip operation must be recorded under clip.render, got %+v", report.Operations)
	}

	// The stages are strictly sequential, so the run's critical path must be
	// the ordered serial chain prepare → render → publish (each stage's wall
	// is its critical-path contribution).
	cp := report.Breakdown().CriticalPath
	names := make([]string, 0, len(cp))
	for _, c := range cp {
		names = append(names, c.Name)
	}
	want := []string{string(StageClipPrepare), string(StageClipRender), string(StageClipPublish)}
	if len(names) != len(want) {
		t.Fatalf("critical path = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("critical path = %v, want %v", names, want)
		}
	}
}

// TestWorker_NoRunBoundRecordsNothing pins the nil-run degradation: without a
// Run bound to ctx the worker still completes (instrumentation must never
// change behaviour) and records no stages.
func TestWorker_NoRunBoundRecordsNothing(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
	}})
	w.WithRenderPublisher(&fakeRenderPublisher{})

	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-obs-2", Payload: renderJobPayload(t, baseRenderRequest())}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

// fakeRenderPublisher records the publish input and returns a deterministic
// publication so the full worker path (render + publish + result envelope)
// can be exercised without Drive or SQLite.
type fakeRenderPublisher struct {
	called int
	input  RenderPublishInput
	out    RenderPublishResult
}

func (f *fakeRenderPublisher) Publish(_ context.Context, in RenderPublishInput) (*RenderPublishResult, error) {
	f.called++
	f.input = in
	out := f.out
	if out.AssetID == "" {
		out = RenderPublishResult{
			AssetID:     "final-video-asset-001",
			DriveFileID: "drive-file-001",
			DriveLink:   "https://drive.google.com/file/d/drive-file-001/view",
			SizeBytes:   in.Outcome.SizeBytes,
			Publish: &PublicationMetrics{
				HashMS: 1, VideoUploadMS: 2, TaxonomyResolveMS: 3, AssetCommitMS: 4, TotalMS: 10,
			},
		}
	}
	return &out, nil
}

// fakeDestinationFolderResolver records the resolve input and returns a
// canned leaf folder ID (or the injected error), so the worker's
// one-time-per-job destination resolution can be exercised without Drive.
type fakeDestinationFolderResolver struct {
	calls int
	input DestinationFolderResolveInput
	out   string
	err   error
}

func (f *fakeDestinationFolderResolver) ResolveDestinationFolder(_ context.Context, in DestinationFolderResolveInput) (string, error) {
	f.calls++
	f.input = in
	if f.err != nil {
		return "", f.err
	}
	if f.out == "" {
		return "leaf-folder-001", nil
	}
	return f.out, nil
}

func fullRenderOutcome() *RenderOutcome {
	return &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
		FFmpegMS:    1234,
	}
}

// TestWorker_DestinationSubfolder_ResolvedOncePerJob verifies the canonical
// script/batch destination rule: a request carrying
// destination.subfolder_name makes the worker resolve the leaf folder ONCE
// per job through the DestinationFolderResolver, and the publisher receives
// the fully-resolved leaf folder ID (the publisher never creates folders
// and never sees the raw subfolder name).
func TestWorker_DestinationSubfolder_ResolvedOncePerJob(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()})
	publisher := &fakeRenderPublisher{}
	w.WithRenderPublisher(publisher)
	resolver := &fakeDestinationFolderResolver{out: "leaf-script-folder-123"}
	w.WithDestinationFolderResolver(resolver)

	req := baseRenderRequest()
	req.Destination = &DestinationSpec{
		DriveFolderID: "root-folder-abc",
		SubfolderName: "Matt Damon 5 Clips Verification",
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-folder-resolve", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if result["phase"] != "rendered" {
		t.Fatalf("phase = %v, want rendered", result["phase"])
	}
	// Exactly one resolution per job, with the caller's root + raw name
	// (sanitisation is the adapter's job, not the worker's).
	if resolver.calls != 1 {
		t.Fatalf("destination resolver calls = %d, want 1 (once per job)", resolver.calls)
	}
	if resolver.input.RootFolderID != "root-folder-abc" || resolver.input.SubfolderName != "Matt Damon 5 Clips Verification" {
		t.Fatalf("resolver input = %+v, want root root-folder-abc + subfolder name", resolver.input)
	}
	// The publisher must receive the RESOLVED leaf, never the root and never
	// the raw subfolder name — it stays dumb by contract.
	if publisher.input.DriveFolderID != "leaf-script-folder-123" {
		t.Fatalf("publisher destination = %q, want the resolved leaf folder", publisher.input.DriveFolderID)
	}
}

// TestWorker_DestinationSubfolder_FailClosedWithoutResolver verifies that a
// subfolder_name declared without a wired DestinationFolderResolver is a
// typed failure before any preparation runs — the publisher must never
// silently fall back to a root upload when a script folder was requested.
func TestWorker_DestinationSubfolder_FailClosedWithoutResolver(t *testing.T) {
	w, mat, _ := newTestWorker(t) // no WithDestinationFolderResolver
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()})

	req := baseRenderRequest()
	req.Destination = &DestinationSpec{DriveFolderID: "root-folder-abc", SubfolderName: "Some Script"}
	_, err := w.Handle(context.Background(), &job.Job{ID: "job-folder-nores", Payload: renderJobPayload(t, req)}, nil)
	if err == nil {
		t.Fatal("subfolder_name without a wired resolver must fail closed")
	}
	if !strings.Contains(err.Error(), "DestinationFolderResolver") {
		t.Fatalf("expected the resolver-missing typed error, got %v", err)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run when destination resolution fails, got %v", mat.calls)
	}
}

// TestWorker_DestinationLeafFolder_PassesThrough verifies the legacy
// behaviour: without destination.subfolder_name the request's
// destination.drive_folder_id IS the resolved leaf and is handed to the
// publisher verbatim (no resolution, no folder creation).
func TestWorker_DestinationLeafFolder_PassesThrough(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()})
	publisher := &fakeRenderPublisher{}
	w.WithRenderPublisher(publisher)
	resolver := &fakeDestinationFolderResolver{}
	w.WithDestinationFolderResolver(resolver)

	req := baseRenderRequest() // default destination = DefaultDriveRootFolderID
	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-folder-passthrough", Payload: renderJobPayload(t, req)}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("destination resolver calls = %d, want 0 (no subfolder_name)", resolver.calls)
	}
	if publisher.input.DriveFolderID != DefaultDriveRootFolderID {
		t.Fatalf("publisher destination = %q, want the request leaf %q verbatim", publisher.input.DriveFolderID, DefaultDriveRootFolderID)
	}
}

// TestWorker_OverlayLineageProjectedIntoResult certifies Gate 7's final
// binding: a clip.render request that declares an overlay must surface the
// complete overlay lineage (render job id + plan fingerprint + render key +
// source video asset id) on the final video result alongside the published
// asset (final_video_asset_id + Drive file), so the final video asset proves
// WHICH overlay it composites.
func TestWorker_OverlayLineageProjectedIntoResult(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
	}}
	publisher := &fakeRenderPublisher{}
	w.WithRenderExecutor(renderer)
	w.WithRenderPublisher(publisher)

	req := baseRenderRequest()
	req.Overlay = &OverlayRefSpec{
		RenderJobID:        "render-michael-jordan-overlay-001",
		PlanFingerprint:    "fp-michael-jordan",
		RenderKey:          "rk-michael-jordan",
		SourceVideoAssetID: "source-video-asset-001",
		StartUS:            50000,
		EndUS:              950000,
	}
	resolver := &fakeOverlayResolver{segment: &OverlaySegment{
		RenderJobID: "render-michael-jordan-overlay-001",
		RenderKey:   "rk-michael-jordan",
		LocalPath:   "/work/overlay-segment.mp4",
		SHA256:      "segment-sha256",
		SizeBytes:   4096,
	}}
	compositor := &fakeOverlayCompositor{composite: &OverlayCompositeResult{
		OutputPath:  "/work/composited-clip.mp4",
		SHA256:      "composited-sha256",
		SizeBytes:   8192,
		CompositeMS: 137,
	}}
	w.WithOverlaySegmentResolver(resolver)
	w.WithOverlayCompositor(compositor)
	// Post-composite probe is mandatory when overlay is declared.
	w.WithOutputProber(&fakeOutputProber{probe: &OutputProbe{
		Container: "mp4", HasVideo: true, HasAudio: true,
		VideoCodec: "h264", VideoProfile: "high", PixelFormat: "yuv420p",
		Width: 1920, Height: 1080, FPS: 24.0, FPSNum: 24, FPSDen: 1,
		AudioCodec: "aac", AudioProfile: "LC", SampleRate: 48000, Channels: 2,
		ChannelLayout: "stereo", AudioBitrate: "128k",
		VideoStreams: 1, AudioStreams: 1, StartPTS: 0,
	}})

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-lineage", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	overlay, ok := result["overlay"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry an overlay block, got %+v", result)
	}
	if overlay["render_job_id"] != "render-michael-jordan-overlay-001" {
		t.Errorf("overlay render_job_id = %v", overlay["render_job_id"])
	}
	if overlay["plan_fingerprint"] != "fp-michael-jordan" {
		t.Errorf("overlay plan_fingerprint = %v", overlay["plan_fingerprint"])
	}
	if overlay["render_key"] != "rk-michael-jordan" {
		t.Errorf("overlay render_key = %v", overlay["render_key"])
	}
	if overlay["source_video_asset_id"] != "source-video-asset-001" {
		t.Errorf("overlay source_video_asset_id = %v", overlay["source_video_asset_id"])
	}
	if overlay["start_us"] != int64(50000) || overlay["end_us"] != int64(950000) {
		t.Errorf("overlay window = %v..%v, want 50000..950000", overlay["start_us"], overlay["end_us"])
	}

	// The compositing pass must have been invoked with the exact declared
	// lineage + window: the resolver got the render_job_id, the compositor
	// got the resolved segment and the [start_us, end_us) window.
	if resolver.got.RenderJobID != "render-michael-jordan-overlay-001" || resolver.got.RenderKey != "rk-michael-jordan" {
		t.Errorf("overlay resolver input = %+v", resolver.got)
	}
	if compositor.got.Segment == nil || compositor.got.Segment.SHA256 != "segment-sha256" {
		t.Errorf("overlay compositor segment = %+v", compositor.got.Segment)
	}
	if compositor.got.StartUS != 50000 || compositor.got.EndUS != 950000 {
		t.Errorf("overlay compositor window = %d..%d, want 50000..950000", compositor.got.StartUS, compositor.got.EndUS)
	}
	if compositor.got.SourcePath != "/work/rendered-clip.mp4" {
		t.Errorf("overlay compositor source = %q", compositor.got.SourcePath)
	}
	if overlay["composited"] != true || overlay["sha256"] != "composited-sha256" || overlay["composite_ms"] != int64(137) {
		t.Errorf("overlay compositing facts = %v", overlay)
	}

	// The final video asset block carries the Drive identity of the derived
	// asset: source_video_asset_id (request) → final_video_asset_id + Drive.
	// The published file must be the COMPOSITED output, never the raw render.
	assetBlock, ok := result["asset"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry a published asset block, got %+v", result)
	}
	if assetBlock["asset_id"] != "final-video-asset-001" {
		t.Errorf("final video asset_id = %v", assetBlock["asset_id"])
	}
	if assetBlock["drive_file_id"] != "drive-file-001" {
		t.Errorf("final video drive_file_id = %v", assetBlock["drive_file_id"])
	}
	if publisher.input.OutputPath != "/work/composited-clip.mp4" {
		t.Errorf("published output = %q, want the composited clip", publisher.input.OutputPath)
	}
	if result["source_asset_id"] != "asset-source" {
		t.Errorf("source_asset_id = %v", result["source_asset_id"])
	}
	// The full publication wall (probe + overlay + Drive upload) is folded
	// into the V2 report as publication_total_ms without overwriting the
	// renderer-owned finalize timing.
	renderBlock, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry a render block, got %+v", result["render"])
	}
	if metrics, ok := renderBlock["metrics_v2"].(*RenderMetricsV2); ok && metrics != nil {
		if int64(metrics.PublicationTotalMS) == NotInstrumented {
			t.Fatalf("publication_total_ms = %d, want the measured publication wall", int64(metrics.PublicationTotalMS))
		}
		if int64(metrics.ArtifactPublishMS) == NotInstrumented {
			t.Fatalf("artifact_publish_ms = %d, want the measured publisher boundary", int64(metrics.ArtifactPublishMS))
		}
		if int64(metrics.RendererOutputFinalizeMS) != NotInstrumented {
			t.Fatalf("renderer_finalize_ms = %d, must not be overwritten by worker publication timing", int64(metrics.RendererOutputFinalizeMS))
		}
	} else {
		t.Fatalf("metrics_v2 = %v, want the V2 report in the render block", renderBlock["metrics_v2"])
	}
}

// TestWorker_OverlayCompositing_FailClosedWithoutWiring certifies the
// fail-closed half of compositing: a request that declares an overlay but
// arrives at a worker without a segment resolver (or compositor) fails with
// a typed error — the final video never claims an overlay it cannot
// composite.
func TestWorker_OverlayCompositing_FailClosedWithoutWiring(t *testing.T) {
	for name, wire := range map[string]func(*Worker){
		"no resolver": func(w *Worker) {},
		"no compositor": func(w *Worker) {
			w.WithOverlaySegmentResolver(&fakeOverlayResolver{segment: &OverlaySegment{
				RenderJobID: "render-job-001", RenderKey: "key-001", LocalPath: "/work/seg.mp4", SHA256: "s",
			}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, _, _ := newTestWorker(t)
			w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
				OutputPath:  "/work/rendered-clip.mp4",
				SizeBytes:   4096,
				DurationSec: 3,
				Width:       1920,
				Height:      1080,
				FPSNum:      24,
				FPSDen:      1,
				Backend:     BackendChrononVulkan,
			}})
			wire(w)

			req := baseRenderRequest()
			req.Overlay = &OverlayRefSpec{
				RenderJobID:        "render-job-001",
				PlanFingerprint:    "fp-001",
				RenderKey:          "key-001",
				SourceVideoAssetID: "source-video-001",
				StartUS:            50000,
				EndUS:              950000,
			}
			_, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-fail", Payload: renderJobPayload(t, req)}, nil)
			if err == nil {
				t.Fatal("overlay declared without compositing wiring must fail")
			}
		})
	}
}

// TestWorker_OverlayCompositing_FailClosedOnResolutionError certifies that
// an unresolvable segment or a failed blend aborts the job — the published
// video never claims an overlay it does not carry.
func TestWorker_OverlayCompositing_FailClosedOnResolutionError(t *testing.T) {
	for name, setup := range map[string]func() (*OverlaySegment, error){
		"resolver error": func() (*OverlaySegment, error) { return nil, errors.New("overlay.render job not found") },
		"compositor error": func() (*OverlaySegment, error) {
			return &OverlaySegment{RenderJobID: "render-job-001", RenderKey: "key-001", LocalPath: "/work/seg.mp4", SHA256: "s"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, _, _ := newTestWorker(t)
			w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
				OutputPath:  "/work/rendered-clip.mp4",
				SizeBytes:   4096,
				DurationSec: 3,
				Width:       1920,
				Height:      1080,
				FPSNum:      24,
				FPSDen:      1,
				Backend:     BackendChrononVulkan,
			}})
			publisher := &fakeRenderPublisher{}
			w.WithRenderPublisher(publisher)

			segment, resolverErr := setup()
			resolver := &fakeOverlayResolver{segment: segment, err: resolverErr}
			compositor := &fakeOverlayCompositor{err: errors.New("blend failed")}
			w.WithOverlaySegmentResolver(resolver)
			w.WithOverlayCompositor(compositor)

			req := baseRenderRequest()
			req.Overlay = &OverlayRefSpec{
				RenderJobID:        "render-job-001",
				PlanFingerprint:    "fp-001",
				RenderKey:          "key-001",
				SourceVideoAssetID: "source-video-001",
				StartUS:            50000,
				EndUS:              950000,
			}
			_, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-fail", Payload: renderJobPayload(t, req)}, nil)
			if err == nil {
				t.Fatal("overlay compositing failure must fail the job")
			}
			if publisher.called != 0 {
				t.Error("publisher must not run when compositing fails")
			}
		})
	}
}

// TestWorker_PrepareFailure_Wrapped verifies a preparation failure surfaces
// as a wrapped error, never a silent success.
func TestWorker_PrepareFailure_Wrapped(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{}
	preparer := newTestPreparer(resolver, mat, tr)
	w, err := NewWorker(preparer, t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	req := baseRenderRequest()
	payload := renderJobPayload(t, req)
	_, err = w.Handle(context.Background(), &job.Job{ID: "job-4", Payload: payload}, nil)
	if err == nil {
		t.Fatal("expected error when asset resolution fails")
	}
	if errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("resolution failure must not be misreported as render-phase sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "resolve source") {
		t.Fatalf("expected the wrapped resolution failure, got %v", err)
	}
}
