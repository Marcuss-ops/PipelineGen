package adapters

import (
	"context"
	"fmt"
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

func TestStockBindingsProcessorNormalizesExplicitSegmentsOneToOne(t *testing.T) {
	segments := []scriptpkg.ScriptSegment{
		{ID: "seg-1", Topic: "opening", SourceText: "Opening source text."},
		{ID: "seg-2", Topic: "pressure", SourceText: "Pressure source text."},
		{ID: "seg-3", Topic: "comeback", SourceText: "Comeback source text."},
		{ID: "seg-4", Topic: "legacy", SourceText: "Legacy source text."},
		{ID: "seg-5", Topic: "lesson", SourceText: "Lesson source text."},
	}
	bindings := make([]scriptpkg.StockBindingInput, len(segments))
	for i, segment := range segments {
		bindings[i] = scriptpkg.StockBindingInput{
			Index: i, SegmentID: segment.ID, AssetID: "asset-" + segment.ID,
			StartMs: int64(i) * 5000, EndMs: int64(i)*5000 + 4000,
		}
	}
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: segments}
	input := ProcessInput{
		StockEnabled:  scriptpkg.ToggleEnabled,
		StockBindings: bindings,
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
			ID: "generated-scene-1", Index: 0, Text: "Generated opening.", Kind: scriptpkg.SceneClip,
		}}},
	}

	got, err := NewStockBindingsProcessor().Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UpdatedSpecScene.Scenes) != len(segments) {
		t.Fatalf("scene count = %d, want %d", len(got.UpdatedSpecScene.Scenes), len(segments))
	}
	for i, scene := range got.UpdatedSpecScene.Scenes {
		if scene.ID != fmt.Sprintf("scene-%d", i) || scene.Index != i || scene.SegmentID != segments[i].ID {
			t.Fatalf("scene %d identity = (%q, %d, %q), want (%q, %d, %q)", i, scene.ID, scene.Index, scene.SegmentID, fmt.Sprintf("scene-%d", i), i, segments[i].ID)
		}
		if scene.Bindings.Stock == nil || scene.Bindings.Stock.AssetID != bindings[i].AssetID {
			t.Fatalf("scene %d stock binding = %#v", i, scene.Bindings.Stock)
		}
	}
	if got.UpdatedSpecScene.Scenes[1].Text != segments[1].SourceText {
		t.Fatalf("missing generated scene must use segment source text, got %q", got.UpdatedSpecScene.Scenes[1].Text)
	}
}
