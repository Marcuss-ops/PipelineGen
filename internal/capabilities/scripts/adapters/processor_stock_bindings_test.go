package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

func TestStockBindingsProcessorDoesNotReplaceShortGeneratedNarrationWithBrief(t *testing.T) {
	brief := "Explain the funny reaction and do not repeat the clip verbatim."
	generated := strings.TrimSpace(strings.Repeat("Generated narration. ", 30))
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{{ID: "segment-a", Topic: "Matt Damon", SourceText: brief}},
	}
	input := ProcessInput{
		StockEnabled:  scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{Index: 0, SceneID: "scene-0", SegmentID: "segment-a", AssetID: "stock-a", StartMs: 0, EndMs: 5000}},
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, SegmentID: "segment-a", Text: generated}}},
	}
	result, err := NewStockBindingsProcessor().Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.UpdatedSpecScene.Scenes[0].Text; got != generated {
		t.Fatalf("generated narration was replaced by editorial brief: got %q", got)
	}
}

func TestStockBindingsProcessorRejectsBindingForFixedMedia(t *testing.T) {
	_, err := NewStockBindingsProcessor().Process(context.Background(), nil, ProcessInput{
		StockEnabled:  scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{Index: 0, SceneID: "scene-0", AssetID: "stock-a", StartMs: 0, EndMs: 5000}},
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, ExecutionMode: scriptpkg.SceneExecutionFixedMedia}}},
	})
	if err == nil || !strings.Contains(err.Error(), "protected fixed_media") {
		t.Fatalf("fixed-media stock binding must fail closed, got %v", err)
	}
}

func TestStockBindingsProcessorTrimsYouTubeFieldsSetsStockAndBuildsDriveURL(t *testing.T) {
	got, err := NewStockBindingsProcessor().Process(context.Background(), nil, ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{
			Index: 0, SceneID: "scene-0", Source: "youtube",
			AssetID:   "  1TwVU-11JCggSBuHtavhKMevMZna-xr51  ",
			DriveLink: "  1TwVU-11JCggSBuHtavhKMevMZna-xr51  ",
			FolderID:  "  folder-boxe  ", Name: " Sugar Ray Robinson fight ",
			StartMs: 0, EndMs: 5000, Fallback: false,
		}},
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", Kind: scriptpkg.SceneClip,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scene := got.UpdatedSpecScene.Scenes[0]
	stock := scene.Bindings.Stock
	if scene.Kind != scriptpkg.SceneStock {
		t.Fatalf("scene kind = %q, want stock", scene.Kind)
	}
	if stock == nil {
		t.Fatal("stock binding is nil")
	}
	if stock.AssetID != "1TwVU-11JCggSBuHtavhKMevMZna-xr51" {
		t.Errorf("asset_id = %q, want trimmed ID", stock.AssetID)
	}
	if stock.DriveLink != "https://drive.google.com/file/d/1TwVU-11JCggSBuHtavhKMevMZna-xr51/view" {
		t.Errorf("drive_link = %q, want canonical URL", stock.DriveLink)
	}
	if stock.FolderID != "folder-boxe" {
		t.Errorf("folder_id = %q, want trimmed folder", stock.FolderID)
	}
	if stock.Fallback {
		t.Fatal("fallback must remain false")
	}
}

func TestStockBindingsProcessorSerializesFallbackFalse(t *testing.T) {
	stock := scriptpkg.StockBinding{AssetID: "asset-1234567890", Fallback: false}
	payload, err := json.Marshal(stock)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "{}" || !containsJSONField(payload, "fallback") {
		t.Fatalf("fallback=false must be explicit in JSON: %s", payload)
	}
}

func containsJSONField(payload []byte, field string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return false
	}
	_, ok := decoded[field]
	return ok
}

func TestStockBindingsProcessorRejectsEmptyAssetAndLink(t *testing.T) {
	_, err := NewStockBindingsProcessor().Process(context.Background(), nil, ProcessInput{
		StockEnabled:  scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{Index: 0, StartMs: 0, EndMs: 1000}},
		SpecScene:     scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{ID: "scene-0"}}},
	})
	if err == nil {
		t.Fatal("expected empty asset_id and drive_link to fail closed")
	}
}

