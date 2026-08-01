package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestMediaModeStockOnlyClearsOppositeClipBinding(t *testing.T) {
	result, err := NewStockBindingsProcessor().Process(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		MediaMode: scriptpkg.MediaModeStockOnly,
	}, ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{
			Index: 0, FolderID: "folder-1", FolderLink: "https://drive.google.com/drive/folders/folder-1", StartMs: 0, EndMs: 1000,
		}},
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "clip-1"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdatedSpecScene.Scenes[0].Bindings.Clip != nil {
		t.Fatal("stock_only must not retain a clip binding")
	}
}

func TestMediaModeClipOnlyClearsOppositeStockBinding(t *testing.T) {
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", Bindings: scriptpkg.SceneBindings{Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}},
		}}},
	}
	result, err := NewClipBindingsProcessor(nil).Process(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		MediaMode:    scriptpkg.MediaModeClipOnly,
		ClipEvidence: &scriptpkg.ClipEvidence{AcceptedClipIDs: []string{"clip-1"}, DriveLinks: map[string]string{"clip-1": "https://drive.google.com/file/d/clip-1/view"}},
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("processor returned nil result")
	}
	if result == nil {
		t.Fatal("processor returned nil result")
	}
	if input.SpecScene.Scenes[0].Bindings.Stock != nil {
		t.Fatal("clip_only must not retain a stock binding")
	}
}
