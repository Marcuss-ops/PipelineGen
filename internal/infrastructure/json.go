// Package testutil provides shared test helpers for the velox codebase.
package platform

import (
	"encoding/json"
	"testing"
)

// MustMarshalJSON marshals v to JSON or fails the test.
// Replaces duplicated mustJSON/mustMarshal helpers across test files.
func MustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	return data
}