func TestStockBindingsProcessorUsesFolderLinkWithoutClipLink(t *testing.T) {
	folderID := "1xxnNHfperYJ6sZiLcNadgvYIR6wG_jB8"
	folderLink := "https://drive.google.com/drive/folders/" + folderID + "?usp=drive_link"
	got, err := NewStockBindingsProcessor().Process(context.Background(), nil, ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{{
			Index: 0, Source: "youtube", FolderID: folderID, FolderLink: folderLink,
			Name: "Sugar Ray Robinson stock folder", StartMs: 0, EndMs: 5000,
		}},
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Kind: scriptpkg.SceneClip}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stock := got.UpdatedSpecScene.Scenes[0].Bindings.Stock
	if stock == nil || stock.FolderLink != folderLink || stock.FolderID != folderID {
		t.Fatalf("folder binding = %#v", stock)
	}
	if stock.DriveLink != "" || stock.AssetID != "" {
		t.Fatalf("folder-only binding must not contain a clip reference: %#v", stock)
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

func TestStockBindingsProcessorRepairsCrossSegmentNarrative(t *testing.T) {
	segments := []scriptpkg.ScriptSegment{
		{ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: strings.Repeat("Mike Tyson domina il racconto con potenza e disciplina. ", 20)},
		{ID: "boxer-muhammad-ali", Topic: "Muhammad Ali", SourceText: strings.Repeat("Muhammad Ali costruisce il racconto con movimento e carisma. ", 20)},
	}
	input := ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{
			{Index: 0, SegmentID: segments[0].ID, AssetID: "asset-tyson", StartMs: 0, EndMs: 4000},
			{Index: 1, SegmentID: segments[1].ID, AssetID: "asset-ali", StartMs: 0, EndMs: 4000},
		},
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: strings.Repeat("Mike Tyson parla della sua potenza. Muhammad Ali arriva nel blocco successivo. ", 12)},
			{ID: "scene-1", Index: 1, Text: "Muhammad Ali"},
		}},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: segments}
	got, err := NewStockBindingsProcessor().Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Text != strings.TrimSpace(segments[0].SourceText) {
		t.Fatal("scene 0 did not restore its explicit source text")
	}
	if got.UpdatedSpecScene.Scenes[1].Text != "Muhammad Ali" {
		t.Fatalf("valid short generated scene was replaced by source text: %q", got.UpdatedSpecScene.Scenes[1].Text)
	}
}

func TestStockBindingsProcessorRejectsWrongSegmentAtZeroSlot(t *testing.T) {
	segments := []scriptpkg.ScriptSegment{
		{ID: "intro-hook", Topic: "Introduzione", SourceText: strings.Repeat("Introduzione. ", 15)},
		{ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: strings.Repeat("Tyson. ", 15)},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: segments}
	_, err := NewStockBindingsProcessor().Process(context.Background(), plan, ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{
			{Index: 0, SceneID: "scene-0", SegmentID: "boxer-mike-tyson", AssetID: "TYSON_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000},
		},
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "intro", Kind: scriptpkg.SceneClip},
		}},
	})
	if err == nil {
		t.Fatal("binding segment_id at index 0 must match the intro-hook scene or fail")
	}
}

