package collections

import (
	"testing"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

func TestCollectionContract_DerivesDimensionAndDistance(t *testing.T) {
	info := &qdrantschema.CollectionInfo{VectorConfigs: map[string]qdrantschema.VectorConfig{
		"text": {Size: 768, Distance: "Cosine"}, "transcript": {Size: 768, Distance: "Cosine"},
		"visual": {Size: 768, Distance: "Cosine"},
	}}
	got, ok := CollectionContract(info, "text")
	if !ok || got.Dimension != 768 || got.Distance != "Cosine" {
		t.Fatalf("got=%+v ok=%v, want dimension=768 distance=Cosine", got, ok)
	}
	if !coreembedding.CanonicalText.MatchesPartial(got) {
		t.Fatalf("derived contract %+v must match canonical text contract", got)
	}
}

func TestCollectionContract_MissingChannel(t *testing.T) {
	info := &qdrantschema.CollectionInfo{VectorConfigs: map[string]qdrantschema.VectorConfig{"visual": {Size: 768, Distance: "Cosine"}}}
	if _, ok := CollectionContract(info, "text"); ok {
		t.Fatal("missing text channel must fail closed")
	}
}

func TestCollectionContract_NilInfo(t *testing.T) {
	if _, ok := CollectionContract(nil, "text"); ok {
		t.Fatal("nil info must fail closed")
	}
}
