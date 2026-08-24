// Package youtube — whisper_transcriber_test.go: unit test for
// the concrete WhisperTranscriberAdapter. Covers the fail-closed
// contract locked in by the Fase 5 wiring.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026).
package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// writeStubScript writes a minimal Python script that
// emulates the real bridge (reads argv[1], writes JSON to
// stdout, exits 0). The script body is provided by the
// caller so each test can exercise a different branch.
func writeStubScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stub_whisper.py")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"+body), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}

func TestNewWhisperTranscriberAdapter_MissingPython(t *testing.T) {
	log := zap.NewNop()
	_, err := NewWhisperTranscriberAdapter(WhisperTranscriberConfig{
		PythonBin:  "definitely-not-a-real-python-binary-xyz",
		ScriptPath: "/dev/null",
	}, log)
	if err == nil {
		t.Fatalf("expected error for missing Python interpreter, got nil")
	}
	if !errors.Is(err, ErrWhisperBridgeUnavailable) {
		t.Fatalf("expected ErrWhisperBridgeUnavailable, got: %v", err)
	}
}

func TestNewWhisperTranscriberAdapter_MissingScript(t *testing.T) {
	log := zap.NewNop()
	_, err := NewWhisperTranscriberAdapter(WhisperTranscriberConfig{
		PythonBin:  "python3",
		ScriptPath: "/nonexistent/path/to/whisper_bridge.py",
	}, log)
	if err == nil {
		t.Fatalf("expected error for missing script, got nil")
	}
	if !errors.Is(err, ErrWhisperBridgeUnavailable) {
		t.Fatalf("expected ErrWhisperBridgeUnavailable, got: %v", err)
	}
}

func TestWhisperTranscriberAdapter_ValidJSON(t *testing.T) {
	// Fake bridge script that returns a real transcript.
	script := writeStubScript(t, `
import json, sys
print(json.dumps({"text": "hello world from bridge", "detected_language": "en", "confidence": 0.92}))
`)
	log := zap.NewNop()
	adapter, err := NewWhisperTranscriberAdapter(WhisperTranscriberConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, log)
	if err != nil {
		t.Fatalf("NewWhisperTranscriberAdapter: %v", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(tmpFile, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	res, err := adapter.TranscribeAudioWithDetection(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if res.Text != "hello world from bridge" {
		t.Fatalf("expected text 'hello world from bridge', got: %q", res.Text)
	}
	if res.DetectedLanguage != "en" {
		t.Fatalf("expected detected_language 'en', got: %q", res.DetectedLanguage)
	}
	if res.Confidence == nil || *res.Confidence != 0.92 {
		t.Fatalf("expected confidence 0.92, got: %v", res.Confidence)
	}
}

func TestWhisperTranscriberAdapter_ScriptErrorJSON(t *testing.T) {
	// Stub script that returns an error JSON on stderr.
	script := writeStubScript(t, `
import json, sys
print(json.dumps({"error": "model not loaded"}), file=sys.stderr)
sys.exit(1)
`)
	log := zap.NewNop()
	adapter, err := NewWhisperTranscriberAdapter(WhisperTranscriberConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, log)
	if err != nil {
		t.Fatalf("NewWhisperTranscriberAdapter: %v", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(tmpFile, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	_, err = adapter.TranscribeAudioWithDetection(context.Background(), tmpFile)
	if err == nil {
		t.Fatalf("expected error from script failure, got nil")
	}
	// The subprocess failure should propagate (non-zero exit).
	if !strings.Contains(err.Error(), "subprocess") {
		t.Fatalf("expected subprocess error, got: %v", err)
	}
}

func TestWhisperTranscriberAdapter_EmptyText(t *testing.T) {
	// Stub script that returns empty text (non-error, but
	// no transcript). The AcquireService treats this as
	// "priority returned nothing" and falls through.
	script := writeStubScript(t, `
import json
print(json.dumps({"text": "", "detected_language": "und", "confidence": 0.0}))
`)
	log := zap.NewNop()
	adapter, err := NewWhisperTranscriberAdapter(WhisperTranscriberConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, log)
	if err != nil {
		t.Fatalf("NewWhisperTranscriberAdapter: %v", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(tmpFile, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	res, err := adapter.TranscribeAudioWithDetection(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("expected nil error for empty text (caller decides), got: %v", err)
	}
	if res.Text != "" {
		t.Fatalf("expected empty text, got: %q", res.Text)
	}
}

func TestWhisperTranscriberAdapter_EmptyLocalPath(t *testing.T) {
	log := zap.NewNop()
	script := writeStubScript(t, `
import json
print(json.dumps({"text": "ok", "detected_language": "en", "confidence": 0.9}))
`)
	adapter, err := NewWhisperTranscriberAdapter(WhisperTranscriberConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, log)
	if err != nil {
		t.Fatalf("NewWhisperTranscriberAdapter: %v", err)
	}

	_, err = adapter.TranscribeAudioWithDetection(context.Background(), "")
	if err == nil {
		t.Fatalf("expected error for empty localPath, got nil")
	}
	if !strings.Contains(err.Error(), "localPath is empty") {
		t.Fatalf("expected 'localPath is empty' error, got: %v", err)
	}
}
