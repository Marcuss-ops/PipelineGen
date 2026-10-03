package rustexec

import (
	"context"
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type semanticRunner struct {
	output []byte
	input  []byte
}

func (r *semanticRunner) Run(_ context.Context, _ string, input []byte, _ int64) ([]byte, []byte, error) {
	r.input = append([]byte(nil), input...)
	return r.output, nil, nil
}
func executorForSemantic(r *semanticRunner) *Executor {
	return &Executor{runner: r, outputLimit: 64 * 1024}
}

func TestVisualNERAdapterRequiresLanguage(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"entities":[]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Extract(context.Background(), " ", "Greek salad", 3); err == nil {
		t.Fatal("expected missing-language error")
	}
	if len(r.input) != 0 {
		t.Fatal("executor must not run when language is missing")
	}
}

func TestVisualNERAdapterRejectsUngroundedEntity(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"entities":[{"text":"boxing","start":0,"end":6,"score":0.9}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Extract(context.Background(), "en", "Greek salad", 3); err == nil {
		t.Fatal("expected grounding error")
	}
}
func TestVisualNERAdapterExtractEntitiesRequiresLanguage(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"entities":[]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "Greek salad"}); err == nil {
		t.Fatal("expected missing-language error")
	}
	if len(r.input) != 0 {
		t.Fatal("executor must not run when language is missing")
	}
}

func TestVisualNERAdapterSendsV1AndAcceptsGroundedEntities(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"entities":[{"text":"salad","start":6,"end":11,"score":0.9}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	entities, err := a.Extract(context.Background(), "en", "Greek salad", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Fatalf("got %d entities", len(entities))
	}
	var req visualNERRequest
	if err := json.Unmarshal(r.input, &req); err != nil {
		t.Fatal(err)
	}
	if req.Version != "visualner.v1" || req.Operation != "extract" {
		t.Fatalf("bad request: %+v", req)
	}
	if req.EntityCount != 3 {
		t.Fatalf("entity_count=%d, want 3", req.EntityCount)
	}
	if req.Language != "en" {
		t.Fatalf("language=%q, want en", req.Language)
	}
}
func TestMediaSamplerAdapterSendsV1AndReturnsWinner(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"salad"}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	_, winner, err := a.SampleScene(context.Background(), "seg", "greek salad", []string{"tomatoes"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if winner != "salad" {
		t.Fatalf("winner=%q", winner)
	}
	var req mediaSamplerRequest
	if err := json.Unmarshal(r.input, &req); err != nil {
		t.Fatal(err)
	}
	if req.Version != "mediasampler.v1" || req.Operation != "select" {
		t.Fatalf("bad request: %+v", req)
	}
}
