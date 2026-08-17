// Package audioasset — loudness_test.go (PR-VO-LOUDNESS-GATE, August 2026).
//
// Hermetic tests for the minimum-loudness gate: the pure volumedetect parser
// and the Generate retry/fallback contract. No real ffmpeg is required —
// the loudnessProber port is injected as a fake.
package audioasset

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// ─── Pure parser ────────────────────────────────────────────────────────────

func TestParseVolumedetect_ExtractsMeanAndMax(t *testing.T) {
	out := "" +
		"[Parsed_volumedetect_0 @ 0x1] n_samples: 96000\n" +
		"[Parsed_volumedetect_0 @ 0x1] mean_volume: -25.3 dB\n" +
		"[Parsed_volumedetect_0 @ 0x1] max_volume: -2.1 dB\n" +
		"[Parsed_volumedetect_0 @ 0x1] histogram_0db: 1234\n"

	l, err := parseVolumedetect([]byte(out))
	if err != nil {
		t.Fatalf("parseVolumedetect: %v", err)
	}
	if l.MeanDB != -25.3 || l.MaxDB != -2.1 {
		t.Fatalf("parsed loudness = %+v, want mean=-25.3 max=-2.1", l)
	}
	if l.IsSilent() {
		t.Fatalf("audible measurement %+v reported silent", l)
	}
}

func TestParseVolumedetect_SilenceIsNegativeInf(t *testing.T) {
	out := "" +
		"[Parsed_volumedetect_0 @ 0x1] mean_volume: -inf dB\n" +
		"[Parsed_volumedetect_0 @ 0x1] max_volume: -91.0 dB\n"

	l, err := parseVolumedetect([]byte(out))
	if err != nil {
		t.Fatalf("parseVolumedetect: %v", err)
	}
	if !math.IsInf(l.MeanDB, -1) {
		t.Fatalf("mean = %v, want -inf", l.MeanDB)
	}
	if !l.IsSilent() {
		t.Fatalf("silent measurement %+v not reported silent", l)
	}
}

func TestParseVolumedetect_MissingStatFailsClosed(t *testing.T) {
	out := "[Parsed_volumedetect_0 @ 0x1] max_volume: -2.1 dB\n"
	if _, err := parseVolumedetect([]byte(out)); err == nil {
		t.Fatal("expected error when mean_volume is missing")
	}
}

// ─── IsSilent boundary ──────────────────────────────────────────────────────

func TestLoudness_IsSilentBoundary(t *testing.T) {
	cases := []struct {
		name string
		max  float64
		sil  bool
	}{
		{"audible speech", -6.0, false},
		{"just above floor", MinAudibleMaxVolumeDB + 0.1, false},
		{"just below floor", MinAudibleMaxVolumeDB - 0.1, true},
		{"digital silence", -91.0, true},
		{"negative infinity", math.Inf(-1), true},
		{"NaN", math.NaN(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Loudness{MaxDB: tc.max}).IsSilent(); got != tc.sil {
				t.Fatalf("IsSilent(max=%.2f) = %v, want %v", tc.max, got, tc.sil)
			}
		})
	}
}

// ─── Generate retry contract ────────────────────────────────────────────────

type fakeLoudnessProber struct {
	calls int
	loud  Loudness
	err   error
}

func (f *fakeLoudnessProber) MeasureLoudness(_ context.Context, _ string) (Loudness, error) {
	f.calls++
	return f.loud, f.err
}

