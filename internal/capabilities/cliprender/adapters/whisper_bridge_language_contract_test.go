package adapters

// whisper_bridge_language_contract_test.go — the Go↔bridge wire contract for
// the transcription language.
//
// The adapter passes `--language <tag>` to scripts/bridges/whisper_transcriber.py
// and then reads `language`, `detected_language` and `language_forced` from its
// JSON. Three separate things could silently break that wire, and none of them
// is visible from Go alone:
//
//  1. the bridge ignores the flag (it used to read ONLY the VELOX_WHISPER_LANGUAGE
//     environment variable, so a per-request language never reached Whisper);
//  2. the bridge reports the forced language as `detected_language`, turning the
//     caller's language check into a tautology;
//  3. the payload shape drifts from what the adapter parses.
//
// This runs the REAL bridge script against a doubling helper (no Whisper model,
// no GPU, no network) and asserts the contract end to end. It skips when no
// python3 is available.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHelperScript is written where the bridge expects its helper
// (<bridges>/../tools/transcribe_detect_lang.py). It ASSERTS the language it was
// given, so the test fails if the bridge stops forwarding `--language`, and it
// reports a detection deliberately different from the requested language — the
// forced echo must never be reported as the detection.
const fakeHelperScript = `import argparse, json, sys

parser = argparse.ArgumentParser()
parser.add_argument("file", nargs="?")
parser.add_argument("--model", default="")
parser.add_argument("--transcribe", action="store_true")
parser.add_argument("--json-only", action="store_true")
parser.add_argument("--language", default=None)
parser.add_argument("--pcm-stdin", action="store_true")
args = parser.parse_args()

if args.pcm_stdin:
    sys.stdin.buffer.read()

forced = args.language or ""
print(json.dumps({
    "language": forced or "und",
    "language_forced": bool(forced),
    "detected_language": "en",
    "probability": 0.9,
    "language_probability": 0.9,
    "duration_seconds": 2.0,
    "transcript_full": "hello",
    "cues": [{"start_ms": 0, "end_ms": 500, "text": "hello"}],
}))
`

// bridgeFixture copies the REAL bridge into a scratch tree shaped like the
// repo's (scripts/bridges next to scripts/tools and scripts/services) so the
// bridge resolves the doubling helper and its generated model-registry import.
func bridgeFixture(t *testing.T) string {
	t.Helper()
	realBridge := filepath.Join("..", "..", "..", "..", "scripts", "bridges", "whisper_transcriber.py")
	content, err := os.ReadFile(realBridge)
	if err != nil {
		t.Fatalf("read the real bridge script %s: %v", realBridge, err)
	}
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	files := map[string]string{
		filepath.Join(scripts, "bridges", "whisper_transcriber.py"):  string(content),
		filepath.Join(scripts, "tools", "transcribe_detect_lang.py"): fakeHelperScript,
		// The bridge imports the generated registry at module scope; the stub
		// keeps the model identity out of this contract's subject.
		filepath.Join(scripts, "services", "model_registry_generated.py"): "WHISPER_MODEL_NAME = \"tiny\"\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return filepath.Join(scripts, "bridges", "whisper_transcriber.py")
}

// runBridge executes the bridge with the given argv and returns its stdout JSON.
func runBridge(t *testing.T, bridge string, argv ...string) map[string]any {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; skipping the bridge subprocess contract test")
	}
	cmd := exec.CommandContext(context.Background(), python, append([]string{bridge}, argv...)...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bridge run %v: %v", argv, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("bridge stdout is not JSON: %v (raw: %s)", err, out)
	}
	return payload
}

// TestWhisperBridge_ForwardsRequestedLanguageAndSeparatesDetection is the
// contract: the requested language MUST reach the helper (proven by the helper
// echoing it back as the decode language) and the genuine detection MUST stay a
// different field, with the forced flag set. A bridge that dropped the flag, or
// that echoed it as the detection, fails here rather than silently mislabelling
// every transcript in production.
func TestWhisperBridge_ForwardsRequestedLanguageAndSeparatesDetection(t *testing.T) {
	bridge := bridgeFixture(t)
	payload := runBridge(t, bridge, "--pcm-stdin", "--language", "it")

	if got, _ := payload["language"].(string); got != "it" {
		t.Errorf("language = %q, want %q — the requested language must reach the helper", got, "it")
	}
	if got, _ := payload["detected_language"].(string); got != "en" {
		t.Errorf("detected_language = %q, want %q — the forced echo must never be reported as a detection", got, "en")
	}
	if forced, _ := payload["language_forced"].(bool); !forced {
		t.Error("language_forced = false, want true when --language was passed")
	}
}

// TestWhisperBridge_NoLanguageMeansAutoDetect pins the other half: with no flag
// and no VELOX_WHISPER_LANGUAGE in the environment the bridge must not claim a
// forced language — auto-detection is what makes the caller's language check
// meaningful.
func TestWhisperBridge_NoLanguageMeansAutoDetect(t *testing.T) {
	bridge := bridgeFixture(t)
	if err := os.Unsetenv("VELOX_WHISPER_LANGUAGE"); err != nil {
		t.Fatalf("unset VELOX_WHISPER_LANGUAGE: %v", err)
	}
	payload := runBridge(t, bridge, "--pcm-stdin")

	if forced, _ := payload["language_forced"].(bool); forced {
		t.Error("language_forced = true, want false with no --language and no env default")
	}
	if got, _ := payload["detected_language"].(string); got != "en" {
		t.Errorf("detected_language = %q, want the helper's detection %q", got, "en")
	}
}
