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
// returns a fixed outcome (or error). It implements the two-half
// cliprender.RenderExecutor port: Submit records the plan (and can fail), and
// Settle returns the fixed outcome. submitErr lets the tests pin the
// fail-closed submit half.
type fakeLocalizationRenderExecutor struct {
	outcome        *cliprender.RenderOutcome
	err            error
	submitErr      error
	materializeErr error
	gotPlan        cliprender.ClipRenderPlanV1
	submits        int
	settles        int
	materializes   int
	materializedTo string
}

func (f *fakeLocalizationRenderExecutor) Submit(_ context.Context, plan cliprender.ClipRenderPlanV1) error {
	f.gotPlan = plan
	f.submits++
	return f.submitErr
}

func (f *fakeLocalizationRenderExecutor) Settle(_ context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	f.gotPlan = plan
	f.settles++
	if f.err != nil {
		return nil, f.err
	}
	return f.outcome, nil
}

// Materialize makes the fake satisfy cliprender.RenderArtifactMaterializer, so
// the locator-first path is exercised without touching the network.
func (f *fakeLocalizationRenderExecutor) Materialize(_ context.Context, outcome *cliprender.RenderOutcome, destPath string) (string, error) {
	f.materializes++
	if f.materializeErr != nil {
		return "", f.materializeErr
	}
	f.materializedTo = destPath
	outcome.OutputPath = destPath
	return destPath, nil
}

// fakeLocatorOnlyExecutor is a renderer WITHOUT Materialize, used to pin the
// fail-closed branch for a locator-only outcome.
type fakeLocatorOnlyExecutor struct{ outcome *cliprender.RenderOutcome }

func (f *fakeLocatorOnlyExecutor) Submit(context.Context, cliprender.ClipRenderPlanV1) error {
	return nil
}

func (f *fakeLocatorOnlyExecutor) Settle(context.Context, cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
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

// localizedRenderFixture writes a source + rendered output pair and returns the
// sealed plan, the real digest of the rendered bytes and its size.
func localizedRenderFixture(t *testing.T) (render.RenderPlan, string, string, int64) {
	t.Helper()
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
	realSHA, _, err := digest.SHA256File(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return appTestRenderPlan(t, srcPath, srcSHA, outPath), outPath, realSHA, info.Size()
}

// TestLocalizationRenderPlanExecutor_ReusesCertifiedDigest pins the waste
// removal: when the render boundary certifies the output digest, the adapter
// adopts it verbatim instead of re-reading the whole artifact. The certified
// digest is deliberately NOT the real digest of the bytes on disk, so a
// regression that goes back to hashing the file cannot pass this test.
func TestLocalizationRenderPlanExecutor_ReusesCertifiedDigest(t *testing.T) {
	plan, outPath, realSHA, size := localizedRenderFixture(t)
	certified := strings.Repeat("a", 64)
	if certified == realSHA {
		t.Fatal("fixture must use a certified digest distinct from the real bytes")
	}
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath:  outPath,
		SizeBytes:   size,
		SHA256:      certified,
		DurationSec: 8.432,
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1}, zap.NewNop())

	facts, err := adapter.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if facts.SHA256 != certified {
		t.Fatalf("facts.SHA256 = %q, want the certified digest %q (a re-hash means the artifact was read twice)", facts.SHA256, certified)
	}
	if facts.SizeBytes != size {
		t.Fatalf("facts.SizeBytes = %d, want %d", facts.SizeBytes, size)
	}
}

// TestLocalizationRenderPlanExecutor_FailsClosedOnUncertifiedOutcome pins the
// fallback: an outcome with no certified digest (a legacy/test renderer) still
// gets the real bytes hashed, never a trusted empty string.
func TestLocalizationRenderPlanExecutor_FailsClosedOnUncertifiedOutcome(t *testing.T) {
	plan, outPath, realSHA, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{OutputPath: outPath, SizeBytes: size, DurationSec: 8.432}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())

	facts, err := adapter.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if facts.SHA256 != realSHA {
		t.Fatalf("facts.SHA256 = %q, want the real bytes digest %q", facts.SHA256, realSHA)
	}
}

