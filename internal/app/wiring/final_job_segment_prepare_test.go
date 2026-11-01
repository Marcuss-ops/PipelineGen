package wiring

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func TestNVENCPresetForMapsX264PresetNames(t *testing.T) {
	cases := map[string]string{
		"ultrafast": "p2", "superfast": "p2", "veryfast": "p2", "faster": "p2",
		"fast": "p4", "medium": "p4", "slow": "p4",
		"slower": "p6", "veryslow": "p6", "p5": "p5",
	}
	for input, want := range cases {
		if got := nvencPresetFor(input); got != want {
			t.Errorf("nvencPresetFor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFinalJobSegmentPreparerEncodesOneAssemblyReadyFragment(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "2",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0000", SourceAssetID: "source-1", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_000_000, TimelineStartFrame: 0, FrameCount: 48,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request, true); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}

// TestFinalJobSegmentPreparerSkipsRebuildingTheDeliveryGeometry proves the fast
// path is still a CERTIFIED fragment: a source that already is 1920x1080/24/
// yuv420p is only cut and capped, without the fps/scale/pad/setsar/format chain
// that would resample the same pixels, and the result passes the same fail
// closed certificate as the normalized path.
func TestFinalJobSegmentPreparerSkipsRebuildingTheDeliveryGeometry(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "delivery-source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-t", "2",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-r", "24", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make delivery source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	needs, err := preparer.sourceNeedsNormalization(context.Background(), source, strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("sourceNeedsNormalization: %v", err)
	}
	if needs {
		t.Fatal("sourceNeedsNormalization = true for a 1920x1080/24/yuv420p source")
	}
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-delivery", SourceAssetID: "source-delivery", SourceSHA256: "sha",
		SourceInUS: 500_000, SourceDurationUS: 1_500_000, TimelineStartFrame: 12, FrameCount: 36,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request, needs); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}

func TestFinalJobSegmentPreparerPadsShortBoundaryToExactFrameCount(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "short-source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-frames:v", "65",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make short source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-boundary", SourceAssetID: "source-short", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_750_000, TimelineStartFrame: 65, FrameCount: 66,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request, true); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}

// TestVerifyFinalJobFragmentRejectsAnOpenGOP pins the certificate on the packet
// flags: the check must still refuse a fragment whose keyframes are not the
// fixed closed-GOP layout, even though it no longer decodes the frames.
func TestVerifyFinalJobFragmentRejectsAnOpenGOP(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "broken.mp4")
	// 72-frame GOP instead of the certified 48.
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-frames:v", "96",
		"-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-r", "24",
		"-g", "72", "-keyint_min", "72", "-sc_threshold", "0", "-bf", "0", "-flags", "+cgop",
		"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart", output,
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("make open-GOP fixture: %v: %s", runErr, out)
	}
	if err := verifyFinalJobFragment(context.Background(), output, 96); err == nil {
		t.Fatal("verifyFinalJobFragment = nil error, want the open GOP refused")
	}
}

func TestFinalJobFragmentCacheReusesTheSameWorkOnly(t *testing.T) {
	dir := t.TempDir()
	policy := mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}
	preparer := newFinalJobSegmentPreparer("", policy, dir, 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0003", SourceAssetID: "source-1", SourceSHA256: "sha-source",
		SourceInUS: 1_500_000, SourceDurationUS: 3_000_000, TimelineStartFrame: 36, FrameCount: 72,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	sourceSHA := strings.Repeat("a", 64)
	key := preparer.fragmentCacheKey(request, sourceSHA)
	if _, _, _, ok := preparer.cachedFragment(key, request.FrameCount, 3_000_000); ok {
		t.Fatal("cache hit on an empty cache")
	}

	path := filepath.Join(dir, "fragment.mp4")
	body := []byte("certified fragment bytes")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	sha := digest.SHA256String(string(body))
	preparer.storeFragment(key, path, sha, int64(len(body)), request.FrameCount, 3_000_000)
	cachedPath, cachedSHA, cachedSize, ok := preparer.cachedFragment(key, request.FrameCount, 3_000_000)
	if !ok || cachedSHA != sha || cachedSize != int64(len(body)) || cachedPath == "" {
		t.Fatalf("cache hit = %q %q %d %v, want the stored fragment back", cachedPath, cachedSHA, cachedSize, ok)
	}

	// The key is the WORK identity: the same window of the same bytes is the
	// same fragment even when it lands at another timeline position or in
	// another revision — that is what makes a re-run cheap.
	moved := request
	moved.SegmentID = "segment-0009"
	moved.TimelineStartFrame = 240
	moved.TimelineRevision = 7
	moved.TimelineSHA256 = strings.Repeat("f", 64)
	if movedKey := preparer.fragmentCacheKey(moved, sourceSHA); movedKey != key {
		t.Fatalf("cache key moved with the timeline (%s != %s), want work identity only", movedKey, key)
	}
	// A different window, a different source or a different encoder policy is a
	// different fragment.
	other := request
	other.SourceInUS = 2_000_000
	if otherKey := preparer.fragmentCacheKey(other, sourceSHA); otherKey == key {
		t.Fatal("cache key ignored the source in-point")
	}
	otherPolicy := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{Codec: "libx264", Preset: "medium", CRF: 20}, dir, 1)
	if otherKey := otherPolicy.fragmentCacheKey(request, sourceSHA); otherKey == key {
		t.Fatal("cache key ignored the encoder policy")
	}
	// Wrong frame count is a miss, not a guess.
	if _, _, _, ok := preparer.cachedFragment(key, request.FrameCount+1, 3_000_000); ok {
		t.Fatal("cache hit with the wrong frame count")
	}
	// Corrupted cache bytes are a miss: the cache can never certify itself.
	if err := os.WriteFile(cachedPath, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper cached fragment: %v", err)
	}
	if tamperedPath, _, _, ok := preparer.cachedFragment(key, request.FrameCount, 3_000_000); ok {
		t.Fatalf("cache hit on tampered bytes (%q), want a miss", tamperedPath)
	}
}
