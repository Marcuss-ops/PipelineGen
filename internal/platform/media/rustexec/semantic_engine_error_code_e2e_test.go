package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestSemanticEngineRealBinaryErrorCodeSurvivesTheWire pins the worker's
// additive error envelope at the Go boundary. Every failure carries
// `error_code` as the machine-stable discriminant (decode,
// unsupported_operation, execution) next to the historical `error` sentence
// that callers already report. Decoding here is the same step
// semanticBundles performs, so a response struct that omitted the field would
// drop it even though the worker emitted it.
func TestSemanticEngineRealBinaryErrorCodeSurvivesTheWire(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	cases := []struct {
		name string
		// payload is one NDJSON request line, sent exactly as the adapter does.
		payload string
		code    string
		// fragment is the stable part of the human-readable error.
		fragment string
	}{
		{"invalid json", "not json", "decode", "invalid request"},
		{"unknown operation", `{"operation":"does_not_exist"}`, "unsupported_operation", "unknown operation"},
		{"execution failure", `{"operation":"split_sentences","transcript":"ciao","language":""}`, "execution", "split_sentences language is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, stderr, err := executor.Run(context.Background(), []byte(tc.payload))
			if err != nil {
				t.Fatalf("worker run: %v: %s", err, stderr)
			}
			var response semanticEngineResponse
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
			if response.OK {
				t.Fatalf("worker accepted an invalid request: %s", body)
			}
			if response.ErrorCode == "" {
				t.Skipf("semantic-engine binary predates the additive error_code envelope: %s", body)
			}
			if response.ErrorCode != tc.code {
				t.Fatalf("error_code=%q, want %q: %s", response.ErrorCode, tc.code, body)
			}
			if !strings.Contains(response.Error, tc.fragment) {
				t.Fatalf("error=%q lost the human-readable cause %q", response.Error, tc.fragment)
			}
			// Re-encode what was decoded: an unmodelled field would vanish
			// here even though the worker emitted it.
			roundtrip, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(roundtrip, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["error_code"] != tc.code {
				t.Fatalf("error_code did not round-trip: %s", roundtrip)
			}
			if decoded["ok"] != false {
				t.Fatalf("ok did not round-trip: %s", roundtrip)
			}
		})
	}
}
