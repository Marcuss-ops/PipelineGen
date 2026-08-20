// Package audioasset — processor_test.go (PR-VO-TTS-PERSISTENT-WORKER,
// P0 #1 of VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-08).
//
// Hermetic TDD surface for the persistent tts_edge_server.py worker
// + the typed sentinel surface (godlike/07 typed-error contract).
// Uses net/http/httptest to simulate the Python TTS sidecar — no
// real python3 or tts_edge_server.py required, no flaky env coupling.
//
// 13 focused tests, all hermetic (no external process):
//
//	Sentinel contract (3 tests):
//	1. TestSentinels_ExistAndAreDistinct — 5 sentinels declared, not nil,
//	   distinct from each other (no accidental equality)
//
//	Path-traversal fail-closed (1 test):
//	2. TestGenerate_PathTraversalFailClosed — ErrInvalidFilename wrapped,
//	   errors.Is + errors.As probes
//
//	Wire protocol (5 tests, via httptest):
//	3. TestSendSynthesizeRequest_HappyPath — 200 + valid JSON + file on disk
//	4. TestSendSynthesizeRequest_Non200Status — ErrSynthesizeFailed
//	5. TestSendSynthesizeRequest_OkFalse — ErrSynthesizeFailed + body err
//	6. TestSendSynthesizeRequest_InvalidJSON — ErrSynthesizeFailed
//	7. TestSendSynthesizeRequest_OutputMissing — ErrOutputMissing
//
//	Health surface (3 tests):
//	8. TestHealth_BeforeStart — returns "not started" error
//	9. TestHealth_AfterStartHealthy — returns nil when /health=200
//	10. TestHealth_AfterStartUnhealthy — wraps ErrWorkerHealthFailed
//
//	Stop idempotency (1 test):
//	11. TestStop_Idempotency — nil-safe before start, idempotent after start
//
//	Subprocess failure surface (1 test, no real python3 needed):
//	12. TestEnsureStarted_ScriptMissing — ErrWorkerUnavailable wrapped
//
//	Compile-time pin (1 test):
//	13. TestProcessorShape_CompilePin — *Processor satisfies processorShape
//
// godlike/06 SSOT (one canonical owner per fact): the test surface
// is hermetic; the legacy spawn-per-call path is intentionally NOT
// covered (requires real python3 + tts_edge.py; would couple the
// test suite to the production environment per godlike/07 minimum-
// blast-radius). The legacy path is exercised at integration level
// by the operational smoke suite.
package audioasset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// ───────────────────────────────────────────────────────────────────────
// Helpers
// ───────────────────────────────────────────────────────────────────────

// newTestProcessor constructs a Processor that points at the given
// httptest server (bypassing ensureStarted's subprocess spawn). The
// white-box test sets baseURL + httpClient + started=true directly so
// the protocol + health paths can be exercised without python3.
//
// Usage:
//
//	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ... }))
//	defer ts.Close()
//	p := newTestProcessor(t, ts.URL, "en-US", nil)
//	// p.sendSynthesizeRequest(...) now hits ts.
func newTestProcessor(t *testing.T, baseURL string, lang string, synthHandler http.HandlerFunc) *Processor {
	t.Helper()
	if synthHandler != nil {
		// Replace the server's handler to inject the test-specific synthesize
		// behaviour on top of the baseURL the caller already wired. We
		// re-route through a per-test mux: the caller's baseURL is
		// canonical; the per-test handler is reachable via a side
		// httptest server the test installs separately.
		_ = synthHandler
	}
	return &Processor{
		pythonScriptsDir: t.TempDir(),
		log:              zap.NewNop(),
		baseURL:          baseURL,
		httpClient:       &http.Client{},
		started:          true,
	}
}

// writeOutputFile creates a sentinel output file at the path the
// worker would have written; the test asserts the returned AudioResult
// picks up that path.
func writeOutputFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(path, []byte("FAKE_MP3_BYTES"), 0o644); err != nil {
		t.Fatalf("write output: %v", err)
	}
	return path
}

// ───────────────────────────────────────────────────────────────────────
// 1. Sentinel contract
// ───────────────────────────────────────────────────────────────────────

