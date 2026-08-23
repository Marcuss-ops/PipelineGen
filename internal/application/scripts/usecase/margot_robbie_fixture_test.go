package usecase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMargotRobbieFixtureHasTenDescriptionGroundedClips(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "tests", "fixtures", "script-generation", "margot-robbie-10-correct.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Margot fixture: %v", err)
	}

	var envelope struct {
		Items []struct {
			Source struct {
				ClipIDs []string `json:"clip_ids"`
			} `json:"source"`
			ScriptParams struct {
				Segments []struct {
					SourceText string   `json:"source_text"`
					ClipIDs    []string `json:"clip_ids"`
				} `json:"segments"`
			} `json:"script_params"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode Margot fixture: %v", err)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("items=%d, want 1", len(envelope.Items))
	}
	item := envelope.Items[0]
	if len(item.Source.ClipIDs) != 10 || len(item.ScriptParams.Segments) != 10 {
		t.Fatalf("fixture must contain 10 source clips and 10 scenes: clips=%d scenes=%d", len(item.Source.ClipIDs), len(item.ScriptParams.Segments))
	}
	seen := make(map[string]bool, 10)
	for i, segment := range item.ScriptParams.Segments {
		if len(segment.ClipIDs) != 1 || segment.ClipIDs[0] != item.Source.ClipIDs[i] {
			t.Fatalf("scene %d is not positionally bound to its source clip", i)
		}
		if segment.SourceText == "" {
			t.Fatalf("scene %d has empty description source_text", i)
		}
		if seen[segment.SourceText] {
			t.Fatalf("scene %d reuses a description source_text", i)
		}
		seen[segment.SourceText] = true
	}
}
