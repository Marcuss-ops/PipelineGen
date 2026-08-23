package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

type fakeVisualPlannerClient struct {
	response string
	err      error
	called   bool
}

func (f *fakeVisualPlannerClient) Chat(_ context.Context, _ []types.Message, _ map[string]any, _ json.RawMessage) (string, error) {
	f.called = true
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func TestOllamaVisualPlannerAdapter_Select_Valid(t *testing.T) {
	client := &fakeVisualPlannerClient{response: `{"selected_asset_id": "asset-2"}`}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.7},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.85},
			{AssetID: "asset-3", Provider: "pexels", Score: 0.6},
		},
	}

	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-2" {
		t.Fatalf("expected asset-2, got %q", selected)
	}
	if !client.called {
		t.Fatal("expected LLM to be called")
	}
}

func TestOllamaVisualPlannerAdapter_Select_FallbackOnUnknownID(t *testing.T) {
	client := &fakeVisualPlannerClient{response: `{"selected_asset_id": "invented-id"}`}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.7},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.9},
			{AssetID: "asset-3", Provider: "pexels", Score: 0.6},
		},
	}

	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-2" {
		t.Fatalf("expected fallback asset-2, got %q", selected)
	}
}

func TestOllamaVisualPlannerAdapter_Select_FallbackOnInvalidJSON(t *testing.T) {
	client := &fakeVisualPlannerClient{response: `this is not json`}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.95},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.6},
		},
	}

	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-1" {
		t.Fatalf("expected fallback asset-1, got %q", selected)
	}
}

func TestOllamaVisualPlannerAdapter_Select_FallbackOnLLMError(t *testing.T) {
	client := &fakeVisualPlannerClient{err: errors.New("ollama timeout")}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.5},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.8},
		},
	}

	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-2" {
		t.Fatalf("expected fallback asset-2, got %q", selected)
	}
}

func TestOllamaVisualPlannerAdapter_Select_FallbackOnTimeout(t *testing.T) {
	client := &fakeVisualPlannerClient{err: context.DeadlineExceeded}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.4},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.9},
		},
	}

	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-2" {
		t.Fatalf("expected fallback asset-2, got %q", selected)
	}
}

func TestOllamaVisualPlannerAdapter_Select_FallbackWhenNoClient(t *testing.T) {
	planner := NewOllamaVisualPlannerAdapter(nil, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.4},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.9},
		},
	}

	start := time.Now()
	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-2" {
		t.Fatalf("expected fallback asset-2, got %q", selected)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("expected instant fallback without client, took %v", elapsed)
	}
}

func TestOllamaVisualPlannerAdapter_Select_EmptyCandidates(t *testing.T) {
	client := &fakeVisualPlannerClient{response: `{"selected_asset_id": "x"}`}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:    "scene-1",
		Text:       "A robot arm assembling a car",
		Slot:       media.SlotPrimaryVideo,
		Candidates: nil,
	}

	_, err := planner.Select(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestOllamaVisualPlannerAdapter_Select_StripsMarkdownCodeBlock(t *testing.T) {
	client := &fakeVisualPlannerClient{response: "```json\n{\"selected_asset_id\": \"asset-3\"}\n```"}
	planner := NewOllamaVisualPlannerAdapter(client, nil)

	req := scriptadapters.VisualSelectionRequest{
		SceneID:      "scene-1",
		SegmentID:    "seg-1",
		Text:         "A robot arm assembling a car",
		VisualIntent: "Industrial robot arm working on an automotive assembly line",
		Slot:         media.SlotPrimaryVideo,
		Candidates: []mediamemory.CandidateOption{
			{AssetID: "asset-1", Provider: "drive", Score: 0.4},
			{AssetID: "asset-2", Provider: "artlist", Score: 0.6},
			{AssetID: "asset-3", Provider: "pexels", Score: 0.5},
		},
	}

	selected, err := planner.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != "asset-3" {
		t.Fatalf("expected asset-3, got %q", selected)
	}
}
