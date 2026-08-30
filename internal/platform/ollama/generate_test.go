package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// TestResolveGenerationFormat is the canonical SSOT for the PR-3
// wire-shape decision tree. It locks the 6 logical cases of the
// 2x2 grid ({OutputModePlainText, OutputModeScriptV1, ""} x
// {caller-supplied Format, empty Format}) via table-driven sub-tests.
//
// godlike/06 SSOT: this test is the SOLE contract surface for the
// Ollama Format wire-shape — the helper is unexported, the production
// call site is in (*Generator).GenerateScript, and any regression
// here would surface as a Hermes-test failure.
func TestResolveGenerationFormat(t *testing.T) {
	callerFormat := json.RawMessage(`"xml"`)
	scriptV1JSON := json.RawMessage(`"json"`)
	cases := []struct {
		name       string
		outputMode types.OutputMode
		format     json.RawMessage
		want       json.RawMessage
	}{
		// PlainText (canonical post-PR-2) — never forces JSON-mode
		{"PlainText + no caller Format => nil",
			types.OutputModePlainText, nil, nil},
		{"PlainText + caller Format => caller (verbatim)",
			types.OutputModePlainText, callerFormat, callerFormat},

		// ScriptV1 (legacy backward-compat) — auto-fills "json"
		{"ScriptV1 + no caller Format => auto-fill quoted json",
			types.OutputModeScriptV1, nil, scriptV1JSON},
		{"ScriptV1 + caller Format => caller (verbatim passthrough)",
			types.OutputModeScriptV1, callerFormat, callerFormat},

		// Empty OutputMode (legacy default) — no JSON-mode constraint
		{"empty OutputMode + no caller Format => nil",
			"", nil, nil},
		{"empty OutputMode + caller Format => caller (verbatim)",
			"", callerFormat, callerFormat},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := types.TextGenerationRequest{
				OutputMode: tc.outputMode,
				Format:     tc.format,
			}
			got := resolveGenerationFormat(req)
			if !bytesEqualRawMessage(got, tc.want) {
				t.Errorf("OutputMode=%q Format=%q: got %q, want %q",
					tc.outputMode, string(tc.format), string(got), string(tc.want))
			}
		})
	}
}

func TestGenerateScriptForwardsExplicitModelOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model   string         `json:"model"`
			Options map[string]any `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}
		if body.Model != "gemma2:2b" {
			t.Errorf("chat model=%q, want explicit request model", body.Model)
		}
		if got := body.Options["num_ctx"]; got != float64(2048) {
			t.Errorf("chat num_ctx=%v, want 2048 for a short scene", got)
		}
		if got := body.Options["num_predict"]; got != float64(96) {
			t.Errorf("chat num_predict=%v, want 96 for a short scene", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Testo breve"},"done":true}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e4b", 5))
	result, err := gen.GenerateScript(context.Background(), types.TextGenerationRequest{
		Model: "gemma2:2b", Language: "it", Title: "test", Prompt: "scrivi una frase",
		SourceText: "testo", MaxChars: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Script != "Testo breve" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestGenerateScriptRecordsCanonicalOperation certifies the provider-owned
// inference boundary: when a Run is bound to ctx, GenerateScript must emit the
// canonical ollama/generate operation with a positive duration and completed
// status (so script_gemma stops being unmeasured on real jobs).
func TestGenerateScriptRecordsCanonicalOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The canonical-operation assertion below requires a strictly positive
		// DurationMs, but a local httptest roundtrip routinely completes in
		// under a millisecond (Milliseconds() rounds to 0). Hold the response
		// briefly so the recorded duration is measurably non-zero.
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Testo breve"},"done":true}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e4b", 5))

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	if _, err := gen.GenerateScript(ctx, types.TextGenerationRequest{
		Model: "gemma2:2b", Language: "it", Title: "test", Prompt: "scrivi una frase",
		SourceText: "testo", MaxChars: 100,
	}); err != nil {
		t.Fatal(err)
	}
	run.Finish()

	var found bool
	for _, op := range run.Report().Operations {
		if op.Component == string(kernobs.ComponentOllama) && op.Operation == string(kernobs.OperationGenerate) {
			found = true
			if op.DurationMs <= 0 {
				t.Errorf("generate operation duration = %d, want > 0", op.DurationMs)
			}
			if op.Status != kernobs.StageStatusCompleted {
				t.Errorf("generate operation status = %q, want completed", op.Status)
			}
			if op.Stage != string(kernobs.StageGenerate) {
				t.Errorf("generate operation stage = %q, want %q", op.Stage, kernobs.StageGenerate)
			}
		}
	}
	if !found {
		t.Fatal("expected an ollama/generate operation in the run report")
	}
}

// TestGenerateScriptAttachesOperationMeta certifies the fan-out convergence:
// when a caller binds OperationMeta (segment_id / worker_id / queued_at) to
// ctx, the canonical ollama/generate operation carries those facts — so a
// parallel fan-out is reconstructable from run_operation_observations alone,
// without the caller re-timing the inference boundary.
func TestGenerateScriptAttachesOperationMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Testo breve"},"done":true,"load_duration":45000000000,"prompt_eval_count":120,"prompt_eval_duration":300000000,"eval_count":340,"eval_duration":2000000000,"total_duration":48000000000}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e4b", 5))

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	ctx = kernobs.WithOperationMeta(ctx, kernobs.OperationMeta{
		WorkerID: "seg-worker-3",
		QueuedAt: time.Now().Add(-10 * time.Millisecond),
		Metadata: map[string]string{"segment_id": "segment-2", "segment_index": "2"},
	})

	if _, err := gen.GenerateScript(ctx, types.TextGenerationRequest{
		Model: "gemma2:2b", Language: "it", Title: "test", Prompt: "scrivi una frase",
		SourceText: "testo", MaxChars: 100,
	}); err != nil {
		t.Fatal(err)
	}
	run.Finish()

	var found bool
	for _, op := range run.Report().Operations {
		if op.Component != string(kernobs.ComponentOllama) || op.Operation != string(kernobs.OperationGenerate) {
			continue
		}
		found = true
		if op.WorkerID != "seg-worker-3" {
			t.Errorf("worker_id = %q, want seg-worker-3", op.WorkerID)
		}
		if op.QueuedAt.IsZero() {
			t.Error("queued_at not attached")
		}
		if op.QueueWaitMs <= 0 {
			t.Errorf("queue_wait_ms = %d, want > 0", op.QueueWaitMs)
		}
		// The Ollama-reported split facts must be merged into metadata_json
		// alongside the fan-out provenance (owner-measured, no second timer).
		var meta map[string]any
		if err := json.Unmarshal([]byte(op.MetadataJSON), &meta); err != nil {
			t.Fatalf("metadata_json %q is not a JSON object: %v", op.MetadataJSON, err)
		}
		if meta["segment_id"] != "segment-2" {
			t.Errorf("metadata segment_id = %q, want segment-2", meta["segment_id"])
		}
		if meta["segment_index"] != "2" {
			t.Errorf("metadata segment_index = %q, want 2", meta["segment_index"])
		}
		if meta["input_tokens"] != float64(120) {
			t.Errorf("metadata input_tokens = %v, want 120", meta["input_tokens"])
		}
		if meta["output_tokens"] != float64(340) {
			t.Errorf("metadata output_tokens = %v, want 340", meta["output_tokens"])
		}
		if meta["model_load_ms"] != float64(45000) {
			t.Errorf("metadata model_load_ms = %v, want 45000 (cold start)", meta["model_load_ms"])
		}
		if meta["inference_work_ms"] != float64(2300) {
			t.Errorf("metadata inference_work_ms = %v, want 2300 (300ms prompt eval + 2000ms eval)", meta["inference_work_ms"])
		}
		if meta["cold_start"] != true {
			t.Errorf("metadata cold_start = %v, want true (45s model load)", meta["cold_start"])
		}
	}
	if !found {
		t.Fatal("expected an ollama/generate operation in the run report")
	}
}

// bytesEqualRawMessage compares two json.RawMessage values, treating
// nil and empty []byte identically (json.RawMessage semantics: both
// serialize to "null" on wire via omitempty).
func bytesEqualRawMessage(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return string(a) == string(b)
}

// TestResolveGenerationFormat_NoInputMutation is the caller-isolation
// smoke test for the resolveGenerationFormat pure-function contract.
// The current value-parameter signature makes helper-side mutation
// of the caller's `req` STRUCTURALLY IMPOSSIBLE (Go passes a copy).
// This test surfaces only caller-observable mutation: if a future
// refactor flips the parameter to *types.TextGenerationRequest AND the
// helper writes back to req.Format (in addition to returning), the
// test sees req.Format post-call differ from the original — failing.
//
// This test does NOT verify that the helper's internal local copy is
// not mutated (Go value semantics make that observable-only-from-the-helper
// check impossible from external code). It only catches the externally-
// observable case where the helper somehow propagates a mutation to the
// caller's variable.
//
// godlike/06 SSOT: this test is the SOLE contract seam for the
// caller-isolation invariant.
func TestResolveGenerationFormat_NoInputMutation(t *testing.T) {
	originalFormat := json.RawMessage(`"xml"`)
	req := types.TextGenerationRequest{
		OutputMode: types.OutputModeScriptV1,
		Format:     originalFormat,
	}
	got := resolveGenerationFormat(req)
	if !bytesEqualRawMessage(got, originalFormat) {
		t.Fatalf("helper changed caller Format: got %q, want %q",
			string(got), string(originalFormat))
	}
	if !bytesEqualRawMessage(req.Format, originalFormat) {
		t.Fatalf("helper MUTATED input: req.Format before=%q, after=%q",
			string(originalFormat), string(req.Format))
	}
}
