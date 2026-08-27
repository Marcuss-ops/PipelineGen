package adapters

// localization_render_test.go — the RenderPlanExecutor adapter: a sealed
// render.RenderPlan + subtitle ASS is mapped into a concrete ClipRenderPlanV1
// (burn subtitles) and executed through the render_clip boundary, returning
// certified RenderFacts.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fakeLocalizationRenderExecutor records the ClipRenderPlanV1 it was handed and
// returns a fixed outcome (or error).
type fakeLocalizationRenderExecutor struct {
	outcome *cliprender.RenderOutcome
	err     error
	gotPlan cliprender.ClipRenderPlanV1
}

func (f *fakeLocalizationRenderExecutor) Render(_ context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	f.gotPlan = plan
	if f.err != nil {
		return nil, f.err
	}
	return f.outcome, nil
}

// appTestRenderPlan builds a valid, sealed render.RenderPlan for one localized
// render: 10s of source at 30fps with a burn (re-encode) execution policy.
func appTestRenderPlan(t *testing.T, srcPath, srcSHA, outPath string) render.RenderPlan {
	t.Helper()
	durationUS := int64(10 * 1000 * 1000) // 10s
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: durationUS,
		Segments: []audio.TimelineSegment{{
			ID:              "clip-1",
			Index:           0,
			TimelineStartUS: 0,
			DurationUS:      durationUS,
			Video: audio.VideoSegment{
				AssetID:          "source-1",
				SourceInUS:       0,
				SourceDurationUS: durationUS,
			},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	rate := audio.IntegerFrameRate(30)
	resolver, err := audio.NewFrameResolver(rate)
	if err != nil {
		t.Fatalf("NewFrameResolver: %v", err)
	}
	durationFrames, err := resolver.FrameCountForDuration(durationUS)
	if err != nil {
		t.Fatalf("FrameCountForDuration: %v", err)
	}
	plan, err := render.Compile(render.CompileInput{
		JobID:      "job-1",
		Revision:   "clip-1/es",
		OutputPath: outPath,
		FrameRate:  rate,
		Timeline:   timeline,
		Manifest: []render.AssetManifestEntry{{
			AssetID:    "source-1",
			Path:       srcPath,
			SHA256:     srcSHA,
			FrameCount: durationFrames,
		}},
		ExecutionPolicy: &render.RenderExecutionPolicy{
			AllowStreamCopy:   false,
			TargetProfileHash: strings.Repeat("b", 64),
			RendererVersion:   "renderer-v1",
			EncoderPolicyHash: strings.Repeat("c", 64),
		},
	})
	if err != nil {
		t.Fatalf("render.Compile: %v", err)
	}
	return plan
}

func appTestSubtitle(t *testing.T) *localization.SubtitleAsset {
	t.Helper()
	return &localization.SubtitleAsset{
		LocalPath: filepath.Join(t.TempDir(), "clip-1.es.ass"),
		SHA256:    strings.Repeat("d", 64),
		StyleHash: "style-sha",
		TrackID:   202,
	}
}

func TestLocalizationRenderPlanExecutor_ExecutesViaClipRender(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(srcPath, []byte("source-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcSHA, _, err := digest.SHA256File(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(dir, "clip-1.es.mp4")
	if err := os.WriteFile(outPath, []byte("rendered-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	wantSHA, _, err := digest.SHA256File(outPath)
	if err != nil {
		t.Fatal(err)
	}

	plan := appTestRenderPlan(t, srcPath, srcSHA, outPath)
	subtitle := appTestSubtitle(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{OutputPath: outPath, SizeBytes: info.Size(), DurationSec: 8.432}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1}, zap.NewNop())

	facts, err := adapter.Execute(context.Background(), plan, subtitle)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if facts.LocalPath != outPath || facts.SizeBytes != info.Size() || facts.SHA256 != wantSHA {
		t.Fatalf("facts: %+v", facts)
	}
	if facts.DurationMS != 8432 {
		t.Fatalf("DurationMS: got %d, want 8432", facts.DurationMS)
	}
	if facts.VideoCodec != "h264" || facts.AudioCodec != "aac" {
		t.Fatalf("codecs: got %q/%q", facts.VideoCodec, facts.AudioCodec)
	}

	// The concrete ClipRenderPlanV1 must carry the source, the burned ASS, and
	// the resolved output contract.
	got := exec.gotPlan
	if got.Source.AssetID != "source-1" || got.Source.Path != srcPath || got.Source.SHA256 != srcSHA {
		t.Fatalf("clip plan source: %+v", got.Source)
	}
	if got.Subtitles == nil || got.Subtitles.Mode != cliprender.SubtitlesModeBurn || got.Subtitles.SHA256 != subtitle.SHA256 || got.Subtitles.StyleID != "style-sha" {
		t.Fatalf("clip plan subtitles: %+v", got.Subtitles)
	}
	if got.Output.ContractID != cliprender.OutputContractVeloxAssemblyReadyV1 || got.Output.Width != 1920 || got.Output.Height != 1080 || got.Output.FPSNum != 30 || got.Output.FPSDen != 1 || got.Output.VideoCodec != "h264" {
		t.Fatalf("clip plan output: %+v", got.Output)
	}
	if got.OutputPath != outPath || got.RunID != "clip-1/es" {
		t.Fatalf("clip plan identity: run=%q out=%q", got.RunID, got.OutputPath)
	}
}

// TestLocalizationRenderPlanExecutor_ExecuteExtendedPropagatesVisualLayers
// verifies the full-fidelity path: watermark style, background (mode + asset),
// and subtitle style all reach the sealed ClipRenderPlanV1 on one render pass.
func TestLocalizationRenderPlanExecutor_ExecuteExtendedPropagatesVisualLayers(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(srcPath, []byte("source-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcSHA, _, _ := digest.SHA256File(srcPath)
	outPath := filepath.Join(dir, "clip-1.es.mp4")
	if err := os.WriteFile(outPath, []byte("rendered-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(outPath)

	plan := appTestRenderPlan(t, srcPath, srcSHA, outPath)
	subtitle := appTestSubtitle(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{OutputPath: outPath, SizeBytes: info.Size(), DurationSec: 8.432}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())

	bgAsset := &cliprender.MaterializedAsset{
		AssetID:   "asset-bg",
		LocalPath: "/scratch/asset-bg.mp4",
		SHA256:    strings.Repeat("f", 64),
	}
	wmStyle := &scriptpkg.VideoVisualStyleSpec{
		WidthPX:      180,
		ScalePercent: 100,
		Shadow:       &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.55, BlurPX: 14, OffsetY: 8},
		TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250},
	}
	subStyle := &scriptpkg.VideoVisualStyleSpec{
		Color:      "#FFFFFF",
		FontSizePX: 54,
		Shadow:     &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.7, BlurPX: 10, OffsetY: 5},
	}

	facts, err := adapter.ExecuteExtended(context.Background(), plan, subtitle, localization.RenderOptions{
		Watermark: &cliprender.MaterializedAsset{
			AssetID:   "asset-wm",
			LocalPath: "/scratch/asset-wm.png",
			SHA256:    strings.Repeat("e", 64),
		},
		WatermarkSpec:  &cliprender.WatermarkSpec{Enabled: true, AssetID: "asset-wm", Position: cliprender.PositionTopRight, Opacity: 0.9, MarginPX: 24, Style: wmStyle},
		Background:     bgAsset,
		BackgroundMode: cliprender.BackgroundModeAsset,
		SubtitlesStyle: subStyle,
	})
	if err != nil {
		t.Fatalf("ExecuteExtended: %v", err)
	}
	if facts.LocalPath != outPath {
		t.Fatalf("facts: %+v", facts)
	}

	got := exec.gotPlan
	if got.Watermark == nil || got.Watermark.Style == nil || got.Watermark.Style.WidthPX != 180 || got.Watermark.Style.Shadow == nil || got.Watermark.Style.Shadow.Opacity != 0.55 || got.Watermark.Style.TransitionIn == nil || got.Watermark.Style.TransitionIn.DurationMS != 250 {
		t.Fatalf("watermark style lost in sealed plan: %+v", got.Watermark)
	}
	if got.Background == nil || got.Background.Mode != cliprender.BackgroundModeAsset || got.Background.AssetID != "asset-bg" || got.Background.Path != "/scratch/asset-bg.mp4" || got.Background.SHA256 != bgAsset.SHA256 {
		t.Fatalf("background lost in sealed plan: %+v", got.Background)
	}
	if got.Subtitles == nil || got.Subtitles.Style == nil || got.Subtitles.Style.Color != "#FFFFFF" || got.Subtitles.Style.FontSizePX != 54 || got.Subtitles.Style.Shadow == nil || got.Subtitles.Style.Shadow.BlurPX != 10 {
		t.Fatalf("subtitle style lost in sealed plan: %+v", got.Subtitles)
	}
}

func TestLocalizationRenderPlanExecutor_NilSubtitleSkipsBurn(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(srcPath, []byte("source-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcSHA, _, _ := digest.SHA256File(srcPath)
	outPath := filepath.Join(dir, "clip-1.es.mp4")
	if err := os.WriteFile(outPath, []byte("rendered"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(outPath)

	plan := appTestRenderPlan(t, srcPath, srcSHA, outPath)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{OutputPath: outPath, SizeBytes: info.Size(), DurationSec: 10}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())

	if _, err := adapter.Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exec.gotPlan.Subtitles != nil {
		t.Fatalf("nil subtitle must skip the burn, got %+v", exec.gotPlan.Subtitles)
	}
}

func TestLocalizationRenderPlanExecutor_RejectsInvalidPlan(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	_ = os.WriteFile(srcPath, []byte("x"), 0o644)
	srcSHA, _, _ := digest.SHA256File(srcPath)
	plan := appTestRenderPlan(t, srcPath, srcSHA, filepath.Join(dir, "out.mp4"))
	plan.PlanSHA256 = strings.Repeat("e", 64) // tamper

	adapter := NewRenderPlanExecutor(&fakeLocalizationRenderExecutor{}, mediaexec.VideoProfile{}, zap.NewNop())
	if _, err := adapter.Execute(context.Background(), plan, nil); err == nil {
		t.Fatal("Execute must reject a drifted render plan")
	}
}

func TestLocalizationRenderPlanExecutor_RejectsIncompleteSubtitle(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	_ = os.WriteFile(srcPath, []byte("x"), 0o644)
	srcSHA, _, _ := digest.SHA256File(srcPath)
	plan := appTestRenderPlan(t, srcPath, srcSHA, filepath.Join(dir, "out.mp4"))

	adapter := NewRenderPlanExecutor(&fakeLocalizationRenderExecutor{}, mediaexec.VideoProfile{}, zap.NewNop())
	if _, err := adapter.Execute(context.Background(), plan, &localization.SubtitleAsset{LocalPath: "/x.ass"}); err == nil {
		t.Fatal("Execute must reject an incomplete subtitle ASS")
	}
}

func TestLocalizationRenderPlanExecutor_PropagatesRenderError(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	_ = os.WriteFile(srcPath, []byte("x"), 0o644)
	srcSHA, _, _ := digest.SHA256File(srcPath)
	plan := appTestRenderPlan(t, srcPath, srcSHA, filepath.Join(dir, "out.mp4"))

	adapter := NewRenderPlanExecutor(&fakeLocalizationRenderExecutor{err: errors.New("rust down")}, mediaexec.VideoProfile{}, zap.NewNop())
	if _, err := adapter.Execute(context.Background(), plan, nil); err == nil {
		t.Fatal("Execute must propagate a render error")
	}
}

func TestLocalizationRenderPlanExecutor_RejectsEmptyOutcome(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.mp4")
	_ = os.WriteFile(srcPath, []byte("x"), 0o644)
	srcSHA, _, _ := digest.SHA256File(srcPath)
	plan := appTestRenderPlan(t, srcPath, srcSHA, filepath.Join(dir, "out.mp4"))

	adapter := NewRenderPlanExecutor(&fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{OutputPath: ""}}, mediaexec.VideoProfile{}, zap.NewNop())
	if _, err := adapter.Execute(context.Background(), plan, nil); err == nil {
		t.Fatal("Execute must reject an empty render outcome")
	}
}

func TestLocalizationRenderPlanExecutor_NilRendererFailsClosed(t *testing.T) {
	adapter := NewRenderPlanExecutor(nil, mediaexec.VideoProfile{}, zap.NewNop())
	if _, err := adapter.Execute(context.Background(), render.RenderPlan{}, nil); err == nil {
		t.Fatal("Execute must fail closed on an unwired renderer")
	}
}
