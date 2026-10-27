package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type semanticEditorialRunner struct {
	transcript string
	manifest   []byte
	operations []string
}

func (r *semanticEditorialRunner) Run(_ context.Context, _ string, input []byte, _ int64) ([]byte, []byte, error) {
	var request struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, nil, err
	}
	r.operations = append(r.operations, request.Operation)
	if request.Operation == "analyze_script" {
		return r.manifest, nil, nil
	}
	if request.Operation != "split_sentences" {
		return nil, nil, fmt.Errorf("unexpected operation %q", request.Operation)
	}
	var split struct {
		OK        bool `json:"ok"`
		Sentences []struct {
			Text      string `json:"text"`
			StartByte int    `json:"start_byte"`
			EndByte   int    `json:"end_byte"`
		} `json:"sentences"`
	}
	for _, sentence := range []string{
		"Marco Rossi founded the studio.",
		"Revenue grew 25% against 18% in 2024.",
		"The hearing was on 15 March 2008.",
	} {
		start := strings.Index(r.transcript, sentence)
		if start < 0 {
			return nil, nil, fmt.Errorf("split fixture sentence %q not present", sentence)
		}
		split.Sentences = append(split.Sentences, struct {
			Text      string `json:"text"`
			StartByte int    `json:"start_byte"`
			EndByte   int    `json:"end_byte"`
		}{Text: sentence, StartByte: start, EndByte: start + len(sentence)})
	}
	split.OK = true
	body, err := json.Marshal(split)
	return body, nil, err
}

func TestSemanticEngineEditorialUsesOneManifestRequestAndPreservesProducts(t *testing.T) {
	transcript := semanticSceneOne + "\n\n" + semanticSceneTwo
	startTwo := len(semanticSceneOne) + 2
	endOne := len(semanticSceneOne)
	endTwo := startTwo + len(semanticSceneTwo)
	scenes := []scriptpkg.SceneAnalysisInput{
		{SceneID: "scene_01", Text: semanticSceneOne, StartByte: ptrInt(0), EndByte: &endOne},
		{SceneID: "scene_02", Text: semanticSceneTwo, StartByte: &startTwo, EndByte: &endTwo},
	}

	editorial := `"editorial":{"summary":"Grounded summary.","bullet_points":[{"text":"Marco Rossi founded the studio.","sentence_index":0,"start":1.5,"end":3.5}],"heavy_sentences":[{"index":0,"text":"Marco Rossi founded the studio.","importance":0.9,"centrality":0.8,"novelty":0.7}],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]},"scene_highlights":[],"timings":{"split_ms":1,"embedding_ms":0,"similarity_ms":2,"ranking_ms":3,"summary_ms":1,"bullet_ms":1,"total_ms":8}},`
	fixture := strings.Replace(semanticFixture, `"scenes": [`, editorial+`"scenes": [`, 1)
	runner := &semanticRunner{output: []byte(fixture)}
	executor := &Executor{runner: runner, outputLimit: 64 * 1024, binaryPath: "semantic-engine"}
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, result, err := adapter.AnalyzeSemanticEditorial(context.Background(), transcript, "en", scenes)
	if err != nil {
		t.Fatalf("combined semantic/editorial request failed: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("worker calls=%d, want one analyze_script request", runner.calls)
	}
	if len(bundles) != 2 || result.Summary != "Grounded summary." || len(result.BulletPoints) != 1 || result.BulletPoints[0] != "Marco Rossi founded the studio." {
		t.Fatalf("combined result lost semantic or editorial products: bundles=%d result=%+v", len(bundles), result)
	}
	if !result.SceneHighlightsCertified || result.Timings.TotalMS != 8 || result.ChapterManifest.SchemaVersion != "chapter_manifest.v1" {
		t.Fatalf("editorial metadata was not preserved: %+v", result)
	}
	if len(result.BulletDetails) != 1 || result.BulletDetails[0].SentenceIndex != 0 || result.BulletDetails[0].StartSec == nil || *result.BulletDetails[0].StartSec != 1.5 || result.BulletDetails[0].EndSec == nil || *result.BulletDetails[0].EndSec != 3.5 || result.HeavySentences[0].Centrality != 0.8 || result.HeavySentences[0].Novelty != 0.7 {
		t.Fatalf("editorial sentence metadata was lost: %+v", result)
	}
	var request semanticEngineRequest
	if err := json.Unmarshal(runner.input, &request); err != nil {
		t.Fatal(err)
	}
	if request.Operation != "analyze_script" || request.Editorial == nil || len(request.Editorial.Scenes) != 2 {
		t.Fatalf("request did not carry the editorial contract: %+v", request)
	}
	if request.Editorial.Request["lexical_only"] != true || request.Editorial.Request["transcript"] != transcript {
		t.Fatalf("editorial lexical fallback inputs differ from current worker contract: %+v", request.Editorial.Request)
	}
}

func TestSemanticEngineEditorialPreservesE5PassagesAndStillAnalyzesOnce(t *testing.T) {
	transcript := semanticSceneOne + "\n\n" + semanticSceneTwo
	startTwo := len(semanticSceneOne) + 2
	endOne := len(semanticSceneOne)
	endTwo := startTwo + len(semanticSceneTwo)
	scenes := []scriptpkg.SceneAnalysisInput{
		{SceneID: "scene_01", Text: semanticSceneOne, StartByte: ptrInt(0), EndByte: &endOne},
		{SceneID: "scene_02", Text: semanticSceneTwo, StartByte: &startTwo, EndByte: &endTwo},
	}
	editorial := `"editorial":{"summary":"Grounded summary.","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]},"scene_highlights":[],"timings":{}},`
	manifest := []byte(strings.Replace(semanticFixture, `"scenes": [`, editorial+`"scenes": [`, 1))
	runner := &semanticEditorialRunner{transcript: transcript, manifest: manifest}
	executor := &Executor{runner: runner, outputLimit: 64 * 1024, binaryPath: "semantic-engine"}
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	embedder := &phraseImpactFakeEmbedder{batchResponse: func(texts []string) [][]float32 {
		vectors := make([][]float32, len(texts))
		for index := range texts {
			vectors[index] = []float32{1, 0}
		}
		return vectors
	}}
	adapter.SetPassageEmbedder(embedder)
	if _, _, err := adapter.AnalyzeSemanticEditorial(context.Background(), transcript, "en", scenes); err != nil {
		t.Fatalf("combined E5 editorial analysis failed: %v", err)
	}
	if len(runner.operations) != 2 || runner.operations[0] != "split_sentences" || runner.operations[1] != "analyze_script" {
		t.Fatalf("worker operations=%v, want preparation split + one analyze_script", runner.operations)
	}
	if len(embedder.calls) != 1 || len(embedder.calls[0]) != 3 {
		t.Fatalf("E5 passage batches=%v, want the three canonical sentences", embedder.calls)
	}
}

func ptrInt(value int) *int { return &value }

type phraseImpactFakeEmbedder struct {
	batchResponse func(texts []string) [][]float32
	calls         [][]string
}

func (f *phraseImpactFakeEmbedder) EmbedPassagesBatch(_ context.Context, texts []string) ([]coreasset.EmbeddingResult, error) {
	f.calls = append(f.calls, append([]string(nil), texts...))
	vectors := f.batchResponse(texts)
	out := make([]coreasset.EmbeddingResult, len(vectors))
	for index, vector := range vectors {
		out[index] = coreasset.EmbeddingResult{Vector: vector, Dimensions: len(vector)}
	}
	return out, nil
}
