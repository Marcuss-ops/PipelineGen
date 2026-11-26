package rustexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestSemanticEngineRealBinaryServesRepeatedBatchesFromTheCache proves the
// whole Go-side opt-in: NewSemanticEngineExecutor forwards SEMANTIC_CACHE_DIR,
// the worker writes one entry, and the second identical batch reports
// cached:true. It runs only when the release binary is installed, like the
// other real-worker suites.
func TestSemanticEngineRealBinaryServesRepeatedBatchesFromTheCache(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH")
	}
	cacheDir := t.TempDir()
	t.Setenv("SEMANTIC_CACHE_DIR", cacheDir)

	executor := NewSemanticEngineExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	defer executor.Close()
	payload := []byte(`{"operation":"analyze_script","language":"it","scenes":[{"scene_id":"s1","text":"Alberto Stasi e Andrea Sempio sono due nomi. Il processo si e' svolto a Bologna nel 2008."}]}` + "\n")

	run := func() semanticEngineResponse {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		stdout, stderr, err := executor.Run(ctx, payload)
		if err != nil {
			t.Fatalf("analyze_script failed: %v: %s", err, stderr)
		}
		var response semanticEngineResponse
		if err := json.Unmarshal(stdout, &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.OK || response.Manifest == nil {
			t.Fatalf("worker response not ok: %+v", response)
		}
		return response
	}

	if first := run(); first.Cached {
		t.Fatal("the cold run must analyse, not report a cache hit")
	}
	if second := run(); !second.Cached {
		t.Fatal("the repeated identical batch must report cached:true")
	}
	// Two planes coexist under SEMANTIC_CACHE_DIR: one manifest file per
	// request fingerprint at the root, and the per-scene store introduced by
	// DEMOL-10 under scenes/. The repeated batch must hit both, so a second
	// manifest or a second scene entry would mean the cache appended instead
	// of serving the cold result.
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	manifests := 0
	sceneStore := false
	for _, entry := range entries {
		if entry.IsDir() {
			if entry.Name() != "scenes" {
				t.Fatalf("unexpected cache subdirectory %q", entry.Name())
			}
			sceneStore = true
			continue
		}
		manifests++
	}
	if manifests != 1 {
		t.Fatalf("manifest cache entries = %d, want 1", manifests)
	}
	if !sceneStore {
		t.Fatal("per-scene cache store missing under scenes/")
	}
	scenes, err := os.ReadDir(filepath.Join(cacheDir, "scenes"))
	if err != nil {
		t.Fatalf("read scene cache dir: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("scene cache entries = %d, want 1", len(scenes))
	}
}