// TestLocalizationRenderPlanExecutor_MaterializesLocatorOnlyOutcome pins the
// locator-first consumer contract: a Settle outcome with no local path is
// materialized on demand through the boundary before it is hashed/published.
func TestLocalizationRenderPlanExecutor_MaterializesLocatorOnlyOutcome(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	certified := strings.Repeat("a", 64)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		SizeBytes:   size,
		SHA256:      certified,
		DurationSec: 8.432,
		ArtifactURL: "http://objectstore:9000/objects/" + certified,
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())

	facts, err := adapter.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exec.materializes != 1 || exec.materializedTo != outPath {
		t.Fatalf("materialize calls=%d dest=%q, want 1/%q", exec.materializes, exec.materializedTo, outPath)
	}
	if facts.LocalPath != outPath {
		t.Fatalf("facts.LocalPath = %q, want the materialized path %q", facts.LocalPath, outPath)
	}
	if facts.SHA256 != certified {
		t.Fatalf("facts.SHA256 = %q, want the certified digest %q", facts.SHA256, certified)
	}
}

// TestLocalizationRenderPlanExecutor_LocatorOnlyWithoutMaterializerFailsClosed
// pins that a locator-only outcome from a renderer that cannot materialize is a
// typed error, never a silent skip.
func TestLocalizationRenderPlanExecutor_LocatorOnlyWithoutMaterializerFailsClosed(t *testing.T) {
	plan, _, _, size := localizedRenderFixture(t)
	exec := &fakeLocatorOnlyExecutor{outcome: &cliprender.RenderOutcome{
		SizeBytes: size, SHA256: strings.Repeat("a", 64), ArtifactURL: "http://store/object",
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())
	if _, err := adapter.Execute(context.Background(), plan, nil); err == nil {
		t.Fatal("Execute must fail closed when a locator-only outcome cannot be materialized")
	}
}

// TestLocalizationRenderPlanExecutor_RehashesOnCertifiedSizeMismatch pins the
// fail-closed guard: a certified digest whose certified size no longer matches
// the file on disk is never trusted, and the real bytes are hashed instead.
func TestLocalizationRenderPlanExecutor_RehashesOnCertifiedSizeMismatch(t *testing.T) {
	plan, outPath, realSHA, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath:  outPath,
		SizeBytes:   size + 1, // certified size drifted from the bytes on disk
		SHA256:      strings.Repeat("b", 64),
		DurationSec: 8.432,
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())

	facts, err := adapter.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if facts.SHA256 != realSHA {
		t.Fatalf("facts.SHA256 = %q, want the re-hashed real digest %q", facts.SHA256, realSHA)
	}
}

// ── Reused overlay: resolution, sealing and fail-closed gates ────────────────

// fakeOverlaySegmentResolver counts resolutions and returns the cached segment
// for a render_key. It stands in for the content-addressed overlays cache: the
// same render_keys are handed in by every language variant, so the real resolver
// hashes each segment once and every variant reuses those bytes.
type fakeOverlaySegmentResolver struct {
	segment *cliprender.OverlaySegment
	byKey   map[string]*cliprender.OverlaySegment
	err     error
	calls   int
	last    cliprender.OverlayResolveInput
	inputs  []cliprender.OverlayResolveInput
}

func (f *fakeOverlaySegmentResolver) Resolve(_ context.Context, in cliprender.OverlayResolveInput) (*cliprender.OverlaySegment, error) {
	f.calls++
	f.last = in
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.byKey != nil {
		return f.byKey[in.RenderKey], nil
	}
	return f.segment, nil
}

// reusedOverlayLineages returns the TWO overlays one scene composites: a reused
// overlay is one certified render per semantic item, and a single-slot model
// would silently drop the second.
func reusedOverlayLineages() []cliprender.OverlayRefSpec {
	return []cliprender.OverlayRefSpec{
		{
			RenderJobID:        "overlay-job-1",
			PlanFingerprint:    strings.Repeat("c", 64),
			RenderKey:          strings.Repeat("e", 64),
			SourceVideoAssetID: "source-1",
			StartUS:            2_000_000,
			EndUS:              5_500_000,
		},
		{
			RenderJobID:        "overlay-job-2",
			PlanFingerprint:    strings.Repeat("c", 64),
			RenderKey:          strings.Repeat("d", 64),
			SourceVideoAssetID: "source-1",
			StartUS:            6_000_000,
			EndUS:              8_000_000,
		},
	}
}

