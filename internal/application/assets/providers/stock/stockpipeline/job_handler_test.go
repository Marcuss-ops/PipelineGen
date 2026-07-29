// Package stockpipeline — job_handler_test.go (P6 test coverage, July 2026).
//
// TDD coverage for StockJobResult.ToResultMap(): round-trip with
// all fields populated, omitempty semantics for FinalizationStatus
// (empty vs non-empty) and FinalizationCompletedAt (zero vs non-zero),
// nil Manifest, empty Chunks, and ManifestKey wire-constant assertion.
package stockpipeline

import (
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestStockJobResult_ToResultMap_AllFieldsPopulated verifies that every
// field survives the round-trip through ToResultMap() with the correct
// key in the result map and the correct typed value.
func TestStockJobResult_ToResultMap_AllFieldsPopulated(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		JobID:         "test-job-123",
		WorkflowID:    "wf-roundtrip",
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	r := StockJobResult{
		Manifest:                manifest,
		FinalStatus:             "SUCCEEDED",
		TotalClips:              42,
		TotalChunks:             7,
		Chunks:                  []ChunkResult{{Index: 1, TimelineStart: 0, TimelineEnd: 25.0}},
		MetadataLink:            "https://drive.example.com/metadata.json",
		MetadataFileID:          "abc123def456",
		FinalizationStatus:      "completed",
		FinalizationCompletedAt: now,
	}

	m := r.ToResultMap()

	// ── Always-present fields ──────────────────────────────────
	if got, ok := m[job.ManifestKey].(*job.ArtifactManifest); !ok {
		t.Errorf("key %q missing or wrong type: %T", job.ManifestKey, m[job.ManifestKey])
	} else if got != manifest {
		t.Errorf("Manifest pointer mismatch: got %p, want %p (same instance must survive)", got, manifest)
	}

	assertString(t, m, "final_status", "SUCCEEDED")
	assertInt(t, m, "total_clips", 42)
	assertInt(t, m, "total_chunks", 7)

	if chunks, ok := m["chunks"].([]ChunkResult); !ok {
		t.Errorf("chunks missing or wrong type: %T", m["chunks"])
	} else if len(chunks) != 1 || chunks[0].Index != 1 {
		t.Errorf("chunks = %+v, want 1 entry with Index=1", chunks)
	}

	assertString(t, m, "metadata_link", "https://drive.example.com/metadata.json")
	assertString(t, m, "metadata_file_id", "abc123def456")

	// ── omitempty-populated fields (non-zero) ──────────────────
	assertString(t, m, "__finalization_status", "completed")

	fc, ok := m["__finalization_completed_at"].(time.Time)
	if !ok {
		t.Errorf("__finalization_completed_at missing or wrong type: %T", m["__finalization_completed_at"])
	} else if !fc.Equal(now) {
		t.Errorf("__finalization_completed_at = %v, want %v", fc, now)
	}
}

// TestStockJobResult_ToResultMap_OmitemptyFinalizationStatus verifies
// that an empty FinalizationStatus is omitted from the result map
// (omitempty contract: zero-value string → key absent).
func TestStockJobResult_ToResultMap_OmitemptyFinalizationStatus(t *testing.T) {
	r := StockJobResult{
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         "omitempty-test",
		},
		FinalStatus:        "INDEX_PENDING",
		FinalizationStatus: "", // zero value — must be omitted
	}
	m := r.ToResultMap()

	if _, exists := m["__finalization_status"]; exists {
		t.Errorf("__finalization_status key present for empty FinalizationStatus (omitempty violation)")
	}
	assertString(t, m, "final_status", "INDEX_PENDING")
}

// TestStockJobResult_ToResultMap_OmitemptyFinalizationCompletedAt verifies
// that a zero time.Time is omitted from the result map.
func TestStockJobResult_ToResultMap_OmitemptyFinalizationCompletedAt(t *testing.T) {
	r := StockJobResult{
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         "zero-time-test",
		},
		FinalStatus:             "SUCCEEDED",
		FinalizationStatus:      "done",
		FinalizationCompletedAt: time.Time{}, // zero value — must be omitted
	}
	m := r.ToResultMap()

	if _, exists := m["__finalization_completed_at"]; exists {
		t.Errorf("__finalization_completed_at key present for zero time (omitempty violation)")
	}
	assertString(t, m, "__finalization_status", "done")
	assertString(t, m, "final_status", "SUCCEEDED")
}

