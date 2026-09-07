package cliprender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"path/filepath"
	"testing"
)

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

// TestWorker_SubtitlesInlineStyle_ReachesSealedPlan verifies the caller's
// typed subtitle style block (font, size, color, stroke) travels into the
// sealed plan's Subtitles.Style verbatim — the RenderingGen burn has no other
// owner for the subtitle typography, so dropping it here silently resets
// every subtitle render to defaults (and previously stripped the stroke).
func TestWorker_SubtitlesInlineStyle_ReachesSealedPlan(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithSubtitleCompiler(&fakeSubtitleCompiler{})
	renderer := &fakeRenderExecutor{outcome: fullRenderOutcome()}
	w.WithRenderExecutor(renderer)

	req := baseRenderRequest()
	req.Subtitles = &SubtitlesSpec{
		Enabled: true,
		Mode:    SubtitlesModeBurn,
		StyleID: "shorts-v1",
		Style: &scriptpkg.VideoVisualStyleSpec{
			Font:       "Poppins",
			FontSizePX: 58,
			Color:      "#FFFFFF",
			Position:   "bottom_center",
			Stroke:     &scriptpkg.VideoStrokeSpec{Color: "#000000", Width: 3},
		},
	}
	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-style", Payload: renderJobPayload(t, req)}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if renderer.plan.Subtitles == nil || renderer.plan.Subtitles.Style == nil {
		t.Fatalf("sealed plan must carry the caller subtitle style, got %+v", renderer.plan.Subtitles)
	}
	sub := renderer.plan.Subtitles.Style
	if sub.Font != "Poppins" || sub.FontSizePX != 58 || sub.Color != "#FFFFFF" || sub.Position != "bottom_center" {
		t.Fatalf("subtitle style dropped or defaulted: %+v", sub)
	}
	if sub.Stroke == nil || sub.Stroke.Color != "#000000" || sub.Stroke.Width != 3 {
		t.Fatalf("subtitle stroke must travel verbatim, got %+v", sub.Stroke)
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
