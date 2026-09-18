// Package translation — argos_server_translator_test.go: unit tests for
// the persistent Argos Translate sidecar adapter. Hermetic: each test
// writes a stub Python HTTP server so no argostranslate install is required.
package translation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// stubArgosServerBody is a minimal Python HTTP server that speaks the
// sidecar protocol (PORT handshake, /health, /translate, /quit).
const stubArgosServerBody = `
import json, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def _json(self, s, p):
        b = json.dumps(p).encode()
        self.send_response(s)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "not found"})
    def do_POST(self):
        if self.path == "/quit":
            self._json(200, {"status": "shutting_down"})
            threading.Thread(target=self.server.shutdown, daemon=True).start()
            return
        n = int(self.headers.get("Content-Length", "0") or "0")
        body = json.loads(self.rfile.read(n))
        self._json(200, {
            "translated_text": "ciao " + body["text"],
            "source": body["source"],
            "target": body["target"],
            "model": "argos-en-it",
            "via": "direct",
        })

srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
srv.daemon_threads = True
print("PORT=%d" % srv.server_address[1], flush=True)
srv.serve_forever()
`

func newStubArgosServerTranslator(t *testing.T) *ArgosServerTranslator {
	t.Helper()
	scriptsDir := t.TempDir()
	bridgeDir := filepath.Join(scriptsDir, "bridges")
	if err := os.MkdirAll(bridgeDir, 0o755); err != nil {
		t.Fatalf("mkdir bridges: %v", err)
	}
	script := filepath.Join(bridgeDir, "argos_server.py")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\n"+stubArgosServerBody), 0o755); err != nil {
		t.Fatalf("write stub server: %v", err)
	}
	a, err := NewArgosServerTranslator(ArgosServerConfig{
		PythonBin:  "python3",
		ScriptsDir: scriptsDir,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewArgosServerTranslator: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return a
}

func TestNewArgosServerTranslator_MissingPython(t *testing.T) {
	_, err := NewArgosServerTranslator(ArgosServerConfig{
		PythonBin:  "definitely-not-a-real-python-binary-xyz",
		ScriptsDir: "scripts",
	}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for missing Python, got nil")
	}
	if !errors.Is(err, ErrArgosBridgeUnavailable) {
		t.Fatalf("expected ErrArgosBridgeUnavailable, got: %v", err)
	}
}

func TestNewArgosServerTranslator_MissingScript(t *testing.T) {
	_, err := NewArgosServerTranslator(ArgosServerConfig{
		PythonBin:  "python3",
		ScriptsDir: "/nonexistent/scripts/dir",
	}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for missing script, got nil")
	}
	if !errors.Is(err, ErrArgosBridgeUnavailable) {
		t.Fatalf("expected ErrArgosBridgeUnavailable, got: %v", err)
	}
}

func TestArgosServerTranslator_Translate_EndToEnd(t *testing.T) {
	a := newStubArgosServerTranslator(t)

	res, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "en",
		TargetLang: "it",
		Text:       "world",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if res.TranslatedText != "ciao world" {
		t.Fatalf("expected 'ciao world', got %q", res.TranslatedText)
	}
	if res.UsedProvider != "argos" || res.UsedModel != "argos-en-it" {
		t.Fatalf("unexpected provenance: provider=%q model=%q", res.UsedProvider, res.UsedModel)
	}

	// Second call reuses the already-started server.
	if _, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "en", TargetLang: "it", Text: "again",
	}); err != nil {
		t.Fatalf("second Translate: %v", err)
	}
}

func TestArgosServerTranslator_UndeterminedSource(t *testing.T) {
	a := newStubArgosServerTranslator(t)
	_, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "und",
		TargetLang: "it",
		Text:       "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "SourceLang") {
		t.Fatalf("expected SourceLang error, got: %v", err)
	}
}

