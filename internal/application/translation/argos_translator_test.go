// Package translation — argos_translator_test.go: unit tests for the
// ArgosTranslate TranslationPort adapter. Hermetic: each test writes a
// stub Python bridge so no real argostranslate install is required.
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

func writeStubArgosScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stub_argos.py")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"+body), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}

func TestNewArgosTranslator_MissingPython(t *testing.T) {
	_, err := NewArgosTranslator(ArgosTranslatorConfig{
		PythonBin:  "definitely-not-a-real-python-binary-xyz",
		ScriptPath: "/dev/null",
	}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for missing Python interpreter, got nil")
	}
	if !errors.Is(err, ErrArgosBridgeUnavailable) {
		t.Fatalf("expected ErrArgosBridgeUnavailable, got: %v", err)
	}
}

func TestNewArgosTranslator_MissingScript(t *testing.T) {
	_, err := NewArgosTranslator(ArgosTranslatorConfig{
		PythonBin:  "python3",
		ScriptPath: "/nonexistent/path/to/argos_bridge.py",
	}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for missing script, got nil")
	}
	if !errors.Is(err, ErrArgosBridgeUnavailable) {
		t.Fatalf("expected ErrArgosBridgeUnavailable, got: %v", err)
	}
}

func TestArgosTranslator_ValidJSON(t *testing.T) {
	script := writeStubArgosScript(t, `
import json
print(json.dumps({"translated_text": "ciao mondo", "source": "en", "target": "it", "model": "argos-en-it", "via": "direct"}))
`)
	adapter, err := NewArgosTranslator(ArgosTranslatorConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewArgosTranslator: %v", err)
	}

	res, err := adapter.Translate(context.Background(), TranslationCommand{
		SourceLang: "en",
		TargetLang: "it",
		Text:       "hello world",
	})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if res.TranslatedText != "ciao mondo" {
		t.Fatalf("expected 'ciao mondo', got %q", res.TranslatedText)
	}
	if res.UsedProvider != "argos" {
		t.Fatalf("expected provider 'argos', got %q", res.UsedProvider)
	}
	if res.UsedModel != "argos-en-it" {
		t.Fatalf("expected model 'argos-en-it', got %q", res.UsedModel)
	}
}

func TestArgosTranslator_ScriptError(t *testing.T) {
	script := writeStubArgosScript(t, `
import json, sys
print(json.dumps({"error": "package not installed"}), file=sys.stderr)
sys.exit(1)
`)
	adapter, err := NewArgosTranslator(ArgosTranslatorConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewArgosTranslator: %v", err)
	}

	_, err = adapter.Translate(context.Background(), TranslationCommand{
		SourceLang: "en",
		TargetLang: "it",
		Text:       "hello",
	})
	if err == nil {
		t.Fatal("expected error from bridge failure, got nil")
	}
	if !strings.Contains(err.Error(), "subprocess") {
		t.Fatalf("expected subprocess error, got: %v", err)
	}
}

func TestArgosTranslator_EmptyText(t *testing.T) {
	script := writeStubArgosScript(t, `print("{}")`)
	adapter, err := NewArgosTranslator(ArgosTranslatorConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewArgosTranslator: %v", err)
	}

	_, err = adapter.Translate(context.Background(), TranslationCommand{
		SourceLang: "en",
		TargetLang: "it",
	})
	if err == nil || !strings.Contains(err.Error(), "Text is empty") {
		t.Fatalf("expected 'Text is empty' error, got: %v", err)
	}
}

func TestArgosTranslator_UndeterminedSourceLang(t *testing.T) {
	script := writeStubArgosScript(t, `print("{}")`)
	adapter, err := NewArgosTranslator(ArgosTranslatorConfig{
		PythonBin:  "python3",
		ScriptPath: script,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewArgosTranslator: %v", err)
	}

	_, err = adapter.Translate(context.Background(), TranslationCommand{
		SourceLang: "und",
		TargetLang: "it",
		Text:       "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "SourceLang") {
		t.Fatalf("expected SourceLang error for undetermined source, got: %v", err)
	}
}
