// Package translation — argos_server_translator_test.go: unit tests for
// the persistent Argos Translate sidecar adapter. Hermetic: each test
// writes a stub Python HTTP server so no argostranslate install is required.
package translation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