// stubArgosTelemetryBody is a sidecar stub that records, into a telemetry
// file, one line per served request: "health 0" for /health probes and
// "translate <in-flight>" for /translate (with the in-flight count observed
// when the request entered the handler). It is the measurement surface for
// the two bottlenecks this adapter had: a /health GET on every Translate, and
// an exclusive lock that made the fan-out strictly sequential.
const stubArgosTelemetryBody = `
import json, os, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TELEMETRY = "__TELEMETRY__"
_lock = threading.Lock()
_inflight = 0


def _record(line):
    with _lock:
        with open(TELEMETRY, "a") as fh:
            fh.write(line + "\n")


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _json(self, s, p):
        b = json.dumps(p).encode()
        self.send_response(s)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path == "/health":
            _record("health 0")
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        global _inflight
        if self.path == "/quit":
            self._json(200, {"status": "shutting_down"})
            threading.Thread(target=self.server.shutdown, daemon=True).start()
            return
        n = int(self.headers.get("Content-Length", "0") or "0")
        body = json.loads(self.rfile.read(n))
        with _lock:
            _inflight += 1
            current = _inflight
        try:
            # Concurrency barrier instead of a fixed sleep: hold the request
            # open until EXPECTED peers are in flight (or a bounded timeout),
            # so the observed peak is a fact about the CLIENT and not about how
            # loaded the machine is when the test runs.
            deadline = time.time() + 2.0
            while time.time() < deadline:
                with _lock:
                    if _inflight >= EXPECTED:
                        break
                time.sleep(0.01)
            self._json(200, {
                "translated_text": "ciao " + body["text"],
                "source": body["source"],
                "target": body["target"],
                "model": "argos-en-it",
                "via": "direct",
            })
        finally:
            _record("translate %d" % current)
            with _lock:
                _inflight -= 1


srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
srv.daemon_threads = True
EXPECTED = __EXPECTED__

# The child environment is part of the contract: the Go adapter must hand the
# sidecar the SAME .argosmodel directory the installer used, under the name
# argostranslate >= 1.9 actually reads (PLURAL).
_record("env " + os.environ.get("ARGOS_PACKAGES_DIR", ""))
print("PORT=%d" % srv.server_address[1], flush=True)
srv.serve_forever()
`

// newStubArgosTelemetryTranslator writes the telemetry stub server and
// returns the adapter plus the telemetry file path.
func newStubArgosTelemetryTranslator(t *testing.T, concurrency int) (*ArgosServerTranslator, string) {
	t.Helper()
	// A single expected peer disables the concurrency barrier: every request
	// is served as soon as it arrives (the default for non-concurrency tests).
	return newStubArgosTelemetryTranslatorFull(t, concurrency, "", 1)
}

func newStubArgosTelemetryTranslatorWithPackages(t *testing.T, concurrency int, packageDir string) (*ArgosServerTranslator, string) {
	t.Helper()
	return newStubArgosTelemetryTranslatorFull(t, concurrency, packageDir, 1)
}

func newStubArgosTelemetryTranslatorFull(t *testing.T, concurrency int, packageDir string, expected int) (*ArgosServerTranslator, string) {
	t.Helper()
	if expected < 1 {
		expected = 1
	}
	t.Helper()
	scriptsDir := t.TempDir()
	bridgeDir := filepath.Join(scriptsDir, "bridges")
	if err := os.MkdirAll(bridgeDir, 0o755); err != nil {
		t.Fatalf("mkdir bridges: %v", err)
	}
	telemetry := filepath.Join(scriptsDir, "telemetry.txt")
	script := filepath.Join(bridgeDir, "argos_server.py")
	body := strings.ReplaceAll(stubArgosTelemetryBody, "__TELEMETRY__", telemetry)
	body = strings.ReplaceAll(body, "__EXPECTED__", fmt.Sprintf("%d", expected))
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\n"+body), 0o755); err != nil {
		t.Fatalf("write telemetry stub server: %v", err)
	}
	a, err := NewArgosServerTranslator(ArgosServerConfig{
		PythonBin:   "python3",
		ScriptsDir:  scriptsDir,
		Concurrency: concurrency,
		PackageDir:  packageDir,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewArgosServerTranslator: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return a, telemetry
}

func readTelemetry(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry: %v", err)
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

// TestArgosServerTranslator_NoHealthProbePerCall pins the removed per-call
// liveness probe: the sidecar is probed exactly once (at spawn), never on the
// translation path. A regression here re-adds one RTT per cue.
func TestArgosServerTranslator_NoHealthProbePerCall(t *testing.T) {
	a, telemetry := newStubArgosTelemetryTranslator(t, 2)
	for i := 0; i < 3; i++ {
		if _, err := a.Translate(context.Background(), TranslationCommand{
			SourceLang: "en", TargetLang: "it", Text: "cue",
		}); err != nil {
			t.Fatalf("Translate %d: %v", i, err)
		}
	}
	health := 0
	for _, line := range readTelemetry(t, telemetry) {
		if strings.HasPrefix(line, "health") {
			health++
		}
	}
	if health != 1 {
		t.Fatalf("sidecar /health probes = %d, want exactly 1 (startup only)", health)
	}
}

// TestArgosServerTranslator_TranslatesConcurrently pins the bounded-parallel
// fan-out: with Concurrency=3 the adapter must keep more than one request in
// flight instead of serialising every cue on an exclusive mutex.
func TestArgosServerTranslator_TranslatesConcurrently(t *testing.T) {
	a, telemetry := newStubArgosTelemetryTranslatorFull(t, 3, "", 3)
	var wg sync.WaitGroup
	errCh := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.Translate(context.Background(), TranslationCommand{
				SourceLang: "en", TargetLang: "it", Text: "concurrent cue",
			})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Translate: %v", err)
		}
	}

	peak := 0
	for _, line := range readTelemetry(t, telemetry) {
		var observed int
		if _, err := fmt.Sscanf(line, "translate %d", &observed); err == nil && observed > peak {
			peak = observed
		}
	}
	if peak < 2 {
		t.Fatalf("peak concurrent in-flight translate requests = %d, want >= 2 (fan-out is serialised)", peak)
	}
}

