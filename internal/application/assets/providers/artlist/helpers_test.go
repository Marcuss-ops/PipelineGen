package artlist

import (
	"testing"

	"go.uber.org/zap"
)

// baseServiceDeps fills in the artlist ServiceDeps defaults that are
// required by NewService but that most tests do not care about.
//
// Tests can pass a partially populated ServiceDeps literal and then
// override only the fields they care about. New mandatory ports are
// added here, so future required dependencies only need to be updated
// in one place.
func baseServiceDeps(t testing.TB, deps ServiceDeps) ServiceDeps {
	t.Helper()

	if deps.ServiceDependencies.Infra.Log == nil {
		deps.ServiceDependencies.Infra.Log = zap.NewNop()
	}

	// Mandatory ports that must be non-nil for NewService.
	if deps.Publisher == nil {
		deps.Publisher = &stubPublisherForArtlist{}
	}
	if deps.RunRepository == nil {
		deps.RunRepository = &stubRunRepoForArtlist{}
	}
	if deps.Transcriber == nil {
		deps.Transcriber = &stubTranscriber{}
	}
	if deps.ServiceDependencies.Repos.TextTrackRepo == nil {
		deps.ServiceDependencies.Repos.TextTrackRepo = &stubTextTrackRepo{}
	}

	return deps
}

func TestGetIntFromResult(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]any
		key      string
		expected int
	}{
		{"nil map", nil, "key", 0},
		{"empty map", map[string]any{}, "key", 0},
		{"int value", map[string]any{"key": 42}, "key", 42},
		{"float64 value", map[string]any{"key": float64(42)}, "key", 42},
		{"string value", map[string]any{"key": "not_a_number"}, "key", 0},
		{"missing key", map[string]any{"other": 1}, "key", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIntFromResult(tt.m, tt.key)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestRunTagResponseStructure(t *testing.T) {
	resp := &RunTagResponse{
		OK:     true,
		Status: "completed",
		RunID:  "run-123",
	}
	if !resp.OK {
		t.Error("expected OK to be true")
	}
	if resp.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", resp.Status)
	}
}
