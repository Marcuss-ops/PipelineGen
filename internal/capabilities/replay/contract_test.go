package replay_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
)

func hash64(c byte) string { return strings.Repeat(string(c), 64) }

func testTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 18000000,
		Segments: []audio.TimelineSegment{
			{ID: "a", Index: 0, TimelineStartUS: 0, DurationUS: 5600000, Video: audio.VideoSegment{AssetID: "clip-a", SourceInUS: 33200000, SourceDurationUS: 5600000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
			{ID: "b", Index: 1, TimelineStartUS: 5600000, DurationUS: 12400000, Video: audio.VideoSegment{AssetID: "clip-b", SourceInUS: 7100000, SourceDurationUS: 12400000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
		},
	}
}

func testPlan(t *testing.T) render.RenderPlan {
	t.Helper()
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30,
		Timeline: testTimeline(),
		Manifest: []render.AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testBundle(t *testing.T) replay.ReplayBundle {
	t.Helper()
	plan := testPlan(t)
	return replay.ReplayBundle{
		Version:             replay.BundleVersion,
		OriginalJobID:       "job-1",
		RenderPlan:          plan,
		PlanSHA256:          plan.PlanSHA256,
		RendererVersion:     "rust-render/v3",
		RustProtocolVersion: "1.4",
		FFmpegVersion:       "6.1",
		EncoderPolicyHash:   hash64('e'),
		Assets:              replay.BuildAssets(plan),
		CreatedAt:           time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}
}

func TestBundleValidates(t *testing.T) {
	if err := testBundle(t).Validate(); err != nil {
		t.Fatalf("valid bundle must validate: %v", err)
	}
}

func TestBuildAssetsDropsLocalPathsAndDeduplicates(t *testing.T) {
	plan := testPlan(t)
	assets := replay.BuildAssets(plan)
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d: %+v", len(assets), assets)
	}
	want := map[string]string{"clip-a": hash64('a'), "clip-b": hash64('b')}
	for _, asset := range assets {
		if asset.SHA256 != want[asset.AssetID] {
			t.Fatalf("asset %s sha mismatch: %s", asset.AssetID, asset.SHA256)
		}
		if asset.CASURI != replay.CanonicalCASURI(asset.SHA256) {
			t.Fatalf("asset %s must carry a canonical CAS URI, got %q", asset.AssetID, asset.CASURI)
		}
		if strings.Contains(asset.CASURI, "/tmp") {
			t.Fatal("replay assets must never carry local paths")
		}
	}
}

func TestBuildAssetsIncludesFinalAudio(t *testing.T) {
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30,
		Timeline: testTimeline(),
		Manifest: []render.AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
		},
		FinalAudio: &render.FinalAudioAsset{AssetID: "final", Path: "/tmp/final.m4a", SHA256: hash64('f'), SizeBytes: 1234},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := replay.BuildAssets(plan)
	if len(assets) != 3 {
		t.Fatalf("expected 3 assets (manifest + final audio), got %d", len(assets))
	}
	found := false
	for _, asset := range assets {
		if asset.AssetID == "final" {
			found = true
			if asset.SHA256 != hash64('f') || asset.SizeBytes != 1234 {
				t.Fatalf("final audio asset mismatch: %+v", asset)
			}
		}
	}
	if !found {
		t.Fatal("final audio asset missing from BuildAssets")
	}
}

func TestBundleRejectsMismatchedPlanSHA(t *testing.T) {
	bundle := testBundle(t)
	bundle.PlanSHA256 = hash64('9')
	if err := bundle.Validate(); err == nil {
		t.Fatal("plan_sha256 mismatch must be rejected")
	}
}

func TestBundleRejectsMissingEnvironment(t *testing.T) {
	cases := map[string]func(*replay.ReplayBundle){
		"renderer version":   func(b *replay.ReplayBundle) { b.RendererVersion = "" },
		"rust protocol":      func(b *replay.ReplayBundle) { b.RustProtocolVersion = "" },
		"ffmpeg version":     func(b *replay.ReplayBundle) { b.FFmpegVersion = "" },
		"bad encoder policy": func(b *replay.ReplayBundle) { b.EncoderPolicyHash = "not-a-hash" },
		"bad original job":   func(b *replay.ReplayBundle) { b.OriginalJobID = "" },
		"missing created_at": func(b *replay.ReplayBundle) { b.CreatedAt = time.Time{} },
	}
	for name, mutate := range cases {
		bundle := testBundle(t)
		mutate(&bundle)
		if err := bundle.Validate(); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestBundleRejectsInvalidAsset(t *testing.T) {
	bundle := testBundle(t)
	bundle.Assets[0].CASURI = ""
	if err := bundle.Validate(); err == nil {
		t.Fatal("asset without CAS URI must be rejected")
	}
	bundle = testBundle(t)
	bundle.Assets[0].SHA256 = "short"
	if err := bundle.Validate(); err == nil {
		t.Fatal("asset with malformed sha256 must be rejected")
	}
	bundle = testBundle(t)
	bundle.Assets[0].SizeBytes = -1
	if err := bundle.Validate(); err == nil {
		t.Fatal("negative size must be rejected")
	}
}