func TestStockBindingsProcessorIntroHookKeepsIntroKindAndClip(t *testing.T) {
	segments := []scriptpkg.ScriptSegment{
		{ID: "intro-hook", Topic: "Introduzione", SourceText: strings.Repeat("Introduzione sui cinque pugili. ", 15)},
		{ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: strings.Repeat("Mike Tyson e la sua potenza. ", 15)},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: segments}
	got, err := NewStockBindingsProcessor().Process(context.Background(), plan, ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{
			{Index: 0, SceneID: "scene-0", SegmentID: "intro-hook", AssetID: "INTRO_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000},
			{Index: 1, SceneID: "scene-1", SegmentID: "boxer-mike-tyson", AssetID: "TYSON_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000},
		},
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: strings.Repeat("Intro. ", 30), Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "post-intro-clip-0"}}},
			{ID: "scene-1", Index: 1, Text: strings.Repeat("Tyson. ", 30), Kind: scriptpkg.SceneClip},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes := got.UpdatedSpecScene.Scenes
	intro := scenes[0]
	stock := intro.Bindings.Stock
	if intro.SegmentID != "intro-hook" || intro.ID != "scene-0" {
		t.Fatalf("intro scene identity = (%q, %q)", intro.ID, intro.SegmentID)
	}
	if intro.Kind != scriptpkg.SceneIntro {
		t.Fatalf("intro-hook scene kind = %q, want intro", intro.Kind)
	}
	if stock == nil || stock.AssetID != "INTRO_STOCK_ASSET_ID" {
		t.Fatalf("intro stock binding = %#v", stock)
	}
	if intro.Bindings.Clip == nil || intro.Bindings.Clip.ClipID != "post-intro-clip-0" {
		t.Fatal("stock binding must not replace the intro clip binding")
	}
	if got := scenes[1]; got.Kind != scriptpkg.SceneStock || got.Bindings.Stock == nil || got.Bindings.Stock.AssetID != "TYSON_STOCK_ASSET_ID" {
		t.Fatalf("boxer scene kind/stock = %q / %#v, want stock", got.Kind, got.Bindings.Stock)
	}
}

func TestStockBindingsProcessorIntroHookDoesNotMoveBoxers(t *testing.T) {
	segments := []scriptpkg.ScriptSegment{
		{ID: "intro-hook", Topic: "Introduzione", SourceText: strings.Repeat("Introduzione. ", 15)},
		{ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: strings.Repeat("Tyson. ", 15)},
		{ID: "boxer-muhammad-ali", Topic: "Muhammad Ali", SourceText: strings.Repeat("Ali. ", 15)},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{Segments: segments}
	got, err := NewStockBindingsProcessor().Process(context.Background(), plan, ProcessInput{
		StockEnabled: scriptpkg.ToggleEnabled,
		StockBindings: []scriptpkg.StockBindingInput{
			{Index: 0, SegmentID: "intro-hook", AssetID: "INTRO_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000},
			{Index: 1, SegmentID: "boxer-mike-tyson", AssetID: "TYSON_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000},
			{Index: 2, SegmentID: "boxer-muhammad-ali", AssetID: "ALI_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000},
		},
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "intro", Kind: scriptpkg.SceneClip},
			{ID: "scene-1", Index: 1, Text: "tyson", Kind: scriptpkg.SceneClip},
			{ID: "scene-2", Index: 2, Text: "ali", Kind: scriptpkg.SceneClip},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		sceneID, segmentID, assetID string
		kind                        scriptpkg.SceneKind
	}{
		{"scene-0", "intro-hook", "INTRO_STOCK_ASSET_ID", scriptpkg.SceneIntro},
		{"scene-1", "boxer-mike-tyson", "TYSON_STOCK_ASSET_ID", scriptpkg.SceneStock},
		{"scene-2", "boxer-muhammad-ali", "ALI_STOCK_ASSET_ID", scriptpkg.SceneStock},
	}
	scenes := got.UpdatedSpecScene.Scenes
	if len(scenes) != len(want) {
		t.Fatalf("scene count = %d, want %d", len(scenes), len(want))
	}
	for i, w := range want {
		scene := scenes[i]
		if scene.ID != w.sceneID || scene.SegmentID != w.segmentID || scene.Kind != w.kind {
			t.Fatalf("scene %d = (%q, %q, %q), want (%q, %q, %q)", i, scene.ID, scene.SegmentID, scene.Kind, w.sceneID, w.segmentID, w.kind)
		}
		if scene.Bindings.Stock == nil || scene.Bindings.Stock.AssetID != w.assetID {
			t.Fatalf("scene %d stock = %#v, want asset %q", i, scene.Bindings.Stock, w.assetID)
		}
	}
}
