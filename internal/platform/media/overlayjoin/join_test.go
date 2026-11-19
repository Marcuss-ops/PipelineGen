package overlayjoin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		Clips: []Clip{{Path: clip, StartUS: 500_000, DurationUS: 1_000_000}},
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