// TestGenerate_RetriesOnSilentAudio pins the gate: a first synthesis that
// produces silent audio is re-synthesized, and the retry's (audible) result
// wins.
func TestGenerate_RetriesOnSilentAudio(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outFile := writeOutputFile(t, filepath.Join(outDir, "hello_en.mp3"))

	var synthCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/synthesize":
			synthCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
				OK:    true,
				Voice: "en-US-RogerNeural",
				Path:  outFile,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	prober := &fakeLoudnessProber{loud: Loudness{MeanDB: -50, MaxDB: -3}}
	p.SetLoudnessProber(prober)

	result, err := p.Generate(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.LocalPath != outFile {
		t.Fatalf("result = %+v, want LocalPath %q", result, outFile)
	}
	if synthCalls != 1 {
		t.Errorf("audible result must not trigger a retry: synthCalls = %d, want 1", synthCalls)
	}
	if prober.calls != 1 {
		t.Errorf("loudness gate must run once per successful synthesis: %d", prober.calls)
	}
}

// TestGenerate_RetriesOnSilentAudioThenAudible verifies the retry loop
// re-runs the synthesis when the gate reports silence and the first audible
// capture wins.
func TestGenerate_RetriesOnSilentAudioThenAudible(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outFile := writeOutputFile(t, filepath.Join(outDir, "hello_en.mp3"))

	var synthCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/synthesize":
			synthCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
				OK:    true,
				Voice: "en-US-RogerNeural",
				Path:  outFile,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	// First measurement is silent (-91 dB), subsequent ones are audible.
	p.SetLoudnessProber(&switchableProber{audibleAfterCalls: 1})

	result, err := p.Generate(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error after silent retry: %v", err)
	}
	if result == nil || result.LocalPath != outFile {
		t.Fatalf("result = %+v", result)
	}
	if synthCalls != 2 {
		t.Errorf("silent first capture must trigger exactly one retry: synthCalls = %d, want 2", synthCalls)
	}
}

// switchableProber reports silence for the first N calls then audible, so the
// retry loop can be exercised deterministically without real ffmpeg.
type switchableProber struct {
	calls             int
	audibleAfterCalls int
}

func (s *switchableProber) MeasureLoudness(_ context.Context, _ string) (Loudness, error) {
	s.calls++
	if s.calls <= s.audibleAfterCalls {
		return Loudness{MeanDB: -91, MaxDB: -91}, nil
	}
	return Loudness{MeanDB: -30, MaxDB: -3}, nil
}

// TestGenerate_SilentAudioExhaustsRetriesFailsClosed pins the hard failure:
// when every synthesis is silent, Generate fails with ErrSilentAudio after the
// retry budget, never returning a silent result as a success.
func TestGenerate_SilentAudioExhaustsRetriesFailsClosed(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outFile := writeOutputFile(t, filepath.Join(outDir, "hello_en.mp3"))

	var synthCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/synthesize":
			synthCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
				OK:    true,
				Voice: "en-US-RogerNeural",
				Path:  outFile,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	p.SetLoudnessProber(&fakeLoudnessProber{loud: Loudness{MeanDB: -91, MaxDB: -91}})

	_, err := p.Generate(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err == nil {
		t.Fatal("expected ErrSilentAudio after exhausting retries")
	}
	if !errors.Is(err, ErrSilentAudio) {
		t.Fatalf("expected ErrSilentAudio wrapped; got %v", err)
	}
	if synthCalls != 3 {
		t.Errorf("expected 3 synthesize attempts (retry budget), got %d", synthCalls)
	}
}

// TestGenerate_LoudnessMeasurementErrorSkipsGate pins the fail-open behaviour
// for a measurement-tool failure: a transient ffmpeg error must not hard-fail
// an otherwise-valid synthesis (the gate is best-effort hardening).
func TestGenerate_LoudnessMeasurementErrorSkipsGate(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outFile := writeOutputFile(t, filepath.Join(outDir, "hello_en.mp3"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/synthesize":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
				OK:    true,
				Voice: "en-US-RogerNeural",
				Path:  outFile,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	p.SetLoudnessProber(&fakeLoudnessProber{err: errors.New("ffmpeg not found")})

	result, err := p.Generate(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("measurement error must skip the gate, not fail: %v", err)
	}
	if result == nil || result.LocalPath != outFile {
		t.Fatalf("result = %+v", result)
	}
}

// TestGenerate_NoLoudnessProberSkipsGate pins the unregistered-capability
// behaviour: without SetLoudnessProber the gate is not enforced (backward
// compatible with existing callers).
func TestGenerate_NoLoudnessProberSkipsGate(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outFile := writeOutputFile(t, filepath.Join(outDir, "hello_en.mp3"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/synthesize":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
				OK:    true,
				Voice: "en-US-RogerNeural",
				Path:  outFile,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil) // no loudness prober registered

	result, err := p.Generate(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unregistered gate must be a no-op: %v", err)
	}
	if result == nil || result.LocalPath != outFile {
		t.Fatalf("result = %+v", result)
	}
}