// TestLocalizationRenderPlanExecutor_SealsReusedOverlay pins that a declared
// overlay reaches the sealed ClipRenderPlanV1 with the resolved segment and the
// declared window in milliseconds — the overlay is composited in the SAME render
// pass instead of being dropped.
func TestLocalizationRenderPlanExecutor_SealsReusedOverlay(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath: outPath, SizeBytes: size, DurationSec: 8.432,
	}}
	segment := &cliprender.OverlaySegment{
		RenderJobID: "overlay-job-1",
		RenderKey:   strings.Repeat("e", 64),
		LocalPath:   filepath.Join(t.TempDir(), "overlay.mov"),
		SHA256:      strings.Repeat("f", 64),
		SizeBytes:   4096,
	}
	resolver := &fakeOverlaySegmentResolver{segment: segment}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop()).WithOverlayResolver(resolver)

	if _, err := adapter.ExecuteExtended(context.Background(), plan, appTestSubtitle(t), localization.RenderOptions{
		Overlays: reusedOverlayLineages()[:1],
	}); err != nil {
		t.Fatalf("ExecuteExtended: %v", err)
	}

	if resolver.calls != 1 {
		t.Fatalf("resolver calls: got %d, want 1", resolver.calls)
	}
	if resolver.last.RenderJobID != "overlay-job-1" || resolver.last.RenderKey != strings.Repeat("e", 64) {
		t.Fatalf("resolver input: %+v", resolver.last)
	}
	if exec.gotPlan.Overlay == nil || len(exec.gotPlan.Overlay.Segments) != 1 {
		t.Fatalf("the sealed clip plan must carry the reused overlay, got %+v", exec.gotPlan.Overlay)
	}
	got := exec.gotPlan.Overlay.Segments[0]
	if got.Path != segment.LocalPath || got.SHA256 != segment.SHA256 || got.RenderKey != segment.RenderKey {
		t.Fatalf("sealed segment: %+v", got)
	}
	if got.StartMS != 2000 || got.EndMS != 5500 {
		t.Fatalf("sealed window: got [%d,%d) ms, want [2000,5500)", got.StartMS, got.EndMS)
	}
}

// TestLocalizationRenderPlanExecutor_SealsEveryReusedOverlay pins the
// multi-item contract at the adapter boundary: a plan declaring N overlays
// resolves ALL N and seals ALL N with their own windows, so a final clip never
// ships with only its first overlay.
func TestLocalizationRenderPlanExecutor_SealsEveryReusedOverlay(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath: outPath, SizeBytes: size, DurationSec: 8.432,
	}}
	segmentPath := func(name string) string { return filepath.Join(t.TempDir(), name) }
	resolver := &fakeOverlaySegmentResolver{byKey: map[string]*cliprender.OverlaySegment{
		strings.Repeat("e", 64): {RenderJobID: "overlay-job-1", RenderKey: strings.Repeat("e", 64), LocalPath: segmentPath("one.mov"), SHA256: strings.Repeat("1", 64), SizeBytes: 4096},
		strings.Repeat("d", 64): {RenderJobID: "overlay-job-2", RenderKey: strings.Repeat("d", 64), LocalPath: segmentPath("two.mov"), SHA256: strings.Repeat("2", 64), SizeBytes: 2048},
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop()).WithOverlayResolver(resolver)

	if _, err := adapter.ExecuteExtended(context.Background(), plan, appTestSubtitle(t), localization.RenderOptions{
		Overlays: reusedOverlayLineages(),
	}); err != nil {
		t.Fatalf("ExecuteExtended: %v", err)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls: got %d, want one per declared overlay", resolver.calls)
	}
	overlay := exec.gotPlan.Overlay
	if overlay == nil || len(overlay.Segments) != 2 {
		t.Fatalf("sealed plan must carry both reused overlays, got %+v", overlay)
	}
	for i, want := range []struct {
		key            string
		sha            string
		startMS, endMS int64
	}{
		{strings.Repeat("e", 64), strings.Repeat("1", 64), 2000, 5500},
		{strings.Repeat("d", 64), strings.Repeat("2", 64), 6000, 8000},
	} {
		got := overlay.Segments[i]
		if got.RenderKey != want.key || got.SHA256 != want.sha || got.StartMS != want.startMS || got.EndMS != want.endMS {
			t.Fatalf("sealed segment %d = %+v, want key=%s window=[%d,%d)", i, got, want.key[:8], want.startMS, want.endMS)
		}
	}
}