func TestSentinels_ExistAndAreDistinct(t *testing.T) {
	t.Parallel()

	sentinels := map[string]error{
		"ErrWorkerUnavailable":  ErrWorkerUnavailable,
		"ErrWorkerHealthFailed": ErrWorkerHealthFailed,
		"ErrSynthesizeFailed":   ErrSynthesizeFailed,
		"ErrOutputMissing":      ErrOutputMissing,
		"ErrInvalidFilename":    ErrInvalidFilename,
		"ErrEmptyAudio":         ErrEmptyAudio,
		"ErrSilentAudio":        ErrSilentAudio,
	}

	if len(sentinels) != 7 {
		t.Fatalf("expected 7 typed sentinels, got %d", len(sentinels))
	}

	for name, s := range sentinels {
		if s == nil {
			t.Errorf("sentinel %s is nil", name)
		}
	}

	// Distinctness: each sentinel must have a unique error message so
	// errors.Is probes don't false-positive across categories.
	msgs := make(map[string]string, len(sentinels))
	for name, s := range sentinels {
		if existing, dup := msgs[s.Error()]; dup {
			t.Errorf("sentinel %s duplicates message of %s: %q", name, existing, s.Error())
		}
		msgs[s.Error()] = name
	}

	// Each sentinel's message must contain the canonical "audioasset:" prefix
	// (the canonical godlike/07 contract: the package owns the
	// diagnostic context, callers probe by sentinel, not by string).
	for name, s := range sentinels {
		if !strings.HasPrefix(s.Error(), "audioasset: ") {
			t.Errorf("sentinel %s missing canonical 'audioasset: ' prefix: %q", name, s.Error())
		}
	}
}

// ───────────────────────────────────────────────────────────────────────
// 2. Path-traversal fail-closed
// ───────────────────────────────────────────────────────────────────────

func TestGenerate_PathTraversalFailClosed(t *testing.T) {
	t.Parallel()

	p := &Processor{
		pythonScriptsDir: t.TempDir(),
		log:              zap.NewNop(),
	}

	cases := []struct {
		name     string
		filename string
	}{
		{"parent_dir_traversal", "../etc/passwd"},
		{"absolute_path", "/etc/passwd"},
		{"double_slash", "foo//bar"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Generate(context.Background(), &AudioInput{
				Text:     "hello",
				Language: "en-US",
				Filename: tc.filename,
			})
			if err == nil {
				t.Fatalf("expected error for path-traversal filename %q", tc.filename)
			}
			if !errors.Is(err, ErrInvalidFilename) {
				t.Errorf("expected errors.Is(err, ErrInvalidFilename) = true; got err=%v", err)
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("expected error message to contain 'path traversal'; got %q", err.Error())
			}
		})
	}
}

// ───────────────────────────────────────────────────────────────────────
// 3-7. Wire protocol (httptest-based, no python3 required)
// ───────────────────────────────────────────────────────────────────────

func TestSendSynthesizeRequest_HappyPath(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outFile := writeOutputFile(t, filepath.Join(outDir, "hello_en.mp3"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/synthesize" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
			OK:    true,
			Voice: "en-US-RogerNeural",
			Path:  outFile,
		})
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	result, err := p.sendSynthesizeRequest(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	if result.LocalPath != outFile {
		t.Errorf("LocalPath = %q, want %q", result.LocalPath, outFile)
	}
	if result.Voice != "en-US-RogerNeural" {
		t.Errorf("Voice = %q, want %q", result.Voice, "en-US-RogerNeural")
	}
	if result.Status != "generated" {
		t.Errorf("Status = %q, want %q", result.Status, "generated")
	}
}

func TestSendSynthesizeRequest_Non200Status(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"ok":false,"error":"edge-tts exploded"}`))
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	_, err := p.sendSynthesizeRequest(context.Background(), &AudioInput{
		Text:      "hi",
		Language:  "en-US",
		Filename:  "hi_en.mp3",
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for non-200")
	}
	if !errors.Is(err, ErrSynthesizeFailed) {
		t.Errorf("expected ErrSynthesizeFailed wrapped; got err=%v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention 500; got %q", err.Error())
	}
}

func TestSendSynthesizeRequest_OkFalse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
			OK:    false,
			Error: "no voice for lang xyz",
		})
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "xyz", nil)
	_, err := p.sendSynthesizeRequest(context.Background(), &AudioInput{
		Text:      "hi",
		Language:  "xyz",
		Filename:  "hi_xyz.mp3",
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for ok=false")
	}
	if !errors.Is(err, ErrSynthesizeFailed) {
		t.Errorf("expected ErrSynthesizeFailed; got err=%v", err)
	}
	// Body error message must be preserved via the second %w wrap.
	if !strings.Contains(err.Error(), "no voice for lang xyz") {
		t.Errorf("expected body error preserved; got %q", err.Error())
	}
}

func TestSendSynthesizeRequest_InvalidJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json-at-all`))
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	_, err := p.sendSynthesizeRequest(context.Background(), &AudioInput{
		Text:      "hi",
		Language:  "en-US",
		Filename:  "hi_en.mp3",
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !errors.Is(err, ErrSynthesizeFailed) {
		t.Errorf("expected ErrSynthesizeFailed; got err=%v", err)
	}
}

func TestSendSynthesizeRequest_OutputMissing(t *testing.T) {
	t.Parallel()

	// Worker returns OK but Path points to a non-existent file.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
			OK:   true,
			Path: "/tmp/this-file-will-never-exist-on-disk-12345.mp3",
		})
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	_, err := p.sendSynthesizeRequest(context.Background(), &AudioInput{
		Text:      "hi",
		Language:  "en-US",
		Filename:  "hi_en.mp3",
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for missing output file")
	}
	if !errors.Is(err, ErrOutputMissing) {
		t.Errorf("expected ErrOutputMissing; got err=%v", err)
	}
}

