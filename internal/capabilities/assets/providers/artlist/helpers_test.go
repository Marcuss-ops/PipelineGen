package artlist

import (
	testsupport "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry/testsupport"
	"testing"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	mediacommitadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit/adapters"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
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

	if deps.Infra.Log == nil {
		deps.Infra.Log = zap.NewNop()
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
	if deps.Repos.TextTrackRepo == nil {
		deps.Repos.TextTrackRepo = &stubTextTrackRepo{}
	}
	// MEDIA-SSOT P1-5 (September 2026): the PRODUCTION SQLite searcher bridge
	// and its `NewSQLiteSearcher(s.assetStore)` fallback in buildSearcherChain
	// were deleted, so the local Artlist catalog searcher is now an explicit
	// injection point (composition wires the PostgreSQL-backed one). These
	// fixture tests build a service around an in-memory SQLite AssetStore, so
	// they must opt into the test-only searcher explicitly instead of relying
	// on a production fallback that no longer exists.
	if deps.LocalSearcher == nil && deps.AssetStore != nil {
		deps.LocalSearcher = NewDBSearcher(deps.AssetStore)
	}
	if deps.Infra.MainDB != nil {
		box := outboxevents.NewRepository(deps.Infra.MainDB)
		assetCommitter := testsupport.NewSQLiteAssetCommitter(deps.Infra.MainDB, box, deps.Infra.Log)
		deps.Finalizer.AssetFinalizerTx = assetfinalizer.NewAssetTxFinalizer(
			deps.Infra.Log,
			assetCommitter,
		)
		if repo, ok := deps.AssetStore.(*sqassets.ClipsRepository); ok {
			mediacommitadapters.WireCanonicalAssetStore(repo.AssetStoreSQLite, assetCommitter)
		}
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
