package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validPreflightPayload = `{
  "version": 2,
  "preset": "custom",
  "items": [
    {
      "id": "preflight-1",
      "title": "Preflight",
      "language": "it",
      "script_params": {"target_words": 150},
      "source": {"type": "text", "topic": "preflight", "source_text": "testo di prova"}
    }
  ]
}`

func writePreflightPayload(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

func TestPreflightAcceptsCanonicalEnvelope(t *testing.T) {
	path := writePreflightPayload(t, validPreflightPayload)
	code, out, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path})
	})
	if code != exitOK {
		t.Fatalf("exit=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "preflight: ok") || !strings.Contains(out, "1 item(s)") {
		t.Fatalf("output = %q", out)
	}
}

func TestPreflightAcceptsCanonicalEnvelopeAsJSON(t *testing.T) {
	path := writePreflightPayload(t, validPreflightPayload)
	code, out, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path, "--json"})
	})
	if code != exitOK {
		t.Fatalf("exit=%d out=%q err=%q", code, out, errOut)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out)
	}
	if report["ok"] != true || report["items"] != float64(1) {
		t.Fatalf("report = %+v", report)
	}
}

func TestPreflightRejectsWrongVersion(t *testing.T) {
	path := writePreflightPayload(t, `{"version":1,"items":[{"source":{"type":"text","source_text":"x"}}]}`)
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (err=%q)", code, exitFailure, errOut)
	}
	if !strings.Contains(errOut, "version must be 2") {
		t.Fatalf("error = %q, want the canonical validator detail", errOut)
	}
}

// TestPreflightRejectsFinalJobWithoutRender pins the precedence the server
// already enforces at ingress: final_job=true with render disabled is a 400,
// not something to discover after generation.
func TestPreflightRejectsFinalJobWithoutRender(t *testing.T) {
	path := writePreflightPayload(t, `{
	  "version": 2,
	  "final_job": true,
	  "items": [{
	    "id": "f1", "language": "it",
	    "script_params": {"target_words": 150},
	    "source": {"type": "text", "source_text": "x"}
	  }]
	}`)
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (err=%q)", code, exitFailure, errOut)
	}
	if !strings.Contains(errOut, "output.render.enabled must be true") {
		t.Fatalf("error = %q", errOut)
	}
}

// TestPreflightRejectsUnknownField mirrors DisallowUnknownFields: a retired
// key is a 400 at ingress, never silently ignored.
func TestPreflightRejectsUnknownField(t *testing.T) {
	path := writePreflightPayload(t, `{
	  "version": 2,
	  "assemble_final": true,
	  "items": [{
	    "id": "f1", "language": "it",
	    "script_params": {"target_words": 150},
	    "source": {"type": "text", "source_text": "x"}
	  }]
	}`)
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (err=%q)", code, exitFailure, errOut)
	}
	if !strings.Contains(errOut, "unknown field") {
		t.Fatalf("error = %q, want the strict decoder's unknown-field error", errOut)
	}
}

// TestPreflightJSONFailureExitsNonZero pins the 2026-11-23 closeout bug: the
// JSON body said ok:false but the process exited 0, so any script using the
// command as a gate accepted a payload the server would reject with 400.
func TestPreflightJSONFailureExitsNonZero(t *testing.T) {
	path := writePreflightPayload(t, `{
	  "version": 2,
	  "items": [{
	    "id": "f1", "language": "it",
	    "script_params": {"target_words": 150},
	    "source": {"type": "text", "source_text": "x"}
	  }],
	  "invented_field": true
	}`)
	code, out, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path, "--json"})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (out=%q err=%q)", code, exitFailure, out, errOut)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out)
	}
	if report["ok"] != false {
		t.Fatalf("ok = %v, want false (%q)", report["ok"], out)
	}
}

// TestPreflightCatchesMisfiledItemField is the 2026-11-23 r41 payload bug: the
// key exists in the contract but was written under script_params (where it does
// not belong) while the item root already carried it. The server answers 400;
// the preflight must say so before the submit.
func TestPreflightCatchesMisfiledItemField(t *testing.T) {
	path := writePreflightPayload(t, `{
	  "version": 2,
	  "items": [{
	    "id": "f1", "language": "it",
	    "max_overlays_per_scene": 10,
	    "script_params": {"target_words": 150, "max_overlays_per_scene": 10},
	    "source": {"type": "text", "source_text": "x"}
	  }]
	}`)
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (err=%q)", code, exitFailure, errOut)
	}
	if !strings.Contains(errOut, "unknown field") || !strings.Contains(errOut, "different nesting") {
		t.Fatalf("error = %q, want the unknown-field hint", errOut)
	}
}

