package rustexec

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Scratch diagnostic: dump the real worker's manifest so the entity ->
// identity reference mismatch can be inspected instead of guessed.
func TestZZScratchDumpManifest(t *testing.T) {
	binary := os.Getenv("VELOX_RUST_SEMANTIC_ENGINE_PATH")
	if binary == "" {
		binary = resolveSemanticEngineBinary()
	}
	if binary == "" {
		t.Skip("semantic-engine release binary not installed")
	}
	text := "Mike Tyson was already one of the most recognizable figures in American sports. He won the heavyweight title in 1986 and lost it in 1990."
	payload, err := json.Marshal(map[string]any{
		"operation": "analyze_script",
		"language":  "en",
		"scenes":    []map[string]any{{"scene_id": "scene", "text": text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	executor := NewSemanticEngineExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	defer executor.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	stdout, stderr, err := executor.Run(ctx, payload)
	if err != nil {
		t.Fatalf("analyze_script failed: %v: %s", err, stderr)
	}
	var response semanticEngineResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !response.OK || response.Manifest == nil {
		t.Fatalf("not ok: %+v", response)
	}
	for _, scene := range response.Manifest.Scenes {
		known := map[string]string{}
		for _, identity := range scene.Identities {
			known[identity.ID] = identity.Kind + "/" + identity.CanonicalSurface
		}
		t.Logf("scene %s identities=%d entities=%d", scene.SceneID, len(scene.Identities), len(scene.Entities))
		for _, entity := range scene.Entities {
			kind, ok := known[entity.ID]
			t.Logf("  entity %-20s type=%-16s identity=%-10s known=%v %s", entity.Text, entity.Kind, entity.ID, ok, kind)
		}
		for id, kind := range known {
			t.Logf("  identity %-10s %s", id, kind)
		}
	}
}
