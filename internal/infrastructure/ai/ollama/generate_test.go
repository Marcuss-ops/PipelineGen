package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
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
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}
		if body.Model != "gemma2:2b" {
			t.Errorf("chat model=%q, want explicit request model", body.Model)
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
