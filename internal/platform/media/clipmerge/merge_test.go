package clipmerge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

// testEncoder is the resolved policy a caller would pass in; the package itself
// must never name an encoder.
func testEncoder() media.EncoderPolicy {
	return media.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 20}
}

// requireFFmpeg skips the test when the local toolchain has no ffmpeg.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not installed", binary)
		}
	}
}

// synthClip writes a real H.264/AAC clip of the requested length so the merge
// exercises the same codecs the delivery contract guarantees.
func synthClip(t *testing.T, dir, name, color string, seconds float64) string {
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

// probeSeconds returns the container duration in seconds.
func probeSeconds(t *testing.T, path string) float64 {
	t.Helper()
	output, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		t.Fatalf("parse duration %q: %v", output, err)
	}
	return seconds
}

// probeStreams returns the stream codec types present in the file.
func probeStreams(t *testing.T, path string) []string {
	t.Helper()
	output, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("probe streams %s: %v", path, err)
	}
	return strings.Fields(string(output))
}

// TestMergeFusesOnlyTransitionLinkedGroups is the end-to-end proof of the rule:
// three delivered clips, a transition only between the first two, produce TWO
// artifacts — a single merged file for [a,b] and the untouched clip c.
func TestMergeFusesOnlyTransitionLinkedGroups(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	a := synthClip(t, dir, "a", "red", 1.0)
	b := synthClip(t, dir, "b", "blue", 1.0)
	c := synthClip(t, dir, "c", "green", 1.0)
	sequence := clipsequence.Sequence{Clips: []clipsequence.Clip{
		{AssetID: "a", Path: a, DurationMS: 1_000},
		{AssetID: "b", Path: b, DurationMS: 1_000, TransitionIn: &clipsequence.Transition{Preset: clipsequence.PresetFlashWhite, DurationMS: 200}},
		{AssetID: "c", Path: c, DurationMS: 1_000},
	}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %+v", plan.Groups)
	}
	artifacts, err := Merge(context.Background(), sequence, plan, Options{OutputDir: filepath.Join(dir, "out"), Encoder: testEncoder()})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %+v", artifacts)
	}
	if !artifacts[0].Merged || artifacts[1].Merged {
		t.Fatalf("merge flags = %v/%v, want true/false", artifacts[0].Merged, artifacts[1].Merged)
	}
	// 1.0s + 1.0s - 0.2s overlap = 1.8s
	if got := probeSeconds(t, artifacts[0].Path); got < 1.6 || got > 2.0 {
		t.Fatalf("merged duration = %.2fs, want ~1.8s", got)
	}
	// The merged artifact keeps both tracks: the assembler must not drop audio.
	streams := probeStreams(t, artifacts[0].Path)
	if len(streams) != 2 {
		t.Fatalf("merged streams = %v, want video+audio", streams)
	}
	// The standalone clip is copied byte for byte: no needless re-encode.
	if got := probeSeconds(t, artifacts[1].Path); got < 0.9 || got > 1.1 {
		t.Fatalf("standalone duration = %.2fs, want ~1.0s", got)
	}
	original, err := os.ReadFile(c)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(artifacts[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(original) != len(copied) {
		t.Fatalf("standalone clip was re-encoded (%d vs %d bytes)", len(copied), len(original))
	}
}

// TestMergeChainsMoreThanTwoClips exercises the second join: after the first
// xfade the offset is measured on the already-shortened output, so a wrong
// running length would truncate or freeze the third clip.
func TestMergeChainsMoreThanTwoClips(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	a := synthClip(t, dir, "a", "red", 1.0)
	b := synthClip(t, dir, "b", "blue", 1.0)
	c := synthClip(t, dir, "c", "green", 1.0)
	sequence := clipsequence.Sequence{Clips: []clipsequence.Clip{
		{AssetID: "a", Path: a, DurationMS: 1_000},
		{AssetID: "b", Path: b, DurationMS: 1_000, TransitionIn: &clipsequence.Transition{Preset: clipsequence.PresetWhipLeft, DurationMS: 200}},
		{AssetID: "c", Path: c, DurationMS: 1_000, TransitionIn: &clipsequence.Transition{Preset: clipsequence.PresetZoomPunch, DurationMS: 200}},
	}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Groups) != 1 || len(plan.Groups[0].Transitions) != 2 {
		t.Fatalf("plan = %+v, want one group with two transitions", plan)
	}
	artifacts, err := Merge(context.Background(), sequence, plan, Options{OutputDir: filepath.Join(dir, "out"), Encoder: testEncoder()})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(artifacts) != 1 || !artifacts[0].Merged {
		t.Fatalf("artifacts = %+v", artifacts)
	}
	// 3.0s - 0.2s - 0.2s = 2.6s
	if got := probeSeconds(t, artifacts[0].Path); got < 2.4 || got > 2.8 {
		t.Fatalf("three-clip merged duration = %.2fs, want ~2.6s", got)
	}
}

// TestMergeRefusesPresetTheLocalRendererCannotReproduce pins the honesty rule:
// a light leak is Chronon's look, so the local assembler refuses the group and
// names the renderer kind instead of substituting a lookalike.
func TestMergeRefusesPresetTheLocalRendererCannotReproduce(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	a := synthClip(t, dir, "a", "red", 1.0)
	b := synthClip(t, dir, "b", "blue", 1.0)
	sequence := clipsequence.Sequence{Clips: []clipsequence.Clip{
		{AssetID: "a", Path: a, DurationMS: 1_000},
		{AssetID: "b", Path: b, DurationMS: 1_000, TransitionIn: &clipsequence.Transition{Preset: clipsequence.PresetLightLeakSoft}},
	}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err = Merge(context.Background(), sequence, plan, Options{OutputDir: filepath.Join(dir, "out"), Encoder: testEncoder()})
	if !errors.Is(err, ErrPresetNotExecutable) {
		t.Fatalf("error = %v, want ErrPresetNotExecutable", err)
	}
	if !strings.Contains(err.Error(), `"LightLeak"`) {
		t.Fatalf("error must name the owning renderer kind: %v", err)
	}
}

// TestMergeRequiresArtifactPaths pins the fail-closed input contract: a plan
// computed before materialization cannot be executed.
func TestMergeRequiresArtifactPaths(t *testing.T) {
	sequence := clipsequence.Sequence{Clips: []clipsequence.Clip{{AssetID: "a", DurationMS: 1_000}}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err = Merge(context.Background(), sequence, plan, Options{OutputDir: t.TempDir(), Encoder: testEncoder()})
	if err == nil || !strings.Contains(err.Error(), "no artifact path") {
		t.Fatalf("error = %v", err)
	}
}

// TestMergeRequiresEncoderPolicy pins the gate contract: the assembler must not
// choose an encoder, so an unresolved policy fails closed instead of falling
// back to a built-in default.
func TestMergeRequiresEncoderPolicy(t *testing.T) {
	sequence := clipsequence.Sequence{Clips: []clipsequence.Clip{{AssetID: "a", DurationMS: 1_000}}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err = Merge(context.Background(), sequence, plan, Options{OutputDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "encoder policy codec is required") {
		t.Fatalf("error = %v", err)
	}
}