// TestLocalizationRenderPlanExecutor_SharedOverlayAcrossLanguages pins the reuse
// at the render boundary: N language variants of one source hand the SAME
// render_key to the resolver, so the overlay is never re-rendered or re-hashed
// per language.
func TestLocalizationRenderPlanExecutor_SharedOverlayAcrossLanguages(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath: outPath, SizeBytes: size, DurationSec: 8.432,
	}}
	resolver := &fakeOverlaySegmentResolver{segment: &cliprender.OverlaySegment{
		RenderJobID: "overlay-job-1", RenderKey: strings.Repeat("e", 64),
		LocalPath: filepath.Join(t.TempDir(), "overlay.mov"), SHA256: strings.Repeat("f", 64), SizeBytes: 4096,
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop()).WithOverlayResolver(resolver)

	for i := 0; i < 3; i++ {
		if _, err := adapter.ExecuteExtended(context.Background(), plan, appTestSubtitle(t), localization.RenderOptions{
			Overlays: reusedOverlayLineages(),
		}); err != nil {
			t.Fatalf("variant %d: ExecuteExtended: %v", i, err)
		}
	}
	// Two declared overlays × three language variants: the resolver is called
	// once per (variant, overlay), and every variant hands in the SAME
	// render_keys — which is what lets the memoizing resolver hash each segment
	// once for the whole fan-out.
	if resolver.calls != 6 {
		t.Fatalf("resolver calls: got %d, want 2 overlays × 3 variants", resolver.calls)
	}
	seen := map[string]int{}
	for _, in := range resolver.inputs {
		seen[in.RenderKey]++
	}
	if seen[strings.Repeat("e", 64)] != 3 || seen[strings.Repeat("d", 64)] != 3 {
		t.Fatalf("every variant must resolve the same reused segments, got %+v", seen)
	}
	if exec.gotPlan.Overlay == nil || len(exec.gotPlan.Overlay.Segments) != 2 {
		t.Fatalf("every variant must seal both reused overlays, got %+v", exec.gotPlan.Overlay)
	}
	for i, seg := range exec.gotPlan.Overlay.Segments {
		if seg.Path == "" || seg.SHA256 == "" {
			t.Fatalf("sealed segment %d is incomplete: %+v", i, seg)
		}
	}
}

// TestLocalizationRenderPlanExecutor_OverlayFailsClosedWithoutResolver pins the
// fail-closed gate: an overlay that cannot be resolved is a typed error, never a
// clip that silently ships without it.
func TestLocalizationRenderPlanExecutor_OverlayFailsClosedWithoutResolver(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath: outPath, SizeBytes: size, DurationSec: 8.432,
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())

	if _, err := adapter.ExecuteExtended(context.Background(), plan, appTestSubtitle(t), localization.RenderOptions{
		Overlays: reusedOverlayLineages(),
	}); err == nil {
		t.Fatal("an overlay without a wired resolver must fail closed")
	}
	if exec.submits != 0 {
		t.Fatalf("no render may be submitted, got %d submits", exec.submits)
	}
}

// TestLocalizationRenderPlanExecutor_OverlayResolveFailureFailsClosed pins that
// an unresolvable segment (unknown render_key / unreadable artifact) aborts
// before any work reaches the renderer.
func TestLocalizationRenderPlanExecutor_OverlayResolveFailureFailsClosed(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath: outPath, SizeBytes: size, DurationSec: 8.432,
	}}
	resolver := &fakeOverlaySegmentResolver{err: errors.New("no cached artifact for render_key")}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop()).WithOverlayResolver(resolver)

	if _, err := adapter.ExecuteExtended(context.Background(), plan, appTestSubtitle(t), localization.RenderOptions{
		Overlays: reusedOverlayLineages(),
	}); err == nil {
		t.Fatal("an unresolvable overlay must fail closed")
	}
	if exec.submits != 0 {
		t.Fatalf("no render may be submitted, got %d submits", exec.submits)
	}
}

// TestLocalizationRenderPlanExecutor_IncompleteOverlayLineageFailsClosed pins the
// all-or-nothing lineage gate at the adapter boundary too.
func TestLocalizationRenderPlanExecutor_IncompleteOverlayLineageFailsClosed(t *testing.T) {
	plan, outPath, _, size := localizedRenderFixture(t)
	exec := &fakeLocalizationRenderExecutor{outcome: &cliprender.RenderOutcome{
		OutputPath: outPath, SizeBytes: size, DurationSec: 8.432,
	}}
	resolver := &fakeOverlaySegmentResolver{segment: &cliprender.OverlaySegment{
		RenderJobID: "overlay-job-1", RenderKey: strings.Repeat("e", 64),
		LocalPath: "/tmp/overlay.mov", SHA256: strings.Repeat("f", 64), SizeBytes: 4096,
	}}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop()).WithOverlayResolver(resolver)

	partial := reusedOverlayLineages()
	partial[0].RenderKey = ""
	if _, err := adapter.ExecuteExtended(context.Background(), plan, appTestSubtitle(t), localization.RenderOptions{
		Overlays: partial,
	}); err == nil {
		t.Fatal("a partial overlay lineage must fail closed")
	}
	if resolver.calls != 0 {
		t.Fatalf("the resolver must not be called for a partial lineage, got %d", resolver.calls)
	}
	if exec.submits != 0 {
		t.Fatalf("no render may be submitted, got %d submits", exec.submits)
	}
}
