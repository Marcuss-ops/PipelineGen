package media

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type recordingTextEmbedder struct {
	singleCalls int
	batchCalls  [][]string
	batchErr    error
}

func (e *recordingTextEmbedder) Embed(_ context.Context, text string) (coreasset.EmbeddingResult, error) {
	e.singleCalls++
	return coreasset.EmbeddingResult{Vector: []float32{float32(len(text))}}, nil
}

func (e *recordingTextEmbedder) EmbedBatch(_ context.Context, texts []string) ([]coreasset.EmbeddingResult, error) {
	e.batchCalls = append(e.batchCalls, append([]string(nil), texts...))
	if e.batchErr != nil {
		return nil, e.batchErr
	}
	results := make([]coreasset.EmbeddingResult, len(texts))
	for i, text := range texts {
		results[i] = coreasset.EmbeddingResult{Vector: []float32{float32(len(text)), float32(i)}}
	}
	return results, nil
}

func TestEmbedSearchTextsBatchesInRequestedOrderAndChunksAt32(t *testing.T) {
	embedder := &recordingTextEmbedder{}
	adapter := &EmbedAssetTextAdapter{embeder: embedder}
	ids := make([]string, 33)
	texts := make(map[string]string, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("asset-%02d", i)
		texts[ids[i]] = fmt.Sprintf("text-%02d", i)
	}

	got, err := adapter.embedSearchTexts(context.Background(), ids, texts)
	if err != nil {
		t.Fatalf("embedSearchTexts: %v", err)
	}
	if len(embedder.batchCalls) != 2 || len(embedder.batchCalls[0]) != 32 || len(embedder.batchCalls[1]) != 1 {
		t.Fatalf("batch sizes = %v, want [32, 1]", []int{len(embedder.batchCalls[0]), len(embedder.batchCalls[1])})
	}
	if got[ids[0]][0] != float32(len(texts[ids[0]])) || got[ids[31]][1] != 31 || got[ids[32]][1] != 0 {
		t.Fatalf("batch vectors were not mapped back to corresponding asset IDs: first=%v last-in-batch=%v last=%v", got[ids[0]], got[ids[31]], got[ids[32]])
	}
	if embedder.singleCalls != 0 {
		t.Fatalf("single Embed called %d times despite batch support", embedder.singleCalls)
	}
}

func TestEmbedSearchTextsDeduplicatesIDsAndSkipsBlankOrMissingTexts(t *testing.T) {
	embedder := &recordingTextEmbedder{}
	adapter := &EmbedAssetTextAdapter{embeder: embedder}
	ids := []string{"a", "blank", "missing", "a", "b"}
	texts := map[string]string{"a": "alpha", "blank": " \t ", "b": "beta"}

	got, err := adapter.embedSearchTexts(context.Background(), ids, texts)
	if err != nil {
		t.Fatalf("embedSearchTexts: %v", err)
	}
	if !reflect.DeepEqual(embedder.batchCalls, [][]string{{"alpha", "beta"}}) {
		t.Fatalf("batch inputs = %v, want one ordered batch [alpha beta]", embedder.batchCalls)
	}
	if len(got) != 2 || len(got["a"]) == 0 || len(got["b"]) == 0 {
		t.Fatalf("mapped vectors = %v, want only a and b", got)
	}
}

func TestEmbedSearchTextsUsesSingleEmbedderCompatibilityFallback(t *testing.T) {
	embedder := &singleOnlyTextEmbedder{}
	adapter := &EmbedAssetTextAdapter{embeder: embedder}
	got, err := adapter.embedSearchTexts(context.Background(), []string{"a", "blank", "b"}, map[string]string{"a": "one", "blank": " ", "b": "two"})
	if err != nil {
		t.Fatalf("embedSearchTexts: %v", err)
	}
	if !reflect.DeepEqual(embedder.inputs, []string{"one", "two"}) || len(got) != 2 {
		t.Fatalf("fallback inputs=%v vectors=%v, want ordered nonblank inputs and two results", embedder.inputs, got)
	}
}

type singleOnlyTextEmbedder struct{ inputs []string }

func (e *singleOnlyTextEmbedder) Embed(_ context.Context, text string) (coreasset.EmbeddingResult, error) {
	e.inputs = append(e.inputs, text)
	return coreasset.EmbeddingResult{Vector: []float32{1}}, nil
}

func TestEmbedSearchTextsBatchFailureReturnsNoPartialResults(t *testing.T) {
	wantErr := errors.New("batch unavailable")
	embedder := &recordingTextEmbedder{batchErr: wantErr}
	adapter := &EmbedAssetTextAdapter{embeder: embedder}
	got, err := adapter.embedSearchTexts(context.Background(), []string{"a", "b"}, map[string]string{"a": "one", "b": "two"})
	if err == nil || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("error = %v, want wrapped %q", err, wantErr)
	}
	if got != nil {
		t.Fatalf("partial batch results = %v, want nil", got)
	}
}