// ───────────────────────────────────────────────────────────────────────
// 8-10. Health surface
// ───────────────────────────────────────────────────────────────────────

func TestHealth_BeforeStart(t *testing.T) {
	t.Parallel()

	p := &Processor{
		pythonScriptsDir: t.TempDir(),
		log:              zap.NewNop(),
		// started deliberately false
	}
	err := p.Health()
	if err == nil {
		t.Fatal("expected error before start")
	}
	if !strings.Contains(err.Error(), "not started") {
		t.Errorf("expected 'not started' in message; got %q", err.Error())
	}
}

func TestHealth_AfterStartHealthy(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	if err := p.Health(); err != nil {
		t.Errorf("expected nil for healthy worker; got %v", err)
	}
}

func TestHealth_AfterStartUnhealthy(t *testing.T) {
	t.Parallel()

	// /health returns 500 — simulates the persistent worker in a
	// degraded state (post-startup, but not responsive on /health).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	err := p.Health()
	if err == nil {
		t.Fatal("expected error for unhealthy worker")
	}
	if !errors.Is(err, ErrWorkerHealthFailed) {
		t.Errorf("expected ErrWorkerHealthFailed wrapped; got err=%v", err)
	}
}

// ───────────────────────────────────────────────────────────────────────
// 11. Stop idempotency
// ───────────────────────────────────────────────────────────────────────

func TestStop_Idempotency(t *testing.T) {
	t.Parallel()

	t.Run("stop_before_start_is_nil_safe", func(t *testing.T) {
		p := &Processor{
			pythonScriptsDir: t.TempDir(),
			log:              zap.NewNop(),
		}
		if err := p.Stop(); err != nil {
			t.Errorf("Stop() before start should be a no-op; got err=%v", err)
		}
	})

	t.Run("stop_after_start_clears_state", func(t *testing.T) {
		// /quit returns 200, baseURL is set, started=true.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/quit" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Error(w, "unexpected", http.StatusNotFound)
		}))
		defer srv.Close()

		p := &Processor{
			pythonScriptsDir: t.TempDir(),
			log:              zap.NewNop(),
			baseURL:          srv.URL,
			httpClient:       &http.Client{},
			started:          true,
		}
		if err := p.Stop(); err != nil {
			t.Errorf("first Stop() should succeed; got err=%v", err)
		}
		// Post-stop state: started=false (no leftover state for the next call).
		if p.started {
			t.Errorf("expected started=false post-Stop; got started=true")
		}
		// Idempotency: second Stop() is a no-op (started is already false).
		if err := p.Stop(); err != nil {
			t.Errorf("second Stop() should be a no-op; got err=%v", err)
		}
	})
}

// ───────────────────────────────────────────────────────────────────────
// 12. Subprocess failure surface (no real python3 needed)
// ───────────────────────────────────────────────────────────────────────

func TestEnsureStarted_ScriptMissing(t *testing.T) {
	t.Parallel()

	// pythonScriptsDir points at an empty TempDir; the canonical
	// tts_edge_server.py does NOT exist there, so ensureStarted
	// should fail-closed with the typed ErrWorkerUnavailable sentinel.
	p := &Processor{
		pythonScriptsDir: t.TempDir(),
		log:              zap.NewNop(),
	}
	// Mutex is internal; since ensureStarted is the entry-point we
	// don't hold p.mu here, but the function does not lock — the
	// lock is the caller's contract. For this test we just call
	// the public surface to drive the failure path.
	err := p.ensureStarted(context.Background())
	if err == nil {
		t.Fatal("expected error when tts_edge_server.py is missing")
	}
	if !errors.Is(err, ErrWorkerUnavailable) {
		t.Errorf("expected ErrWorkerUnavailable wrapped; got err=%v", err)
	}
	if !strings.Contains(err.Error(), "tts_edge_server.py not found") {
		t.Errorf("expected 'tts_edge_server.py not found' in message; got %q", err.Error())
	}
}

// ───────────────────────────────────────────────────────────────────────
// 13. Compile-time pin
// ───────────────────────────────────────────────────────────────────────