func TestPreflightRejectsInvalidJSON(t *testing.T) {
	path := writePreflightPayload(t, `{not json`)
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (err=%q)", code, exitFailure, errOut)
	}
	if !strings.Contains(errOut, "preflight: FAIL") {
		t.Fatalf("error = %q", errOut)
	}
}

// TestPreflightWithoutTokenReportsMissingTokenOnce pins the 2026-11-23 r42
// experience: without a token every asset request answers 401, and the report
// printed the same "token non valido" line once per clip. The missing
// credential is one fact, so it must be one line that names the cause.
func TestPreflightWithoutTokenReportsMissingTokenOnce(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "")
	path := writePreflightPayload(t, `{
	  "version": 2,
	  "items": [{
	    "id": "f1", "language": "it",
	    "script_params": {
	      "target_words": 150,
	      "segments": [
	        {"id": "s1", "clip_ids": ["clip-a"]},
	        {"id": "s2", "clip_ids": ["clip-b"]}
	      ]
	    },
	    "source": {"type": "text", "source_text": "x"}
	  }]
	}`)
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path, "--min-free-gb", "0"})
	})
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d (err=%q)", code, exitFailure, errOut)
	}
	if got := strings.Count(errOut, "token API mancante"); got != 1 {
		t.Fatalf("missing-token line printed %d times, want 1 (err=%q)", got, errOut)
	}
	if strings.Contains(errOut, "token non valido") {
		t.Fatalf("unreachable server must not be blamed on a rejected token: %q", errOut)
	}
}

// TestCheckAssetsCollapsesAuthRejection covers the other half: the token is
// present but the catalog refuses it, so every clip reports the same 401 and
// the verdict must collapse to a single credential problem.
func TestCheckAssetsCollapsesAuthRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	checks := checkAssets(context.Background(), server.URL, "stale-token", []clipRequirement{
		{ClipID: "clip-a", NeedSpeech: true},
		{ClipID: "clip-b", NeedSpeech: true},
	})
	rejected := assetAuthRejection(checks)
	if rejected == "" {
		t.Fatalf("401 answers were not recognised as a credential problem: %+v", checks)
	}
	if !strings.Contains(rejected, "2 clip(s) su 2") || !strings.Contains(rejected, "VELOX_ADMIN_TOKEN") {
		t.Fatalf("collapsed message = %q", rejected)
	}
	for _, check := range checks {
		if check.OK || !check.authRejected {
			t.Fatalf("check %q: ok=%t authRejected=%t", check.ClipID, check.OK, check.authRejected)
		}
	}
}

// TestPreflightOfflineDoesNotClaimVerifiedClips pins the false green found
// while re-running the r42 payload with --offline: the note said "non
// verificati" and the summary line right below claimed "22 clip(s) verificati".
func TestPreflightOfflineDoesNotClaimVerifiedClips(t *testing.T) {
	path := writePreflightPayload(t, `{
	  "version": 2,
	  "items": [{
	    "id": "f1", "language": "it",
	    "script_params": {
	      "target_words": 150,
	      "segments": [{"id": "s1", "clip_ids": ["clip-a"]}]
	    },
	    "source": {"type": "text", "source_text": "x"}
	  }]
	}`)
	code, out, errOut := capture(t, func() int {
		return run([]string{"preflight", "--payload", path, "--offline", "--min-free-gb", "0", "--json"})
	})
	if code != exitOK {
		t.Fatalf("exit=%d, want %d (out=%q err=%q)", code, exitOK, out, errOut)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out)
	}
	if report["offline"] != true {
		t.Fatalf("report must record the skipped catalog check: %+v", report)
	}
	code, out, _ = capture(t, func() int {
		return run([]string{"preflight", "--payload", path, "--offline", "--min-free-gb", "0"})
	})
	if code != exitOK || !strings.Contains(out, "non verificati (--offline)") {
		t.Fatalf("exit=%d out=%q, want the offline wording", code, out)
	}
	if strings.Contains(out, "clip(s) verificati") {
		t.Fatalf("offline run claims verified clips: %q", out)
	}
}

func TestPreflightRequiresPayloadFlag(t *testing.T) {
	code, _, errOut := capture(t, func() int {
		return run([]string{"preflight"})
	})
	if code != exitUsage {
		t.Fatalf("exit=%d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut, "--payload FILE is required") {
		t.Fatalf("error = %q", errOut)
	}
}
