package rustexec

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

// TestParseTimeSeconds pins the dual-format timestamp parser: plain float
// seconds AND the HH:MM:SS.mmm form produced by youtube_pipeline.formatTime.
// Regression for the silent 262-byte stub clips: strconv.ParseFloat alone
// rejected "00:00:15.000", degraded endSec to 0, and the Rust cut ran `-t 0`.
func TestParseTimeSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"0", 0},
		{"15", 15},
		{"15.5", 15.5},
		{"00:00:15.000", 15},
		{"00:00:15", 15},
		{"00:01:05.500", 65.5},
		{"01:02:03.004", 3723.004},
		{"1:05", 65},
		{" 00:00:19.000 ", 19},
	}
	for _, c := range cases {
		got, err := parseTimeSeconds(c.in)
		if err != nil {
			t.Fatalf("parseTimeSeconds(%q) error = %v", c.in, err)
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("parseTimeSeconds(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseTimeSeconds_RejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "banana", "00:XX:12", "::", "-", "00:00:NaN", "15:00:00:00", "-1", "00:60:00"} {
		if _, err := parseTimeSeconds(in); err == nil {
			t.Fatalf("parseTimeSeconds(%q) expected error, got nil", in)
		}
	}
}

func TestCutCopyInvalidTimestampsFailBeforeRust(t *testing.T) {
	for _, tc := range []struct {
		name, start, end string
	}{
		{"invalid start", "banana", "5"},
		{"invalid end", "2", "invalid"},
		{"end before start", "5", "2"},
		{"zero duration", "2", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{stdout: []byte(`{"ok":true}`)}
			client := NewClient("muscles", "ffmpeg", nil)
			client.runner = runner
			err := (&VideoProcessor{client: client}).CutCopy(context.Background(), "in.mp4", "out.mp4", tc.start, tc.end, false)
			if err == nil {
				t.Fatal("CutCopy() accepted invalid timestamp range")
			}
			if runner.input != nil {
				t.Fatalf("Rust runner was invoked for invalid range: %s", runner.input)
			}
		})
	}
}

func TestCutAndNormalizeInvalidTimestampsFailBeforeRust(t *testing.T) {
	opts := mediaexec.CutAndNormalizeOptions{Policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}}
	for _, tc := range []struct {
		name, start, end string
	}{
		{"invalid start", "banana", "5"},
		{"invalid end", "2", "invalid"},
		{"end before start", "5", "2"},
		{"zero duration", "2", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{stdout: []byte(`{"ok":true}`)}
			client := NewClient("muscles", "ffmpeg", nil)
			client.runner = runner
			processor := &VideoProcessor{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}
			err := processor.CutAndNormalize(context.Background(), "in.mp4", "out.mp4", tc.start, tc.end, opts)
			if err == nil {
				t.Fatal("CutAndNormalize() accepted invalid timestamp range")
			}
			if runner.input != nil {
				t.Fatalf("Rust runner was invoked for invalid range: %s", runner.input)
			}
		})
	}
}

func TestCutAndNormalizeParsesFloatAndHHMMSSMillis(t *testing.T) {
	for _, tc := range []struct {
		name, start, end   string
		wantStart, wantEnd float64
	}{
		{"formatted", "00:00:15.500", "00:00:18.250", 15.5, 18.25},
		{"float", "2", "5", 2, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_and_normalize"}`)}
			client := NewClient("muscles", "ffmpeg", nil)
			client.runner = runner
			processor := &VideoProcessor{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}
			opts := mediaexec.CutAndNormalizeOptions{Policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}}
			if err := processor.CutAndNormalize(context.Background(), "in.mp4", "out.mp4", tc.start, tc.end, opts); err != nil {
				t.Fatalf("CutAndNormalize() error = %v", err)
			}
			var sent request
			if err := json.Unmarshal(runner.input, &sent); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if math.Abs(sent.StartSec-tc.wantStart) > 1e-9 || math.Abs(sent.EndSec-tc.wantEnd) > 1e-9 {
				t.Fatalf("StartSec/EndSec = %v/%v, want %v/%v", sent.StartSec, sent.EndSec, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

// TestCutAndNormalizeSendsFormattedTimestampSeconds pins the wire contract:
// a formatted HH:MM:SS.mmm end timestamp must reach the Rust executor as
// real seconds, not 0 (the stub root cause).
func TestCutAndNormalizeSendsFormattedTimestampSeconds(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_and_normalize"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client, profile: mediaexec.VideoProfile{}.WithDefaults()}

	opts := mediaexec.CutAndNormalizeOptions{
		Policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
	}
	if err := processor.CutAndNormalize(context.Background(), "in.mp4", "out.mp4", "0", "00:00:15.000", opts); err != nil {
		t.Fatalf("CutAndNormalize() error = %v", err)
	}

	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.StartSec != 0 {
		t.Fatalf("StartSec = %v, want 0", sent.StartSec)
	}
	if math.Abs(sent.EndSec-15) > 1e-9 {
		t.Fatalf("EndSec = %v, want 15 (formatted timestamp must parse to seconds, not 0)", sent.EndSec)
	}
}

func TestCutCopySendsFormattedTimestampSeconds(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"cut_copy"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client}

	if err := processor.CutCopy(context.Background(), "in.mp4", "out.mp4", "00:00:01.000", "00:01:05.500", false); err != nil {
		t.Fatalf("CutCopy() error = %v", err)
	}

	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if math.Abs(sent.StartSec-1) > 1e-9 || math.Abs(sent.EndSec-65.5) > 1e-9 {
		t.Fatalf("StartSec/EndSec = %v/%v, want 1/65.5", sent.StartSec, sent.EndSec)
	}
}
