package artlist

import (
	"testing"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

// baseServiceDeps fills in the artlist ServiceDeps defaults that are
// required by NewService but that most tests do not care about.
//
// Tests can pass a partially populated ServiceDeps literal and then
// override only the fields they care about. New mandatory ports are
// added here, so future required dependencies only need to be updated
// in one place.
//
// The helper receives a value, mutates its local copy, and returns it.
// Callers must use the returned value; the original argument is not
// modified. Only the fail-closed ports (Publisher, RunRepository,
// Transcriber, TextTrackRepo) and the default logger are filled.
// AssetStore and other optional ports are NOT defaulted here; tests
// must supply them explicitly when they matter.
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
	if deps.ServiceDependencies.Infra.MainDB != nil {
		deps.ServiceDependencies.Finalizer.AssetFinalizerTx = assetfinalizer.NewAssetTxFinalizer(
			deps.ServiceDependencies.Infra.Log,
			sqassets.NewSQLiteAssetCommitter(deps.ServiceDependencies.Infra.MainDB, outboxevents.NewRepository(deps.ServiceDependencies.Infra.MainDB), deps.ServiceDependencies.Infra.Log),
		)
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
