package rustexec

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type mediaexecGolden struct {
	Request  request  `json:"request"`
	Response response `json:"response"`
}

func loadMediaexecGolden(t *testing.T, name string) mediaexecGolden {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "testdata", "mediaexec", "v1", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared fixture %s: %v", path, err)
	}
	var fixture mediaexecGolden
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode shared fixture %s: %v", path, err)
	}
	return fixture
}

func TestMediaexecV1SharedGoldens(t *testing.T) {
	for _, name := range []string{"probe", "cut_batch", "render_stock", "normalize"} {
		t.Run(name, func(t *testing.T) {
			fixture := loadMediaexecGolden(t, name)
			if fixture.Request.Version != ProtocolVersion {
				t.Fatalf("request version = %q, want %s", fixture.Request.Version, ProtocolVersion)
			}
			if err := fixture.Request.Validate(); err != nil {
				t.Fatalf("request validation failed: %v", err)
			}
			if fixture.Request.Operation.String() != name {
				t.Fatalf("request operation = %q, want %q", fixture.Request.Operation, name)
			}
			if fixture.Response.Operation != name || !fixture.Response.OK {
				t.Fatalf("incompatible response: operation=%q ok=%v", fixture.Response.Operation, fixture.Response.OK)
			}

			switch name {
			case "probe":
				metadata := fixture.Response.Metadata
				if fixture.Request.SourcePath != "/fixtures/input.mp4" || metadata == nil {
					t.Fatal("probe fixture must contain the canonical source_path and metadata")
				}
				if metadata.DurationSec != 12.5 || metadata.Width != 1920 || metadata.Height != 1080 || metadata.FPS != 24 || metadata.VideoCodec != "h264" || metadata.AudioCodec != "aac" || metadata.SampleRate != 48000 || metadata.Channels != 2 || !metadata.HasVideo || !metadata.HasAudio {
					t.Fatalf("probe metadata drift: %+v", metadata)
				}
			case "cut_batch":
				if len(fixture.Request.Jobs) != 1 || len(fixture.Response.Items) != 1 {
					t.Fatal("cut_batch fixture must contain one job and one result")
				}
				job, item := fixture.Request.Jobs[0], fixture.Response.Items[0]
				if job.JobID != "clip-001" || item.JobID != job.JobID || job.OutputPath != item.OutputPath || item.Status != "validated" || item.SizeBytes != 123456 || item.DurationSec != 5 {
					t.Fatalf("cut_batch fixture drift: job=%+v item=%+v", job, item)
				}
			case "render_stock":
				if len(fixture.Request.InputPaths) != 2 || len(fixture.Request.Transitions) != 1 || len(fixture.Request.EffectPaths) != 1 {
					t.Fatalf("render_stock fixture must contain two inputs, one transition, and one effect path")
				}
				transition := fixture.Request.Transitions[0]
				effect := fixture.Request.EffectPaths[0]
				if transition.ClipIndex != 1 || transition.Segment != "end" || transition.ID != "fadeblack" || effect.ClipIndex != 1 || effect.Path != "/fixtures/effect-001.mp4" {
					t.Fatalf("render_stock fixture drift: inputs=%v transition=%+v effect=%+v", fixture.Request.InputPaths, transition, effect)
				}
			case "normalize":
				if fixture.Request.SourcePath != "/fixtures/input.mp4" || fixture.Request.OutputPath != "/fixtures/normalized.mp4" || fixture.Request.Codec != "h264_nvenc" || fixture.Request.Preset != "p1" || fixture.Request.CRF != 23 || fixture.Request.Width != 1920 || fixture.Request.Height != 1080 || fixture.Request.FPS != 24 || fixture.Request.KeyframeInterval != 48 || fixture.Request.AudioCodec != "aac" || fixture.Request.AudioBitrate != "128k" || fixture.Request.SampleRate != 48000 || fixture.Request.Channels != 2 || !fixture.Request.KeepAudio {
					t.Fatalf("normalize fixture drift: %+v", fixture.Request)
				}
			}
		})
	}
}