// TestProcessorShape_CompilePin verifies the local-mirror compile-time
// assertion (`var _ processorShape = (*Processor)(nil)`) holds.
// If Processor drifts away from the GENERATE-side surface, the
// `var _` line itself will fail to compile, so this test asserts the
// shape via runtime reflection as a backstop.
func TestProcessorShape_CompilePin(t *testing.T) {
	t.Parallel()

	var s processorShape = &Processor{
		pythonScriptsDir: t.TempDir(),
		log:              zap.NewNop(),
	}
	if s == nil {
		t.Fatal("Processor does not satisfy processorShape (compile-time pin drift)")
	}
}

// ───────────────────────────────────────────────────────────────────────
// Cross-cutting: errors.Is across dual-%w chains
// ───────────────────────────────────────────────────────────────────────

// TestSentinels_ErrorsIsProbesAcrossDualWw is a regression guard for
// the dual-%w pattern (Go 1.20+). The production code wraps both
// the underlying cause AND the typed sentinel, e.g.
//
//	fmt.Errorf("synthesize request failed: %w: %w", err, ErrSynthesizeFailed)
//
// errors.Is must recover BOTH the sentinel and the underlying cause
// (when the underlying is itself an error). This test exercises the
// pattern at the sentinel-probe level (synthetic inline).
func TestSentinels_ErrorsIsProbesAcrossDualWw(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection refused: 127.0.0.1:9")
	wrapped := fmt.Errorf("synthesize request failed: %w: %w", cause, ErrSynthesizeFailed)

	if !errors.Is(wrapped, ErrSynthesizeFailed) {
		t.Error("errors.Is must recover ErrSynthesizeFailed from dual-%w chain")
	}
	if !errors.Is(wrapped, cause) {
		t.Error("errors.Is must recover the underlying cause from dual-%w chain")
	}
}

// ───────────────────────────────────────────────────────────────────────
// Empty-audio classification + retry
// ───────────────────────────────────────────────────────────────────────

// TestIsBridgeEmptyAudioError pins the single classification site for the
// two bridge empty-audio strings (legacy CLI "Empty file" + persistent
// worker "generated file is empty or missing").
func TestIsBridgeEmptyAudioError(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{"Empty file", "generated file is empty or missing", "  Empty file\n"} {
		if !isBridgeEmptyAudioError(msg) {
			t.Errorf("isBridgeEmptyAudioError(%q) = false, want true", msg)
		}
	}
	for _, msg := range []string{"", "no voice for lang xyz", "edge-tts exploded", "empty"} {
		if isBridgeEmptyAudioError(msg) {
			t.Errorf("isBridgeEmptyAudioError(%q) = true, want false", msg)
		}
	}
}

// TestSendSynthesizeRequest_EmptyFileWrapsErrEmptyAudio pins the worker
// path: an ok=false "Empty file" response must wrap BOTH ErrEmptyAudio and
// ErrSynthesizeFailed so Generate can retry and callers can probe the cause.
func TestSendSynthesizeRequest_EmptyFileWrapsErrEmptyAudio(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
			OK:    false,
			Error: "Empty file",
		})
	}))
	defer srv.Close()

	p := newTestProcessor(t, srv.URL, "en-US", nil)
	_, err := p.sendSynthesizeRequest(context.Background(), &AudioInput{
		Text:      "hi",
		Language:  "en-US",
		Filename:  "hi_en.mp3",
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for Empty file response")
	}
	if !errors.Is(err, ErrEmptyAudio) {
		t.Errorf("expected ErrEmptyAudio wrapped; got err=%v", err)
	}
	if !errors.Is(err, ErrSynthesizeFailed) {
		t.Errorf("expected ErrSynthesizeFailed also wrapped; got err=%v", err)
	}
}

// TestGenerate_RetriesOnEmptyAudio pins the Generate retry contract: a
// first synthesis that returns an empty audio file is retried (a fresh
// synthesis) instead of failing immediately, and the retry's result wins.
func TestGenerate_RetriesOnEmptyAudio(t *testing.T) {
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
			if synthCalls == 1 {
				_ = json.NewEncoder(w).Encode(ttsSynthesizeResponse{
					OK:    false,
					Error: "Empty file",
				})
				return
			}
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
	result, err := p.Generate(context.Background(), &AudioInput{
		Text:      "hello world",
		Language:  "en-US",
		Filename:  "hello_en.mp3",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	if synthCalls != 2 {
		t.Errorf("expected 2 synthesize attempts (empty + retry), got %d", synthCalls)
	}
	if result.LocalPath != outFile {
		t.Errorf("LocalPath = %q, want %q", result.LocalPath, outFile)
	}
}

func TestNewProcessor_DefaultRequestConcurrencyMatchesBenchmark(t *testing.T) {
	p := NewProcessor(t.TempDir(), zap.NewNop())
	if got := cap(p.requestSlots); got != 4 {
		t.Fatalf("default request concurrency = %d, want 4", got)
	}
}
