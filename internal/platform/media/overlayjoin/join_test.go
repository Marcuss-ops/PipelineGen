package overlayjoin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func touch(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func validSpec(t *testing.T) Spec {
	t.Helper()
	dir := t.TempDir()
	return Spec{
		BackgroundPath: touch(t, dir, "bg.mp4"),
		AudioPath:      touch(t, dir, "final_audio.m4a"),
		OutputPath:     filepath.Join(dir, "final_video.mp4"),
		Width:          1920,
		Height:         1080,
		FPS:            24,
		Encoder:        mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		DurationUS:     77_520_000,
		Clips: []Clip{
			{Path: touch(t, dir, "item-0.mp4"), StartUS: 0, DurationUS: 13_291_667},
			{Path: touch(t, dir, "item-1.mp4"), StartUS: 13_291_667, DurationUS: 5_000_000},
			{Path: touch(t, dir, "item-2.mp4"), StartUS: 25_392_000, DurationUS: 10_083_333},
		},
	}
}

func TestArgsPlacesEveryClipOnItsPlanWindow(t *testing.T) {
	spec := validSpec(t)
	args, err := Args(spec)
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	graph := args[indexOf(args, "-filter_complex")+1]
	for _, want := range []string{
		"scale=1920:1080",
		"fps=24",
		"setpts=PTS-STARTPTS+0.000000/TB",
		"setpts=PTS-STARTPTS+13.291667/TB",
		"enable='between(t,0.000000,13.291667)'",
		"enable='between(t,25.392000,35.475333)'",
		"overlay=0:0:format=auto:eof_action=pass",
	} {
		if !strings.Contains(graph, want) {
			t.Fatalf("filtergraph missing %q:\n%s", want, graph)
		}
	}
	if got := args[indexOf(args, "-map")+1]; got != "[v2]" {
		t.Fatalf("last overlay output not mapped: %q", got)
	}
	if got := args[indexOf(args, "-t")+1]; got != "77.520" {
		t.Fatalf("timeline duration not bounded: %q", got)
	}
}

func TestArgsWithoutBackgroundUsesBlackCanvas(t *testing.T) {
	spec := validSpec(t)
	spec.BackgroundPath = ""
	args, err := Args(spec)
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "color=c=black:s=1920x1080:r=24") {
		t.Fatalf("black canvas missing: %v", args)
	}
}

func TestArgsRequiresCertifiedAudio(t *testing.T) {
	spec := validSpec(t)
	spec.AudioPath = ""
	if _, err := Args(spec); err == nil {
		t.Fatal("expected a missing-audio error")
	}
}

func TestArgsRequiresResolvedEncoder(t *testing.T) {
	spec := validSpec(t)
	spec.Encoder = mediaexec.EncoderPolicy{}
	if _, err := Args(spec); err == nil {
		t.Fatal("expected a missing-encoder-policy error")
	}
}

// TestArgsUsesTheCertifiedVideoRecipe pins the delivery lane to the same avcC
// identity as stock and clip.render. Profile/level, refs, B-frames, GOP,
// timescale and the bt709/tv VUI are all part of the emitted parameter sets.
func TestArgsUsesTheCertifiedVideoRecipe(t *testing.T) {
	spec := validSpec(t)
	args, err := Args(spec)
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-c:v libx264",
		"-preset veryfast",
		"-crf 23",
		"-refs 1",
		"-profile:v high",
		"-level:v 4.0",
		"-pix_fmt yuv420p",
		"-g 48",
		"-bf 0",
		"-colorspace bt709",
		"-color_primaries bt709",
		"-color_trc bt709",
		"-color_range tv",
		"-video_track_timescale 90000",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("canonical join args are missing %q:\n%s", want, joined)
		}
	}
}

func TestArgsRejectsANonCanonicalEncoder(t *testing.T) {
	spec := validSpec(t)
	spec.Encoder = mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}
	if _, err := Args(spec); err == nil {
		t.Fatal("a non-canonical encoder must be rejected")
	}
}

func TestArgsRejectsMissingClip(t *testing.T) {
	spec := validSpec(t)
	spec.Clips[1].Path = filepath.Join(t.TempDir(), "absent.mp4")
	if _, err := Args(spec); err == nil {
		t.Fatal("expected a missing-clip error")
	}
}

func TestRunAssemblesARealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	bg := filepath.Join(dir, "bg.mp4")
	clip := filepath.Join(dir, "clip.mp4")
	audio := filepath.Join(dir, "audio.m4a")
	mustFFmpeg(t, "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=24:d=3", "-pix_fmt", "yuv420p", bg)
	mustFFmpeg(t, "-f", "lavfi", "-i", "color=c=red:s=320x180:r=24:d=1", "-pix_fmt", "yuv420p", clip)
	mustFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "aac", audio)
	out := filepath.Join(dir, "final.mp4")
	result, err := Run(context.Background(), Spec{
		BackgroundPath: bg, AudioPath: audio, OutputPath: out,
		Width: 320, Height: 180, FPS: 24, DurationUS: 3_000_000,
		Encoder: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		Clips:   []Clip{{Path: clip, StartUS: 500_000, DurationUS: 1_000_000}},
	}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.SizeBytes == 0 {
		t.Fatal("empty artifact")
	}
	if info, err := os.Stat(out); err != nil || info.Size() != result.SizeBytes {
		t.Fatalf("artifact not on disk: %v", err)
	}
}

// TestRunEmitsTheCanonicalAVCC is the end-to-end proof for the local overlay
// deliverable: a 1080p join must carry the same decoder configuration as stock
// and clip.render, not a generic x264 SPS/PPS with B-frames.
func TestRunEmitsTheCanonicalAVCC(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not available", binary)
		}
	}
	dir := t.TempDir()
	bg := filepath.Join(dir, "bg.mp4")
	clip := filepath.Join(dir, "clip.mp4")
	audio := filepath.Join(dir, "audio.m4a")
	mustFFmpeg(t, "-f", "lavfi", "-i", "color=c=blue:s=1920x1080:r=24:d=3", "-pix_fmt", "yuv420p", bg)
	mustFFmpeg(t, "-f", "lavfi", "-i", "color=c=red:s=1920x1080:r=24:d=1", "-pix_fmt", "yuv420p", clip)
	mustFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "aac", audio)
	out := filepath.Join(dir, "final.mp4")
	_, err := Run(context.Background(), Spec{
		BackgroundPath: bg, AudioPath: audio, OutputPath: out,
		Width: 1920, Height: 1080, FPS: 24, DurationUS: 3_000_000,
		Encoder: mediaexec.EncoderPolicy{Codec: kernelmedia.CanonicalVideoCodec, Preset: kernelmedia.CanonicalVideoPreset, CRF: kernelmedia.CanonicalVideoCRF},
		Clips:   []Clip{{Path: clip, StartUS: 500_000, DurationUS: 1_000_000}},
	}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := avccSHA256(t, out); got != kernelmedia.CanonicalVideoExtradataSHA256 {
		t.Fatalf("joined avcC = %s, want %s", got, kernelmedia.CanonicalVideoExtradataSHA256)
	}
}

func avccSHA256(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=extradata", "-show_data", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("probe extradata: %v", err)
	}
	var doc struct {
		Streams []struct {
			Extradata string `json:"extradata"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || len(doc.Streams) == 0 {
		t.Fatalf("decode extradata: %v", err)
	}
	var raw []byte
	for _, line := range strings.Split(doc.Streams[0].Extradata, "\n") {
		_, payload, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		payload = strings.TrimLeft(payload, " ")
		if ascii := strings.Index(payload, "  "); ascii >= 0 {
			payload = payload[:ascii]
		}
		for _, word := range strings.Fields(payload) {
			if len(word) != 2 && len(word) != 4 {
				t.Fatalf("malformed extradata word %q", word)
			}
			decoded, err := hex.DecodeString(word)
			if err != nil {
				t.Fatalf("decode extradata word %q: %v", word, err)
			}
			raw = append(raw, decoded...)
		}
	}
	if len(raw) == 0 {
		t.Fatal("no extradata in probe")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mustFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
	}
}

func indexOf(args []string, flag string) int {
	for i, arg := range args {
		if arg == flag {
			return i
		}
	}
	return -1
}
