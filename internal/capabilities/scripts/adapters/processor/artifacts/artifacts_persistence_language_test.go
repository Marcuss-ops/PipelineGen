package artifacts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPersistGeneratedArtifactsSerializesLocalizedScriptProjection(t *testing.T) {
	jobID := "localized-script-json-test"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID)) })
	result := &script.GenerationResult{
		Language: "it",
		Output: script.ScriptOutput{
			Text:      "La storia italiana.",
			WordCount: 3,
			SpecScene: script.SpecSceneOutput{Version: 1, Scenes: []script.SpecScene{{ID: "scene-1", Index: 0, Text: "La storia italiana."}}},
		},
	}
	if _, err := PersistGeneratedArtifacts(context.Background(), jobID, result, nil); err != nil {
		t.Fatalf("PersistGeneratedArtifacts: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workspaceOutputDir(jobID), "script.json"))
	if err != nil {
		t.Fatalf("read script.json: %v", err)
	}
	var got struct {
		Language string `json:"language"`
		Output   struct {
			Text      string `json:"text"`
			SpecScene struct {
				Scenes []struct {
					Text string `json:"text"`
				} `json:"scenes"`
			} `json:"specscene"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode script.json: %v", err)
	}
	if got.Language != "it" || got.Output.Text != "La storia italiana." || len(got.Output.SpecScene.Scenes) != 1 || got.Output.SpecScene.Scenes[0].Text != "La storia italiana." {
		t.Fatalf("script.json did not preserve the localized projection: %+v", got)
	}
}
