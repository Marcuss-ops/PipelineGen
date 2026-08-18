package multilingual

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenderer_ValidateContract locks the per-clip output contract checks:
// streams, duration tolerance, resolution, codec, fps, burn-in presence and
// non-empty file. validate is called directly with a synthetic probeResult so
// each failing dimension is tested in isolation without spawning ffmpeg.
func TestRenderer_ValidateContract(t *testing.T) {
	dir := t.TempDir()
	assPath := writeTempFile(t, dir, "subs.ass", validASSForTest("it"))
	emptyAssPath := writeTempFile(t, dir, "empty.ass", "[Script Info]\nScriptType: v4.00+\n")
	outPath := writeTempFile(t, dir, "out.mp4", "video-bytes")

	validProbe := &probeResult{
		HasVideo: true, HasAudio: true, DurationMs: 5000,
		Width: renderWidth, Height: renderHeight, FPS: 30, VideoCodec: renderVideoCodec,
	}
	validInput := func() VariantInput {
		return VariantInput{
			SourceDuration: 5 * time.Second,
			SourceFPS:      30,
			ASSPath:        assPath,
		}
	}

	cases := []struct {
		name    string
		probe   *probeResult
		input   VariantInput
		path    string
		wantErr bool
	}{
		{"valid contract", validProbe, validInput(), outPath, false},
		{"no video stream", &probeResult{HasVideo: false, HasAudio: true, DurationMs: 5000, Width: renderWidth, Height: renderHeight, FPS: 30, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"no audio stream", &probeResult{HasVideo: true, HasAudio: false, DurationMs: 5000, Width: renderWidth, Height: renderHeight, FPS: 30, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"zero duration", &probeResult{HasVideo: true, HasAudio: true, DurationMs: 0, Width: renderWidth, Height: renderHeight, FPS: 30, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"duration drift", &probeResult{HasVideo: true, HasAudio: true, DurationMs: 6200, Width: renderWidth, Height: renderHeight, FPS: 30, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"wrong resolution", &probeResult{HasVideo: true, HasAudio: true, DurationMs: 5000, Width: 640, Height: 360, FPS: 30, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"wrong codec", &probeResult{HasVideo: true, HasAudio: true, DurationMs: 5000, Width: renderWidth, Height: renderHeight, FPS: 30, VideoCodec: "mpeg4"}, validInput(), outPath, true},
		{"fps below sane range", &probeResult{HasVideo: true, HasAudio: true, DurationMs: 5000, Width: renderWidth, Height: renderHeight, FPS: 0, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"fps drifts from source", &probeResult{HasVideo: true, HasAudio: true, DurationMs: 5000, Width: renderWidth, Height: renderHeight, FPS: 60, VideoCodec: renderVideoCodec}, validInput(), outPath, true},
		{"empty ASS (no burn-in)", validProbe, func() VariantInput { in := validInput(); in.ASSPath = emptyAssPath; return in }(), outPath, true},
		{"missing ASS file", validProbe, func() VariantInput { in := validInput(); in.ASSPath = filepath.Join(dir, "nope.ass"); return in }(), outPath, true},
		{"empty output file", validProbe, validInput(), writeTempFile(t, dir, "empty.mp4", ""), true},
	}

	r := &Renderer{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.validate(c.input, c.path, c.probe)
			if c.wantErr && err == nil {
				t.Fatalf("validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
		})
	}
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestParseFPS(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"30000/1001", 29.97002997},
		{"25/1", 25},
		{"30", 30},
		{"0/0", 0},
		{"", 0},
		{"garbage", 0},
	}
	for _, c := range cases {
		got := ParseFPS(c.in)
		if c.want == 0 {
			if got != 0 {
				t.Errorf("ParseFPS(%q) = %v, want 0", c.in, got)
			}
			continue
		}
		if got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("ParseFPS(%q) = %v, want ~%v", c.in, got, c.want)
		}
	}
}
