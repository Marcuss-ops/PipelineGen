package rustexec

import (
	"strings"
	"testing"
)

// TestWorkerFailureCarriesTheErrorCode pins the operator-facing rendering of a
// worker failure: the machine-stable error_code is a bracketed tag so failures
// can be filtered by class, while the worker's own sentence stays intact. A
// response without a code (worker predating the additive envelope) keeps the
// historical message byte-for-byte.
func TestWorkerFailureCarriesTheErrorCode(t *testing.T) {
	cases := []struct {
		name     string
		response semanticEngineResponse
		want     string
	}{
		{
			name:     "decode",
			response: semanticEngineResponse{ErrorCode: "decode", Error: "invalid request: expected ident"},
			want:     "semantic-engine: worker [decode]: invalid request: expected ident",
		},
		{
			name:     "unsupported operation",
			response: semanticEngineResponse{ErrorCode: "unsupported_operation", Error: "unknown operation: nope"},
			want:     "semantic-engine: worker [unsupported_operation]: unknown operation: nope",
		},
		{
			name:     "execution",
			response: semanticEngineResponse{ErrorCode: "execution", Error: "split_sentences language is required\n"},
			want:     "semantic-engine: worker [execution]: split_sentences language is required",
		},
		{
			name:     "legacy worker without a code",
			response: semanticEngineResponse{Error: "invalid request"},
			want:     "semantic-engine: worker: invalid request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := workerFailure(tc.response)
			if err == nil {
				t.Fatal("worker failure returned no error")
			}
			if err.Error() != tc.want {
				t.Fatalf("error=%q, want %q", err.Error(), tc.want)
			}
			if code := tc.response.ErrorCode; code != "" && !strings.Contains(err.Error(), "["+code+"]") {
				t.Fatalf("error=%q does not expose the filterable code %q", err.Error(), code)
			}
		})
	}
}
