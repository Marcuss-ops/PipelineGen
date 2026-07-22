package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestStockBindingsProcessorBindsPerSceneAndKeepsClip(t *testing.T) {
	p := NewStockBindingsProcessor()
	input := ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{
			Index: 0, SceneID: "scene-0", AssetID: "stock-1",
			DriveLink: "https://drive.google.com/file/d/stock-1/view",
			StartMs:   1000, EndMs: 5000,
		}},
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", Index: 0, Text: "text", Kind: scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "clip-1"}},
		}}},
	}
	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.ClipID != "clip-1" {
		t.Fatal("stock binding must not replace clip binding")
	}
	stock := got.UpdatedSpecScene.Scenes[0].Bindings.Stock
	if stock == nil || stock.AssetID != "stock-1" || stock.DurationMs != 4000 {
		t.Fatalf("unexpected stock binding: %#v", stock)
	}
}

func TestStockBindingsProcessorDisabledClearsStock(t *testing.T) {
	got, err := NewStockBindingsProcessor().Process(context.Background(), nil, ProcessInput{
		StockEnabled: scriptpkg.ToggleDisabled,
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "s", Text: "text", Kind: scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{Stock: &scriptpkg.StockBinding{AssetID: "old"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Stock != nil {
		t.Fatal("disabled stock must be absent from SpecScene")
	}
}

func TestStockBindingsProcessorRejectsInvalidInterval(t *testing.T) {
	_, err := NewStockBindingsProcessor().Process(context.Background(), nil, ProcessInput{
		StockEnabled:  scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{Index: 0, AssetID: "stock", StartMs: 5, EndMs: 5}},
		SpecScene:     scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{ID: "s", Text: "text", Kind: scriptpkg.SceneClip}}},
	})
	if err == nil {
		t.Fatal("expected invalid stock interval to fail")
	}
}