// TestArgosServerTranslator_HTTPClientKeepsSidecarConversationsAlive pins the
// transport of the sidecar client: the Go default of 2 idle connections per
// host is below the adapter's own concurrency bound, so every extra cue used
// to reopen a loopback TCP connection.
func TestArgosServerTranslator_HTTPClientKeepsSidecarConversationsAlive(t *testing.T) {
	a, _ := newStubArgosTelemetryTranslator(t, 4)
	if _, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "en", TargetLang: "it", Text: "keep-alive",
	}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	transport, ok := a.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("transport = %#v, want a tuned *http.Transport", a.httpClient.Transport)
	}
	if transport.MaxIdleConnsPerHost < cap(a.sem) {
		t.Fatalf("MaxIdleConnsPerHost = %d, want >= concurrency %d", transport.MaxIdleConnsPerHost, cap(a.sem))
	}
	if a.httpClient.Timeout != a.requestTimeout {
		t.Fatalf("client timeout = %s, want the configured request timeout %s", a.httpClient.Timeout, a.requestTimeout)
	}
}

// TestArgosServerTranslator_PackageDirPropagated pins the installer/sidecar
// agreement: the configured .argosmodel directory reaches the child process as
// ARGOS_PACKAGE_DIR. When it does not, the sidecar cannot see installed models
// and every translation degrades to the slow Ollama fallback.
func TestArgosServerTranslator_PackageDirPropagated(t *testing.T) {
	packageDir := filepath.Join(t.TempDir(), "argos-packages")
	a, telemetry := newStubArgosTelemetryTranslatorWithPackages(t, 1, packageDir)
	if _, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "en", TargetLang: "it", Text: "models",
	}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	found := false
	for _, line := range readTelemetry(t, telemetry) {
		if strings.TrimSpace(line) == "env "+packageDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("sidecar did not receive ARGOS_PACKAGES_DIR=%q; telemetry=%v", packageDir, readTelemetry(t, telemetry))
	}
}

// TestArgosServerTranslator_RelaunchesAfterStop pins the invalidate/relaunch
// contract: a stopped (or crashed) sidecar is respawned on the next request
// instead of leaving every later call to fail against a dead socket.
func TestArgosServerTranslator_RelaunchesAfterStop(t *testing.T) {
	a, _ := newStubArgosTelemetryTranslator(t, 1)
	if _, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "en", TargetLang: "it", Text: "first",
	}); err != nil {
		t.Fatalf("first Translate: %v", err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	res, err := a.Translate(context.Background(), TranslationCommand{
		SourceLang: "en", TargetLang: "it", Text: "second",
	})
	if err != nil {
		t.Fatalf("Translate after Stop: %v", err)
	}
	if res.TranslatedText != "ciao second" {
		t.Fatalf("unexpected text after relaunch: %q", res.TranslatedText)
	}
}