// TestStockJobResult_ToResultMap_NilManifest verifies that a nil
// Manifest does not panic and is correctly represented as nil in
// the result map.
func TestStockJobResult_ToResultMap_NilManifest(t *testing.T) {
	r := StockJobResult{
		Manifest:    nil,
		FinalStatus: "FAILED",
	}
	m := r.ToResultMap()

	v, ok := m[job.ManifestKey]
	if !ok {
		t.Errorf("key %q missing (nil manifest must still be present)", job.ManifestKey)
	}
	// Go interface-footgun: interface{}((*T)(nil)) != nil.
	// Use a type assertion to unwrap the typed nil value.
	if mv, ok := v.(*job.ArtifactManifest); !ok || mv != nil {
		t.Errorf("key %q = %v (%T), want nil *ArtifactManifest", job.ManifestKey, v, v)
	}
	assertString(t, m, "final_status", "FAILED")
}

// TestStockJobResult_ToResultMap_EmptyChunks verifies that a nil
// Chunks slice is correctly represented in the result map.
func TestStockJobResult_ToResultMap_EmptyChunks(t *testing.T) {
	r := StockJobResult{
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
		},
		FinalStatus: "SUCCEEDED",
		Chunks:      nil,
	}
	m := r.ToResultMap()

	v, ok := m["chunks"]
	if !ok {
		t.Errorf("chunks key missing (nil chunks must still be present)")
	}
	// Go interface-footgun: interface{}(([]T)(nil)) != nil.
	// Use a type assertion to unwrap the typed nil slice.
	if chunks, ok := v.([]ChunkResult); !ok || chunks != nil {
		t.Errorf("chunks = %v (%T), want nil []ChunkResult", v, v)
	}
}

// TestStockJobResult_ToResultMap_ManifestKeyConstant verifies that
// job.ManifestKey is "__artifact_manifest" — the wire key the
// broker's downstream runner reads per domain/job.ManifestKey.
func TestStockJobResult_ToResultMap_ManifestKeyConstant(t *testing.T) {
	if job.ManifestKey != "__artifact_manifest" {
		t.Errorf("job.ManifestKey = %q, want %q (wire-format contract: broker runner reads this key)",
			job.ManifestKey, "__artifact_manifest")
	}
}

// TestStockJobResult_ToResultMap_AllFieldsZero verifies the zero-value
// round-trip: all fields at their zero value, only the 7 always-present
// keys exist in the map.
func TestStockJobResult_ToResultMap_AllFieldsZero(t *testing.T) {
	r := StockJobResult{}
	m := r.ToResultMap()

	// 7 always-present keys
	wantKeys := 7
	if len(m) != wantKeys {
		t.Errorf("map has %d keys, want %d (zero-valued omitempty fields must be absent): %v", len(m), wantKeys, keysOf(m))
	}

	// omitempty fields must be absent
	for _, omitKey := range []string{"__finalization_status", "__finalization_completed_at"} {
		if _, exists := m[omitKey]; exists {
			t.Errorf("omitempty key %q present for zero-valued struct", omitKey)
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────────────

func assertString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, ok := m[key].(string)
	if !ok {
		t.Errorf("key %q missing or wrong type: %T, want string = %q", key, m[key], want)
		return
	}
	if got != want {
		t.Errorf("key %q = %q, want %q", key, got, want)
	}
}

func assertInt(t *testing.T, m map[string]any, key string, want int) {
	t.Helper()
	got, ok := m[key].(int)
	if !ok {
		t.Errorf("key %q missing or wrong type: %T, want int = %d", key, m[key], want)
		return
	}
	if got != want {
		t.Errorf("key %q = %d, want %d", key, got, want)
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
