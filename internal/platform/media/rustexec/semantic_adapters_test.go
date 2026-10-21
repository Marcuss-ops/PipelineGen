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
	calls  int
}

func (r *semanticRunner) Run(_ context.Context, _ string, input []byte, _ int64) ([]byte, []byte, error) {
	r.calls++
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
func TestVisualNERAnalyzeDecodesGroundedRelations(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"entities":[{"text":"salad","start":6,"end":11,"score":0.9,"type":"CONCEPT"},{"text":"Greek","start":0,"end":5,"score":0.8,"type":"LOC"}],"relations":[{"source":0,"target":1,"sentence_index":0,"evidence":"Greek salad"}],"lower_thirds":[],"bullets":[],"chart_points":[]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	analysis, err := a.Analyze(context.Background(), "en", "Greek salad", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Entities) != 2 || len(analysis.Relations) != 1 {
		t.Fatalf("entities=%d relations=%d", len(analysis.Entities), len(analysis.Relations))
	}
}

func TestVisualNERAnalyzeRejectsOutOfRangeRelation(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"entities":[{"text":"salad","start":6,"end":11,"score":0.9}],"relations":[{"source":0,"target":3,"sentence_index":0,"evidence":"Greek salad"}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "en", "Greek salad", 3); err == nil {
		t.Fatal("expected out-of-range relation error")
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

func TestMediaSamplerSampleGroupSendsGroupOperationAndLedger(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"asset-1","group_ids":["asset-1","asset-2"]}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	_, group, err := a.SelectGroup(context.Background(), "seg", "greek salad", nil,
		[]mediaSamplerCandidate{{ID: "asset-1", Label: "salad"}, {ID: "asset-2", Label: "restaurant"}},
		false, 3,
		[]mediaSamplerBoundAsset{{AssetID: "asset-9", SceneID: "other-scene"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(group) != 2 || group[0] != "asset-1" || group[1] != "asset-2" {
		t.Fatalf("group=%v", group)
	}
	var req mediaSamplerGroupRequest
	if err := json.Unmarshal(r.input, &req); err != nil {
		t.Fatal(err)
	}
	if req.Operation != "select_group" || req.GroupSize != 3 {
		t.Fatalf("bad request: %+v", req)
	}
	if len(req.BoundAssets) != 1 || req.BoundAssets[0].AssetID != "asset-9" {
		t.Fatalf("ledger: %+v", req.BoundAssets)
	}
}

func TestMediaSamplerSampleGroupFailsClosedOutsideTwoToFive(t *testing.T) {
	a, _ := NewMediaSamplerAdapter(executorForSemantic(&semanticRunner{}))
	for _, bad := range []int{0, 1, 6, 99} {
		if _, _, err := a.SelectGroup(context.Background(), "seg", "s", nil, nil, false, bad, nil); err == nil {
			t.Fatalf("group size %d must fail closed", bad)
		}
	}
}

func TestMediaSamplerWorkerErrorSurfacesSelectGroupFailure(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":null,"group_ids":[],"error":"select_group requires 2..=5"}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	if _, _, err := a.SelectGroup(context.Background(), "seg", "s", nil, nil, false, 5, nil); err == nil {
		t.Fatal("worker error must surface")
	}
}

func TestMediaSamplerGroupRejectsDuplicateMembers(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"asset-1","group_ids":["asset-1","asset-1"]}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	if _, _, err := a.SelectGroup(context.Background(), "seg", "s", nil, nil, false, 5, nil); err == nil {
		t.Fatal("duplicate group member must fail closed")
	}
}
