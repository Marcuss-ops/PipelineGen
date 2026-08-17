package render

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// testFS is the real-filesystem adapter used by the render tests. It
// mirrors internal/platform/filesystem.OS but lives here so the
// capability package does not import the platform adapter (which would
// invert the adapter→contract dependency direction).
type testFS struct{}

func (testFS) Open(path string) (io.ReadCloser, error) { return os.Open(path) }

func (testFS) Size(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func testRenderTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 18000000,
		Segments: []audio.TimelineSegment{
			{ID: "a", Index: 0, TimelineStartUS: 0, DurationUS: 5600000, Video: audio.VideoSegment{AssetID: "clip-a", SourceInUS: 33200000, SourceDurationUS: 5600000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
			{ID: "b", Index: 1, TimelineStartUS: 5600000, DurationUS: 12400000, Video: audio.VideoSegment{AssetID: "clip-b", SourceInUS: 7100000, SourceDurationUS: 12400000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
		},
	}
}

func TestCompileUsesIntegerFramesForSourceAndTimeline(t *testing.T) {
	plan, err := Compile(CompileInput{
		JobID:      "job-1",
		Revision:   "rev-1",
		OutputPath: "final.mp4",
		FPS:        30,
		Timeline:   testRenderTimeline(),
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FrameCount: 1000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.VideoTracks[0].Segments[0]; got.Timeline.StartFrame != 0 || got.Timeline.FrameCount != 168 || got.Source.InFrame != 996 || got.Source.FrameCount != 168 {
		t.Fatalf("unexpected first frame range: %+v", got)
	}
	if got := plan.VideoTracks[0].Segments[1]; got.Timeline.StartFrame != 168 || got.Timeline.FrameCount != 372 || got.Source.InFrame != 213 || got.Source.FrameCount != 372 {
		t.Fatalf("unexpected second frame range: %+v", got)
	}
	if plan.TimelineHash == "" || plan.ManifestSHA256 == "" || plan.PlanSHA256 == "" {
		t.Fatal("sealed plan must contain all hashes")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan did not validate: %v", err)
	}
}

func TestCompileUsesAllVideoSegmentsOwnedByTimelineScene(t *testing.T) {
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: 5_000_000, Segments: []audio.TimelineSegment{{
		ID: "scene", Index: 0, DurationUS: 5_000_000,
		VideoSegments: []audio.VideoSegment{
			{AssetID: "clip-a", SourceDurationUS: 2_000_000, TimelineDurationUS: 2_000_000},
			{AssetID: "clip-b", SourceDurationUS: 3_000_000, TimelineOffsetUS: 2_000_000, TimelineDurationUS: 3_000_000},
		},
		Audio: audio.AudioIntent{Mode: audio.AudioSilence},
	}}}
	plan, err := Compile(CompileInput{JobID: "multi", Revision: "r1", OutputPath: "final.mp4", FPS: 30, Timeline: timeline, Manifest: []AssetManifestEntry{
		{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FrameCount: 100},
		{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FrameCount: 100},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.VideoTracks[0].Segments) != 2 || plan.VideoTracks[0].Segments[0].AssetID != "clip-a" || plan.VideoTracks[0].Segments[1].AssetID != "clip-b" {
		t.Fatalf("render plan lost multi-clip order: %+v", plan.VideoTracks[0].Segments)
	}
}

func TestCompileUsesRationalFrameRate(t *testing.T) {
	plan, err := Compile(CompileInput{
		JobID: "job-rational", Revision: "rev-1", OutputPath: "final.mp4",
		FrameRate: audio.FrameRate{Numerator: 30000, Denominator: 1001},
		Timeline:  testRenderTimeline(),
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FrameCount: 1000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.FPSNumerator != 30000 || plan.FPSDenominator != 1001 || plan.FPS != 30 {
		t.Fatalf("rational frame rate was not preserved: %+v", plan)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("rational plan did not validate: %v", err)
	}
}

func TestCompileMapsFreezeTailToSingleFrozenSourceFrame(t *testing.T) {
	// A clip (16s) shorter than its narration (17.52s) freezes on its final
	// frame: the freeze tail stretches one source frame across the remaining
	// destination frames instead of inventing a black gap in the renderer.
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 17_520_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene", Index: 0, TimelineStartUS: 0, DurationUS: 17_520_000,
			VideoSegments: []audio.VideoSegment{
				{AssetID: "clip-a", SourceInUS: 0, SourceDurationUS: 16_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 16_000_000},
				{AssetID: "clip-a", SourceInUS: 16_000_000, TimelineOffsetUS: 16_000_000, TimelineDurationUS: 1_520_000, Freeze: true},
			},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	plan, err := Compile(CompileInput{
		JobID: "job-freeze", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30,
		Timeline: timeline,
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FrameCount: 480},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	segments := plan.VideoTracks[0].Segments
	if len(segments) != 2 {
		t.Fatalf("render plan must carry real clip + freeze tail, got %+v", segments)
	}
	real := segments[0]
	if real.Freeze || real.Source != (RenderSource{InFrame: 0, FrameCount: 480}) || real.Timeline.FrameCount != 480 {
		t.Fatalf("real clip segment = %+v, want 480 real frames", real)
	}
	freeze := segments[1]
	if !freeze.Freeze || freeze.Source.FrameCount != 1 || freeze.Source.InFrame != 479 || freeze.Timeline.StartFrame != 480 || freeze.Timeline.FrameCount != 46 {
		t.Fatalf("freeze tail = %+v, want 1 frozen source frame (479) stretched over 46 destination frames from frame 480", freeze)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("freeze-tail plan did not validate: %v", err)
	}
}

func TestCompileRejectsSourceRangeBeyondAssetFrameCount(t *testing.T) {
	_, err := Compile(CompileInput{
		JobID: "job-range", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30,
		Timeline: testRenderTimeline(),
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FrameCount: 1000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FrameCount: 1000},
		},
	})
	if err == nil {
		t.Fatal("source frame range beyond asset frame_count must be rejected")
	}
}

func TestRenderPlanRejectsHashTampering(t *testing.T) {
	manifest := []AssetManifestEntry{{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FrameCount: 2000}, {AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FrameCount: 1000}}
	plan, err := Compile(CompileInput{JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30, Timeline: testRenderTimeline(), Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	plan.Timeline.Segments[0].DurationUS++
	if err := plan.Validate(); err == nil {
		t.Fatal("timeline mutation must be rejected")
	}
	plan, err = Compile(CompileInput{JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30, Timeline: testRenderTimeline(), Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	plan.ManifestSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := plan.Validate(); err == nil {
		t.Fatal("manifest hash mutation must be rejected")
	}
}

func TestRenderPlanValidatesManifestBytes(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/clip.mp4"
	contents := []byte("canonical clip bytes")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	plan, err := Compile(CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30,
		Timeline: testRenderTimeline(),
		Manifest: []AssetManifestEntry{{AssetID: "clip-a", Path: path, SHA256: hex.EncodeToString(sum[:]), FrameCount: 2000}, {AssetID: "clip-b", Path: path, SHA256: hex.EncodeToString(sum[:]), FrameCount: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateManifestFiles(testFS{}); err != nil {
		t.Fatalf("matching manifest should validate: %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateManifestFiles(testFS{}); err == nil {
		t.Fatal("tampered manifest bytes must be rejected")
	}
}
